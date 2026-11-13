package repository

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	app "github.com/yyhuni/lunafox/server/internal/modules/aiapproval/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *Store) executionSourceVisible(ctx context.Context, user int, approval *app.Approval) error {
	if approval == nil || approval.TaskID == nil && approval.ConversationID == nil {
		return app.ErrExecutionUnavailable
	}
	var allowed bool
	if approval.TaskID != nil {
		err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM ai_task task JOIN tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE task.tenant_id=?::uuid AND task.owner_user_id=? AND task.id=?::bigint AND task.deleted_at IS NULL AND u.is_active AND t.status='active' AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=task.target_id AND x.deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=task.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')))`, assets.DefaultTenantID, user, *approval.TaskID).Scan(&allowed).Error
		if err != nil {
			return err
		}
		if !allowed {
			return app.ErrExecutionUnavailable
		}
	}
	if approval.ConversationID != nil {
		err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM ai_conversation c JOIN tenant_membership m ON m.tenant_id=c.tenant_id AND m.user_id=c.owner_user_id JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE c.tenant_id=?::uuid AND c.owner_user_id=? AND c.id=?::uuid AND u.is_active AND t.status='active' AND tianqiong_ai.finding_case_review_conversation_visible(c.id))`, assets.DefaultTenantID, user, *approval.ConversationID).Scan(&allowed).Error
		if err != nil {
			return err
		}
		if !allowed {
			return app.ErrExecutionUnavailable
		}
	}
	return nil
}

func (s *Store) ReadExecutionContext(ctx context.Context, user int, id, before, after int64, limit int) (app.ExecutionContextPage, error) {
	out := app.ExecutionContextPage{Items: []app.ExecutionActivity{}}
	if user <= 0 || id <= 0 || before < 0 || after < 0 || before > 0 && after > 0 || limit < 1 || limit > 100 {
		return out, app.ErrInvalidInput
	}
	source, err := s.ReadExecution(ctx, user, id)
	if err != nil {
		return out, err
	}
	if source.TaskID == nil || source.ConversationID != nil || len(source.Items) == 0 {
		return out, app.ErrExecutionUnavailable
	}
	approval, err := s.ReadApproval(ctx, user, id)
	if err != nil {
		return out, err
	}
	if err = s.executionSourceVisible(ctx, user, approval); err != nil {
		return out, err
	}
	taskID, err := strconv.ParseInt(*source.TaskID, 10, 64)
	if err != nil || taskID <= 0 {
		return out, app.ErrExecutionUnavailable
	}
	where := ` FROM tianqiong_ai.activity a JOIN ai_task task ON task.exploration_id=a.exploration_id WHERE task.tenant_id=?::uuid AND task.owner_user_id=? AND task.id=? AND task.deleted_at IS NULL AND (task.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=task.target_id AND x.deleted_at IS NULL)) AND NOT EXISTS(SELECT 1 FROM ai_task_archive ar WHERE ar.task_id=task.id AND ar.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting')) `
	args := []any{assets.DefaultTenantID, user, taskID}
	switch {
	case strings.HasPrefix(source.Session, "main:"):
		segment, e := strconv.Atoi(strings.TrimPrefix(source.Session, "main:"))
		if e != nil || segment < 0 {
			return out, app.ErrExecutionUnavailable
		}
		where += ` AND a.worker='mainagent' AND COALESCE(a.main_seg,0)=? `
		args = append(args, segment)
	case source.Session == "plan":
		where += ` AND a.worker='planner' AND a.node_id IS NULL AND a.execution_id=?::uuid `
		args = append(args, approval.RunID)
	case strings.HasPrefix(source.Session, "intent:") && approval.IntentID != nil:
		where += ` AND a.node_id=? AND a.worker=? AND a.execution_id=?::uuid `
		args = append(args, *approval.IntentID, source.Items[0].Worker, approval.RunID)
	default:
		return out, app.ErrExecutionUnavailable
	}
	selectCols := `SELECT a.id,a.node_id,COALESCE(a.worker,''),a.created_at,COALESCE(a.kind,''),COALESCE(a.tool,''),COALESCE(a.tool_use_id,''),a.is_error,COALESCE(a.summary,''),a.main_seg `
	read := func(cursor int64, older, inclusive bool) ([]app.ExecutionActivity, bool, error) {
		operator, order := ">", "ASC"
		if older {
			operator, order = "<", "DESC"
		} else if inclusive {
			operator = ">="
		}
		params := append(append([]any{}, args...), cursor, limit+1)
		rows, e := s.db.WithContext(ctx).Raw(selectCols+where+` AND a.id `+operator+` ? ORDER BY a.id `+order+` LIMIT ?`, params...).Rows()
		if e != nil {
			return nil, false, e
		}
		defer rows.Close()
		items := []app.ExecutionActivity{}
		for rows.Next() {
			var item app.ExecutionActivity
			var node sql.NullInt64
			if e = rows.Scan(&item.Seq, &node, &item.Worker, &item.TS, &item.Kind, &item.Tool, &item.ToolUseID, &item.IsError, &item.Summary, &item.MainSeg); e != nil {
				return nil, false, e
			}
			if node.Valid {
				value := strconv.FormatInt(node.Int64, 10)
				item.IntentID = &value
			}
			if item.Kind == "human_message" {
				item.Kind = "user"
			} else if item.Kind == "assistant_message" {
				item.Kind = "result"
			}
			items = append(items, item)
		}
		if e = rows.Err(); e != nil {
			return nil, false, e
		}
		if e = rows.Close(); e != nil {
			return nil, false, e
		}
		more := len(items) > limit
		if more {
			items = items[:limit]
		}
		if older {
			for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
				items[left], items[right] = items[right], items[left]
			}
		}
		return items, more, nil
	}
	if before > 0 {
		out.Items, out.HasBefore, err = read(before, true, false)
		out.HasAfter = true
	} else if after > 0 {
		out.Items, out.HasAfter, err = read(after, false, false)
		out.HasBefore = true
	} else {
		var left, right []app.ExecutionActivity
		left, out.HasBefore, err = read(source.Seq, true, false)
		if err == nil {
			right, out.HasAfter, err = read(source.Seq, false, true)
		}
		out.Items = append(left, right...)
	}
	if err != nil {
		return app.ExecutionContextPage{}, err
	}
	if err = s.executionSourceVisible(ctx, user, approval); err != nil {
		return app.ExecutionContextPage{}, err
	}
	if before == 0 && after == 0 {
		found := false
		for _, item := range out.Items {
			if item.Seq == source.Seq && item.Kind == "tool_use" {
				found = true
				break
			}
		}
		if !found {
			return app.ExecutionContextPage{}, app.ErrExecutionUnavailable
		}
	}
	return out, nil
}
