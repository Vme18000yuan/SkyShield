// Package aicleanup deletes owned AI history while retaining registered assets
// and vulnerabilities. PostgreSQL commits a retryable filesystem cleanup job.
package aicleanup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

var (
	ErrForbidden  = errors.New("无权删除这些 AI 数据")
	ErrNotFound   = errors.New("AI 记录不存在")
	ErrBusy       = errors.New("相关 AI 执行仍在运行，请先停止并等待执行结束")
	ErrInvalid    = errors.New("删除参数无效")
	ErrDependency = errors.New("其他 AI 任务仍引用此任务，请先删除依赖任务或解除关联")
)

type Result struct {
	Deleted        int64 `json:"deleted"`
	CleanupPending bool  `json:"cleanupPending"`
}

type cleanupPayload struct {
	Conversations []string `json:"conversations,omitempty"`
	GatherRuns    []string `json:"gather_runs,omitempty"`
	FindingIDs    []int64  `json:"finding_ids,omitempty"`
	TrafficKind   string   `json:"traffic_kind,omitempty"`
}

type cleanupJob struct {
	ID            string
	TenantID      string
	OwnerUserID   int
	Kind          string
	ScopeID       string
	ExplorationID int64
	Payload       json.RawMessage
}

type Service struct {
	db         *gorm.DB
	root       string
	traffic    *aitraffic.Manager
	done       chan struct{}
	activeTask func(int64) bool
}

func New(db *gorm.DB, root string, manager *aitraffic.Manager) *Service {
	return &Service{db: db, root: root, traffic: manager, done: make(chan struct{})}
}

// SetActiveTaskCheck also fences the short interval after a cooperative stop
// commits but before the local executor has closed its files.
func (s *Service) SetActiveTaskCheck(check func(int64) bool) { s.activeTask = check }

func authorize(tx *gorm.DB, user int) error {
	if user <= 0 {
		return ErrForbidden
	}
	var role string
	err := tx.Raw(`SELECT m.role FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=?::uuid AND m.user_id=? AND t.status='active' AND u.is_active FOR SHARE OF m,u`, assets.DefaultTenantID, user).Row().Scan(&role)
	if errors.Is(err, sql.ErrNoRows) || err == nil && role != "admin" && role != "analyst" {
		return ErrForbidden
	}
	return err
}

// Locks the same parent row used for admission, so deletion and a new run
// cannot both be admitted. Queued (not yet executing) tasks may be deleted.
func lockScope(tx *gorm.DB, user int, kind, id string) (int64, error) {
	if err := authorize(tx, user); err != nil {
		return 0, err
	}
	if kind == "conversation" {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return 0, ErrInvalid
		}
		var found string
		err = tx.Raw(`SELECT id::text FROM ai_conversation WHERE tenant_id=?::uuid AND owner_user_id=? AND id=?::uuid FOR UPDATE`, assets.DefaultTenantID, user, id).Row().Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		if err != nil {
			return 0, err
		}
		var busy bool
		err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_run WHERE conversation_id=?::uuid AND status IN ('running','stopping'))`, id).Scan(&busy).Error
		if err == nil && busy {
			err = ErrBusy
		}
		return 0, err
	}
	if kind != "task" {
		return 0, ErrInvalid
	}
	task, err := strconv.ParseInt(id, 10, 64)
	if err != nil || task <= 0 || strconv.FormatInt(task, 10) != id {
		return 0, ErrInvalid
	}
	var exp int64
	var status string
	err = tx.Raw(`SELECT exploration_id,status FROM ai_task WHERE tenant_id=?::uuid AND owner_user_id=? AND id=? FOR UPDATE`, assets.DefaultTenantID, user, task).Row().Scan(&exp, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	var busy bool
	err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_task_main_chat_run WHERE task_id=? AND status IN ('running','stopping')) OR EXISTS(SELECT 1 FROM ai_agent_trigger_job j JOIN ai_run r ON r.id=j.run_id WHERE j.source_task_id=? AND r.status IN ('running','stopping')) OR EXISTS(SELECT 1 FROM ai_credential_job WHERE task_id=? AND status='processing' AND lease_until>clock_timestamp())`, task, task, task).Scan(&busy).Error
	if err == nil && (status == "running" || busy) {
		err = ErrBusy
	}
	return exp, err
}

func queue(tx *gorm.DB, user int, kind, id string, exp int64, payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	job := uuid.NewString()
	err = tx.Exec(`INSERT INTO ai_storage_cleanup(id,tenant_id,owner_user_id,kind,scope_id,exploration_id,payload) VALUES(?::uuid,?::uuid,?,?,?,?,?::jsonb)`, job, assets.DefaultTenantID, user, kind, id, exp, string(raw)).Error
	return job, err
}

