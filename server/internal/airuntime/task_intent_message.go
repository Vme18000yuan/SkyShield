package airuntime

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
)

type taskIntentMessage struct {
	ID, RequestID, DisplayText, ModelText, Worker string
	IntentID                                      int64
}

func pendingIntentMessages(ctx context.Context, db *sql.DB, task TaskExecution) ([]taskIntentMessage, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.id::text,m.intent_id,m.request_id,m.display_text,m.model_text,m.worker FROM public.ai_task_intent_message m JOIN public.ai_task t ON t.id=m.task_id JOIN tianqiong_ai.exploration_nodes n ON n.exploration_id=t.exploration_id AND n.id=m.intent_id WHERE m.task_id=$1 AND m.tenant_id=$2::uuid AND m.owner_user_id=$3 AND m.status='pending' AND n.state='open' AND t.status='running' AND t.active_run=$4::uuid AND t.lease_until>clock_timestamp() ORDER BY m.created_at LIMIT 50`, task.ID, task.TenantID, task.UserID, task.RunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []taskIntentMessage
	for rows.Next() {
		var item taskIntentMessage
		if err = rows.Scan(&item.ID, &item.IntentID, &item.RequestID, &item.DisplayText, &item.ModelText, &item.Worker); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func regularTaskIntents(ctx context.Context, db *sql.DB, explorationID int64, limit int) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `SELECT n.id FROM tianqiong_ai.exploration_nodes n WHERE n.exploration_id=$1 AND n.kind='intent' AND n.state='open' AND NOT EXISTS(SELECT 1 FROM public.ai_task_intent_message m WHERE m.intent_id=n.id AND m.status='pending') ORDER BY n.priority DESC,n.id LIMIT $2`, explorationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// The visible user turn and its idempotency receipt commit together under the
// task lease, before the Worker emits any response.
func prepareIntentMessage(ctx context.Context, db *sql.DB, task TaskExecution, message taskIntentMessage) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var activity sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT activity_id FROM public.ai_task_intent_message WHERE id=$1::uuid AND task_id=$2 AND intent_id=$3 AND status='pending' FOR UPDATE`, message.ID, task.ID, message.IntentID).Scan(&activity)
	if err != nil {
		return err
	}
	if !activity.Valid {
		if err = tx.QueryRowContext(ctx, `INSERT INTO tianqiong_ai.activity(exploration_id,node_id,worker,kind,summary,detail) VALUES($1,$2,'user','user',$3,$3) RETURNING id`, task.ExplorationID, message.IntentID, message.DisplayText).Scan(&activity.Int64); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE public.ai_task_intent_message SET activity_id=$1 WHERE id=$2::uuid AND status='pending'`, activity.Int64, message.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func intentMessageRecorded(store *transcript.Store, explorationID, intentID int64, requestID string) (bool, error) {
	records, err := store.Load(store.MainPath(agent.WorkerSessionID(explorationID, intentID)))
	if err != nil {
		return false, err
	}
	marker := fmt.Sprintf("<!-- ARTEX_WORKER_CHAT:%s -->", requestID)
	for _, record := range records {
		if record.Message != nil && record.Message.Role == llm.RoleUser && strings.Contains(record.Message.Text(), marker) {
			return true, nil
		}
	}
	return false, nil
}

func finishIntentMessage(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, `UPDATE public.ai_task_intent_message SET status='delivered',delivered_at=clock_timestamp() WHERE id=$1::uuid AND status='pending'`, id)
	return err
}
