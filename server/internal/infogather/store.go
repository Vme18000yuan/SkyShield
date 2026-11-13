package infogather

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	securityapp "github.com/yyhuni/lunafox/server/internal/modules/security/application"
	"gorm.io/gorm"
)

var ErrForbidden = errors.New("当前账号无信息收集权限")
var ErrNotFound = errors.New("信息收集记录不存在")

type Run struct {
	ID           string          `json:"id"`
	Target       string          `json:"target"`
	TargetKind   string          `json:"targetKind"`
	Mode         string          `json:"mode"`
	Status       string          `json:"status"`
	Error        string          `json:"error"`
	Coverage     json.RawMessage `json:"coverage"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	Pending      int             `json:"pending"`
	Imported     int             `json:"imported"`
	Observations int             `json:"observations"`
	ExecutionID  string          `json:"-"`
}

type Record struct {
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Payload  json.RawMessage `json:"payload"`
	Status   string          `json:"status"`
	TargetID *int            `json:"targetId"`
	Reason   string          `json:"reason"`
}

type registrationWriter interface {
	Register(context.Context, assetapp.BatchRegisterInput) (*assetapp.BatchRegisterResult, error)
}
type vulnerabilityWriter interface {
	BatchCreateContext(context.Context, int, []securityapp.VulnerabilityCreateItem) (int64, error)
}

type Store struct {
	db     *gorm.DB
	assets registrationWriter
	risks  vulnerabilityWriter
}

func NewStore(db *gorm.DB, assets registrationWriter, risks vulnerabilityWriter) *Store {
	return &Store{db: db, assets: assets, risks: risks}
}

func (s *Store) authorize(ctx context.Context, user int, execution string) error {
	if s == nil || s.db == nil || user <= 0 {
		return ErrForbidden
	}
	query := `SELECT EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
WHERE m.tenant_id=?::uuid AND m.user_id=? AND m.role IN ('admin','analyst') AND t.status='active' AND u.is_active`
	args := []any{assetapp.DefaultTenantID, user}
	if execution != "" {
		if parsed, err := uuid.Parse(execution); err != nil || parsed.String() != execution {
			return ErrForbidden
		}
		query += ` AND EXISTS(SELECT 1 FROM ai_run r JOIN ai_conversation c ON c.id=r.conversation_id AND c.owner_user_id=r.owner_user_id AND c.tenant_id=r.tenant_id
JOIN ai_tool_catalog tool ON tool.tenant_id=r.tenant_id AND tool.key='run_info_gather' AND tool.system AND tool.enabled AND tool.agents @> '["info_gatherer"]'::jsonb
WHERE r.id=?::uuid AND r.tenant_id=m.tenant_id AND r.owner_user_id=m.user_id AND r.status='running' AND r.lease_until>clock_timestamp() AND c.agent_key='info_gatherer'))`
		args = append(args, execution)
	} else {
		query += ")"
	}
	var allowed bool
	if err := s.db.WithContext(ctx).Raw(query, args...).Scan(&allowed).Error; err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func (s *Store) start(ctx context.Context, user int, execution, toolID string, target Scope, mode string) (string, bool, error) {
	return s.startWithCoverage(ctx, user, execution, toolID, target, mode, nil)
}

func (s *Store) startWithCoverage(ctx context.Context, user int, execution, toolID string, target Scope, mode string, coverage json.RawMessage) (string, bool, error) {
	if err := s.authorize(ctx, user, execution); err != nil {
		return "", false, err
	}
	if toolID == "" || len(toolID) > 200 {
		return "", false, errors.New("缺少可信工具调用标识")
	}
	if len(coverage) == 0 {
		coverage = json.RawMessage(`{}`)
	}
	id := uuid.NewString()
	result := s.db.WithContext(ctx).Exec(`INSERT INTO ai_info_gather_run(id,tenant_id,owner_user_id,execution_id,tool_use_id,target,target_kind,mode,status,coverage)
