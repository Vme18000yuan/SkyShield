package infogather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Autumn-27/norma/skill"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/ailocal"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

type skillResolver func(context.Context, int, string) ([]skill.Skill, error)
type Executor interface {
	Execute(context.Context, int, string, Scope, string, func(string)) ([]byte, json.RawMessage, error)
}

// PhaseExecutor exposes one real Skill phase to the model at a time. It is
// separate from the legacy one-call executor so older callers remain intact.
type PhaseExecutor interface {
	ExecutePhase(context.Context, int, string, Scope, string, string, int, func(string)) ([]byte, json.RawMessage, error)
	ArtifactDirectory(int, string) string
}

type ResumePhaseExecutor interface {
	ExecuteResumedPhase(context.Context, int, string, Scope, string, string, int, func(string)) ([]byte, json.RawMessage, error)
}

type SandboxExecutor struct {
	Root    string
	Resolve skillResolver
}

// Chat tools execute under work/sessions/<conversation>. Return an absolute
// path inside the same sandbox root so LS/Read resolve it from every session.
func (e *SandboxExecutor) ArtifactDirectory(user int, id string) string {
	return filepath.Join(e.Root, assetapp.DefaultTenantID, fmt.Sprintf("user-%d", user), "work", "info-gather", id)
}

var taskIDPattern = regexp.MustCompile(`Running in background as (task_[A-Za-z0-9_-]+)\.`)