func (s *Service) DeleteConversation(ctx context.Context, user int, id string) (bool, error) {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		result, err := s.DeleteScope(ctx, user, "conversation", id)
		if !errors.Is(err, ErrBusy) {
			return result.CleanupPending, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, ErrBusy
		case <-tick.C:
		}
	}
}

func (s *Service) DeleteScope(ctx context.Context, user int, kind, id string) (Result, error) {
	var jobID string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Use the dispatcher's lock order before taking a conversation/task lock.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(?,?)`, 0x4147454e, 0x4a4f4253).Error; err != nil {
			return err
		}
		exp, err := lockScope(tx, user, kind, id)
		if err != nil {
			return err
		}
		if kind == "task" && s.activeTask != nil {
			taskID, _ := strconv.ParseInt(id, 10, 64)
			if s.activeTask(taskID) {
				return ErrBusy
			}
		}
		payload := cleanupPayload{}
		if kind == "conversation" {
			payload.Conversations = []string{id}
		} else {
			if err = tx.Raw(`SELECT id FROM tianqiong_ai.findings WHERE task_id=?::bigint`, id).Scan(&payload.FindingIDs).Error; err != nil {
				return err
			}
			var shared bool
			if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_task_relation WHERE source_task_id=?::bigint)`, id).Scan(&shared).Error; err != nil {
				return err
			}
			if shared {
				return ErrDependency
			}
			// A queued trigger may not have a conversation yet. Cancel it before
			// the task FK becomes NULL, otherwise dispatch could create a new chat.
			if err = tx.Exec(`UPDATE ai_agent_trigger_job SET status='cancelled',error_message='来源任务已删除',updated_at=clock_timestamp() WHERE tenant_id=?::uuid AND owner_user_id=? AND source_task_id=?::bigint AND status IN ('queued','starting','running')`, assets.DefaultTenantID, user, id).Error; err != nil {
				return err
			}
			if err = tx.Raw(`SELECT DISTINCT c.id::text FROM ai_agent_trigger_job j JOIN ai_conversation c ON c.id=j.conversation_id WHERE j.source_task_id=?::bigint AND c.tenant_id=?::uuid AND c.owner_user_id=? ORDER BY c.id::text`, id, assets.DefaultTenantID, user).Scan(&payload.Conversations).Error; err != nil {
				return err
			}
			for _, conversation := range payload.Conversations {
				if _, err = lockScope(tx, user, "conversation", conversation); err != nil {
					return err
				}
			}
		}
		if len(payload.Conversations) > 0 {
			if err = tx.Raw(`SELECT g.id::text FROM ai_info_gather_run g WHERE g.tenant_id=?::uuid AND g.owner_user_id=? AND g.execution_id IN (SELECT id FROM ai_run WHERE conversation_id=ANY(?::uuid[]))`, assets.DefaultTenantID, user, pq.StringArray(payload.Conversations)).Scan(&payload.GatherRuns).Error; err != nil {
				return err
			}
		}
		jobID, err = queue(tx, user, kind, id, exp, payload)
		if err != nil {
			return err
		}
		if len(payload.Conversations) > 0 {
			args := pq.StringArray(payload.Conversations)
			if err = tx.Exec(`UPDATE ai_agent_trigger_job SET status='cancelled',error_message='会话已删除',updated_at=clock_timestamp() WHERE tenant_id=?::uuid AND owner_user_id=? AND conversation_id=ANY(?::uuid[]) AND status IN ('queued','starting','running')`, assets.DefaultTenantID, user, args).Error; err != nil {
				return err
			}
			for _, statement := range []string{
				`DELETE FROM ai_llm_record WHERE tenant_id=?::uuid AND owner_user_id=? AND conversation_id=ANY(?::uuid[])`,
				`DELETE FROM ai_info_gather_run WHERE tenant_id=?::uuid AND owner_user_id=? AND execution_id IN (SELECT id FROM ai_run WHERE conversation_id=ANY(?::uuid[]))`,
				`DELETE FROM ai_chat_attachment WHERE tenant_id=?::uuid AND owner_user_id=? AND scope='session' AND scope_id=ANY(?::text[])`,
				`DELETE FROM ai_conversation WHERE tenant_id=?::uuid AND owner_user_id=? AND id=ANY(?::uuid[])`,
			} {
				if err = tx.Exec(statement, assets.DefaultTenantID, user, args).Error; err != nil {
					return err
				}
			}
		}
		if kind == "task" {
			if err = tx.Exec(`SELECT set_config('tianqiong.execution_mode','storage_cleanup',TRUE),set_config('tianqiong.storage_cleanup_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE)`, jobID, strconv.Itoa(user)).Error; err != nil {
				return err
			}
			// Delete graph-owned rows while their parent identities are still present.
			// Global asset_entity/vulnerability rows and standalone reports are untouched.
			for _, statement := range []string{
				`DELETE FROM tianqiong_ai.findings WHERE task_id=?::bigint`,
				`DELETE FROM tianqiong_ai.task_intelligence WHERE task_id=?::bigint`,
				`DELETE FROM ai_llm_record WHERE task_id=?::bigint`,
				`DELETE FROM ai_llm_record_epoch WHERE task_id=?::bigint`,
				`DELETE FROM ai_chat_attachment WHERE scope='task' AND scope_id=?`,
			} {
				if err = tx.Exec(statement, id).Error; err != nil {
					return err
				}
			}
			for _, statement := range []string{
				`DELETE FROM tianqiong_ai.activity WHERE exploration_id=?`,
				`DELETE FROM tianqiong_ai.exploration_anchors WHERE node_id IN (SELECT id FROM tianqiong_ai.exploration_nodes WHERE exploration_id=?)`,
				`DELETE FROM tianqiong_ai.exploration_edges WHERE exploration_id=?`,
				`DELETE FROM tianqiong_ai.task_constraints WHERE exploration_id=?`,
				`DELETE FROM tianqiong_ai.exploration_nodes WHERE exploration_id=?`,
			} {
				if err = tx.Exec(statement, exp).Error; err != nil {
					return err
				}
			}
			if err = tx.Exec(`DELETE FROM ai_task WHERE id=?::bigint`, id).Error; err != nil {
				return err
			}
			if err = tx.Exec(`DELETE FROM tianqiong_ai.explorations WHERE id=?`, exp).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	// The durable job remains if a worker is draining or filesystem removal fails.
	pending := s.cleanOne(ctx, jobID) != nil
	return Result{Deleted: 1, CleanupPending: pending}, nil
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			s.cleanPending(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (s *Service) Done() <-chan struct{} { return s.done }
func (s *Service) cleanPending(ctx context.Context) {
	var ids []string
	if err := s.db.WithContext(ctx).Raw(`SELECT id::text FROM ai_storage_cleanup WHERE next_attempt_at<=clock_timestamp() ORDER BY created_at LIMIT 50`).Scan(&ids).Error; err != nil {
		if ctx.Err() == nil {
			slog.Warn("AI 文件清理队列不可用")
		}
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		_ = s.cleanOne(ctx, id)
	}
}
func (s *Service) cleanOne(ctx context.Context, id string) error {
	var cleanupErr error
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job cleanupJob
		result := tx.Raw(`SELECT * FROM ai_storage_cleanup WHERE id=?::uuid FOR UPDATE SKIP LOCKED`, id).Scan(&job)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var exists bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_storage_cleanup WHERE id=?::uuid)`, id).Scan(&exists).Error; err != nil {
				return err
			}
			if exists {
				cleanupErr = ErrBusy
			}
			return nil
		}
		var restored bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_conversation WHERE ?='conversation' AND id::text=? AND tenant_id=?::uuid AND owner_user_id=?) OR EXISTS(SELECT 1 FROM ai_task WHERE ?='task' AND id::text=? AND tenant_id=?::uuid AND owner_user_id=?)`, job.Kind, job.ScopeID, job.TenantID, job.OwnerUserID, job.Kind, job.ScopeID, job.TenantID, job.OwnerUserID).Scan(&restored).Error; err != nil {
			return err
		}
		taskID, _ := strconv.ParseInt(job.ScopeID, 10, 64)
		if restored || job.Kind == "task" && s.activeTask != nil && s.activeTask(taskID) {
			cleanupErr = ErrBusy
		} else if job.Kind == "traffic" {
			cleanupErr = s.finishTrafficCleanup(ctx, tx, job)
		} else {
			cleanupErr = s.removeFiles(ctx, job)
		}
		if cleanupErr != nil {
			if !errors.Is(cleanupErr, ErrBusy) {
				slog.Warn("AI 文件清理失败，将重试", "cleanup_id", id, "error", cleanupErr)
			}
			return tx.Exec(`UPDATE ai_storage_cleanup SET attempts=attempts+1,last_error=?,next_attempt_at=clock_timestamp()+interval '15 seconds' WHERE id=?::uuid`, fmt.Sprintf("%T: %s", cleanupErr, cleanupErr.Error()), id).Error
		}
		return tx.Exec(`DELETE FROM ai_storage_cleanup WHERE id=?::uuid`, id).Error
	})
	if err != nil {
		return err
	}
	return cleanupErr
}
