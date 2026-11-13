package ainotification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/notify"
	"gorm.io/gorm"
)

// decodeSnapshot accepts both the string IDs used by SkyShield's unified
// assets and the numeric IDs used by ARTEX's original graph.
func decodeSnapshot(raw []byte) (notify.Snapshot, error) {
	var value struct {
		Kind       string            `json:"kind"`
		FindingID  int64             `json:"finding_id"`
		TaskID     int64             `json:"task_id"`
		VulnClass  string            `json:"vulnclass"`
		Name       string            `json:"name"`
		Severity   string            `json:"severity"`
		Summary    string            `json:"summary"`
		AssetIDs   []json.RawMessage `json:"asset_ids"`
		FromStatus string            `json:"from_status"`
		ToStatus   string            `json:"to_status"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return notify.Snapshot{}, err
	}
	if value.TaskID <= 0 || value.FindingID <= 0 || (value.Kind != notify.EventFindingCreated && value.Kind != notify.EventFindingStatusChanged) {
		return notify.Snapshot{}, ErrInvalid
	}
	out := notify.Snapshot{Kind: value.Kind, FindingID: value.FindingID, TaskID: value.TaskID, VulnClass: value.VulnClass, Name: value.Name, Severity: value.Severity, Summary: value.Summary, FromStatus: value.FromStatus, ToStatus: value.ToStatus}
	for _, rawID := range value.AssetIDs {
		var number json.Number
		if err := json.Unmarshal(rawID, &number); err != nil {
			continue
		}
		id, err := strconv.ParseInt(string(number), 10, 64)
		if err == nil && id > 0 {
			out.AssetIDs = append(out.AssetIDs, id)
		}
	}
	return out, nil
}

// FanOutOne freezes the channel set once per event. Disabled settings leave
// events queued, and channels only receive events after their latest enable.
func (s *Store) FanOutOne(ctx context.Context) (bool, error) {
	processed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var eventID, tenant string
		var user int
		var created time.Time
		var raw []byte
		var visible bool
		err := tx.Raw(`SELECT e.event_id,e.tenant_id::text,e.owner_user_id,e.created_at,e.snapshot,tianqiong_ai.outbound_notification_visible(e.event_id,e.owner_user_id)
 FROM ai_notification_events e LEFT JOIN ai_notification_settings pref ON pref.tenant_id=e.tenant_id AND pref.owner_user_id=e.owner_user_id
 WHERE e.fanned_at IS NULL AND COALESCE(pref.enabled,TRUE) ORDER BY e.created_at,e.event_id FOR UPDATE OF e SKIP LOCKED LIMIT 1`).Row().Scan(&eventID, &tenant, &user, &created, &raw, &visible)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		processed = true
		if visible {
			snapshot, err := decodeSnapshot(raw)
			if err != nil {
				return err
			}
			rows, err := tx.Raw(`SELECT id,filter FROM ai_notification_channels WHERE tenant_id=?::uuid AND owner_user_id=? AND enabled AND deleted_at IS NULL AND created_at<=? AND enabled_at<=? ORDER BY id FOR SHARE`, tenant, user, created, created).Rows()
			if err != nil {
				return err
			}
			ids := []int64{}
			for rows.Next() {
				var id int64
				var filterRaw []byte
				if err = rows.Scan(&id, &filterRaw); err != nil {
					rows.Close()
					return err
				}
				var filter notify.Filter
				if err = json.Unmarshal(filterRaw, &filter); err != nil || filter.Validate() != nil {
					rows.Close()
					return errors.New("invalid stored notification filter")
				}
				if notify.Match(filter, snapshot) {
					ids = append(ids, id)
				}
			}
			if err = rows.Err(); err != nil {
				rows.Close()
				return err
			}
			if err = rows.Close(); err != nil {
				return err
			}
			for _, id := range ids {
				if err = tx.Exec(`INSERT INTO ai_notification_deliveries(event_id,channel_id) VALUES(?,?) ON CONFLICT(event_id,channel_id) DO NOTHING`, eventID, id).Error; err != nil {
					return err
				}
			}
		}
		return tx.Exec(`UPDATE ai_notification_events SET fanned_at=clock_timestamp() WHERE event_id=?`, eventID).Error
	})
	return processed, err
}