func shellArg(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (e *SandboxExecutor) Execute(ctx context.Context, user int, id string, target Scope, mode string, progress func(string)) ([]byte, json.RawMessage, error) {
	return e.executeManaged(ctx, user, id, target, mode, "", 0, false, progress)
}

var phaseLabels = map[string]string{
	"passive": "被动子域采集", "organization": "组织线索", "dns_brute": "子域字典枚举",
	"dns": "DNS 解析", "axfr": "区域传送检查", "cdn": "CDN 与 ASN 识别",
	"http": "HTTP 探测", "ports": "端口扫描", "web": "Web 分析", "finish": "报告与归集准备",
}

func (e *SandboxExecutor) ExecutePhase(ctx context.Context, user int, id string, target Scope, mode, phase string, maxWeb int, progress func(string)) ([]byte, json.RawMessage, error) {
	if _, ok := phaseLabels[phase]; !ok || maxWeb < 1 || maxWeb > 50 {
		return nil, nil, errors.New("信息收集阶段或 Web 入口数量无效")
	}
	return e.executeManaged(ctx, user, id, target, mode, phase, maxWeb, false, progress)
}

func (e *SandboxExecutor) ExecuteResumedPhase(ctx context.Context, user int, id string, target Scope, mode, phase string, maxWeb int, progress func(string)) ([]byte, json.RawMessage, error) {
	if _, ok := phaseLabels[phase]; !ok || maxWeb < 1 || maxWeb > 50 {
		return nil, nil, errors.New("信息收集阶段或 Web 入口数量无效")
	}
	return e.executeManaged(ctx, user, id, target, mode, phase, maxWeb, true, progress)
}

func (e *SandboxExecutor) executeManaged(ctx context.Context, user int, id string, target Scope, mode, phase string, maxWeb int, resume bool, progress func(string)) ([]byte, json.RawMessage, error) {
	ctx, release, permitErr := airuntime.AcquireAuxiliaryToolProcess(ctx)
	if permitErr != nil {
		return nil, nil, permitErr
	}
	defer release()
	if e.Resolve == nil || !filepath.IsAbs(e.Root) {
		return nil, nil, errors.New("信息收集技能未配置")
	}
	items, err := e.Resolve(ctx, user, "info_gatherer")
	if err != nil {
		return nil, nil, err
	}
	workspace := filepath.Join(e.Root, assetapp.DefaultTenantID, fmt.Sprintf("user-%d", user), "work")
	var script string
	for _, item := range items {
		if item.Name != "info-gath" {
			continue
		}
		rel, err := filepath.Rel(workspace, item.Dir)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !strings.HasPrefix(rel, ".skills"+string(filepath.Separator)) {
			return nil, nil, errors.New("技能目录不在受控工作区")
		}
		name := "recon.sh"
		if phase != "" {
			name = "agentic_recon.py"
		}
		script = filepath.Join(rel, "scripts", name)
	}
	if script == "" {
		return nil, nil, errors.New("info-gath 技能未启用或未分配给信息收集 Agent")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	info, err := root.Stat(script)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("info-gath 执行脚本不可用或已被自定义，请检查 Skill 配置")
	}
	runDir := filepath.Join("info-gather", id)
	if err = root.MkdirAll(runDir, 0700); err != nil {
		return nil, nil, err
	}
	relay, err := httpoutbound.NewLocalRelay(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer relay.Close()
	environment := relay.Environment()
	environment = append(environment, "RECON_MANAGED_PROXY=1", "RECON_HTTP_PROXY="+relay.URL())
	readPaths, toolDir := localToolResources(os.Getenv("TIANQIONG_INFO_GATHER_TOOLS_DIR"))
	if toolDir != "" {
		environment = append(environment, "PATH="+toolDir+":/usr/local/bin:/usr/bin:/bin")
	}
	readPaths = append(readPaths, filepath.Join(workspace, filepath.Dir(filepath.Dir(script))))
	if relay.CABundle() != "" {
		readPaths = append(readPaths, relay.CABundle())
	}
	jobWorkspace := filepath.Join(workspace, runDir)
	client, err := ailocal.NewClientWithReadPaths(ctx, jobWorkspace, "", environment, readPaths)
	if err != nil {
		return nil, nil, err
	}
	defer client.Close()
	var command string
	if phase == "" {
		command = "bash " + shellArg(filepath.Join(workspace, script)) + " " + shellArg(target.Input) + " --no-cloud --stage-timeout 300 --output-dir " + shellArg(jobWorkspace) + " --run-id " + shellArg(id)
		if mode == "fast" {
			command += " --fast"
		} else if mode == "deep" {
			command += " --deep"
		}
	} else {
		command = "python3 " + shellArg(filepath.Join(workspace, script)) + " " + shellArg(target.Input) + " --mode " + shellArg(mode) +
			" --stage " + shellArg(phase) + " --stage-timeout 300 --max-web-hosts " + fmt.Sprint(maxWeb) +
			" --output-dir " + shellArg(jobWorkspace) + " --run-id " + shellArg(id)
		if resume {
			command += " --resume"
		}
	}
	input, _ := json.Marshal(map[string]any{"command": command, "run_in_background": true})
	tc := &tool.ToolContext{WorkingDir: jobWorkspace, ToolUseID: id, MaxOutputChars: 4000}
	started, err := client.Call(ctx, "Bash", input, tc)
	if err != nil {
		return nil, nil, err
	}
	match := taskIDPattern.FindStringSubmatch(started.Flatten())
	if started.IsError || len(match) != 2 {
		return nil, nil, errors.New("无法启动受控采集进程")
	}
	if progress != nil {
		if phase == "" {
			progress("信息收集已启动，正在执行阶段任务。")
		} else {
			progress("正在执行：" + phaseLabels[phase] + "。")
		}
	}
	for {
		input, _ = json.Marshal(map[string]any{"task_id": match[1], "block": true, "timeout_ms": 20000, "max_bytes": 1000})
		result, err := client.Call(ctx, "TaskOutput", input, tc)
		if err != nil {
			return nil, nil, err
		}
		if result.IsError {
			return nil, nil, errors.New("无法读取采集进程状态")
		}
		header := strings.SplitN(result.Flatten(), "\n", 2)[0]
		if strings.Contains(header, " | completed") || strings.Contains(header, " | failed") || strings.Contains(header, " | killed") {
			if phase != "" && !strings.Contains(header, " | completed") {
				return nil, nil, errors.New("采集阶段未能执行完成；此前完成的阶段已保留")
			}
			break
		}
		if progress != nil {
			if phase == "" {
				progress("信息收集仍在运行，正在等待阶段结果。")
			} else {
				progress(phaseLabels[phase] + "仍在运行，正在等待阶段结果。")
			}
		}
	}
	return readRunFiles(root, runDir, id)
}

// Grant read/execute only to named regular binaries; never expose the entire
// deployment data directory or follow a symlink to another private location.
func localToolResources(dir string) ([]string, string) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) == "/" {
		return nil, ""
	}
	paths := []string{}
	for _, name := range []string{"subfinder", "httpx", "dnsx", "katana", "ffuf"} {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, ""
	}
	return paths, filepath.Clean(dir)
}

