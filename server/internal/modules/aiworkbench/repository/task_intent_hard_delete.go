// Adapted from ARTEX's intent cleanup and token rollup; AGPL-3.0-only.
package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Autumn-27/artex/sidequestion"
	"github.com/google/uuid"
	"github.com/lib/pq"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type cleanupNode struct {
	ID                   int64
	Kind, State, Summary string
}
type cleanupEdge struct {
	Src, Dst       int64
	Rel            string
	SrcExp, DstExp int64
}

// Preserve shared descendants, source-task evidence used by other tasks, goals,
// root facts and platform assets. All changes and the planner event commit once.
func hardDeleteTaskIntent(tx *gorm.DB, user int, taskID, intentID int64, scope humanGoalScope, reason string) error {
	var nodes []cleanupNode
	if err := tx.Raw(`SELECT id,kind,state,COALESCE(payload->>'summary','') AS summary FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? ORDER BY id`, scope.ExplorationID).Scan(&nodes).Error; err != nil {
		return err
	}
	byID := make(map[int64]cleanupNode, len(nodes))
	protected := map[int64]bool{}
	for _, n := range nodes {
		byID[n.ID] = n
		if n.Kind == "goal" || n.Kind == "fact" && n.State == "origin" {
			protected[n.ID] = true
		}
	}
	root, ok := byID[intentID]
	if !ok || root.Kind != "intent" || (root.State != "open" && root.State != "paused" && root.State != "running") {
		return aiapp.ErrTaskControlConflict
	}
	var edges []cleanupEdge
	if err := tx.Raw(`SELECT e.src_id AS src,e.dst_id AS dst,e.rel,src.exploration_id AS src_exp,dst.exploration_id AS dst_exp FROM tianqiong_ai.exploration_edges e JOIN tianqiong_ai.exploration_nodes src ON src.id=e.src_id JOIN tianqiong_ai.exploration_nodes dst ON dst.id=e.dst_id WHERE src.exploration_id=? OR dst.exploration_id=?`, scope.ExplorationID, scope.ExplorationID).Scan(&edges).Error; err != nil {
		return err
	}
	parents, children := map[int64][]int64{}, map[int64][]int64{}
	for _, edge := range edges {
		parents[edge.Dst] = append(parents[edge.Dst], edge.Src)
		if edge.SrcExp != edge.DstExp {
			if edge.SrcExp == scope.ExplorationID {
				protected[edge.Src] = true
			}
			if edge.DstExp == scope.ExplorationID {
				protected[edge.Dst] = true
			}
		}
		if edge.SrcExp == scope.ExplorationID && edge.DstExp == scope.ExplorationID && (edge.Rel == "yields" || edge.Rel == "derived_from") {
			children[edge.Src] = append(children[edge.Src], edge.Dst)
		}
	}
	if protected[intentID] {
		return aiapp.ErrTaskControlConflict
	}
	remove := map[int64]bool{intentID: true}
	for changed := true; changed; {
		changed = false
		for source := range remove {
			for _, child := range children[source] {
				if remove[child] || protected[child] {
					continue
				}
				exclusive := true
				for _, parent := range parents[child] {
					if !remove[parent] {
						exclusive = false
						break
					}
				}
				if exclusive {
					remove[child] = true
					changed = true
				}
			}
		}
	}
	ids, intentIDs := make([]int64, 0, len(remove)), []int64{}
	live := scope.Status == "running" && scope.LeaseUntil != nil && scope.LeaseUntil.After(time.Now())
	for id := range remove {
		node := byID[id]
		if live && node.Kind == "intent" && node.State == "running" {
			return aiapp.ErrTaskControlConflict
		}
		ids = append(ids, id)
		if node.Kind == "intent" {
			intentIDs = append(intentIDs, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	sort.Slice(intentIDs, func(i, j int) bool { return intentIDs[i] < intentIDs[j] })
	// A task row is held for update. SKIP LOCKED avoids a cycle with a Worker
	// that already owns a node lock and is waiting for that task fence.
	var locked []int64
	if err := tx.Raw(`SELECT id FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=ANY(?::bigint[]) FOR UPDATE SKIP LOCKED`, scope.ExplorationID, pq.Int64Array(ids)).Scan(&locked).Error; err != nil {
		return err
	}
	if len(locked) != len(ids) {
		return errIntentDeleteBusy
	}
	var unsafe bool
	if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_edges e WHERE e.exploration_id<>? AND (e.src_id=ANY(?::bigint[]) OR e.dst_id=ANY(?::bigint[]))) OR EXISTS(SELECT 1 FROM tianqiong_ai.findings f JOIN ai_finding_retest r ON r.finding_id=f.id WHERE f.node_id=ANY(?::bigint[]) AND r.status IN ('pending','running')) OR EXISTS(SELECT 1 FROM ai_task_intent_control c WHERE c.task_id=? AND c.intent_id=ANY(?::bigint[]) AND c.intent_id<>? AND c.action='cancel' AND c.status IN ('pending','paused'))`, scope.ExplorationID, pq.Int64Array(ids), pq.Int64Array(ids), pq.Int64Array(ids), taskID, pq.Int64Array(intentIDs), intentID).Scan(&unsafe).Error; err != nil {
		return err
	}
	if unsafe {
		return aiapp.ErrTaskControlConflict
	}
	encodedIDs, err := pq.Int64Array(ids).Value()
	if err != nil {
		return err
	}
	if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_intent_hard',TRUE),set_config('tianqiong.delete_ids',?,TRUE)`, encodedIDs).Error; err != nil {
		return err
	}
	for _, id := range intentIDs {
		buckets, err := intentUsageRollup(tx, scope.ExplorationID, id)
		if err != nil {
			return err
		}
		if err = tx.Exec(`DELETE FROM tianqiong_ai.activity WHERE exploration_id=? AND node_id=?`, scope.ExplorationID, id).Error; err != nil {
			return err
		}
		for _, bucket := range buckets {
			metadata, _ := json.Marshal(map[string]any{"cancelled_intent_id": id, "token_day": bucket.Day.Format(time.DateOnly), "token_rollup": true})
			if err = tx.Exec(`SELECT set_config('tianqiong.execution_id',?,TRUE)`, bucket.Run).Error; err != nil {
				return err
			}
			if err = tx.Exec(`INSERT INTO tianqiong_ai.activity(exploration_id,worker,kind,summary,metadata,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,created_at) VALUES(?,'token-ledger','result',?,?::jsonb,?,?,?,?,?)`, scope.ExplorationID, fmt.Sprintf("已取消意图 #%d 的 Token 计量", id), string(metadata), bucket.Input, bucket.Output, bucket.Read, bucket.Write, bucket.Day).Error; err != nil {
				return err
			}
		}
		parent := sidequestion.Parent{TaskID: taskID, ExplorationID: scope.ExplorationID, IntentID: id}
		if err = tx.Exec(`DELETE FROM ai_side_session WHERE tenant_id=? AND owner_user_id=? AND task_id=? AND parent_key=?`, assets.DefaultTenantID, user, taskID, parent.Key()).Error; err != nil {
			return err
		}
	}
	if err = tx.Exec(`DELETE FROM ai_task_intent_message WHERE task_id=? AND intent_id=ANY(?::bigint[])`, taskID, pq.Int64Array(intentIDs)).Error; err != nil {
		return err
	}
	if err = tx.Exec(`DELETE FROM tianqiong_ai.findings WHERE task_id=? AND node_id=ANY(?::bigint[])`, taskID, pq.Int64Array(ids)).Error; err != nil {
		return err
	}
	result := tx.Exec(`DELETE FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=ANY(?::bigint[])`, scope.ExplorationID, pq.Int64Array(ids))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(ids)) {
		return aiapp.ErrTaskControlConflict
	}
	if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','human_intent',TRUE)`).Error; err != nil {
		return err
	}
	if err = insertHumanGoalEvent(tx, user, taskID, "cancelled", map[string]any{"intent_id": intentID, "summary": root.Summary, "detail": reason}); err != nil {
		return err
	}
	if scope.Status != "paused" && !(scope.Status == "created" && !scope.Queued) {
		return reviveForHumanGoal(tx, user, taskID, scope)
	}
	return nil
}

