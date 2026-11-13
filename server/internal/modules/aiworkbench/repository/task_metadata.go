package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *TaskStore) UpdateTaskMetadata(ctx context.Context, user int, id int64, update aiapp.TaskMetadataUpdate) (*aiapp.Task, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		if update.SetCategoryID {
			if err := lockCatalogCategory(tx, user, update.CategoryID); err != nil {
				return err
			}
		}
		q := tx.Exec(`UPDATE ai_task SET name=COALESCE(?,name),remark=COALESCE(?,remark),category_id=CASE WHEN ? THEN ?::bigint ELSE category_id END,pinned_at=CASE WHEN ?::boolean IS NULL THEN pinned_at WHEN ?::boolean THEN COALESCE(pinned_at,clock_timestamp()) ELSE NULL END,updated_at=clock_timestamp() WHERE tenant_id=? AND owner_user_id=? AND id=? AND deleted_at IS NULL`, update.Name, update.Remark, update.SetCategoryID, update.CategoryID, update.Pinned, update.Pinned, assets.DefaultTenantID, user, id)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return aiapp.ErrTaskNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetTask(ctx, user, id)
}

func (s *TaskStore) ListTaskPage(ctx context.Context, user int, before int64, filter aiapp.TaskListFilter) ([]aiapp.Task, error) {
	tx := s.db.WithContext(ctx)
	var query strings.Builder
	query.WriteString(`SELECT ` + taskColumns + taskOwnerJoin + ` WHERE ` + taskOwnerWhere)
	args := []any{assets.DefaultTenantID, user}
	if filter.Query != "" {
		query.WriteString(` AND (strpos(lower(t.name),lower(?))>0 OR strpos(lower(t.goal),lower(?))>0 OR strpos(lower(t.description),lower(?))>0 OR strpos(lower(t.remark),lower(?))>0 OR EXISTS(SELECT 1 FROM ai_task_category c WHERE c.id=t.category_id AND strpos(lower(c.name),lower(?))>0))`)
		args = append(args, filter.Query, filter.Query, filter.Query, filter.Query, filter.Query)
	}
	if filter.Status == "queued" {
		query.WriteString(` AND t.queued=TRUE`)
	} else if filter.Status != "" {
		query.WriteString(` AND t.status=? AND t.queued=FALSE`)
		args = append(args, filter.Status)
	}
	if filter.NoCategory {
		query.WriteString(` AND t.category_id IS NULL`)
	} else if filter.CategoryID != nil {
		query.WriteString(` AND t.category_id=?`)
		args = append(args, *filter.CategoryID)
	}
	if before > 0 {
		var pinned *time.Time
		err := tx.Raw(`SELECT t.pinned_at`+taskOwnerJoin+` WHERE `+taskOwnerWhere+` AND t.id=?`, assets.DefaultTenantID, user, before).Row().Scan(&pinned)
		if errors.Is(err, sql.ErrNoRows) {
			return []aiapp.Task{}, nil
		}
		if err != nil {
			return nil, err
		}
		if pinned != nil {
			query.WriteString(` AND (t.pinned_at IS NULL OR t.pinned_at<? OR (t.pinned_at=? AND t.id<?))`)
			args = append(args, *pinned, *pinned, before)
		} else {
			query.WriteString(` AND t.pinned_at IS NULL AND t.id<?`)
			args = append(args, before)
		}
	}
	query.WriteString(` ORDER BY t.pinned_at DESC NULLS LAST,t.id DESC LIMIT 51`)
	items := []aiapp.Task{}
	err := tx.Raw(query.String(), args...).Scan(&items).Error
	if err == nil {
		err = s.hydrateTaskLLM(ctx, items)
	}
	if err == nil {
		err = s.hydrateTaskSources(ctx, items)
	}
	return items, err
}

var _ aiapp.TaskMetadataStore = (*TaskStore)(nil)
