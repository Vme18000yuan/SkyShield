package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func catalogReadRole(tx *gorm.DB, user int) error {
	var active bool
	if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=? AND m.user_id=? AND tenant.status='active' AND u.is_active=TRUE)`, assets.DefaultTenantID, user).Scan(&active).Error; err != nil {
		return err
	}
	if !active {
		return assets.ErrAssetWriteForbidden
	}
	return nil
}
func catalogError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return aiapp.ErrTaskNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return aiapp.ErrTaskCatalogConflict
	}
	return err
}
func lockCatalogCategory(tx *gorm.DB, user int, id *int64) error {
	if id == nil {
		return nil
	}
	var present int
	err := tx.Raw(`SELECT 1 FROM ai_task_category WHERE tenant_id=? AND owner_user_id=? AND id=? FOR SHARE`, assets.DefaultTenantID, user, *id).Row().Scan(&present)
	return catalogError(err)
}

func (s *TaskStore) ListTaskCategories(ctx context.Context, user int) ([]aiapp.TaskCategory, error) {
	tx := s.db.WithContext(ctx)
	if err := catalogReadRole(tx, user); err != nil {
		return nil, err
	}
	items := []aiapp.TaskCategory{}
	err := tx.Raw(`SELECT c.id,c.name,count(t.id) FILTER (WHERE t.deleted_at IS NULL) AS task_count,c.created_at,c.updated_at FROM ai_task_category c LEFT JOIN ai_task t ON t.category_id=c.id AND t.tenant_id=c.tenant_id AND t.owner_user_id=c.owner_user_id WHERE c.tenant_id=? AND c.owner_user_id=? GROUP BY c.id ORDER BY c.name,c.id`, assets.DefaultTenantID, user).Scan(&items).Error
	return items, err
}
func (s *TaskStore) SaveTaskCategory(ctx context.Context, user int, item aiapp.TaskCategory, create bool) (aiapp.TaskCategory, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		var row *sql.Row
		if create {
			row = tx.Raw(`INSERT INTO ai_task_category(tenant_id,owner_user_id,name,nkey) VALUES(?,?,?,?) RETURNING id,name,created_at,updated_at`, assets.DefaultTenantID, user, item.Name, strings.ToLower(item.Name)).Row()
		} else {
			row = tx.Raw(`UPDATE ai_task_category SET name=?,nkey=?,updated_at=clock_timestamp() WHERE tenant_id=? AND owner_user_id=? AND id=? RETURNING id,name,created_at,updated_at`, item.Name, strings.ToLower(item.Name), assets.DefaultTenantID, user, item.ID).Row()
		}
		if err := row.Scan(&item.ID, &item.Name, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return catalogError(err)
		}
		return tx.Raw(`SELECT count(*) FROM ai_task WHERE tenant_id=? AND owner_user_id=? AND category_id=? AND deleted_at IS NULL`, assets.DefaultTenantID, user, item.ID).Scan(&item.TaskCount).Error
	})
	return item, err
}
func (s *TaskStore) DeleteTaskCategory(ctx context.Context, user int, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		q := tx.Exec(`DELETE FROM ai_task_category WHERE tenant_id=? AND owner_user_id=? AND id=?`, assets.DefaultTenantID, user, id)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return aiapp.ErrTaskNotFound
		}
		return nil
	})
}

type rowScanner interface{ Scan(...any) error }

func scanTaskTemplate(row rowScanner) (aiapp.TaskTemplate, error) {
	var item aiapp.TaskTemplate
	var raw []byte
	err := row.Scan(&item.ID, &item.Name, &item.Description, &item.Goal, &item.Remark, &item.CategoryID, &raw, &item.WorkerCount, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, catalogError(err)
	}
	item.InterceptRules = []aiapp.TaskTemplateRule{}
	if err = json.Unmarshal(raw, &item.InterceptRules); err != nil {
		return item, err
	}
	return item, nil
}

const templateColumns = `id,name,description,goal,remark,category_id,intercept_rules,worker_count,created_at,updated_at`

func (s *TaskStore) ListTaskTemplates(ctx context.Context, user int) ([]aiapp.TaskTemplate, error) {
	tx := s.db.WithContext(ctx)
	if err := catalogReadRole(tx, user); err != nil {
		return nil, err
	}
	rows, err := tx.Raw(`SELECT `+templateColumns+` FROM ai_task_template WHERE tenant_id=? AND owner_user_id=? ORDER BY updated_at DESC,id DESC`, assets.DefaultTenantID, user).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []aiapp.TaskTemplate{}
	for rows.Next() {
		item, err := scanTaskTemplate(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *TaskStore) SaveTaskTemplate(ctx context.Context, user int, item aiapp.TaskTemplate, create bool) (aiapp.TaskTemplate, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		if err := lockCatalogCategory(tx, user, item.CategoryID); err != nil {
			return err
		}
		if item.InterceptRules == nil {
			item.InterceptRules = []aiapp.TaskTemplateRule{}
		}
		rules, err := json.Marshal(item.InterceptRules)
		if err != nil {
			return err
		}
		var row *sql.Row
		if create {
			row = tx.Raw(`INSERT INTO ai_task_template(tenant_id,owner_user_id,name,nkey,description,goal,remark,category_id,intercept_rules,worker_count) VALUES(?,?,?,?,?,?,?,?,?::jsonb,?) RETURNING `+templateColumns, assets.DefaultTenantID, user, item.Name, strings.ToLower(item.Name), item.Description, item.Goal, item.Remark, item.CategoryID, string(rules), item.WorkerCount).Row()
		} else {
			row = tx.Raw(`UPDATE ai_task_template SET name=?,nkey=?,description=?,goal=?,remark=?,category_id=?,intercept_rules=?::jsonb,worker_count=?,updated_at=clock_timestamp() WHERE tenant_id=? AND owner_user_id=? AND id=? RETURNING `+templateColumns, item.Name, strings.ToLower(item.Name), item.Description, item.Goal, item.Remark, item.CategoryID, string(rules), item.WorkerCount, assets.DefaultTenantID, user, item.ID).Row()
		}
		item, err = scanTaskTemplate(row)
		return err
	})
	return item, err
}
func (s *TaskStore) DeleteTaskTemplate(ctx context.Context, user int, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		q := tx.Exec(`DELETE FROM ai_task_template WHERE tenant_id=? AND owner_user_id=? AND id=?`, assets.DefaultTenantID, user, id)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return aiapp.ErrTaskNotFound
		}
		return nil
	})
}

var _ aiapp.TaskCatalogStore = (*TaskStore)(nil)