VALUES(?::uuid,?::uuid,?,?::uuid,?,?,?,?, 'running',?::jsonb) ON CONFLICT(tenant_id,owner_user_id,execution_id,tool_use_id) DO NOTHING`,
		id, assetapp.DefaultTenantID, user, execution, toolID, target.Name, target.Kind, mode, string(coverage))
	if result.Error != nil {
		return "", false, result.Error
	}
	if result.RowsAffected == 1 {
		return id, true, nil
	}
	var existingID, name, kind, existingMode string
	err := s.db.WithContext(ctx).Raw(`SELECT id::text,target,target_kind,mode FROM ai_info_gather_run WHERE tenant_id=?::uuid AND owner_user_id=? AND execution_id=?::uuid AND tool_use_id=?`,
		assetapp.DefaultTenantID, user, execution, toolID).Row().Scan(&existingID, &name, &kind, &existingMode)
	if err != nil {
		return "", false, err
	}
	if name != target.Name || kind != target.Kind || existingMode != mode {
		return "", false, errors.New("工具调用标识已用于其他采集目标")
	}
	return existingID, false, nil
}

// checkpoint records validated phase output while the conversation is still
// running. The original input is server-owned metadata, not model-supplied
// state from the writable skill workspace.
func (s *Store) checkpoint(ctx context.Context, user int, execution, id string, report *Envelope, coverage json.RawMessage) error {
	if err := s.authorize(ctx, user, execution); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run Run
		err := tx.Raw(`SELECT status,execution_id::text,coverage FROM ai_info_gather_run WHERE id=?::uuid AND tenant_id=?::uuid AND owner_user_id=? FOR UPDATE`, id, assetapp.DefaultTenantID, user).Row().Scan(&run.Status, &run.ExecutionID, &run.Coverage)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if run.Status != "running" || activeExecution(run) != execution {
			return errors.New("采集运行已结束或不属于当前对话")
		}
		if err := saveReportRecords(tx, id, report); err != nil {
			return err
		}
		return tx.Exec(`UPDATE ai_info_gather_run SET coverage=?::jsonb,updated_at=clock_timestamp() WHERE id=?::uuid`, string(coverage), id).Error
	})
}

func saveReportRecords(tx *gorm.DB, id string, report *Envelope) error {
	if report == nil {
		return nil
	}
	type row struct {
		kind, id string
		payload  []byte
	}
	rows := make([]row, 0, len(report.Assets)+len(report.Vulnerabilities)+len(report.Observations))
	for _, value := range report.Assets {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		rows = append(rows, row{"asset", value.ID, b})
	}
	for _, value := range report.Vulnerabilities {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		rows = append(rows, row{"vulnerability", value.ID, b})
	}
	vulnerabilities := make(map[string]Vulnerability, len(report.Vulnerabilities))
	for _, value := range report.Vulnerabilities {
		vulnerabilities[value.ID] = value
	}
	for _, value := range report.Observations {
		if existing, ok := vulnerabilities[value.ID]; ok && sameRiskObservation(value, existing) {
			continue
		}
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		rows = append(rows, row{"observation", value.ID, b})
	}
	for start := 0; start < len(rows); start += 100 {
		end := min(start+100, len(rows))
		placeholders := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*4)
		for _, value := range rows[start:end] {
			placeholders = append(placeholders, "(?::uuid,?,?,?::jsonb)")
			args = append(args, id, value.kind, value.id, string(value.payload))
		}
		q := `INSERT INTO ai_info_gather_record(run_id,kind,record_id,payload) VALUES ` + strings.Join(placeholders, ",") +
			` ON CONFLICT(run_id,kind,record_id) DO UPDATE SET payload=EXCLUDED.payload,updated_at=clock_timestamp() WHERE ai_info_gather_record.status='pending'`
		if err := tx.Exec(q, args...).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) findTargets(ctx context.Context, requested Scope) ([]assetdomain.TargetRef, error) {
	rows, err := s.db.WithContext(ctx).Raw(`SELECT id,name,type FROM target WHERE deleted_at IS NULL ORDER BY name,id`).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []assetdomain.TargetRef{}
	for rows.Next() {
		var target assetdomain.TargetRef
		if err = rows.Scan(&target.ID, &target.Name, &target.Type); err != nil {
			return nil, err
		}
		if targetContainsScope(target, requested) {
			out = append(out, target)
		}
	}
	return out, rows.Err()
}

func (s *Store) finish(ctx context.Context, user int, id, status, message string, report *Envelope, coverage json.RawMessage) error {
	if err := s.authorize(ctx, user, ""); err != nil {
		return err
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	if len(coverage) == 0 {
		coverage = json.RawMessage(`{}`)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current string
		err := tx.Raw(`SELECT status FROM ai_info_gather_run WHERE id=?::uuid AND tenant_id=?::uuid AND owner_user_id=? FOR UPDATE`, id, assetapp.DefaultTenantID, user).Row().Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if current != "running" {
			return nil
		}
		if err := saveReportRecords(tx, id, report); err != nil {
			return err
		}
		return tx.Exec(`UPDATE ai_info_gather_run SET status=?,error_message=?,coverage=?::jsonb,updated_at=clock_timestamp() WHERE id=?::uuid`, status, message, string(coverage), id).Error
	})
}

func (s *Store) recoverStale(ctx context.Context, user int) error {
	return s.db.WithContext(ctx).Exec(`UPDATE ai_info_gather_run g SET status='interrupted',error_message='采集运行已中断，已保存的记录可继续处理',updated_at=clock_timestamp()
