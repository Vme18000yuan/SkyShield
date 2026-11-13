package repository

import (
	"database/sql"
	"github.com/lib/pq"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

// The caller holds the tenant row lock shared with task-chain updates.
// Lock affected tasks before deleting the profile, advance only a deleted
// active cursor, and leave an exhausted cursor exhausted.
func detachTaskProfile(tx *gorm.DB, profileID string) error {
	var affected []struct {
		ID          int64
		OwnerUserID int
		Previous    sql.NullString
	}
	if err := tx.Raw(`SELECT t.id,t.owner_user_id,t.active_llm_profile_id::text AS previous FROM ai_task t JOIN ai_task_llm_profile p ON p.task_id=t.id AND p.tenant_id=t.tenant_id WHERE t.tenant_id=? AND p.profile_id=? ORDER BY t.id FOR UPDATE OF t`, assets.DefaultTenantID, profileID).Scan(&affected).Error; err != nil {
		return err
	}
	if len(affected) == 0 {
		return nil
	}
	ids := make([]int64, len(affected))
	for i, row := range affected {
		ids[i] = row.ID
	}
	if err := tx.Exec(`UPDATE ai_task t SET active_llm_profile_id=CASE WHEN t.active_llm_profile_id=?::uuid THEN (SELECT n.profile_id FROM ai_task_llm_profile n WHERE n.task_id=t.id AND n.position>p.position AND n.status='ready' ORDER BY n.position LIMIT 1) ELSE t.active_llm_profile_id END,llm_chain_revision=t.llm_chain_revision+1 FROM ai_task_llm_profile p WHERE t.tenant_id=? AND t.id=ANY(?::bigint[]) AND p.task_id=t.id AND p.profile_id=?::uuid`, profileID, assets.DefaultTenantID, pq.Int64Array(ids), profileID).Error; err != nil {
		return err
	}
	for _, row := range affected {
		var previous *string
		if row.Previous.Valid {
			previous = &row.Previous.String
		}
		if err := tx.Exec(`INSERT INTO ai_task_llm_history(tenant_id,task_id,owner_user_id,mode,previous_profile_id,next_profile_id,reason) SELECT tenant_id,id,owner_user_id,'manual',?,active_llm_profile_id,'管理员删除模型配置' FROM ai_task WHERE tenant_id=? AND id=?`, previous, assets.DefaultTenantID, row.ID).Error; err != nil {
			return err
		}
	}
	return nil
}
