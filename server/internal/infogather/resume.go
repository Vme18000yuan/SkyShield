package infogather

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

// resumeAgenticRun reattaches saved, incomplete work only to a live execution
// owned by the same user in the same conversation. The original execution ID
// is retained for audit, and imported records are never reset or copied.
func (s *Store) resumeAgenticRun(ctx context.Context, user int, execution, id string) (Run, error) {
	if err := s.authorize(ctx, user, execution); err != nil {
		return Run{}, err
	}
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return Run{}, ErrNotFound
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run Run
		var toolID string
		err := tx.Raw(`SELECT status,execution_id::text,coverage,target,target_kind,tool_use_id FROM ai_info_gather_run WHERE id=?::uuid AND tenant_id=?::uuid AND owner_user_id=? FOR UPDATE`, id, assetapp.DefaultTenantID, user).Row().Scan(&run.Status, &run.ExecutionID, &run.Coverage, &run.Target, &run.TargetKind, &toolID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if run.Status != "partial" && run.Status != "interrupted" && run.Status != "running" {
			return errors.New("该采集已结束，不能续接")
		}
		previous := activeExecution(run)
		if previous == "" {
			return errors.New("采集运行状态无效")
		}
		var allowed bool
		err = tx.Raw(`SELECT EXISTS(
			SELECT 1 FROM ai_run original JOIN ai_run current ON current.conversation_id=original.conversation_id AND current.tenant_id=original.tenant_id AND current.owner_user_id=original.owner_user_id
			WHERE original.id=?::uuid AND current.id=?::uuid AND current.tenant_id=?::uuid AND current.owner_user_id=? AND current.status='running' AND current.lease_until>clock_timestamp()
			AND NOT EXISTS (SELECT 1 FROM ai_run previous WHERE previous.id=?::uuid AND previous.id<>current.id AND previous.status='running' AND previous.lease_until>clock_timestamp())
		)`, run.ExecutionID, execution, assetapp.DefaultTenantID, user, previous).Scan(&allowed).Error
		if err != nil {
			return err
		}
		if !allowed {
			return errors.New("仅可在原会话中续接已停止的采集")
		}
		var compatible bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_runtime_seed a JOIN ai_skill s ON a.tenant_id=s.tenant_id AND a.seed_key='builtin_info_gather_resume_v1/' || s.id::text WHERE s.tenant_id=?::uuid AND s.builtin_key='info-gath')`, assetapp.DefaultTenantID).Scan(&compatible).Error; err != nil {
			return err
		}
		if !compatible {
			return errors.New("当前信息收集 Skill 不支持安全续接，请检查自定义脚本")
		}
		var state struct {
			Steps   map[string]string `json:"steps"`
			Session phaseSession      `json:"session"`
		}
		if json.Unmarshal(run.Coverage, &state) != nil || len(state.Steps) == 0 {
			return errors.New("采集没有可续接的阶段结果")
		}
		input := state.Session.Input
		if state.Session.Kind != "agentic" || input == "" {
			var detail string
			err = tx.Raw(`SELECT detail FROM ai_conversation_activity WHERE tenant_id=?::uuid AND owner_user_id=? AND run_id=?::uuid AND tool_use_id=? AND kind='tool_use' AND tool='prepare_info_gather' ORDER BY id LIMIT 1`, assetapp.DefaultTenantID, user, run.ExecutionID, toolID).Row().Scan(&detail)
			if err != nil {
				return errors.New("无法确认原始采集范围")
			}
			var request struct {
				Target string `json:"target"`
			}
			if json.Unmarshal([]byte(detail), &request) != nil {
				return errors.New("无法确认原始采集范围")
			}
			input = request.Target
		}
		target, err := parseScope(input)
		if err != nil || target.Name != run.Target || target.Kind != run.TargetKind {
			return errors.New("原始采集范围不匹配")
		}
		coverage := phaseCoverageState(run.Coverage, target.Input, execution, true)
		return tx.Exec(`UPDATE ai_info_gather_run SET status='running',error_message='',coverage=?::jsonb,updated_at=clock_timestamp() WHERE id=?::uuid AND tenant_id=?::uuid AND owner_user_id=?`, string(coverage), id, assetapp.DefaultTenantID, user).Error
	})
	if err != nil {
		return Run{}, err
	}
	return s.Run(ctx, user, id)
}
