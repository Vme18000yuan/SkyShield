package infogather

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

// FinalizeAfterExecutionFailure performs no new collection. While the original
// execution lease is still active, it turns completed phase checkpoints into
// a partial report and uses the same authorization, scope and asset gates as
// an ordinary finish_info_gather call.
func (s *Service) FinalizeAfterExecutionFailure(ctx context.Context, user int, execution string) (string, error) {
	if err := s.store.authorize(ctx, user, execution); err != nil {
		return "", err
	}
	rows, err := s.store.db.WithContext(ctx).Raw(`SELECT id::text FROM ai_info_gather_run WHERE tenant_id=?::uuid AND owner_user_id=? AND (execution_id=?::uuid OR coverage->'session'->>'active_execution'=?) AND status='running' ORDER BY created_at LIMIT 20`, assetapp.DefaultTenantID, user, execution, execution).Rows()
	if err != nil {
		return "", err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	var summaries []string
	for _, id := range ids {
		run, err := s.store.Run(ctx, user, id)
		if err != nil {
			return strings.Join(summaries, "\n"), err
		}
		if _, err := phaseTarget(run, execution); err != nil {
			continue // Legacy one-call or non-agentic run.
		}
		var state struct {
			Steps map[string]string `json:"steps"`
		}
		if json.Unmarshal(run.Coverage, &state) != nil || len(state.Steps) == 0 {
			continue // Do not manufacture results for an unstarted scan.
		}
		result, err := s.finishPhases(ctx, user, execution, id, nil)
		if err != nil {
			return strings.Join(summaries, "\n"), err
		}
		imported := 0
		if result.Sync != nil {
			imported = result.Sync.Imported
		}
		summaries = append(summaries, fmt.Sprintf("AI 对话未能继续；系统已汇总此前完成的采集阶段，已同步 %d 条记录，%d 条待确认。%s", imported, result.Run.Pending, result.Message))
	}
	return strings.Join(summaries, "\n"), nil
}
