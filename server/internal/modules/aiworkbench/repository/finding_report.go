package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	artexdb "github.com/Autumn-27/artex/db"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

func (s *TaskStore) ReadFindingTraffic(ctx context.Context, user int, findingID int64) (aiapp.FindingTrafficRecord, error) {
	result := aiapp.FindingTrafficRecord{Bindings: []artexdb.FindingTrafficBinding{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Raw(`SELECT f.id,f.node_id,f.task_id,f.evidence_version,f.report_evidence_version FROM tianqiong_ai.findings f JOIN ai_task t ON t.id=f.task_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE t.tenant_id=? AND t.owner_user_id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE AND f.id=? AND f.node_id IS NOT NULL`, assets.DefaultTenantID, user, findingID).Row().Scan(&result.FindingID, &result.NodeID, &result.TaskID, &result.Version, &result.ReportVersion)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Raw(`SELECT b.id,b.finding_id,b.snapshot_id,b.role,b.note,b.position,b.created_at,s.id,s.source_traffic_id,s.captured_at,s.url,s.method,s.status,s.content_type,s.req_head,s.resp_head,s.req_hash,s.resp_hash,s.req_len,s.resp_len,s.created_at FROM tianqiong_ai.finding_traffic_bindings b JOIN tianqiong_ai.traffic_evidence_snapshots s ON s.id=b.snapshot_id WHERE b.finding_id=? ORDER BY b.position,b.id`, findingID).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var binding artexdb.FindingTrafficBinding
			v := &binding.Snapshot
			if err = rows.Scan(&binding.ID, &binding.FindingID, &binding.SnapshotID, &binding.Role, &binding.Note, &binding.Position, &binding.CreatedAt, &v.ID, &v.SourceTrafficID, &v.CapturedAt, &v.URL, &v.Method, &v.Status, &v.ContentType, &v.ReqHead, &v.RespHead, &v.ReqHash, &v.RespHash, &v.ReqLen, &v.RespLen, &v.CreatedAt); err != nil {
				return err
			}
			result.Bindings = append(result.Bindings, binding)
		}
		return rows.Err()
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}

func (s *TaskStore) UpdateFindingReport(ctx context.Context, user int, executionID, toolUseID string, nodeID int64, report string, version *int64) (bool, error) {
	request, _ := json.Marshal(struct {
		NodeID  int64
		Report  string
		Version *int64
	}{nodeID, report, version})
	hash := sha256.Sum256(request)
	digest := hex.EncodeToString(hash[:])
	replayed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scheduleWriteRole(tx, user); err != nil {
			return err
		}
		// Serialize duplicate tool-use IDs even if they name different findings.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?,0))`, executionID+"/"+toolUseID).Error; err != nil {
			return err
		}
		var found string
		err := tx.Raw(`SELECT id::text FROM ai_run WHERE tenant_id=? AND owner_user_id=? AND id=? AND status='running' AND lease_until>clock_timestamp() FOR SHARE`, assets.DefaultTenantID, user, executionID).Row().Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.Raw(`SELECT ?::text FROM ai_task t WHERE t.tenant_id=? AND t.owner_user_id=? AND tianqiong_ai.task_run_live(t.id,t.owner_user_id,?::uuid) FOR SHARE`, executionID, assets.DefaultTenantID, user, executionID).Row().Scan(&found)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrRunLeaseLost
		}
		if err != nil {
			return err
		}
		var prior struct{ Operation, RequestHash string }
		q := tx.Raw(`SELECT operation,request_hash FROM ai_tool_mutation_receipt WHERE tenant_id=? AND execution_id=? AND tool_use_id=?`, assets.DefaultTenantID, executionID, toolUseID).Scan(&prior)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected > 0 {
			if prior.Operation != "finding_report" || prior.RequestHash != digest {
				return aiapp.ErrInvalidInput
			}
			replayed = true
			return nil
		}
		var findingID, taskID, currentVersion int64
		err = tx.Raw(`SELECT f.id,t.id,f.evidence_version FROM tianqiong_ai.findings f JOIN ai_task t ON t.id=f.task_id WHERE t.tenant_id=? AND t.owner_user_id=? AND t.deleted_at IS NULL AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=t.target_id AND deleted_at IS NULL)) AND f.node_id=? FOR UPDATE OF f FOR SHARE OF t`, assets.DefaultTenantID, user, nodeID).Row().Scan(&findingID, &taskID, &currentVersion)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrTaskNotFound
		}
		if err != nil {
			return err
		}
		if version != nil && *version != currentVersion {
			return aiapp.ErrEvidenceVersionConflict
		}
		reportVersion := int64(-1)
		if version != nil {
			reportVersion = *version
		} else if currentVersion == 0 {
			reportVersion = 0
		}
		if err = tx.Exec(`SELECT set_config('tianqiong.task_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE),set_config('tianqiong.execution_id',?,TRUE),set_config('tianqiong.execution_mode','conversation_report',TRUE)`, strconv.FormatInt(taskID, 10), strconv.Itoa(user), executionID).Error; err != nil {
			return err
		}
		if err = tx.Exec(`UPDATE tianqiong_ai.findings SET report=?,report_evidence_version=COALESCE(?::bigint,CASE WHEN evidence_version=0 THEN 0 ELSE -1 END) WHERE id=?`, report, version, findingID).Error; err != nil {
			return err
		}
		// Preserve all existing scanner fields and keep separate reports when
		// several AI findings map to the same product vulnerability.
		if err = tx.Exec(`UPDATE vulnerability v SET raw_output=jsonb_set(CASE WHEN jsonb_typeof(v.raw_output)='object' THEN v.raw_output ELSE jsonb_build_object('original',v.raw_output) END,'{aiReports}',COALESCE(CASE WHEN jsonb_typeof(v.raw_output->'aiReports')='object' THEN v.raw_output->'aiReports' END,'{}'::jsonb)||jsonb_build_object(?::text,jsonb_build_object('report',?::text,'evidenceVersion',?::bigint)),TRUE) FROM ai_finding_risk_link l WHERE l.vulnerability_id=v.id AND l.finding_id=?`, strconv.FormatInt(findingID, 10), report, reportVersion, findingID).Error; err != nil {
			return err
		}
		result, _ := json.Marshal(map[string]any{"findingId": findingID, "nodeId": nodeID, "chars": len([]rune(report))})
		return tx.Exec(`INSERT INTO ai_tool_mutation_receipt(tenant_id,owner_user_id,execution_id,tool_use_id,operation,request_hash,result) VALUES(?,?,?,?,?,?,?::jsonb)`, assets.DefaultTenantID, user, executionID, toolUseID, "finding_report", digest, string(result)).Error
	})
	return replayed, err
}