func readRunFiles(root *os.Root, dir, id string) ([]byte, json.RawMessage, error) {
	f, err := root.Open(dir)
	if err != nil {
		return nil, nil, err
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return nil, nil, err
	}
	var base string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".tmp" || entry.Name() == ".task-output" {
			continue
		}
		// The sandbox also uses this directory as HOME, so tools may create
		// configuration and caches alongside the target's result directory.
		candidate := filepath.Join(dir, entry.Name(), "runs", id)
		info, err := root.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		if base != "" {
			return nil, nil, errors.New("采集结果目录不唯一")
		}
		base = candidate
	}
	if base == "" {
		return nil, nil, errors.New("采集未生成结果目录")
	}
	read := func(name string, limit int64) ([]byte, error) {
		file, err := root.Open(filepath.Join(base, name))
		if err != nil {
			return nil, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("采集结果不是普通文件")
		}
		body, err := io.ReadAll(io.LimitReader(file, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(body)) > limit {
			return nil, errors.New("采集结果超过大小限制")
		}
		return body, nil
	}
	body, err := read("skyshield-import.json", maxReportBytes)
	if err != nil {
		return nil, nil, errors.New("采集未生成可导入结果")
	}
	manifest, _ := read("manifest.json", 2<<20)
	passive, _ := read("raw/subdomain-status.json", 2<<20)
	crtStderr, _ := read("raw/source-crtsh.stderr", 2048)
	return body, passiveCoverage(sanitizedCoverage(manifest), passive, crtStderr), nil
}

var curlStatusPattern = regexp.MustCompile(`\berror:\s*(\d{3})\b`)

// Only expose known source names and HTTP status, never command output or URLs.
func passiveCoverage(raw json.RawMessage, source, stderr []byte) json.RawMessage {
	var current struct {
		Domains int `json:"domains"`
		Sources []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			ReturnCode int    `json:"returncode"`
			Attempts   int    `json:"attempts"`
		} `json:"sources"`
	}
	if json.Unmarshal(source, &current) != nil {
		return raw
	}
	var coverage map[string]any
	if json.Unmarshal(raw, &coverage) != nil {
		return raw
	}
	details, _ := coverage["details"].(map[string]any)
	if details == nil {
		details = map[string]any{}
		coverage["details"] = details
	}
	failed := []map[string]any{}
	succeeded := []string{}
	seen := map[string]bool{}
	for _, entry := range current.Sources {
		name := map[string]string{"source-crtsh": "certificate_transparency", "source-subfinder": "subfinder"}[entry.Name]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if entry.Status == "success" {
			succeeded = append(succeeded, name)
		} else if entry.Status == "failed" || entry.Status == "timeout" {
			item := map[string]any{"source": name, "status": entry.Status}
			if entry.Attempts >= 1 && entry.Attempts <= 3 {
				item["attempts"] = entry.Attempts
			}
			if name == "certificate_transparency" && entry.ReturnCode == 22 {
				if match := curlStatusPattern.FindSubmatch(stderr); len(match) == 2 {
					if status, err := strconv.Atoi(string(match[1])); err == nil && status >= 100 && status <= 599 {
						item["http_status"] = status
					}
				}
			}
			failed = append(failed, item)
		}
	}
	domains := max(0, min(current.Domains, 1000000))
	details["passive"] = map[string]any{"domains": domains, "successful_sources": succeeded, "failed_sources": failed}
	value, err := json.Marshal(coverage)
	if err != nil {
		return raw
	}
	return value
}

