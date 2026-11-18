package ainotification

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Autumn-27/artex/notify"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type Delivery struct {
	ID              int64      `json:"id,string"`
	FindingID       int64      `json:"finding_id,omitempty,string"`
	VulnerabilityID int64      `json:"vulnerability_id,omitempty,string"`
	TaskID          int64      `json:"task_id,omitempty,string"`
	EventKind       string     `json:"event_kind"`
	ChannelID       int64      `json:"channel_id,string"`
	ChannelName     string     `json:"channel_name"`
	ChannelKind     string     `json:"channel_kind"`
	State           string     `json:"state"`
	Attempts        int        `json:"attempts"`
	LastError       string     `json:"last_error"`
	CreatedAt       time.Time  `json:"created_at"`
	SentAt          *time.Time `json:"sent_at,omitempty"`
	NextAttempt     time.Time  `json:"next_attempt_at"`
	Title           string     `json:"title"`
	Severity        string     `json:"severity"`
}

type DeliveryPage struct {
	Items []Delivery `json:"items"`
	Total int64      `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
}

func (s *Store) Deliveries(ctx context.Context, user, page, size int) (DeliveryPage, error) {
	return s.FilteredDeliveries(ctx, user, page, size, 0, "")
}

func (s *Store) FilteredDeliveries(ctx context.Context, user, page, size int, channelID int64, state string) (DeliveryPage, error) {
	out := DeliveryPage{Items: []Delivery{}, Page: page, Size: size}
	if page < 0 || page > 10000 || size < 1 || size > 100 || channelID < 0 || (state != "" && state != "pending" && state != "sending" && state != "sent" && state != "failed" && state != "skipped") {
		return out, ErrInvalid
	}
	db := s.db.WithContext(ctx)
	if err := s.requireMember(db, user); err != nil {
		return out, err
	}
	from := ` FROM ai_notification_deliveries d JOIN ai_notification_events e ON e.event_id=d.event_id JOIN ai_notification_channels c ON c.id=d.channel_id WHERE c.tenant_id=?::uuid AND c.owner_user_id=? AND e.tenant_id=c.tenant_id AND e.owner_user_id=c.owner_user_id AND tianqiong_ai.outbound_notification_visible(e.event_id,?) AND (?::bigint=0 OR c.id=?) AND (?='' OR d.state=?)`
	if err := db.Raw(`SELECT count(*)`+from, assets.DefaultTenantID, user, user, channelID, channelID, state, state).Row().Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := db.Raw(`SELECT d.id,COALESCE(e.finding_id,0),COALESCE(e.task_id,0),COALESCE(e.vulnerability_id,0),e.kind,c.id,c.name,c.kind,d.state,d.attempts,d.last_error,d.created_at,d.sent_at,d.next_attempt_at,e.snapshot`+from+` ORDER BY d.id DESC LIMIT ? OFFSET ?`, assets.DefaultTenantID, user, user, channelID, channelID, state, state, size, page*size).Rows()
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Delivery
		var raw []byte
		if err = rows.Scan(&item.ID, &item.FindingID, &item.TaskID, &item.VulnerabilityID, &item.EventKind, &item.ChannelID, &item.ChannelName, &item.ChannelKind, &item.State, &item.Attempts, &item.LastError, &item.CreatedAt, &item.SentAt, &item.NextAttempt, &raw); err != nil {
			return out, err
		}
		snapshot, e := decodeSnapshot(raw)
		if e != nil {
			return out, e
		}
		item.Title = snapshot.Name
		if item.Title == "" {
			item.Title = snapshot.VulnClass
		}
		item.Severity = snapshot.Severity
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if err = s.requireMember(db, user); err != nil {
		return DeliveryPage{}, err
	}
	return out, nil
}

type Statistics struct {
	Channels        int64 `json:"channels"`
	EnabledChannels int64 `json:"channels_on"`
	Pending         int64 `json:"pending"`
	Failed          int64 `json:"failed"`
	SentToday       int64 `json:"sent_today"`
	BacklogAgeMS    int64 `json:"backlog_age_ms"`
}

func (s *Store) Statistics(ctx context.Context, user int) (Statistics, error) {
	var out Statistics
	db := s.db.WithContext(ctx)
	if err := s.requireMember(db, user); err != nil {
		return out, err
	}
	if err := db.Raw(`SELECT count(*),count(*) FILTER (WHERE enabled) FROM ai_notification_channels WHERE tenant_id=?::uuid AND owner_user_id=? AND deleted_at IS NULL`, assets.DefaultTenantID, user).Row().Scan(&out.Channels, &out.EnabledChannels); err != nil {
		return out, err
	}
	err := db.Raw(`SELECT count(*) FILTER (WHERE d.state IN ('pending','sending')),count(*) FILTER (WHERE d.state='failed'),count(*) FILTER (WHERE d.state='sent' AND d.sent_at>=date_trunc('day',clock_timestamp())),COALESCE(EXTRACT(EPOCH FROM (clock_timestamp()-min(d.created_at) FILTER (WHERE d.state IN ('pending','sending'))))*1000,0)::bigint
 FROM ai_notification_deliveries d JOIN ai_notification_events e ON e.event_id=d.event_id JOIN ai_notification_channels c ON c.id=d.channel_id WHERE c.tenant_id=?::uuid AND c.owner_user_id=? AND e.tenant_id=c.tenant_id AND e.owner_user_id=c.owner_user_id AND tianqiong_ai.outbound_notification_visible(e.event_id,?)`, assets.DefaultTenantID, user, user).Row().Scan(&out.Pending, &out.Failed, &out.SentToday, &out.BacklogAgeMS)
	if err != nil {
		return out, err
	}
	if err = s.requireMember(db, user); err != nil {
		return Statistics{}, err
	}
	return out, nil
}

func (s *Store) RetryDelivery(ctx context.Context, user int, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.requireMember(tx, user); err != nil {
			return err
		}
		var state string
		var enabled, visible bool
		err := tx.Raw(`SELECT d.state,c.enabled AND c.deleted_at IS NULL AND COALESCE(pref.enabled,TRUE),tianqiong_ai.outbound_notification_visible(e.event_id,?) FROM ai_notification_deliveries d JOIN ai_notification_channels c ON c.id=d.channel_id JOIN ai_notification_events e ON e.event_id=d.event_id LEFT JOIN ai_notification_settings pref ON pref.tenant_id=c.tenant_id AND pref.owner_user_id=c.owner_user_id WHERE d.id=? AND c.tenant_id=?::uuid AND c.owner_user_id=? AND e.tenant_id=c.tenant_id AND e.owner_user_id=c.owner_user_id FOR UPDATE OF d`, user, id, assets.DefaultTenantID, user).Row().Scan(&state, &enabled, &visible)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !visible {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state != "failed" || !enabled {
			return ErrConflict
		}
		return tx.Exec(`UPDATE ai_notification_deliveries SET state='pending',attempts=0,next_attempt_at=clock_timestamp(),last_error='',lease_token=NULL WHERE id=?`, id).Error
	})
}

func (s *Store) TestChannel(ctx context.Context, user int, id int64) error {
	db := s.db.WithContext(ctx)
	if err := s.requireMember(db, user); err != nil {
		return err
	}
	row, err := scanChannel(db.Raw(`SELECT `+channelColumns+` FROM ai_notification_channels WHERE id=? AND tenant_id=?::uuid AND owner_user_id=? AND deleted_at IS NULL`, id, assets.DefaultTenantID, user).Row())
	if err != nil {
		return err
	}
	config, err := s.decrypt(user, row)
	if err != nil {
		return err
	}
	channel, ok := notify.Get(row.Kind)
	if !ok {
		return ErrInvalid
	}
	if err = channel.Validate(config); err != nil {
		return ErrInvalid
	}
	message := notify.Message{Items: []notify.Item{{Name: "SkyShield AI 通知测试", VulnClass: "test", Severity: "info", Summary: "这是一条测试消息。"}}}
	kept, err := channel.Send(ctx, config, message)
	if err != nil || kept != 1 {
		return errors.New("AI notification test failed")
	}
	return nil
}

type KindMetadata struct {
	Kind              string   `json:"kind"`
	DefaultRatePerMin int      `json:"default_rate_per_min"`
	SecretKeys        []string `json:"secret_keys"`
}

func Kinds() []KindMetadata {
	out := []KindMetadata{}
	for _, kind := range notify.Kinds() {
		channel, _ := notify.Get(kind)
		out = append(out, KindMetadata{Kind: kind, DefaultRatePerMin: channel.DefaultRatePerMin(), SecretKeys: channel.SecretKeys()})
	}
	return out
}
