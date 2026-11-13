// Package ainotification hosts ARTEX finding notification channels without
// sharing account-private events with installation-wide destinations.
package ainotification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/notify"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

var (
	ErrInvalid  = errors.New("invalid AI notification settings")
	ErrNotFound = errors.New("AI notification resource not found")
	ErrConflict = errors.New("AI notification resource changed")
)

type SecretCodec interface {
	Seal(string, string) ([]byte, error)
	Open(string, []byte) (string, error)
}

type Store struct {
	db    *gorm.DB
	codec SecretCodec
}

func NewStore(db *gorm.DB, codec SecretCodec) *Store {
	if db == nil || codec == nil {
		panic("AI notification database and secret codec are required")
	}
	return &Store{db: db, codec: codec}
}

type Settings struct {
	Enabled           bool   `json:"enabled"`
	PublicBaseURL     string `json:"public_base_url"`
	DigestIntervalMin int    `json:"digest_interval_min"`
}

type SettingsPatch struct {
	Enabled           *bool   `json:"enabled"`
	PublicBaseURL     *string `json:"public_base_url"`
	DigestIntervalMin *int    `json:"digest_interval_min"`
}

type Channel struct {
	ID         int64          `json:"id,string"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Enabled    bool           `json:"enabled"`
	Mode       string         `json:"mode"`
	Config     map[string]any `json:"config"`
	Filter     notify.Filter  `json:"filter"`
	RatePerMin int            `json:"rate_per_min"`
	Version    int64          `json:"version"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	SecretKeys []string       `json:"secret_keys"`
}

type ChannelInput struct {
	Name       *string        `json:"name"`
	Kind       *string        `json:"kind"`
	Enabled    *bool          `json:"enabled"`
	Mode       *string        `json:"mode"`
	Config     map[string]any `json:"config"`
	Filter     *notify.Filter `json:"filter"`
	RatePerMin *int           `json:"rate_per_min"`
	Version    *int64         `json:"version"`
}

type channelRecord struct {
	ID                   int64
	Name, Kind, Mode     string
	Enabled              bool
	Cipher, RawFilter    []byte
	RatePerMin           int
	Version              int64
	CreatedAt, UpdatedAt time.Time
}