func sanitizedCoverage(raw []byte) json.RawMessage {
	var manifest struct {
		Steps map[string]struct {
			Status string `json:"status"`
		} `json:"steps"`
		Tools map[string]struct {
			Usable bool `json:"usable"`
		} `json:"tools"`
		Coverage map[string]json.RawMessage `json:"coverage"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return json.RawMessage(`{}`)
	}
	steps := map[string]string{}
	for _, name := range []string{"passive", "organization", "dns_brute", "dns", "axfr", "cdn", "http", "ports", "web", "cloud"} {
		status := manifest.Steps[name].Status
		switch status {
		case "success", "partial", "failed", "timeout", "skipped", "interrupted":
			steps[name] = status
		}
	}
	tools := map[string]bool{}
	for _, name := range []string{"subfinder", "httpx", "dnsx", "dig", "nmap", "masscan", "curl", "whois", "katana"} {
		if v, ok := manifest.Tools[name]; ok {
			tools[name] = v.Usable
		}
	}
	details := map[string]any{}
	for name, allowed := range map[string][]string{
		"ports": {"total", "completed", "timed_out", "failed", "unprocessed", "reused"},
		"web":   {"discovered_origins", "selected", "omitted", "attempted", "completed", "partial", "failed", "timed_out", "pending", "workers", "per_worker_rate", "crawler_timed_out", "crawler_failed", "failed_requests"},
		"axfr":  {"success", "refused", "failed", "timeout"},
		"http":  {"seeds", "alive", "cdn_ips_excluded"},
	} {
		var values map[string]json.RawMessage
		if json.Unmarshal(manifest.Coverage[name], &values) != nil {
			continue
		}
		filtered := map[string]int{}
		for _, key := range allowed {
			var number int
			if json.Unmarshal(values[key], &number) == nil && number >= 0 && number <= 1000000 {
				filtered[key] = number
			}
		}
		if len(filtered) > 0 {
			details[name] = filtered
		}
	}
	var organization struct {
		FailedSources []struct {
			Source     string `json:"source"`
			Status     string `json:"status"`
			HTTPStatus int    `json:"http_status"`
			Attempts   int    `json:"attempts"`
		} `json:"failed_sources"`
	}
	if json.Unmarshal(manifest.Coverage["organization"], &organization) == nil {
		items := []map[string]any{}
		for _, item := range organization.FailedSources {
			if len(items) == 4 {
				break
			}
			if item.Source != "whois" && item.Source != "rdap" && item.Source != "certificate_transparency" {
				continue
			}
			if item.Status != "failed" && item.Status != "timeout" {
				continue
			}
			value := map[string]any{"source": item.Source, "status": item.Status}
			if item.Attempts >= 1 && item.Attempts <= 3 {
				value["attempts"] = item.Attempts
			}
			if item.HTTPStatus >= 100 && item.HTTPStatus <= 599 {
				value["http_status"] = item.HTTPStatus
			}
			items = append(items, value)
		}
		details["organization"] = map[string]any{"failed_sources": items}
	}
	var agentic struct {
		Unexecuted []string `json:"unexecuted"`
	}
	if json.Unmarshal(manifest.Coverage["agentic"], &agentic) == nil {
		unexecuted := []string{}
		seen := map[string]bool{}
		for _, phase := range agentic.Unexecuted {
			if _, allowed := phaseLabels[phase]; allowed && phase != "finish" && !seen[phase] {
				seen[phase] = true
				unexecuted = append(unexecuted, phase)
			}
		}
		details["agentic"] = map[string]any{"unexecuted": unexecuted}
	}
	out, err := json.Marshal(map[string]any{"steps": steps, "tools": tools, "details": details, "policy": map[string]any{"http_proxy": true, "active_cloud": false, "service_detail": "limited"}})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}

var _ Executor = (*SandboxExecutor)(nil)
