package airuntime

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Autumn-27/artex/agent"
	"github.com/lib/pq"
)

type humanTaskEvent struct {
	id    int64
	event agent.TriggerEvent
}

func readHumanTaskEvents(ctx context.Context, db *sql.DB, task TaskExecution) ([]humanTaskEvent, error) {
	rows, err := db.QueryContext(ctx, `SELECT e.id,e.kind,e.payload FROM public.ai_task_human_event e JOIN public.ai_task t ON t.id=e.task_id WHERE e.task_id=$1 AND t.tenant_id=$2::uuid AND t.owner_user_id=$3 AND t.active_run=$4::uuid AND t.status='running' AND t.lease_until>clock_timestamp() AND t.deleted_at IS NULL AND e.processed_at IS NULL ORDER BY e.id LIMIT 100`, task.ID, task.TenantID, task.UserID, task.RunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []humanTaskEvent{}
	for rows.Next() {
		var item humanTaskEvent
		var kind string
		var raw []byte
		if err = rows.Scan(&item.id, &kind, &raw); err != nil {
			return nil, err
		}
		var body struct {
			Goals    []string `json:"goals"`
			Hints    []string `json:"hints"`
			OldGoal  string   `json:"old_goal"`
			NewGoal  string   `json:"new_goal"`
			Detail   string   `json:"detail"`
			IntentID int64    `json:"intent_id"`
			Summary  string   `json:"summary"`
		}
		if err = json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		item.event = agent.TriggerEvent{Kind: kind, Goals: body.Goals, Hints: body.Hints, OldGoal: body.OldGoal, NewGoal: body.NewGoal, Detail: body.Detail, IntentID: body.IntentID, Summary: body.Summary}
		out = append(out, item)
	}
	return out, rows.Err()
}
func hasHumanTaskEvents(ctx context.Context, db *sql.DB, task TaskExecution) (bool, error) {
	var found bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.ai_task_human_event e JOIN public.ai_task t ON t.id=e.task_id WHERE e.task_id=$1 AND t.tenant_id=$2::uuid AND t.owner_user_id=$3 AND t.active_run=$4::uuid AND t.status='running' AND t.lease_until>clock_timestamp() AND t.deleted_at IS NULL AND e.processed_at IS NULL)`, task.ID, task.TenantID, task.UserID, task.RunID).Scan(&found)
	return found, err
}
func acknowledgeHumanTaskEvents(ctx context.Context, db *sql.DB, task TaskExecution, events []humanTaskEvent) error {
	if len(events) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.id)
	}
	_, err := db.ExecContext(ctx, `UPDATE public.ai_task_human_event SET processed_at=clock_timestamp(),processed_run=$1::uuid WHERE task_id=$2 AND id=ANY($3::bigint[]) AND processed_at IS NULL`, task.RunID, task.ID, pq.Int64Array(ids))
	return err
}
