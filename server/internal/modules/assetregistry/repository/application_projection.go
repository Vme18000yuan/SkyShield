package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lib/pq"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type applicationWebsite struct {
	ID          int64
	TargetID    int
	URL         string
	Title       string
	Tech        pq.StringArray `gorm:"type:text[]"`
	StatusCode  *int64
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}
type applicationQueueItem struct {
	ID        int64
	WebsiteID int64
}

func (s *AssetRegistryStore) ProcessApplicationBatch(ctx context.Context, batchSize int) (bool, error) {
	if batchSize < 1 || batchSize > 1000 {
		return false, errors.New("invalid application projection batch size")
	}
	worked := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked bool
		if err := tx.Raw(`SELECT pg_try_advisory_xact_lock(?::integer,?::integer)`, 0x41505053, 0x57454253).Scan(&locked).Error; err != nil {
			return err
		}
		if !locked {
			return nil
		}
		historyLimit := max(1, batchSize/2)
		var lastID int64
		err := tx.Raw(`SELECT last_website_id FROM asset_application_projection_progress WHERE id=TRUE AND completed=FALSE FOR UPDATE SKIP LOCKED`).Row().Scan(&lastID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var history []struct{ ID int64 }
		if err == nil {
			worked = true
			if e := tx.Raw(`SELECT id FROM website WHERE id>? ORDER BY id LIMIT ?`, lastID, historyLimit).Scan(&history).Error; e != nil {
				return e
			}
			next := lastID
			if len(history) > 0 {
				next = history[len(history)-1].ID
			}
			if e := tx.Exec(`UPDATE asset_application_projection_progress SET last_website_id=?,completed=?,updated_at=CURRENT_TIMESTAMP WHERE id=TRUE`, next, len(history) < historyLimit).Error; e != nil {
				return e
			}
		}
		manualWorked, err := enqueueManualApplicationSites(tx, historyLimit)
		if err != nil {
			return err
		}
		worked = worked || manualWorked
		targetWorked, err := enqueueTargetApplicationSites(tx, historyLimit)
		if err != nil {
			return err
		}
		worked = worked || targetWorked
		var queued []applicationQueueItem
		if err := tx.Raw(`SELECT id,website_id FROM asset_application_projection_queue ORDER BY id FOR UPDATE SKIP LOCKED LIMIT ?`, batchSize-historyLimit+1).Scan(&queued).Error; err != nil {
			return err
		}
		if len(queued) > 0 {
			worked = true
		}
		candidates := map[int64]bool{}
		for _, v := range history {
			candidates[v.ID] = true
		}
		for _, v := range queued {
			candidates[v.WebsiteID] = true
		}
		ids := make([]int64, 0, len(candidates))
		for id := range candidates {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var websites []applicationWebsite
		if len(ids) > 0 {
			if err := tx.Raw(`SELECT w.id,w.target_id,w.url,COALESCE(w.title,'') title,COALESCE(w.tech,'{}'::varchar[]) tech,w.status_code,
			 COALESCE(site.first_seen_at,w.created_at) first_seen_at,COALESCE(site.last_seen_at,w.created_at) last_seen_at
			 FROM website w LEFT JOIN asset_website_binding b ON b.website_id=w.id LEFT JOIN asset_entity site ON site.id=b.asset_id WHERE w.id=ANY(?::bigint[]) ORDER BY w.id`, pq.Int64Array(ids)).Scan(&websites).Error; err != nil {
				return err
			}
		}
		byID := make(map[int64]applicationWebsite, len(websites))
		for _, website := range websites {
			byID[website.ID] = website
		}
		type group struct {
			id          int64
			first, last time.Time
		}
		groups := map[string]*group{}
		for _, id := range ids {
			website, exists := byID[id]
			if !exists {
				continue
			}
			candidate, ok := assetapp.RecognizeWebApplication(website.URL, website.Title, []string(website.Tech), website.StatusCode)
			if !ok {
				if err := tx.Exec(`DELETE FROM asset_application_website WHERE website_id=?`, id).Error; err != nil {
					return err
				}
				continue
			}
			manualID, manualObservationID, err := explicitApplicationForWebsite(tx, website.TargetID, candidate.Scope, candidate.IdentityName)
			if err != nil {
				return err
			}
			identity, err := json.Marshal([]string{"app", "web", candidate.Scope, candidate.IdentityName})
			if err != nil {
				return err
			}
			key := "auto:" + string(identity)
			if manualID > 0 {
				key = fmt.Sprintf("manual:%d", manualID)
			}
			current := groups[key]
			if current == nil {
				current = &group{first: website.FirstSeenAt, last: website.LastSeenAt}
				if manualID > 0 {
					current.id = manualID
				} else if err := tx.Raw(`SELECT tq_register_asset_v2(?::uuid,'app',?::jsonb::text,?,NULL,NULL,?)`, assetapp.DefaultTenantID, string(identity), candidate.Name, website.FirstSeenAt).Row().Scan(&current.id); err != nil {
					return fmt.Errorf("register identified application: %w", err)
				}
				groups[key] = current
			} else {
				if website.FirstSeenAt.Before(current.first) {
					current.first = website.FirstSeenAt
				}
				if website.LastSeenAt.After(current.last) {
					current.last = website.LastSeenAt
				}
			}
			var observationID any
			if manualObservationID > 0 {
				observationID = manualObservationID
			}
			if err := tx.Exec(`INSERT INTO asset_application_website(tenant_id,website_id,application_id,scope,manual_observation_id,evidence_kind,evidence_value) VALUES(?::uuid,?,?,?,?,?,?) ON CONFLICT(website_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,application_id=EXCLUDED.application_id,scope=EXCLUDED.scope,manual_observation_id=EXCLUDED.manual_observation_id,evidence_kind=EXCLUDED.evidence_kind,evidence_value=EXCLUDED.evidence_value`, assetapp.DefaultTenantID, id, current.id, candidate.Scope, observationID, candidate.EvidenceKind, candidate.EvidenceValue).Error; err != nil {
				return err
			}
		}
		for _, group := range groups {
			if err := tx.Exec(`UPDATE asset_entity SET first_seen_at=LEAST(first_seen_at,?),last_seen_at=GREATEST(last_seen_at,?) WHERE tenant_id=?::uuid AND id=?`, group.first, group.last, assetapp.DefaultTenantID, group.id).Error; err != nil {
				return err
			}
		}
		if len(queued) > 0 {
			queueIDs := make([]int64, 0, len(queued))
			for _, item := range queued {
				queueIDs = append(queueIDs, item.ID)
			}
			if err := tx.Exec(`DELETE FROM asset_application_projection_queue WHERE id=ANY(?::bigint[])`, pq.Int64Array(queueIDs)).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return worked, err
}

var _ assetapp.ApplicationProjectionStore = (*AssetRegistryStore)(nil)
