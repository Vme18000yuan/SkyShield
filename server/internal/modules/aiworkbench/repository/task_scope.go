package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

const taskScopeColumns = `s.id,s.task_id,s.kind,s.organization_id,COALESCE(o.name,'') AS company_name,COALESCE(s.domain,'') AS domain,COALESCE(s.net::text,'') AS net,COALESCE(s.value,'') AS value,s.source,s.reason,s.created_at`

func (s *TaskStore) ListTaskScopes(ctx context.Context, user int, taskID int64) ([]aiapp.TaskScope, error) {
	task, err := s.GetTask(ctx, user, taskID)
	if err != nil {
		return nil, err
	}
	items := []aiapp.TaskScope{}
	err = s.db.WithContext(ctx).Raw(`SELECT `+taskScopeColumns+`,(s.task_id<>?) AS inherited,CASE WHEN s.task_id<>? THEN s.task_id ELSE 0 END AS source_task_id FROM ai_task_scope s JOIN ai_task source ON source.id=s.task_id AND source.tenant_id=s.tenant_id AND source.owner_user_id=s.owner_user_id LEFT JOIN organization o ON o.id=s.organization_id AND o.deleted_at IS NULL WHERE s.tenant_id=? AND s.owner_user_id=? AND source.deleted_at IS NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL)) AND (s.task_id=? OR EXISTS(SELECT 1 FROM ai_task_relation r WHERE r.task_id=? AND r.source_task_id=s.task_id AND r.tenant_id=s.tenant_id)) AND (s.organization_id IS NULL OR o.id IS NOT NULL) ORDER BY (s.task_id<>?),s.id`, taskID, taskID, task.TenantID, user, taskID, taskID, taskID).Scan(&items).Error
	return items, err
}

func (s *TaskStore) TaskCoverage(ctx context.Context, user int, taskID int64) (aiapp.TaskCoverage, error) {
	cov := aiapp.TaskCoverage{ByType: []aiapp.TaskCoverageByType{}}
	task, err := s.GetTask(ctx, user, taskID)
	if err != nil {
		return cov, err
	}
	if !task.CoverageEnabled {
		return cov, nil
	}
	cov.Enabled = true
	scopes, err := s.ListTaskScopes(ctx, user, taskID)
	if err != nil {
		return cov, err
	}
	cov.ScopeRows = len(scopes)
	err = s.db.WithContext(ctx).Raw(`SELECT asset_type AS type,count(*) AS total,count(*) FILTER (WHERE tested) AS tested FROM tianqiong_ai.task_scope_assets(?,?) GROUP BY asset_type ORDER BY asset_type`, taskID, user).Scan(&cov.ByType).Error
	if err != nil {
		return cov, err
	}
	for _, row := range cov.ByType {
		cov.Denominator += row.Total
		cov.Tested += row.Tested
	}
	if cov.Denominator > 0 {
		pct := float64(cov.Tested) / float64(cov.Denominator)
		cov.Pct = &pct
	}
	return cov, nil
}

func resolveScopeOrganization(tx *gorm.DB, value string) (int, string, error) {
	type org struct {
		ID   int
		Name string
	}
	items := []org{}
	if id, err := strconv.ParseInt(value, 10, 32); err == nil && id > 0 {
		if err = tx.Raw(`SELECT id,name FROM organization WHERE id=? AND deleted_at IS NULL FOR SHARE`, id).Scan(&items).Error; err != nil {
			return 0, "", err
		}
	} else if err := tx.Raw(`SELECT id,name FROM organization WHERE lower(name)=lower(?) AND deleted_at IS NULL ORDER BY id LIMIT 2 FOR SHARE`, value).Scan(&items).Error; err != nil {
		return 0, "", err
	}
	if len(items) != 1 {
		return 0, "", aiapp.ErrInvalidInput
	}
	return items[0].ID, items[0].Name, nil
}

func (s *TaskStore) AddTaskScope(ctx context.Context, user int, taskID int64, runID string, item aiapp.TaskScope) (aiapp.TaskScope, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if runID == "" {
			if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
				return err
			}
			if err := tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_scope',TRUE)`).Error; err != nil {
				return err
			}
			item.Source = "manual"
		} else {
			if err := tx.Exec(`SELECT set_config('tianqiong.task_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE),set_config('tianqiong.execution_id',?,TRUE),set_config('tianqiong.execution_mode',tianqiong_ai.task_run_mode(?::bigint,?::uuid),TRUE)`, strconv.FormatInt(taskID, 10), strconv.Itoa(user), runID, taskID, runID).Error; err != nil {
				return err
			}
			var exploration, checked int64
			if err := tx.Raw(`SELECT exploration_id FROM ai_task WHERE id=? AND tenant_id=? AND owner_user_id=? AND deleted_at IS NULL`, taskID, assets.DefaultTenantID, user).Row().Scan(&exploration); err != nil {
				return catalogError(err)
			}
			if err := tx.Raw(`SELECT id FROM tianqiong_ai.assert_task_write(?,NULL)`, exploration).Row().Scan(&checked); err != nil || checked != taskID {
				return aiapp.ErrRunLeaseLost
			}
			item.Source = "agent"
		}
		if item.Kind == "company" {
			id, name, err := resolveScopeOrganization(tx, item.Value)
			if err != nil {
				return err
			}
			item.OrganizationID, item.CompanyName, item.Key, item.Value = &id, name, strconv.Itoa(id), ""
		}
		var id int64
		result := tx.Raw(`INSERT INTO ai_task_scope(tenant_id,owner_user_id,task_id,kind,scope_key,organization_id,domain,net,value,source,reason) VALUES(?,?,?,?,?,?,NULLIF(?,''),NULLIF(?,'')::cidr,NULLIF(?,''),?,?) ON CONFLICT(task_id,kind,scope_key) DO NOTHING RETURNING id`, assets.DefaultTenantID, user, taskID, item.Kind, item.Key, item.OrganizationID, item.Domain, item.Net, item.Value, item.Source, item.Reason).Row().Scan(&id)
		if errors.Is(result, sql.ErrNoRows) {
			if err := tx.Raw(`SELECT id FROM ai_task_scope WHERE task_id=? AND kind=? AND scope_key=?`, taskID, item.Kind, item.Key).Row().Scan(&id); err != nil {
				return err
			}
		} else if result != nil {
			return result
		}
		return tx.Raw(`SELECT `+taskScopeColumns+` FROM ai_task_scope s LEFT JOIN organization o ON o.id=s.organization_id AND o.deleted_at IS NULL WHERE s.id=? AND s.task_id=?`, id, taskID).Row().Scan(&item.ID, &item.TaskID, &item.Kind, &item.OrganizationID, &item.CompanyName, &item.Domain, &item.Net, &item.Value, &item.Source, &item.Reason, &item.CreatedAt)
	})
	return item, err
}

func (s *TaskStore) DeleteTaskScope(ctx context.Context, user int, taskID, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockHumanGoalScope(tx, user, taskID); err != nil {
			return err
		}
		if err := tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_scope',TRUE)`).Error; err != nil {
			return err
		}
		result := tx.Exec(`DELETE FROM ai_task_scope WHERE id=? AND task_id=? AND tenant_id=? AND owner_user_id=?`, id, taskID, assets.DefaultTenantID, user)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return aiapp.ErrTaskNotFound
		}
		return nil
	})
}

var _ aiapp.TaskScopeStore = (*TaskStore)(nil)