func (s *Store) requireMember(tx *gorm.DB, user int) error {
	if user <= 0 {
		return ErrNotFound
	}
	var allowed bool
	err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM tenant_membership m JOIN auth_user u ON u.id=m.user_id JOIN tenant t ON t.id=m.tenant_id WHERE m.tenant_id=?::uuid AND m.user_id=? AND m.role IN ('admin','analyst') AND u.is_active AND t.status='active')`, assets.DefaultTenantID, user).Scan(&allowed).Error
	if err != nil {
		return err
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}

func secretScope(user int, id int64) string {
	return fmt.Sprintf("ai-notification/%s/%d/%d", assets.DefaultTenantID, user, id)
}

func (s *Store) decrypt(user int, row channelRecord) (map[string]any, error) {
	raw, err := s.codec.Open(secretScope(user, row.ID), row.Cipher)
	if err != nil {
		return nil, err
	}
	var config map[string]any
	if err = json.Unmarshal([]byte(raw), &config); err != nil || config == nil {
		return nil, errors.New("invalid stored AI notification configuration")
	}
	return config, nil
}

func (s *Store) present(user int, row channelRecord) (Channel, error) {
	config, err := s.decrypt(user, row)
	if err != nil {
		return Channel{}, err
	}
	channel, ok := notify.Get(row.Kind)
	if !ok {
		return Channel{}, errors.New("unknown stored AI notification channel")
	}
	return Channel{ID: row.ID, Name: row.Name, Kind: row.Kind, Enabled: row.Enabled, Mode: row.Mode, Config: notify.MaskConfig(row.Kind, config), Filter: notify.ParseFilter(row.RawFilter), RatePerMin: row.RatePerMin, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, SecretKeys: channel.SecretKeys()}, nil
}

const channelColumns = `id,name,kind,enabled,mode,config_cipher,filter,rate_per_min,version,created_at,updated_at`

func scanChannel(row interface{ Scan(...any) error }) (channelRecord, error) {
	var item channelRecord
	err := row.Scan(&item.ID, &item.Name, &item.Kind, &item.Enabled, &item.Mode, &item.Cipher, &item.RawFilter, &item.RatePerMin, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

func (s *Store) Settings(ctx context.Context, user int) (Settings, error) {
	out := Settings{Enabled: true, DigestIntervalMin: 30}
	db := s.db.WithContext(ctx)
	if err := s.requireMember(db, user); err != nil {
		return Settings{}, err
	}
	err := db.Raw(`SELECT enabled,public_base_url,digest_interval_min FROM ai_notification_settings WHERE tenant_id=?::uuid AND owner_user_id=?`, assets.DefaultTenantID, user).Row().Scan(&out.Enabled, &out.PublicBaseURL, &out.DigestIntervalMin)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return out, err
}

func validateSettings(in *Settings) error {
	in.PublicBaseURL = strings.TrimSpace(in.PublicBaseURL)
	if in.DigestIntervalMin < 1 || in.DigestIntervalMin > 10080 || len(in.PublicBaseURL) > 2048 {
		return ErrInvalid
	}
	if in.PublicBaseURL != "" {
		u, err := url.Parse(in.PublicBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ErrInvalid
		}
		in.PublicBaseURL = strings.TrimRight(in.PublicBaseURL, "/")
	}
	return nil
}

func (s *Store) SaveSettings(ctx context.Context, user int, in Settings) error {
	if err := validateSettings(&in); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.requireMember(tx, user); err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO ai_notification_settings(tenant_id,owner_user_id,enabled,public_base_url,digest_interval_min) VALUES(?::uuid,?,?,?,?) ON CONFLICT(tenant_id,owner_user_id) DO UPDATE SET enabled=excluded.enabled,public_base_url=excluded.public_base_url,digest_interval_min=excluded.digest_interval_min,updated_at=clock_timestamp()`, assets.DefaultTenantID, user, in.Enabled, in.PublicBaseURL, in.DigestIntervalMin).Error
	})
}

