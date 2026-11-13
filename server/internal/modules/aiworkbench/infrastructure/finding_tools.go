package infrastructure

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

type FindingReportAccess interface {
	FindingTraffic(context.Context, int, int64) (aiapp.FindingTrafficRecord, error)
	SaveFindingReport(context.Context, int, string, string, int64, string, *int64) (bool, error)
	EnsureFindingReviewRead(context.Context, int, string, int64) error
}

// FindingReportTools keep native finding evidence and report operations inside
// the current account's task and the current execution's lease.
func FindingReportTools(access FindingReportAccess, user int, dataRoot string) []tool.CoreTool {
	if access == nil || user <= 0 || dataRoot == "" {
		return nil
	}
	allow := func(context.Context, json.RawMessage, permission.Context) permission.Decision {
		return permission.Allowed()
	}
	return []tool.CoreTool{
		tool.Build(tool.Spec{
			Name: "get_finding_traffic", Description: "读取漏洞的证据清单和版本。finding_id 是独立漏洞记录 ID；空清单可正常用于非 HTTP 漏洞。读取版本后再写报告；带 binding_id、side、offset 可分页读取正文。",
			Schema:   map[string]any{"type": "object", "properties": map[string]any{"finding_id": map[string]any{"type": "string"}, "binding_id": map[string]any{"type": "string"}, "side": map[string]any{"type": "string", "enum": []string{"request", "response"}}, "offset": map[string]any{"type": "integer", "minimum": 0}, "length": map[string]any{"type": "integer", "minimum": 0}}, "required": []string{"finding_id"}},
			ReadOnly: func(json.RawMessage) bool { return true }, Concurrent: func(json.RawMessage) bool { return true }, Permissions: allow,
			Run: func(ctx context.Context, raw json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
				var req struct {
					FindingID json.RawMessage `json:"finding_id"`
					BindingID json.RawMessage `json:"binding_id"`
					Side      string          `json:"side"`
					Offset    int64           `json:"offset"`
					Length    int64           `json:"length"`
				}
				if json.Unmarshal(raw, &req) != nil {
					return tool.Errorf("证据参数无效"), nil
				}
				id, err := parseAutoTaskID(req.FindingID)
				if err != nil {
					return tool.Errorf("漏洞编号无效"), nil
				}
				if err = access.EnsureFindingReviewRead(ctx, user, airuntime.ExecutionID(ctx), id); err != nil {
					return tool.Errorf("漏洞不属于当前审查范围或运行已结束"), nil
				}
				record, err := access.FindingTraffic(ctx, user, id)
				if err != nil {
					return tool.Errorf("读取漏洞证据失败，请检查编号及访问权限"), nil
				}
				var result any
				if len(req.BindingID) == 0 {
					for i := range record.Bindings {
						record.Bindings[i].Snapshot.ReqHead = ""
						record.Bindings[i].Snapshot.RespHead = ""
					}
					result = artexdb.FindingTraffic{FindingID: record.FindingID, Version: record.Version, ReportVersion: record.ReportVersion, Bindings: record.Bindings}
				} else {
					bindingID, err := parseAutoTaskID(req.BindingID)
					if err != nil {
						return tool.Errorf("证据绑定编号无效"), nil
					}
					if req.Side == "" {
						req.Side = "response"
					}
					var binding *artexdb.FindingTrafficBinding
					for i := range record.Bindings {
						if record.Bindings[i].ID == bindingID {
							binding = &record.Bindings[i]
							break
						}
					}
					if binding == nil || req.Offset < 0 || req.Length < 0 || (req.Side != "request" && req.Side != "response") {
						return tool.Errorf("证据参数无效或无权访问"), nil
					}
					dir := filepath.Join(dataRoot, assets.DefaultTenantID, fmt.Sprintf("user-%d", user), "evidence")
					body, err := readFindingEvidence(dir, binding.Snapshot, req.Side, req.Offset, req.Length)
					if err != nil {
						return tool.Errorf("读取证据正文失败"), nil
					}
					head := binding.Snapshot.ReqHead
					if req.Side == "response" {
						head = binding.Snapshot.RespHead
					}
					result = map[string]any{"binding_id": strconv.FormatInt(bindingID, 10), "head": head, "body": body}
				}
				if err = access.EnsureFindingReviewRead(ctx, user, airuntime.ExecutionID(ctx), id); err != nil {
					return tool.Errorf("漏洞不属于当前审查范围或运行已结束"), nil
				}
				out, err := json.Marshal(result)
				if err != nil {
					return tool.Errorf("证据结果编码失败"), nil
				}
				return tool.Text(string(out)), nil
			},
		}),
		tool.Build(tool.Spec{
			Name: "update_finding_report", Description: "为已登记的漏洞保存完整 Markdown 报告。finding_id 使用探索节点编号，evidence_version 使用 get_finding_traffic 实际读取的版本。",
			Schema:   map[string]any{"type": "object", "properties": map[string]any{"finding_id": map[string]any{"type": "integer", "minimum": 1}, "report": map[string]any{"type": "string"}, "evidence_version": map[string]any{"type": "integer", "minimum": 0}}, "required": []string{"finding_id", "report"}},
			ReadOnly: func(json.RawMessage) bool { return false }, Concurrent: func(json.RawMessage) bool { return false }, Permissions: allow,
			Run: func(ctx context.Context, raw json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
				if tc == nil || tc.ToolUseID == "" || airuntime.ExecutionID(ctx) == "" {
					return tool.Errorf("缺少有效运行上下文"), nil
				}
				var req struct {
					FindingID json.RawMessage `json:"finding_id"`
					Report    string          `json:"report"`
					Version   *int64          `json:"evidence_version"`
				}
				if json.Unmarshal(raw, &req) != nil {
					return tool.Errorf("报告参数无效"), nil
				}
				id, err := parseAutoTaskID(req.FindingID)
				if err != nil {
					return tool.Errorf("漏洞节点编号无效"), nil
				}
				_, err = access.SaveFindingReport(ctx, user, airuntime.ExecutionID(ctx), tc.ToolUseID, id, req.Report, req.Version)
				if errors.Is(err, aiapp.ErrEvidenceVersionConflict) {
					return tool.Errorf("漏洞证据已更新，请重新读取证据并生成报告"), nil
				}
				if err != nil {
					return tool.Errorf("报告保存失败，请检查运行状态、漏洞编号和访问权限"), nil
				}
				return tool.Text(fmt.Sprintf("finding %d report updated (%d chars)", id, len(req.Report))), nil
			},
		}),
	}
}