WHERE g.tenant_id=?::uuid AND g.owner_user_id=? AND g.status='running' AND NOT EXISTS(SELECT 1 FROM ai_run r WHERE r.id::text=CASE WHEN g.coverage->'session'->>'kind'='agentic' THEN COALESCE(NULLIF(g.coverage->'session'->>'active_execution',''),g.execution_id::text) ELSE g.execution_id::text END AND r.tenant_id=g.tenant_id AND r.owner_user_id=g.owner_user_id AND r.status='running' AND r.lease_until>clock_timestamp())`, assetapp.DefaultTenantID, user).Error
}

const runColumns = `g.id::text,g.target,g.target_kind,g.mode,g.status,g.error_message,g.coverage,g.created_at,g.updated_at,g.execution_id::text,
(SELECT count(*) FROM ai_info_gather_record r WHERE r.run_id=g.id AND r.status='pending' AND (r.kind<>'observation' OR r.payload->>'category' IN ('ANONYMOUS_DATA','REVIEW_TOKEN_DIFFERENCE','PUBLIC_LISTING','DNS_ZONE_TRANSFER'))),
(SELECT count(*) FROM ai_info_gather_record r WHERE r.run_id=g.id AND r.status='imported'),
(SELECT count(*) FROM ai_info_gather_record r WHERE r.run_id=g.id AND r.status='pending' AND r.kind='observation' AND COALESCE(r.payload->>'category','') NOT IN ('ANONYMOUS_DATA','REVIEW_TOKEN_DIFFERENCE','PUBLIC_LISTING','DNS_ZONE_TRANSFER'))`

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var out Run
	err := row.Scan(&out.ID, &out.Target, &out.TargetKind, &out.Mode, &out.Status, &out.Error, &out.Coverage, &out.CreatedAt, &out.UpdatedAt, &out.ExecutionID, &out.Pending, &out.Imported, &out.Observations)
	return out, err
}

func (s *Store) Runs(ctx context.Context, user int) ([]Run, error) {
	if err := s.authorize(ctx, user, ""); err != nil {
		return nil, err
	}
	if err := s.recoverStale(ctx, user); err != nil {
		return nil, err
	}
	rows, err := s.db.WithContext(ctx).Raw(`SELECT `+runColumns+` FROM ai_info_gather_run g WHERE g.tenant_id=?::uuid AND g.owner_user_id=? ORDER BY g.created_at DESC,g.id LIMIT 200`, assetapp.DefaultTenantID, user).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Run{}
	for rows.Next() {
		item, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Run(ctx context.Context, user int, id string) (Run, error) {
	if err := s.authorize(ctx, user, ""); err != nil {
		return Run{}, err
	}
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return Run{}, ErrNotFound
	}
	row := s.db.WithContext(ctx).Raw(`SELECT `+runColumns+` FROM ai_info_gather_run g WHERE g.id=?::uuid AND g.tenant_id=?::uuid AND g.owner_user_id=?`, id, assetapp.DefaultTenantID, user).Row()
	out, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return out, err
}

func (s *Store) Records(ctx context.Context, user int, id string, offset int) ([]Record, int, error) {
	if _, err := s.Run(ctx, user, id); err != nil {
		return nil, 0, err
	}
	if offset < 0 || offset > 1000000 {
		return nil, 0, errors.New("分页参数无效")
	}
	var total int
	if err := s.db.WithContext(ctx).Raw(`SELECT count(*) FROM ai_info_gather_record WHERE run_id=?::uuid AND status='pending'`, id).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	rows, err := s.db.WithContext(ctx).Raw(`SELECT kind,record_id,payload,status,target_id,reason FROM ai_info_gather_record WHERE run_id=?::uuid AND status='pending' ORDER BY kind,record_id LIMIT 100 OFFSET ?`, id, offset).Rows()
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var item Record
		var target sql.NullInt64
		if err := rows.Scan(&item.Kind, &item.ID, &item.Payload, &item.Status, &target, &item.Reason); err != nil {
			return nil, 0, err
		}
		if target.Valid {
			v := int(target.Int64)
			item.TargetID = &v
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

// IgnoreSelected is an explicit, reversible-in-storage disposition. Evidence
// remains in the database and is not re-created by later checkpoints.
func (s *Store) IgnoreSelected(ctx context.Context, user int, id string, refs []RecordRef) (int, error) {
	run, err := s.Run(ctx, user, id)
	if err != nil {
		return 0, err
	}
	if run.Status == "running" || len(refs) == 0 || len(refs) > 100 {
		return 0, errors.New("请选择已结束采集中的 1 至 100 条记录")
	}
	seen := map[RecordRef]bool{}
	for _, ref := range refs {
		if (ref.Kind != "asset" && ref.Kind != "vulnerability" && ref.Kind != "observation") || !recordIDPattern.MatchString(ref.ID) || seen[ref] {
			return 0, errors.New("待处理记录无效或重复")
		}
		seen[ref] = true
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var role string
		if err := tx.Raw(`SELECT m.role FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=?::uuid AND m.user_id=? AND t.status='active' AND u.is_active FOR SHARE OF m,u`, assetapp.DefaultTenantID, user).Row().Scan(&role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrForbidden
			}
			return err
		}
		if role != "admin" && role != "analyst" {
			return ErrForbidden
		}
		var status string
		if err := tx.Raw(`SELECT status FROM ai_info_gather_run WHERE id=?::uuid AND tenant_id=?::uuid AND owner_user_id=? FOR UPDATE`, id, assetapp.DefaultTenantID, user).Row().Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if status == "running" {
			return errors.New("采集尚未结束")
		}
		for _, ref := range refs {
			result := tx.Exec(`UPDATE ai_info_gather_record SET status='ignored',reason='用户已标记处理',updated_at=clock_timestamp() WHERE run_id=?::uuid AND kind=? AND record_id=? AND status='pending'`, id, ref.Kind, ref.ID)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("部分记录已被处理，请刷新后重试")
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(refs), nil
}

func (r Run) scope() Scope   { return Scope{Name: r.Target, Kind: r.TargetKind, Input: r.Target} }
func (r Run) String() string { return fmt.Sprintf("%s: %s", r.ID, r.Status) }
