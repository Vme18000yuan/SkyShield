// Package aivuln registers independent-chat findings in the vulnerability
// inventory. Graph-backed tasks keep their own report_finding projection.
package aivuln

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	contractresults "github.com/yyhuni/lunafox/contracts/results"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	securityapp "github.com/yyhuni/lunafox/server/internal/modules/security/application"
	securitydomain "github.com/yyhuni/lunafox/server/internal/modules/security/domain"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

var ErrForbidden = errors.New("无权访问该对话或运行已结束")
var ErrNotFound = errors.New("证据不存在或无权访问")

type Writer interface {
	CreateAIReportContext(context.Context, int, securityapp.VulnerabilityCreateItem) (int64, error)
}
type TrafficReader interface {
	Exchange(context.Context, int, string, string) (string, string, error)
}
type Service struct {
	db      *gorm.DB
	root    string
	writer  Writer
	Traffic TrafficReader
}

func New(db *gorm.DB, root string, writer Writer) *Service {
	return &Service{db: db, root: root, writer: writer}
}

type Report struct {
	TargetID      int      `json:"target_id,omitempty"`
	Name          string   `json:"name,omitempty"`
	URL           string   `json:"url"`
	VulnType      string   `json:"vuln_type"`
	Severity      string   `json:"severity"`
	Summary       string   `json:"summary"`
	Evidence      string   `json:"evidence"`
	EvidencePaths []string `json:"evidence_paths,omitempty"`
	TrafficIDs    []string `json:"traffic_ids,omitempty"`
	Request       string   `json:"request,omitempty"`
	Response      string   `json:"response,omitempty"`
}
type Result struct {
	VulnerabilityID int    `json:"vulnerability_id"`
	Created         bool   `json:"created"`
	Message         string `json:"message"`
}

func validateReport(r Report) error {
	if _, err := contractresults.ValidateObservedAssetURL(r.URL); err != nil || r.TargetID < 0 || len(r.URL) > 8192 {
		return errors.New("请提供有效的 HTTP(S) URL 和目标 ID")
	}
	if utf8.RuneCountInString(r.Name) > 1000 || strings.TrimSpace(r.VulnType) == "" || len(r.VulnType) > 200 || strings.TrimSpace(r.Summary) == "" || len(r.Summary) > 32000 || len(r.Evidence) > 64000 || len(r.Request) > maxEvidenceBytes || len(r.Response) > maxEvidenceBytes {
		return errors.New("漏洞类型、描述或证据长度无效")
	}
	if r.Severity != securitydomain.NormalizeSeverity(r.Severity) {
		return errors.New("严重程度无效")
	}
	if !utf8.ValidString(r.Name+r.URL+r.VulnType+r.Summary+r.Evidence+r.Request+r.Response) || strings.ContainsRune(r.Name+r.URL+r.VulnType+r.Summary+r.Evidence+r.Request+r.Response, '\x00') || len(r.EvidencePaths) > 8 || len(r.TrafficIDs) > 8 {
		return errors.New("证据格式无效或数量超过限制")
	}
	if strings.TrimSpace(r.Evidence+r.Request+r.Response) == "" && len(r.EvidencePaths)+len(r.TrafficIDs) == 0 {
		return errors.New("请提供证据说明、证据文件或已记录的流量 ID")
	}
	for _, p := range r.EvidencePaths {
		if !safeEvidencePath(p) {
			return errors.New("证据路径必须位于当前会话工作目录")
		}
	}
	for _, id := range r.TrafficIDs {
		if len(id) == 0 || len(id) > 128 || !utf8.ValidString(id) || strings.ContainsRune(id, '\x00') {
			return errors.New("流量 ID 无效")
		}
	}
	return nil
}