type intentUsageBucket struct {
	Day                        time.Time
	Run                        string
	Input, Output, Read, Write int64
}
type intentUsageFrame struct{ Input, Output, Read, Write sql.NullInt64 }

// A result is authoritative for a run; if it lacks dimensions, use the last
// cumulative usage frame. Interrupted runs keep their final cumulative frame.
func intentUsageRollup(tx *gorm.DB, exploration, intent int64) ([]intentUsageBucket, error) {
	rows, err := tx.Raw(`SELECT kind,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,created_at,execution_id::text FROM tianqiong_ai.activity WHERE exploration_id=? AND node_id=? AND kind IN ('usage','result') ORDER BY id`, exploration, intent).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type usageKey struct {
		Day time.Time
		Run string
	}
	byDay := map[usageKey]intentUsageBucket{}
	var pending intentUsageFrame
	var pendingAt time.Time
	var pendingRun string
	fallbackRun := uuid.NewString()
	hasPending := false
	add := func(at time.Time, run string, f intentUsageFrame) {
		if f.Input.Int64 == 0 && f.Output.Int64 == 0 && f.Read.Int64 == 0 && f.Write.Int64 == 0 {
			return
		}
		u := at.UTC()
		day := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
		key := usageKey{Day: day, Run: run}
		bucket := byDay[key]
		bucket.Day = day
		bucket.Run = run
		bucket.Input += f.Input.Int64
		bucket.Output += f.Output.Int64
		bucket.Read += f.Read.Int64
		bucket.Write += f.Write.Int64
		byDay[key] = bucket
	}
	for rows.Next() {
		var kind string
		var frame intentUsageFrame
		var at time.Time
		var execution sql.NullString
		if err = rows.Scan(&kind, &frame.Input, &frame.Output, &frame.Read, &frame.Write, &at, &execution); err != nil {
			return nil, err
		}
		run := execution.String
		if run == "" {
			run = fallbackRun
		}
		if hasPending && pendingRun != run {
			add(pendingAt, pendingRun, pending)
			pending, pendingAt, pendingRun, hasPending = intentUsageFrame{}, time.Time{}, "", false
		}
		hasCurrent := frame.Input.Valid || frame.Output.Valid || frame.Read.Valid || frame.Write.Valid
		if kind == "usage" {
			if hasCurrent {
				pending, pendingAt, pendingRun, hasPending = frame, at, run, true
			}
			continue
		}
		if hasCurrent {
			if hasPending {
				if !frame.Input.Valid {
					frame.Input = pending.Input
				}
				if !frame.Output.Valid {
					frame.Output = pending.Output
				}
				if !frame.Read.Valid {
					frame.Read = pending.Read
				}
				if !frame.Write.Valid {
					frame.Write = pending.Write
				}
			}
			add(at, run, frame)
		} else if hasPending {
			add(at, run, pending)
		}
		pending, pendingAt, pendingRun, hasPending = intentUsageFrame{}, time.Time{}, "", false
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if hasPending {
		add(pendingAt, pendingRun, pending)
	}
	result := make([]intentUsageBucket, 0, len(byDay))
	for _, bucket := range byDay {
		result = append(result, bucket)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Day.Before(result[j].Day) })
	return result, nil
}