func readFindingEvidence(dir string, snapshot artexdb.TrafficEvidenceSnapshot, side string, offset, length int64) (map[string]any, error) {
	if length == 0 || length > 8192 {
		length = 8192
	}
	hash, total := snapshot.ReqHash, snapshot.ReqLen
	if side == "response" {
		hash, total = snapshot.RespHash, snapshot.RespLen
	} else if side != "request" {
		return nil, errors.New("invalid evidence side")
	}
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != hash || total < 0 {
		return nil, errors.New("invalid evidence hash")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(filepath.Join("blobs", hash[:2], hash+".bin"))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	h := sha256.New()
	actual, err := io.Copy(h, file)
	if err != nil {
		return nil, err
	}
	if actual != total || !bytes.Equal(h.Sum(nil), decoded) {
		return nil, errors.New("invalid evidence content")
	}

	if offset > total {
		return nil, errors.New("offset outside evidence")
	}
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, length))
	if err != nil {
		return nil, err
	}
	if offset+int64(len(raw)) < total && len(raw) >= utf8.UTFMax {
		start := len(raw) - 1
		for start > 0 && raw[start]&0xc0 == 0x80 {
			start--
		}
		if !utf8.FullRune(raw[start:]) {
			raw = raw[:start]
		}
	}
	binary := bytes.IndexByte(raw, 0) >= 0 || (offset == 0 && !utf8.Valid(raw))
	content := string(bytes.ToValidUTF8(raw, []byte("�")))
	if binary {
		content = fmt.Sprintf("[二进制正文，%d 字节；请下载查看]", total)
	}
	next := offset + int64(len(raw))
	return map[string]any{"content": content, "offset": offset, "total": total, "next_offset": next, "truncated": next < total, "binary": binary}, nil
}