// Register fences writes with the live run, ownership and role in one transaction.
// A retry returns the previous ID; an unrelated existing finding keeps its human
// disposition and provenance, while this report is retained as separate evidence.
func (s *Service) Register(ctx context.Context, user int, run, call string, input Report) (Result, error) {
	var out Result
	if _, err := uuid.Parse(run); err != nil || user <= 0 || call == "" || len(call) > 200 || strings.ContainsRune(call, '\x00') || !utf8.ValidString(call) {
		return out, ErrForbidden
	}
	if err := validateReport(input); err != nil {
		return out, err
	}
	canonical, _ := json.Marshal(input)
	hash := fmt.Sprintf("%x", sha256.Sum256(canonical))
	newReceipt := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conversation, tenant string
		err := tx.Raw(`SELECT r.conversation_id::text,r.tenant_id::text FROM ai_run r
JOIN ai_conversation c ON c.id=r.conversation_id AND c.owner_user_id=r.owner_user_id AND c.tenant_id=r.tenant_id
JOIN tenant_membership m ON m.tenant_id=r.tenant_id AND m.user_id=r.owner_user_id
JOIN auth_user u ON u.id=m.user_id JOIN tenant t ON t.id=m.tenant_id
JOIN ai_tool_catalog tool ON tool.tenant_id=r.tenant_id AND tool.key='report_vulnerability' AND tool.system AND tool.enabled AND tool.agents @> jsonb_build_array(c.agent_key)
WHERE r.id=?::uuid AND r.owner_user_id=? AND r.tenant_id=?::uuid AND r.status='running' AND r.lease_until>clock_timestamp()
AND m.role IN ('admin','analyst') AND u.is_active AND t.status='active' FOR UPDATE OF r FOR SHARE OF c,m,u,t,tool`, run, user, assetapp.DefaultTenantID).Row().Scan(&conversation, &tenant)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		var previousHash string
		err = tx.Raw(`SELECT vulnerability_id,input_hash FROM ai_conversation_vulnerability_report WHERE run_id=?::uuid AND tool_use_id=?`, run, call).Row().Scan(&out.VulnerabilityID, &previousHash)
		if err == nil {
			if hash != previousHash {
				return errors.New("同一工具调用不能登记不同的漏洞")
			}
			out.Message = "漏洞已登记"
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var targets []securitydomain.TargetRef
		q := tx.Table("target").Select("id,name,type").Where("deleted_at IS NULL")
		if input.TargetID > 0 {
			q = q.Where("id=?", input.TargetID)
		}
		if err = q.Find(&targets).Error; err != nil {
			return err
		}
		var matches []securitydomain.TargetRef
		for _, target := range targets {
			if securitydomain.IsURLMatchTarget(input.URL, target) {
				matches = append(matches, target)
			}
		}
		if len(matches) != 1 {
			return errors.New("URL 未唯一匹配已有目标，请先确认资产归属并提供 target_id")
		}
		var targetID int
		if err = tx.Raw(`SELECT id FROM target WHERE id=? AND deleted_at IS NULL FOR SHARE`, matches[0].ID).Row().Scan(&targetID); err != nil {
			return err
		}
		evidence, err := s.snapshot(ctx, user, tenant, conversation, input)
		if err != nil {
			return err
		}
		raw := map[string]any{"aiConversationId": conversation, "aiRunId": run, "evidence": input.Evidence, "status": "needs_review", "confidence": "agent_report"}
		name := strings.TrimSpace(input.Name)
		if name == "" {
			// Older tool calls did not carry a separate title. Preserve their
			// original label rather than inventing a name from evidence prose.
			name = strings.TrimSpace(input.VulnType)
		}
		created, err := s.writer.CreateAIReportContext(dbtx.WithTransaction(ctx, tx), targetID, securityapp.VulnerabilityCreateItem{URL: input.URL, DisplayName: name, VulnType: input.VulnType, Severity: input.Severity, Source: "ai", Description: input.Summary, RawOutput: raw})
		if err != nil {
			return err
		}
		if err = tx.Raw(`SELECT id FROM vulnerability WHERE target_id=? AND url=? AND vuln_type=? AND severity=?`, targetID, input.URL, input.VulnType, input.Severity).Row().Scan(&out.VulnerabilityID); err != nil {
			return err
		}
		// Link an existing registry endpoint without creating a speculative asset.
		if err = tx.Exec(`UPDATE vulnerability v SET asset_id=(SELECT min(b.asset_id) FROM endpoint e JOIN asset_endpoint_binding b ON b.endpoint_id=e.id JOIN asset_entity a ON a.id=b.asset_id WHERE a.tenant_id=?::uuid AND e.target_id=v.target_id AND e.url=v.url HAVING count(DISTINCT b.asset_id)=1) WHERE v.id=? AND v.asset_id IS NULL`, tenant, out.VulnerabilityID).Error; err != nil {
			return err
		}
		body, err := json.Marshal(evidence)
		if err != nil {
			return err
		}
		var live bool
		if err = tx.Raw(`SELECT lease_until>clock_timestamp() FROM ai_run WHERE id=?::uuid`, run).Row().Scan(&live); err != nil {
			return err
		}
		if !live {
			return ErrForbidden
		}
		if err = tx.Exec(`INSERT INTO ai_conversation_vulnerability_report(tenant_id,owner_user_id,conversation_id,run_id,tool_use_id,input_hash,vulnerability_id,report,evidence) VALUES(?::uuid,?,?::uuid,?::uuid,?,?,?,?::jsonb,?::jsonb)`, tenant, user, conversation, run, call, hash, out.VulnerabilityID, string(canonical), string(body)).Error; err != nil {
			return err
		}
		newReceipt = true
		out.Created = created > 0
		out.Message = "已同步到漏洞管理，等待人工复核"
		return nil
	})
	if err == nil && newReceipt {
		s.linkRecordedEvidence(ctx, user, run, call, input.TrafficIDs)
	}
	return out, err
}
