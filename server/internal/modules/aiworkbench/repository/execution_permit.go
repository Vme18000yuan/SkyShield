package repository

import (
	"context"
	"encoding/json"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
	"strings"
)

type ExecutionPermitStore struct{ db *gorm.DB }

func NewExecutionPermitStore(db *gorm.DB) *ExecutionPermitStore { return &ExecutionPermitStore{db} }

func (s *ExecutionPermitStore) ClaimExecution(ctx context.Context, user int, id, owner, kind string) (bool, error) {
	claimed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		globalLimit, userLimit := 16, 8
		model := kind == "model" || strings.HasPrefix(kind, "model:")
		globalKey, userKey := "global_execution_limit", "user_execution_limit"
		class := "root"
		if kind == "process" || kind == "process_aux" {
			class = "process"
			globalKey, userKey = "global_process_limit", "user_process_limit"
		}
		if model {
			class = "model"
			globalLimit, userLimit = 32, 8
			globalKey, userKey = "global_model_limit", "user_model_limit"
		}
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?,0))`, "ai-permit/"+assets.DefaultTenantID).Error; err != nil {
			return err
		}
		var raw string
		if err := tx.Raw(`SELECT COALESCE((SELECT options::text FROM ai_runtime_setting WHERE tenant_id=?),'{}')`, assets.DefaultTenantID).Row().Scan(&raw); err != nil {
			return err
		}
		var options map[string]int
		// Decode individual numeric fields; the options document also contains
		// booleans and secrets metadata which are unrelated to admission.
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &fields) == nil {
			options = map[string]int{}
			for _, key := range []string{globalKey, userKey} {
				var n int
				if json.Unmarshal(fields[key], &n) == nil {
					options[key] = n
				}
			}
		}
		ceiling := 16
		if model {
			ceiling = 64
		}
		if n := options[globalKey]; n > 0 {
			globalLimit = min(n, ceiling)
		}
		if n := options[userKey]; n > 0 {
			userLimit = min(n, ceiling)
		}
		userLimit = min(userLimit, globalLimit)
		if err := tx.Exec(`DELETE FROM ai_execution_permit WHERE lease_until<=clock_timestamp()`).Error; err != nil {
			return err
		}
		var counts struct{ Total, Owned, Base, OwnedBase int }
		if err := tx.Raw(`SELECT COUNT(*) total,COUNT(*) FILTER(WHERE owner_user_id=?) owned,COUNT(*) FILTER(WHERE kind='process') AS base,COUNT(*) FILTER(WHERE kind='process' AND owner_user_id=?) AS owned_base FROM ai_execution_permit WHERE tenant_id=? AND CASE WHEN kind='model' OR kind LIKE 'model:%' THEN 'model' WHEN kind IN ('process','process_aux') THEN 'process' ELSE 'root' END=?`, user, user, assets.DefaultTenantID, class).Scan(&counts).Error; err != nil {
			return err
		}
		if counts.Total >= globalLimit || counts.Owned >= userLimit {
			return nil
		}
		if kind == "process" && (counts.Base >= globalLimit-max(1, globalLimit/4) || counts.OwnedBase >= userLimit-max(1, userLimit/4)) {
			return nil
		}

		if strings.HasPrefix(kind, "model:") {
			var raw string
			if err := tx.Raw(`SELECT options::text FROM ai_llm_profile WHERE tenant_id=? AND id=?::uuid`, assets.DefaultTenantID, strings.TrimPrefix(kind, "model:")).Row().Scan(&raw); err != nil {
				return err
			}
			var profile struct {
				MaxConcurrent int `json:"max_concurrent"`
			}
			if err := json.Unmarshal([]byte(raw), &profile); err != nil {
				return err
			}
			maximum := profile.MaxConcurrent
			if maximum <= 0 {
				maximum = 4
			}
			maximum = min(maximum, 64)
			var used int64
			if err := tx.Raw(`SELECT COUNT(*) FROM ai_execution_permit WHERE tenant_id=? AND kind=?`, assets.DefaultTenantID, kind).Scan(&used).Error; err != nil {
				return err
			}
			if used >= int64(maximum) {
				return nil
			}
		}
		result := tx.Exec(`INSERT INTO ai_execution_permit(id,tenant_id,owner_user_id,kind,lease_owner,lease_until)
		 SELECT ?,m.tenant_id,m.user_id,?,?,clock_timestamp()+interval '45 seconds'
		 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
		 WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE`, id, kind, owner, assets.DefaultTenantID, user)
		claimed = result.RowsAffected == 1
		return result.Error
	})
	return claimed, err
}
func (s *ExecutionPermitStore) RenewExecution(ctx context.Context, id, owner string) (bool, error) {
	r := s.db.WithContext(ctx).Exec(`UPDATE ai_execution_permit p SET lease_until=clock_timestamp()+interval '45 seconds'
	 WHERE p.id=? AND p.lease_owner=? AND p.lease_until>clock_timestamp() AND EXISTS(
	 SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
	 WHERE m.tenant_id=p.tenant_id AND m.user_id=p.owner_user_id AND t.status='active' AND u.is_active=TRUE
	 AND (p.kind<>'tool_test' OR m.role='admin'))`, id, owner)
	return r.RowsAffected == 1, r.Error
}
func (s *ExecutionPermitStore) ReleaseExecution(ctx context.Context, id, owner string) error {
	return s.db.WithContext(ctx).Exec(`DELETE FROM ai_execution_permit WHERE id=? AND lease_owner=?`, id, owner).Error
}
