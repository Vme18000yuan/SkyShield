package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lib/pq"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type rootProjectionItem struct {
	ID          int64
	TenantID    string
	AssetType   string
	Label       string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	DeletedAt   *time.Time
}
type rootQueueItem struct {
	ID         int64
	SourceKind string
	SourceID   int64
}
type rootCandidate struct {
	kind  string
	item  rootProjectionItem
	root  string
	valid bool
}
type rootAggregate struct {
	tenant, root string
	first, last  time.Time
	id           int64
}

// ProcessRootDomainBatch advances one bounded historical page and a bounded
// event page in the same transaction. The triggers enqueue future changes.
func (s *AssetRegistryStore) ProcessRootDomainBatch(ctx context.Context, batchSize int) (bool, error) {
	if batchSize < 1 || batchSize > 1000 {
		return false, errors.New("invalid root domain batch size")
	}
	worked := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A single projection writer prevents a historical page from racing a
		// newer queue event on another server and overwriting its mapping.
		var locked bool
		if err := tx.Raw(`SELECT pg_try_advisory_xact_lock(?::integer,?::integer)`, 0x524f4f54, 0x444f4d41).Scan(&locked).Error; err != nil {
			return err
		}
		if !locked {
			return nil
		}
		historyLimit := max(1, batchSize/2)
		var kind string
		var lastID int64
		err := tx.Raw(`SELECT source_kind,last_id FROM asset_root_projection_progress WHERE completed=FALSE ORDER BY CASE source_kind WHEN 'target' THEN 0 ELSE 1 END FOR UPDATE SKIP LOCKED LIMIT 1`).Row().Scan(&kind, &lastID)
		var history []rootProjectionItem
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			worked = true
			var query *gorm.DB
			if kind == "target" {
				query = tx.Raw(`SELECT id,?::text AS tenant_id,type AS asset_type,name AS label,created_at AS first_seen_at,created_at AS last_seen_at,deleted_at FROM target WHERE id>? ORDER BY id LIMIT ?`, assetapp.DefaultTenantID, lastID, historyLimit).Scan(&history)
			} else if kind == "asset" {
				query = tx.Raw(`SELECT id,tenant_id::text,asset_type,label,first_seen_at,last_seen_at,NULL::timestamptz AS deleted_at FROM asset_entity WHERE id>? AND asset_type IN ('subdomain','service','endpoint') ORDER BY id LIMIT ?`, lastID, historyLimit).Scan(&history)
			} else {
				return fmt.Errorf("unknown root projection history kind %q", kind)
			}
			if query.Error != nil {
				return query.Error
			}
			nextID := lastID
			if len(history) > 0 {
				nextID = history[len(history)-1].ID
			}
			if err := tx.Exec(`UPDATE asset_root_projection_progress SET last_id=?,completed=?,updated_at=CURRENT_TIMESTAMP WHERE source_kind=?`, nextID, len(history) < historyLimit, kind).Error; err != nil {
				return err
			}
		}
		var queued []rootQueueItem
		if err := tx.Raw(`SELECT id,source_kind,source_id FROM asset_root_projection_queue ORDER BY id FOR UPDATE SKIP LOCKED LIMIT ?`, batchSize-historyLimit+1).Scan(&queued).Error; err != nil {
			return err
		}
		if len(queued) > 0 {
			worked = true
		}

		// A source can be present in both pages. Process it once and keep the
		// queue row locked until its mapping and cursor are committed.
		candidates := make(map[string]rootCandidate, len(history)+len(queued))
		key := func(k string, id int64) string { return fmt.Sprintf("%s/%d", k, id) }
		for _, item := range history {
			candidates[key(kind, item.ID)] = rootCandidate{kind: kind, item: item}
		}
		var assets, targets []int64
		for _, entry := range queued {
			if entry.SourceKind == "asset" {
				assets = append(assets, entry.SourceID)
			} else if entry.SourceKind == "target" {
				targets = append(targets, entry.SourceID)
			} else {
				return fmt.Errorf("unknown root projection event kind %q", entry.SourceKind)
			}
			candidates[key(entry.SourceKind, entry.SourceID)] = rootCandidate{kind: entry.SourceKind, item: rootProjectionItem{ID: entry.SourceID}}
		}
		if len(assets) > 0 {
			var items []rootProjectionItem
			if err := tx.Raw(`SELECT id,tenant_id::text,asset_type,label,first_seen_at,last_seen_at,NULL::timestamptz AS deleted_at FROM asset_entity WHERE id=ANY(?::bigint[])`, pq.Int64Array(assets)).Scan(&items).Error; err != nil {
				return err
			}
			for _, item := range items {
				candidates[key("asset", item.ID)] = rootCandidate{kind: "asset", item: item}
			}
		}
		if len(targets) > 0 {
			var items []rootProjectionItem
			if err := tx.Raw(`SELECT id,?::text AS tenant_id,type AS asset_type,name AS label,created_at AS first_seen_at,created_at AS last_seen_at,deleted_at FROM target WHERE id=ANY(?::bigint[])`, assetapp.DefaultTenantID, pq.Int64Array(targets)).Scan(&items).Error; err != nil {
				return err
			}
			for _, item := range items {
				candidates[key("target", item.ID)] = rootCandidate{kind: "target", item: item}
			}
		}

		roots := map[string]*rootAggregate{}
		for k, candidate := range candidates {
			rootKind := candidate.item.AssetType
			if candidate.kind == "target" {
				if rootKind != "domain" || candidate.item.DeletedAt != nil {
					continue
				}
				rootKind = "target"
			}
			root, ok := assetapp.RegistrableRootDomain(rootKind, candidate.item.Label)
			if !ok || candidate.item.TenantID == "" {
				continue
			}
			candidate.root, candidate.valid = root, true
			candidates[k] = candidate
			rootKey := candidate.item.TenantID + "/" + root
			if group := roots[rootKey]; group != nil {
				if candidate.item.FirstSeenAt.Before(group.first) {
					group.first = candidate.item.FirstSeenAt
				}
				if candidate.item.LastSeenAt.After(group.last) {
					group.last = candidate.item.LastSeenAt
				}
			} else {
				roots[rootKey] = &rootAggregate{tenant: candidate.item.TenantID, root: root, first: candidate.item.FirstSeenAt, last: candidate.item.LastSeenAt}
			}
		}
		rootKeys := make([]string, 0, len(roots))
		for k := range roots {
			rootKeys = append(rootKeys, k)
		}
		sort.Strings(rootKeys)
		for _, k := range rootKeys {
			group := roots[k]
			if err := tx.Raw(`SELECT tq_register_asset(?::uuid,'root_domain',?,NULL,?)`, group.tenant, group.root, group.first).Row().Scan(&group.id); err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE asset_entity SET last_seen_at=GREATEST(last_seen_at,?) WHERE tenant_id=? AND id=?`, group.last, group.tenant, group.id).Error; err != nil {
				return err
			}
		}
		candidateKeys := make([]string, 0, len(candidates))
		for k := range candidates {
			candidateKeys = append(candidateKeys, k)
		}
		sort.Strings(candidateKeys)
		for _, k := range candidateKeys {
			candidate := candidates[k]
			if !candidate.valid {
				if candidate.kind == "target" {
					if err := tx.Exec(`DELETE FROM asset_root_domain_target WHERE target_id=?`, candidate.item.ID).Error; err != nil {
						return err
					}
				} else {
					if err := tx.Exec(`DELETE FROM asset_root_domain_source WHERE source_asset_id=?`, candidate.item.ID).Error; err != nil {
						return err
					}
				}
				continue
			}
			rootID := roots[candidate.item.TenantID+"/"+candidate.root].id
			if candidate.kind == "target" {
				if err := tx.Exec(`INSERT INTO asset_root_domain_target(tenant_id,target_id,root_asset_id) VALUES(?,?,?) ON CONFLICT(target_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,root_asset_id=EXCLUDED.root_asset_id`, candidate.item.TenantID, candidate.item.ID, rootID).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Exec(`INSERT INTO asset_root_domain_source(tenant_id,source_asset_id,root_asset_id) VALUES(?,?,?) ON CONFLICT(source_asset_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,root_asset_id=EXCLUDED.root_asset_id`, candidate.item.TenantID, candidate.item.ID, rootID).Error; err != nil {
					return err
				}
			}
		}
		if len(queued) > 0 {
			ids := make([]int64, 0, len(queued))
			for _, entry := range queued {
				ids = append(ids, entry.ID)
			}
			if err := tx.Exec(`DELETE FROM asset_root_projection_queue WHERE id=ANY(?::bigint[])`, pq.Int64Array(ids)).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return worked, err
}

var _ assetapp.RootDomainStore = (*AssetRegistryStore)(nil)
