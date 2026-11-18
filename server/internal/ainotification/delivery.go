package ainotification

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Autumn-27/artex/notify"
	"gorm.io/gorm"
)

const deliveryLease = 3 * time.Minute
const deliveryAttempts = 3

type claimedDelivery struct {
	ID       int64
	EventID  string
	Snapshot notify.Snapshot
}
type deliveryClaim struct {
	Channel                channelRecord
	User                   int
	Tenant, Token, BaseURL string
	DigestMinutes          int
	Items                  []claimedDelivery
}

func newToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// ClaimNext serializes sends per channel in PostgreSQL, so both the rate limit
// and the lease continue to work when more than one Server is running.
func (s *Store) ClaimNext(ctx context.Context) (*deliveryClaim, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	var claim *deliveryClaim
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE ai_notification_deliveries SET state='failed',lease_token=NULL,last_error='delivery attempts exhausted' WHERE id IN (SELECT id FROM ai_notification_deliveries WHERE state='sending' AND next_attempt_at<=clock_timestamp() AND attempts>=? ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED)`, deliveryAttempts).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE ai_notification_deliveries d SET state='skipped',lease_token=NULL,last_error='task unavailable' WHERE d.id IN (SELECT pending.id FROM ai_notification_deliveries pending JOIN ai_notification_events e ON e.event_id=pending.event_id WHERE pending.state='pending' AND NOT tianqiong_ai.outbound_notification_visible(e.event_id,e.owner_user_id) ORDER BY pending.id LIMIT 100 FOR UPDATE OF pending SKIP LOCKED)`).Error; err != nil {
			return err
		}
		var row channelRecord
		var user int
		var tenant, base string
		var digest int
		err := tx.Raw(`SELECT c.id,c.name,c.kind,c.enabled,c.mode,c.config_cipher,c.filter,c.rate_per_min,c.version,c.created_at,c.updated_at,c.owner_user_id,c.tenant_id::text,COALESCE(pref.public_base_url,''),COALESCE(pref.digest_interval_min,30)
 FROM ai_notification_channels c LEFT JOIN ai_notification_settings pref ON pref.tenant_id=c.tenant_id AND pref.owner_user_id=c.owner_user_id
 WHERE c.enabled AND c.deleted_at IS NULL AND COALESCE(pref.enabled,TRUE) AND c.next_send_at<=clock_timestamp()
 AND NOT EXISTS(SELECT 1 FROM ai_notification_deliveries active WHERE active.channel_id=c.id AND active.state='sending' AND active.next_attempt_at>clock_timestamp())
 AND EXISTS(SELECT 1 FROM ai_notification_deliveries d JOIN ai_notification_events e ON e.event_id=d.event_id WHERE d.channel_id=c.id AND e.tenant_id=c.tenant_id AND e.owner_user_id=c.owner_user_id AND d.state IN ('pending','sending') AND d.attempts<? AND d.next_attempt_at<=clock_timestamp()
  AND tianqiong_ai.outbound_notification_visible(e.event_id,e.owner_user_id)
  AND (c.mode='realtime' OR d.created_at<=clock_timestamp()-make_interval(mins=>COALESCE(pref.digest_interval_min,30))))
 ORDER BY c.next_send_at,c.id FOR UPDATE OF c SKIP LOCKED LIMIT 1`, deliveryAttempts).Row().Scan(&row.ID, &row.Name, &row.Kind, &row.Enabled, &row.Mode, &row.Cipher, &row.RawFilter, &row.RatePerMin, &row.Version, &row.CreatedAt, &row.UpdatedAt, &user, &tenant, &base, &digest)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		limit := 1
		if row.Mode == "digest" {
			limit = 100
		}
		rows, err := tx.Raw(`SELECT d.id,d.event_id,e.snapshot FROM ai_notification_deliveries d JOIN ai_notification_events e ON e.event_id=d.event_id WHERE d.channel_id=? AND e.tenant_id=?::uuid AND e.owner_user_id=? AND d.state IN ('pending','sending') AND d.attempts<? AND d.next_attempt_at<=clock_timestamp() AND tianqiong_ai.outbound_notification_visible(e.event_id,e.owner_user_id) ORDER BY d.created_at,d.id FOR UPDATE OF d SKIP LOCKED LIMIT ?`, row.ID, tenant, user, deliveryAttempts, limit).Rows()
		if err != nil {
			return err
		}
		items := []claimedDelivery{}
		for rows.Next() {
			var item claimedDelivery
			var raw []byte
			if err = rows.Scan(&item.ID, &item.EventID, &raw); err != nil {
				rows.Close()
				return err
			}
			item.Snapshot, err = decodeSnapshot(raw)
			if err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		rawIDs, _ := json.Marshal(ids)
		if err = tx.Exec(`UPDATE ai_notification_deliveries SET state='sending',attempts=attempts+1,lease_token=?,next_attempt_at=clock_timestamp()+make_interval(secs=>?),batch_id=? WHERE id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb))`, token, int64(deliveryLease.Seconds()), token, string(rawIDs)).Error; err != nil {
			return err
		}
		interval := time.Duration(0)
		if row.RatePerMin > 0 {
			interval = time.Minute / time.Duration(row.RatePerMin)
		}
		if err = tx.Exec(`UPDATE ai_notification_channels SET next_send_at=clock_timestamp()+(? * interval '1 microsecond') WHERE id=?`, interval.Microseconds(), row.ID).Error; err != nil {
			return err
		}
		claim = &deliveryClaim{Channel: row, User: user, Tenant: tenant, Token: token, BaseURL: base, DigestMinutes: digest, Items: items}
		return nil
	})
	return claim, err
}

