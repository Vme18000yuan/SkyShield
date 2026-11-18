package aivuln

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Autumn-27/artex/traffic"
	"github.com/google/uuid"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
)

const maxEvidenceBytes = 128 << 10

// Files are bounded immutable snapshots. Text is never rendered as HTML.
type Evidence struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Text      string `json:"text,omitempty"`
	Request   string `json:"request,omitempty"`
	Response  string `json:"response,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

func safeEvidencePath(p string) bool {
	return p != "" && len(p) <= 1000 && filepath.IsLocal(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, "\\\x00") && p != "."
}

func readEvidence(root *os.Root, name string) Evidence {
	out := Evidence{Name: name, Source: "artifact"}
	if !safeEvidencePath(name) {
		out.Error = "证据路径无效"
		return out
	}
	// A tool can create a FIFO in its workspace. Never let opening such a path
	// block the report transaction (or its run heartbeat) before the stat check.
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		out.Error = "证据文件不存在或不可读取"
		return out
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		out.Error = "证据不是普通文件"
		return out
	}
	b, err := io.ReadAll(io.LimitReader(f, maxEvidenceBytes+1))
	if err != nil {
		out.Error = "证据读取失败"
		return out
	}
	out.Truncated = len(b) > maxEvidenceBytes
	if out.Truncated {
		b = b[:maxEvidenceBytes]
	} else {
		out.SHA256 = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	out.Text = strings.ToValidUTF8(strings.ReplaceAll(string(b), "\x00", "\\x00"), "�")
	return out
}

func (s *Service) snapshot(ctx context.Context, user int, tenant, conversation string, input Report) ([]Evidence, error) {
	items := []Evidence{}
	if input.Evidence != "" || input.Request != "" || input.Response != "" {
		items = append(items, Evidence{Name: "Agent 证据说明", Source: "agent_report", Text: input.Evidence, Request: input.Request, Response: input.Response})
	}
	if len(input.EvidencePaths) > 0 {
		// OpenRoot contains all traversal, including symlinks, within this session.
		base, err := os.OpenRoot(filepath.Join(s.root, tenant, fmt.Sprintf("user-%d", user), "work"))
		if err != nil {
			return nil, errors.New("会话证据目录不存在")
		}
		defer base.Close()
		root, err := base.OpenRoot(filepath.Join("sessions", conversation))
		if err != nil {
			return nil, errors.New("会话证据目录不存在")
		}
		defer root.Close()
		for _, name := range input.EvidencePaths {
			item := readEvidence(root, name)
			if item.Error != "" {
				return nil, fmt.Errorf("%s: %s", name, item.Error)
			}
			items = append(items, item)
		}
	}
	for _, id := range input.TrafficIDs {
		if s.Traffic == nil {
			return nil, errors.New("流量证据服务不可用")
		}
		request, response, err := s.Traffic.Exchange(ctx, user, conversation, id)
		if err != nil {
			return nil, errors.New("流量证据不存在或不属于当前对话")
		}
		item := Evidence{Name: "HTTP " + id, Source: "captured_traffic", Request: strings.ToValidUTF8(strings.ReplaceAll(request, "\x00", "\\x00"), "�"), Response: strings.ToValidUTF8(strings.ReplaceAll(response, "\x00", "\\x00"), "�"), Truncated: strings.Contains(request+response, "…[truncated] @blob sha256:")}
		if len(item.Request) > maxEvidenceBytes {
			item.Request = strings.ToValidUTF8(item.Request[:maxEvidenceBytes], "�")
			item.Truncated = true
		}
		if len(item.Response) > maxEvidenceBytes {
			item.Response = strings.ToValidUTF8(item.Response[:maxEvidenceBytes], "�")
			item.Truncated = true
		}
		items = append(items, item)
	}
	return items, nil
}

// EvidenceForVulnerability never accepts a client-supplied filesystem path.
// Both conversation and info-gather evidence are restricted to their owner.
func (s *Service) EvidenceForVulnerability(ctx context.Context, user, id int) ([]Evidence, error) {
	db := dbtx.Resolve(ctx, s.db).WithContext(ctx)
	items := []Evidence{}
	var target int
	var address, source string
	var raw []byte
	err := db.Raw(`SELECT v.target_id,v.url,v.source,v.raw_output FROM vulnerability v JOIN target t ON t.id=v.target_id AND t.deleted_at IS NULL WHERE v.id=? AND EXISTS(SELECT 1 FROM tenant_membership m JOIN auth_user u ON u.id=m.user_id JOIN tenant tn ON tn.id=m.tenant_id WHERE m.user_id=? AND m.tenant_id=?::uuid AND u.is_active AND tn.status='active')`, id, user, assetapp.DefaultTenantID).Row().Scan(&target, &address, &source, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := db.Raw(`SELECT evidence FROM ai_conversation_vulnerability_report WHERE vulnerability_id=? AND owner_user_id=? AND tenant_id=?::uuid ORDER BY id LIMIT 20`, id, user, assetapp.DefaultTenantID).Rows()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return nil, err
		}
		var found []Evidence
		if err = json.Unmarshal(body, &found); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, found...)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if source == "info-gath" {
		var meta struct {
			RunID    string `json:"runId"`
			RecordID string `json:"recordId"`
		}
		if json.Unmarshal(raw, &meta) != nil {
			return items, nil
		}
		if _, err := uuid.Parse(meta.RunID); err != nil {
			return items, nil
		}
		var payload []byte
		// Legacy rows have no recordId. Match the imported record by target+URL.
		err = db.Raw(`SELECT r.payload FROM ai_info_gather_record r JOIN ai_info_gather_run g ON g.id=r.run_id WHERE g.id=?::uuid AND g.tenant_id=?::uuid AND g.owner_user_id=? AND r.status='imported' AND r.target_id=? AND r.kind IN ('vulnerability','observation') AND (r.payload->>'url'=? OR r.payload->>'value'=? OR ('dns://'||(r.payload->>'value'))=?) AND (?='' OR r.record_id=?) ORDER BY r.kind DESC LIMIT 1`, meta.RunID, assetapp.DefaultTenantID, user, target, address, address, address, meta.RecordID, meta.RecordID).Row().Scan(&payload)
		if errors.Is(err, sql.ErrNoRows) {
			return items, nil
		}
		if err != nil {
			return nil, err
		}
		var record struct {
			Evidence []string `json:"evidence"`
		}
		if err = json.Unmarshal(payload, &record); err != nil {
			return nil, err
		}
		items = append(items, s.infoGatherFiles(user, meta.RunID, address, record.Evidence)...)
	}
	return items, nil
}

func (s *Service) infoGatherFiles(user int, run, address string, names []string) []Evidence {
	missing := []Evidence{{Name: "信息收集证据", Source: "artifact", Error: "证据目录不存在或不可读取"}}
	workspace, err := os.OpenRoot(filepath.Join(s.root, assetapp.DefaultTenantID, fmt.Sprintf("user-%d", user), "work"))
	if err != nil {
		return missing
	}
	defer workspace.Close()
	base, err := workspace.OpenRoot(filepath.Join("info-gather", run))
	if err != nil {
		return missing
	}
	defer base.Close()
	f, err := base.Open(".")
	if err != nil {
		return missing
	}
	dirs, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return missing
	}
	folder := ""
	for _, dir := range dirs {
		if dir.IsDir() && dir.Name() != ".tmp" && dir.Name() != ".task-output" {
			if folder != "" {
				return missing
			}
			folder = dir.Name()
		}
	}
	if folder == "" {
		return missing
	}
	root, err := base.OpenRoot(filepath.Join(folder, "runs", run))
	if err != nil {
		return missing
	}
	defer root.Close()
	items := []Evidence{}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] || len(items) >= 20 {
			continue
		}
		seen[name] = true
		item := readInfoPacket(root, name)
		items = append(items, item)
		if name != "recon.json" || item.Error != "" || item.Truncated {
			continue
		}
		// Older info-gath exports point only to recon.json. Follow a body reference
		// only from the exact finding URL, within this immutable run directory.
		var recon struct {
			Findings []struct {
				URL           string `json:"url"`
				Evidence      string `json:"evidence"`
				TokenEvidence string `json:"token_evidence"`
			} `json:"unauth_findings"`
		}
		if json.Unmarshal([]byte(item.Text), &recon) != nil {
			continue
		}
		for _, finding := range recon.Findings {
			if finding.URL != address {
				continue
			}
			for _, path := range []string{finding.Evidence, finding.TokenEvidence} {
				if path == "" || seen[path] || len(items) >= 20 {
					continue
				}
				seen[path] = true
				packet := readInfoPacket(root, path)
				items = append(items, packet)
			}
		}
	}
	return items
}

func readInfoPacket(root *os.Root, name string) Evidence {
	packet := readLegacyInfoEvidence(root, name)
	if !strings.HasSuffix(name, ".body") || packet.Error != "" {
		return packet
	}
	packet.Response = packet.Text
	packet.Text = ""
	packet.Source = "response_body"
	stem := strings.TrimSuffix(packet.Name, ".body")
	if headers := readEvidence(root, stem+".headers"); headers.Error == "" {
		packet.Response = traffic.JoinHTTPMessage(headers.Text, []byte(packet.Response))
		packet.Truncated = packet.Truncated || headers.Truncated
		packet.Source = "artifact"
	}
	if request := readEvidence(root, stem+".request"); request.Error == "" {
		packet.Request = request.Text
		packet.Truncated = packet.Truncated || request.Truncated
	}
	return packet
}

// Early exports forgot the per-origin prefix. Accept a unique match in the
// known fingerprint directories only, never search outside this run.
func readLegacyInfoEvidence(root *os.Root, name string) Evidence {
	item := readEvidence(root, name)
	if item.Error == "" || !safeEvidencePath(name) || !strings.HasPrefix(name, "raw/http/") {
		return item
	}
	f, err := root.OpenFile("raw", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return item
	}
	dirs, err := f.ReadDir(513)
	f.Close()
	if err != nil && !errors.Is(err, io.EOF) || len(dirs) > 512 {
		return item
	}
	var matched *Evidence
	for _, dir := range dirs {
		if !dir.IsDir() || !strings.HasPrefix(dir.Name(), "fp-") {
			continue
		}
		found := readEvidence(root, filepath.Join("raw", dir.Name(), name))
		if found.Error != "" {
			continue
		}
		if matched != nil {
			return item
		}
		matched = &found
	}
	if matched != nil {
		return *matched
	}
	return item
}
