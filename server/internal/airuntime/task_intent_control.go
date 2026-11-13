package airuntime

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
)

// pollIntentPauses runs separately from the planner so an operator can interrupt
// a Worker even while the planner is waiting on a model. Requests are stored in
// PostgreSQL and are bound to this exact execution lease.
func pollIntentPauses(ctx context.Context, db *sql.DB, task TaskExecution, mu *sync.Mutex, works map[int64]*taskWork) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		rows, err := db.QueryContext(ctx, `SELECT c.id::text,c.intent_id,c.created_at FROM public.ai_task_intent_control c JOIN public.ai_task t ON t.id=c.task_id WHERE c.task_id=$1 AND c.run_id=$2::uuid AND c.status='pending' AND t.active_run=c.run_id AND t.status='running' AND t.lease_until>clock_timestamp() ORDER BY c.created_at LIMIT 100`, task.ID, task.RunID)
		if err != nil {
			continue
		}
		type request struct {
			id      string
			intent  int64
			created time.Time
		}
		var pending []request
		for rows.Next() {
			var item request
			if rows.Scan(&item.id, &item.intent, &item.created) == nil {
				pending = append(pending, item)
			}
		}
		rows.Close()
		for _, item := range pending {
			mu.Lock()
			work := works[item.intent]
			if work != nil && work.pauseRequest == "" {
				work.pauseRequest = item.id
				work.cancel(agent.AbortWorkPausedByUser)
			}
			mu.Unlock()
			if work == nil && time.Since(item.created) > 2*time.Second {
				// An intent can finish between the HTTP check and this poll. A
				// short grace period also covers claim-to-registration startup.
				_ = finishIntentPause(ctx, db, item.id, "conflict")
			}
		}
	}
}

func finishIntentPause(ctx context.Context, db *sql.DB, id, status string) error {
	_, err := db.ExecContext(ctx, `UPDATE public.ai_task_intent_control SET status=$1,finished_at=clock_timestamp() WHERE id=$2::uuid AND status='pending'`, status, id)
	return err
}
