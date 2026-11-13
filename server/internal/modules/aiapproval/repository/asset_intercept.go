package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lib/pq"
	app "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *Store) SeedAssetRules(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('artex-asset-intercept-defaults',0))`).Error; err != nil {
			return err
		}
		var done bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_runtime_seed WHERE tenant_id=? AND seed_key='artex_asset_intercept_defaults_v1')`, assets.DefaultTenantID).Scan(&done).Error; err != nil || done {
			return err
		}
		for _, rule := range []struct{ pattern, note string }{{".gov", "[内置] 政府网站 (.gov)"}, {".gov.cn", "[内置] 政府网站 (.gov.cn)"}, {".edu", "[内置] 教育网站 (.edu)"}, {".edu.cn", "[内置] 教育网站 (.edu.cn)"}} {
			if err := tx.Exec(`INSERT INTO ai_asset_intercept_rule(tenant_id,enabled,action,kind,pattern,note,builtin) VALUES(?,TRUE,'block','fuzzy_domain',?,?,TRUE)`, assets.DefaultTenantID, rule.pattern, rule.note).Error; err != nil {
				return err
			}
		}
		return tx.Exec(`INSERT INTO ai_runtime_seed(tenant_id,seed_key) VALUES(?,'artex_asset_intercept_defaults_v1')`, assets.DefaultTenantID).Error
	})
}

func assetRuleScope(tx *gorm.DB, user int, taskID int64, write bool) error {
	if taskID == 0 {
		return member(tx, user, true, write)
	}
	if write {
		if err := writer(tx, user); err != nil {
			return err
		}
	} else if err := member(tx, user, false, false); err != nil {
		return err
	}
	query := `SELECT 1 FROM ai_task WHERE id=? AND tenant_id=? AND owner_user_id=? AND deleted_at IS NULL AND (original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=ai_task.target_id AND deleted_at IS NULL))`
	if write {
		query += ` FOR UPDATE`
	}
	var present int
	err := tx.Raw(query, taskID, assets.DefaultTenantID, user).Row().Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return app.ErrNotFound
	}
	return err
}

const assetRuleColumns = `id,enabled,action,kind,pattern,note,builtin,created_at,updated_at`

func (s *Store) ListAssetRules(ctx context.Context, user int, taskID int64) ([]app.AssetRule, error) {
	tx := s.db.WithContext(ctx)
	if err := assetRuleScope(tx, user, taskID, false); err != nil {
		return nil, err
	}
	items := []app.AssetRule{}
	err := tx.Raw(`SELECT `+assetRuleColumns+` FROM ai_asset_intercept_rule WHERE tenant_id=? AND task_id IS NOT DISTINCT FROM NULLIF(?,0) ORDER BY builtin DESC,id DESC`, assets.DefaultTenantID, taskID).Scan(&items).Error
	return items, err
}
func (s *Store) SaveAssetRule(ctx context.Context, user int, taskID int64, rule app.AssetRule, create bool) (app.AssetRule, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assetRuleScope(tx, user, taskID, true); err != nil {
			return err
		}
		var row *sql.Row
		if create {
			row = tx.Raw(`INSERT INTO ai_asset_intercept_rule(tenant_id,task_id,owner_user_id,enabled,action,kind,pattern,note) VALUES(?,NULLIF(?::bigint,0),CASE WHEN ?::bigint=0 THEN NULL ELSE ?::integer END,?,?,?,?,?) RETURNING `+assetRuleColumns, assets.DefaultTenantID, taskID, taskID, user, rule.Enabled, rule.Action, rule.Kind, rule.Pattern, rule.Note).Row()
		} else {
			row = tx.Raw(`UPDATE ai_asset_intercept_rule SET enabled=?,action=?,kind=?,pattern=?,note=?,updated_at=clock_timestamp() WHERE tenant_id=? AND task_id IS NOT DISTINCT FROM NULLIF(?,0) AND id=? RETURNING `+assetRuleColumns, rule.Enabled, rule.Action, rule.Kind, rule.Pattern, rule.Note, assets.DefaultTenantID, taskID, rule.ID).Row()
		}
		err := row.Scan(&rule.ID, &rule.Enabled, &rule.Action, &rule.Kind, &rule.Pattern, &rule.Note, &rule.Builtin, &rule.CreatedAt, &rule.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrNotFound
		}
		return err
	})
	return rule, err
}
func (s *Store) DeleteAssetRule(ctx context.Context, user int, taskID, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assetRuleScope(tx, user, taskID, true); err != nil {
			return err
		}
		q := tx.Exec(`DELETE FROM ai_asset_intercept_rule WHERE tenant_id=? AND task_id IS NOT DISTINCT FROM NULLIF(?,0) AND id=?`, assets.DefaultTenantID, taskID, id)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return app.ErrNotFound
		}
		return nil
	})
}
func (s *Store) ToggleAssetRule(ctx context.Context, user int, taskID, id int64, enabled bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assetRuleScope(tx, user, taskID, true); err != nil {
			return err
		}
		q := tx.Exec(`UPDATE ai_asset_intercept_rule SET enabled=?,updated_at=clock_timestamp() WHERE tenant_id=? AND task_id IS NOT DISTINCT FROM NULLIF(?,0) AND id=?`, enabled, assets.DefaultTenantID, taskID, id)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return app.ErrNotFound
		}
		return nil
	})
}

func activeAssetGateRun(tx *gorm.DB, user int, taskID int64, runID string) error {
	if err := member(tx, user, false, false); err != nil {
		return err
	}
	var active bool
	var err error
	if taskID > 0 {
		err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_task WHERE tenant_id=? AND owner_user_id=? AND id=? AND tianqiong_ai.task_run_live(id,owner_user_id,?::uuid))`, assets.DefaultTenantID, user, taskID, runID).Scan(&active).Error
	} else {
		err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_run WHERE tenant_id=? AND owner_user_id=? AND id::text=? AND status='running' AND lease_until>clock_timestamp())`, assets.DefaultTenantID, user, runID).Scan(&active).Error
	}
	if err != nil {
		return err
	}
	if !active {
		return app.ErrRunInactive
	}
	return nil
}
func (s *Store) EffectiveAssetRules(ctx context.Context, user int, taskID int64, runID string) ([]app.AssetRule, error) {
	tx := s.db.WithContext(ctx)
	if err := activeAssetGateRun(tx, user, taskID, runID); err != nil {
		return nil, err
	}
	items := []app.AssetRule{}
	err := tx.Raw(`SELECT `+assetRuleColumns+` FROM ai_asset_intercept_rule WHERE tenant_id=? AND (task_id IS NULL OR task_id=? AND owner_user_id=?) ORDER BY task_id NULLS FIRST,builtin DESC,id DESC`, assets.DefaultTenantID, taskID, user).Scan(&items).Error
	return items, err
}

func (s *Store) AssetCandidates(ctx context.Context, user int, taskID int64, runID string, ids []int64) ([]app.AssetCandidate, error) {
	tx := s.db.WithContext(ctx)
	if err := activeAssetGateRun(tx, user, taskID, runID); err != nil {
		return nil, err
	}
	unique := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || len(unique) >= 1000 {
			return nil, app.ErrInvalidInput
		}
		unique[id] = true
	}
	items := []app.AssetCandidate{}
	if err := tx.Raw(`SELECT id,asset_type AS type,label FROM asset_entity WHERE tenant_id=? AND id=ANY(?::bigint[]) ORDER BY id`, assets.DefaultTenantID, pq.Int64Array(ids)).Scan(&items).Error; err != nil {
		return nil, err
	}
	if len(items) != len(unique) {
		return nil, app.ErrNotFound
	}
	index := make(map[int64]int, len(items))
	for i := range items {
		index[items[i].ID] = i
	}
	rows, err := tx.Raw(`SELECT o.asset_id,o.payload FROM asset_observation o LEFT JOIN target t ON t.id=o.target_id WHERE o.tenant_id=? AND o.asset_id=ANY(?::bigint[]) AND (o.original_target_id IS NULL OR t.id IS NOT NULL AND t.deleted_at IS NULL) ORDER BY o.id DESC LIMIT 10001`, assets.DefaultTenantID, pq.Int64Array(ids)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > 10000 {
			return nil, app.ErrInvalidInput
		}
		var id int64
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var fields map[string]any
		if json.Unmarshal(raw, &fields) != nil {
			return nil, app.ErrInvalidInput
		}
		input := assets.AssetInput{}
		for _, entry := range []struct {
			keys []string
			dst  *string
		}{{[]string{"domain", "rootDomain", "root_domain"}, &input.Domain}, {[]string{"ip"}, &input.IP}, {[]string{"serviceIp", "service_ip"}, &input.ServiceIP}, {[]string{"url"}, &input.URL}} {
			for _, key := range entry.keys {
				if value, ok := fields[key].(string); ok && value != "" {
					*entry.dst = value
					break
				}
			}
		}
		for _, key := range []string{"boundDomains", "bound_domains"} {
			if values, ok := fields[key].([]any); ok {
				for _, value := range values {
					if host, ok := value.(string); ok {
						input.BoundDomains = append(input.BoundDomains, host)
					}
				}
			}
		}
		if extra, ok := fields["extra"].(map[string]any); ok && input.ServiceIP == "" {
			if value, ok := extra["ip"].(string); ok {
				input.ServiceIP = value
			}
		}
		items[index[id]].Inputs = append(items[index[id]].Inputs, input)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	ports, err := tx.Raw(`SELECT b.asset_id,h.host,host(h.ip) FROM asset_host_port_binding b JOIN host_port_mapping h ON h.id=b.host_port_id JOIN target t ON t.id=h.target_id AND t.deleted_at IS NULL WHERE b.asset_id=ANY(?::bigint[]) LIMIT 10001`, pq.Int64Array(ids)).Rows()
	if err != nil {
		return nil, err
	}
	defer ports.Close()
	count = 0
	for ports.Next() {
		count++
		if count > 10000 {
			return nil, app.ErrInvalidInput
		}
		var id int64
		var hostname, ip string
		if err = ports.Scan(&id, &hostname, &ip); err != nil {
			return nil, err
		}
		items[index[id]].Inputs = append(items[index[id]].Inputs, assets.AssetInput{Domain: hostname, IP: ip})
	}
	return items, ports.Err()
}

var _ app.AssetRuleStore = (*Store)(nil)