// Recheck the claim immediately before sending. Revoked tasks are skipped;
// temporarily disabled account settings return the delivery to the queue.
func (s *Store) EligibleClaim(ctx context.Context, claim *deliveryClaim) ([]claimedDelivery, error) {
	items := []claimedDelivery{}
	for _, item := range claim.Items {
		var visible, enabled, accountEnabled bool
		err := s.db.WithContext(ctx).Raw(`SELECT tianqiong_ai.outbound_notification_visible(e.event_id,e.owner_user_id),c.enabled AND c.deleted_at IS NULL AND c.version=?,COALESCE(pref.enabled,TRUE)
 FROM ai_notification_deliveries d JOIN ai_notification_events e ON e.event_id=d.event_id JOIN ai_notification_channels c ON c.id=d.channel_id LEFT JOIN ai_notification_settings pref ON pref.tenant_id=c.tenant_id AND pref.owner_user_id=c.owner_user_id
 WHERE d.id=? AND e.tenant_id=c.tenant_id AND e.owner_user_id=c.owner_user_id AND d.state='sending' AND d.lease_token=? AND d.next_attempt_at>clock_timestamp()`, claim.Channel.Version, item.ID, claim.Token).Row().Scan(&visible, &enabled, &accountEnabled)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !visible || !enabled {
			if err = s.db.WithContext(ctx).Exec(`UPDATE ai_notification_deliveries SET state='skipped',lease_token=NULL,last_error='task or channel unavailable' WHERE id=? AND state='sending' AND lease_token=?`, item.ID, claim.Token).Error; err != nil {
				return nil, err
			}
			continue
		}
		if !accountEnabled {
			if err = s.db.WithContext(ctx).Exec(`UPDATE ai_notification_deliveries SET state='pending',attempts=GREATEST(0,attempts-1),lease_token=NULL,next_attempt_at=clock_timestamp() WHERE id=? AND state='sending' AND lease_token=?`, item.ID, claim.Token).Error; err != nil {
				return nil, err
			}
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) FinishClaim(ctx context.Context, claim *deliveryClaim, items []claimedDelivery, kept int, sendErr error) error {
	if kept < 0 || kept > len(items) {
		kept = 0
		sendErr = errors.New("invalid delivery count")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for index, item := range items {
			query := `UPDATE ai_notification_deliveries SET state='sent',sent_at=clock_timestamp(),lease_token=NULL,last_error='' WHERE id=? AND state='sending' AND lease_token=?`
			if sendErr == nil && index < kept {
				if err := tx.Exec(query, item.ID, claim.Token).Error; err != nil {
					return err
				}
				continue
			}
			if sendErr == nil {
				if err := tx.Exec(`UPDATE ai_notification_deliveries SET state='pending',attempts=GREATEST(0,attempts-1),next_attempt_at=clock_timestamp(),lease_token=NULL WHERE id=? AND state='sending' AND lease_token=?`, item.ID, claim.Token).Error; err != nil {
					return err
				}
				continue
			}
			permanent := notify.IsPermanent(sendErr)
			if err := tx.Exec(`UPDATE ai_notification_deliveries SET state=CASE WHEN ? OR attempts>=? THEN 'failed' ELSE 'pending' END,next_attempt_at=clock_timestamp()+make_interval(secs=>CASE WHEN attempts<=1 THEN 60 ELSE 300 END),lease_token=NULL,last_error=CASE WHEN ? THEN 'permanent delivery failure' ELSE 'temporary delivery failure' END WHERE id=? AND state='sending' AND lease_token=?`, permanent, deliveryAttempts, permanent, item.ID, claim.Token).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) assetNames(ctx context.Context, claim *deliveryClaim, item claimedDelivery) ([]string, error) {
	if item.Snapshot.TaskID == 0 || len(item.Snapshot.AssetIDs) == 0 {
		return []string{}, nil
	}
	raw, _ := json.Marshal(item.Snapshot.AssetIDs)
	rows, err := s.db.WithContext(ctx).Raw(`SELECT a.label FROM asset_entity a JOIN ai_task t ON t.id=? AND t.tenant_id=a.tenant_id WHERE a.tenant_id=?::uuid AND a.id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)) AND tianqiong_ai.outbound_notification_visible(?,?) AND (t.target_id IS NULL OR
 EXISTS(SELECT 1 FROM asset_website_binding b JOIN website x ON x.id=b.website_id WHERE b.asset_id=a.id AND x.target_id=t.target_id) OR
 EXISTS(SELECT 1 FROM asset_endpoint_binding b JOIN endpoint x ON x.id=b.endpoint_id WHERE b.asset_id=a.id AND x.target_id=t.target_id) OR
 EXISTS(SELECT 1 FROM asset_subdomain_binding b JOIN subdomain x ON x.id=b.subdomain_id WHERE b.asset_id=a.id AND x.target_id=t.target_id) OR
 EXISTS(SELECT 1 FROM asset_host_port_binding b JOIN host_port_mapping x ON x.id=b.host_port_id WHERE b.asset_id=a.id AND x.target_id=t.target_id) OR
 EXISTS(SELECT 1 FROM asset_observation o WHERE o.asset_id=a.id AND o.tenant_id=t.tenant_id AND o.target_id=t.target_id) OR
 EXISTS(SELECT 1 FROM asset_application_visible_website w JOIN website x ON x.id=w.website_id WHERE w.tenant_id=t.tenant_id AND w.application_id=a.id AND x.target_id=t.target_id) OR
 EXISTS(SELECT 1 FROM asset_root_domain_target r JOIN target x ON x.id=r.target_id AND x.deleted_at IS NULL WHERE r.tenant_id=t.tenant_id AND r.root_asset_id=a.id AND r.target_id=t.target_id AND x.type='domain' AND x.name=a.label AND a.asset_type='root_domain')) ORDER BY a.id`, item.Snapshot.TaskID, claim.Tenant, string(raw), item.EventID, claim.User).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var label string
		if err = rows.Scan(&label); err != nil {
			return nil, err
		}
		out = append(out, label)
	}
	return out, rows.Err()
}

func (s *Store) message(ctx context.Context, claim *deliveryClaim, items []claimedDelivery) (notify.Message, error) {
	out := notify.Message{Items: make([]notify.Item, 0, len(items)), Batch: claim.Channel.Mode == "digest", WindowMinutes: claim.DigestMinutes, HomeURL: claim.BaseURL}
	for _, item := range items {
		names, err := s.assetNames(ctx, claim, item)
		if err != nil {
			return notify.Message{}, err
		}
		detail := ""
		if claim.BaseURL != "" && item.Snapshot.TaskID == 0 {
			detail = fmt.Sprintf("%s/vulnerabilities/%d/", claim.BaseURL, item.Snapshot.FindingID)
		} else if claim.BaseURL != "" {
			detail = fmt.Sprintf("%s/ai/tasks/%d/?tab=findings&finding=%d", claim.BaseURL, item.Snapshot.TaskID, item.Snapshot.FindingID)
		}
		out.Items = append(out.Items, notify.Item{FindingID: item.Snapshot.FindingID, Name: item.Snapshot.Name, VulnClass: item.Snapshot.VulnClass, Severity: item.Snapshot.Severity, Summary: item.Snapshot.Summary, Assets: names, DetailURL: detail, FromStatus: item.Snapshot.FromStatus, ToStatus: item.Snapshot.ToStatus})
	}
	return out, nil
}