func (s *Store) PatchSettings(ctx context.Context, user int, patch SettingsPatch) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.requireMember(tx, user); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO ai_notification_settings(tenant_id,owner_user_id) VALUES(?::uuid,?) ON CONFLICT DO NOTHING`, assets.DefaultTenantID, user).Error; err != nil {
			return err
		}
		var in Settings
		if err := tx.Raw(`SELECT enabled,public_base_url,digest_interval_min FROM ai_notification_settings WHERE tenant_id=?::uuid AND owner_user_id=? FOR UPDATE`, assets.DefaultTenantID, user).Row().Scan(&in.Enabled, &in.PublicBaseURL, &in.DigestIntervalMin); err != nil {
			return err
		}
		if patch.Enabled != nil {
			in.Enabled = *patch.Enabled
		}
		if patch.PublicBaseURL != nil {
			in.PublicBaseURL = *patch.PublicBaseURL
		}
		if patch.DigestIntervalMin != nil {
			in.DigestIntervalMin = *patch.DigestIntervalMin
		}
		if err := validateSettings(&in); err != nil {
			return err
		}
		return tx.Exec(`UPDATE ai_notification_settings SET enabled=?,public_base_url=?,digest_interval_min=?,updated_at=clock_timestamp() WHERE tenant_id=?::uuid AND owner_user_id=?`, in.Enabled, in.PublicBaseURL, in.DigestIntervalMin, assets.DefaultTenantID, user).Error
	})
}

func (s *Store) Channels(ctx context.Context, user int) ([]Channel, error) {
	db := s.db.WithContext(ctx)
	if err := s.requireMember(db, user); err != nil {
		return nil, err
	}
	rows, err := db.Raw(`SELECT `+channelColumns+` FROM ai_notification_channels WHERE tenant_id=?::uuid AND owner_user_id=? AND deleted_at IS NULL ORDER BY enabled DESC,id`, assets.DefaultTenantID, user).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		row, e := scanChannel(rows)
		if e != nil {
			return nil, e
		}
		item, e := s.present(user, row)
		if e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = s.requireMember(db, user); err != nil {
		return nil, err
	}
	return out, nil
}

var configFields = map[string]map[string]bool{
	"dingtalk": {"webhook": true, "secret": true}, "feishu": {"webhook": true, "secret": true}, "wecom": {"webhook": true},
	"webhook":  {"url": true, "method": true, "headers": true, "body_template": true, "content_type": true},
	"telegram": {"bot_token": true, "chat_id": true, "base_url": true},
	"email":    {"host": true, "port": true, "username": true, "password": true, "from": true, "to": true, "tls": true},
}

func validChannel(item Channel, config map[string]any) error {
	if strings.TrimSpace(item.Name) == "" || len([]rune(item.Name)) > 200 || !utf8.ValidString(item.Name) || strings.ContainsRune(item.Name, '\x00') || (item.Mode != "realtime" && item.Mode != "digest") || item.RatePerMin < 0 || item.RatePerMin > 100000 {
		return ErrInvalid
	}
	provider, ok := notify.Get(item.Kind)
	if !ok {
		return ErrInvalid
	}
	for key := range config {
		if !configFields[item.Kind][key] {
			return ErrInvalid
		}
	}
	raw, err := json.Marshal(config)
	if err != nil || len(raw) > 65536 {
		return ErrInvalid
	}
	if err = provider.Validate(config); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err = item.Filter.Validate(); err != nil || len(item.Filter.TaskIDs) > 500 || len(item.Filter.AssetIDs) > 500 || len(item.Filter.VulnClassInclude) > 100 || len(item.Filter.VulnClassExclude) > 100 {
		return ErrInvalid
	}
	for _, id := range append(append([]int64{}, item.Filter.TaskIDs...), item.Filter.AssetIDs...) {
		if id <= 0 {
			return ErrInvalid
		}
	}
	for _, word := range append(append([]string{}, item.Filter.VulnClassInclude...), item.Filter.VulnClassExclude...) {
		if len(word) > 500 || !utf8.ValidString(word) || strings.ContainsRune(word, '\x00') {
			return ErrInvalid
		}
	}
	return nil
}

func (s *Store) SaveChannel(ctx context.Context, user int, id int64, in ChannelInput) (Channel, error) {
	var out Channel
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.requireMember(tx, user); err != nil {
			return err
		}
		item := Channel{Mode: "realtime"}
		stored := map[string]any{}
		oldEnabled := false
		oldKind := ""
		var oldConfig []byte
		if id > 0 {
			row, err := scanChannel(tx.Raw(`SELECT `+channelColumns+` FROM ai_notification_channels WHERE id=? AND tenant_id=?::uuid AND owner_user_id=? AND deleted_at IS NULL FOR UPDATE`, id, assets.DefaultTenantID, user).Row())
			if err != nil {
				return err
			}
			if in.Version == nil || *in.Version != row.Version {
				return ErrConflict
			}
			stored, err = s.decrypt(user, row)
			if err != nil {
				return err
			}
			oldConfig, _ = json.Marshal(stored)
			oldEnabled = row.Enabled
			oldKind = row.Kind
			item = Channel{ID: row.ID, Name: row.Name, Kind: row.Kind, Enabled: row.Enabled, Mode: row.Mode, Filter: notify.ParseFilter(row.RawFilter), RatePerMin: row.RatePerMin, Version: row.Version}
		}
		if in.Name != nil {
			item.Name = strings.TrimSpace(*in.Name)
		}
		if in.Kind != nil {
			item.Kind = *in.Kind
		}
		if in.Enabled != nil {
			item.Enabled = *in.Enabled
		}
		if in.Mode != nil {
			item.Mode = *in.Mode
		}
		if in.Filter != nil {
			item.Filter = *in.Filter
		}
		provider, ok := notify.Get(item.Kind)
		if !ok {
			return ErrInvalid
		}
		if in.RatePerMin != nil {
			item.RatePerMin = *in.RatePerMin
		} else if id == 0 {
			item.RatePerMin = provider.DefaultRatePerMin()
		}
		var config map[string]any
		if id == 0 || oldKind != item.Kind {
			config = map[string]any{}
			for key, value := range in.Config {
				config[key] = value
			}
			encoded, err := json.Marshal(config)
			if err != nil || strings.Contains(string(encoded), notify.MaskedPrefix) {
				return ErrInvalid
			}
		} else {
			var err error
			config, err = notify.PrepareConfigUpdate(item.Kind, stored, in.Config)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalid, err)
			}
		}
		if err := validChannel(item, config); err != nil {
			return err
		}
		raw, err := json.Marshal(config)
		if err != nil {
			return ErrInvalid
		}
		filter, err := json.Marshal(item.Filter)
		if err != nil {
			return ErrInvalid
		}
		if id == 0 {
			err = tx.Raw(`INSERT INTO ai_notification_channels(tenant_id,owner_user_id,name,kind,enabled,enabled_at,mode,config_cipher,filter,rate_per_min) VALUES(?::uuid,?,?,?,?,CASE WHEN ? THEN clock_timestamp() ELSE NULL END,?,?::bytea,?::jsonb,?) RETURNING id`, assets.DefaultTenantID, user, item.Name, item.Kind, item.Enabled, item.Enabled, item.Mode, []byte{}, string(filter), item.RatePerMin).Row().Scan(&id)
			if err != nil {
				return err
			}
		}
		cipher, err := s.codec.Seal(secretScope(user, id), string(raw))
		if err != nil {
			return err
		}
		configChanged := oldKind != "" && (oldKind != item.Kind || string(oldConfig) != string(raw))
		if err = tx.Exec(`UPDATE ai_notification_channels SET name=?,kind=?,enabled=?,enabled_at=CASE WHEN ? AND (NOT enabled OR ? OR enabled_at IS NULL) THEN clock_timestamp() WHEN NOT ? THEN NULL ELSE enabled_at END,mode=?,config_cipher=?,filter=?::jsonb,rate_per_min=?,version=version+1,updated_at=clock_timestamp() WHERE id=?`, item.Name, item.Kind, item.Enabled, item.Enabled, configChanged, item.Enabled, item.Mode, cipher, string(filter), item.RatePerMin, id).Error; err != nil {
			return err
		}
		if id > 0 && (!item.Enabled && oldEnabled || configChanged) {
			if err = tx.Exec(`UPDATE ai_notification_deliveries SET state='skipped',lease_token=NULL,last_error='channel changed or disabled' WHERE channel_id=? AND state IN ('pending','sending')`, id).Error; err != nil {
				return err
			}
		}
		row, err := scanChannel(tx.Raw(`SELECT `+channelColumns+` FROM ai_notification_channels WHERE id=?`, id).Row())
		if err != nil {
			return err
		}
		out, err = s.present(user, row)
		return err
	})
	return out, err
}

func (s *Store) DeleteChannel(ctx context.Context, user int, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.requireMember(tx, user); err != nil {
			return err
		}
		result := tx.Exec(`UPDATE ai_notification_channels SET deleted_at=clock_timestamp(),enabled=false,enabled_at=NULL,config_cipher=''::bytea,version=version+1,updated_at=clock_timestamp() WHERE id=? AND tenant_id=?::uuid AND owner_user_id=? AND deleted_at IS NULL`, id, assets.DefaultTenantID, user)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrNotFound
		}
		return tx.Exec(`UPDATE ai_notification_deliveries SET state='skipped',lease_token=NULL,last_error='channel deleted' WHERE channel_id=? AND state IN ('pending','sending')`, id).Error
	})
}