func (s *TaskStore) SearchTaskTrace(ctx context.Context, user int, taskID int64, query string, limit int) ([]aiapp.TaskActivity, error) {
	items := []aiapp.TaskActivity{}
	err := s.db.WithContext(ctx).Raw(`SELECT a.id,a.node_id,COALESCE(a.worker,'') worker,COALESCE(a.kind,'') kind,COALESCE(a.tool,'') tool,COALESCE(a.tool_use_id,'') tool_use_id,a.is_error,COALESCE(a.summary,'') summary,COALESCE(a.detail,'') detail,a.created_at FROM ai_task t JOIN ai_task source ON source.tenant_id=t.tenant_id AND source.owner_user_id=t.owner_user_id AND source.deleted_at IS NULL JOIN tianqiong_ai.activity a ON a.exploration_id=source.exploration_id JOIN tianqiong_ai.exploration_nodes n ON n.id=a.node_id AND n.kind='intent' JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE t.tenant_id=? AND t.owner_user_id=? AND t.id=? AND t.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE AND (source.id=t.id OR (n.state IN ('done','blocked','exhausted','stopped') AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL)) AND EXISTS(SELECT 1 FROM ai_task_relation r WHERE r.task_id=t.id AND r.source_task_id=source.id AND r.tenant_id=t.tenant_id))) AND COALESCE(a.kind,'') NOT IN ('thinking','usage') AND (strpos(lower(COALESCE(a.summary,'')),lower(?))>0 OR strpos(lower(COALESCE(a.detail,'')),lower(?))>0) ORDER BY a.id DESC LIMIT ?`, assets.DefaultTenantID, user, taskID, query, query, limit).Scan(&items).Error
	return items, err
}

var _ aiapp.FindingReportStore = (*TaskStore)(nil)
var _ aiapp.TaskTraceSearchStore = (*TaskStore)(nil)
