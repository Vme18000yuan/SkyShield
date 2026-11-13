package infogather

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCoverageSummaryExplainsPartialResultsWithoutRawEvidence(t *testing.T) {
	manifest := []byte(`{
		"steps":{"dns":{"status":"success"},"organization":{"status":"partial"},"cdn":{"status":"partial"},"ports":{"status":"partial"},"web":{"status":"timeout"},"unexpected":{"status":"failed"}},
		"tools":{"dnsx":{"usable":false},"dig":{"usable":true},"whois":{"usable":false},"unknown":{"usable":false}},
		"coverage":{
			"ports":{"total":69,"completed":5,"timed_out":1,"unprocessed":62,"secret":"fixture-secret"},
			"web":{"selected":5,"completed":1,"omitted":77,"url":"https://private.invalid/?token=fixture-secret"},
			"organization":{"failed_sources":[{"source":"certificate_transparency","status":"failed","http_status":502,"token":"fixture-secret"},{"source":"untrusted","status":"failed","http_status":500}]}
		}
	}`)
	coverage := sanitizedCoverage(manifest)
	if !json.Valid(coverage) {
		t.Fatalf("invalid coverage: %s", coverage)
	}
	for _, prohibited := range []string{"fixture-secret", "private.invalid", "untrusted", "unexpected", "unknown"} {
		if strings.Contains(string(coverage), prohibited) {
			t.Fatalf("untrusted data leaked in coverage: %s", coverage)
		}
	}
	summary := coverageSummary(coverage)
	for _, expected := range []string{"端口扫描", "完成 5/69", "超时 1", "未完成 62", "Web 分析", "完成 1/5", "dnsx 未安装，DNS 已使用 dig 完成", "whois 未安装", "HTTP 502", "另有 77 个 Web 入口"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("summary missing %q: %s", expected, summary)
		}
	}
}

func TestCoverageSummaryHandlesOlderRuns(t *testing.T) {
	coverage := json.RawMessage(`{"steps":{"dns":"success","web":"timeout"},"tools":{"dnsx":false,"dig":true}}`)
	summary := coverageSummary(coverage)
	if !strings.Contains(summary, "Web 分析（超时）") || !strings.Contains(summary, "DNS 已使用 dig 完成") {
		t.Fatalf("older coverage not explained: %s", summary)
	}
}

func TestPassiveCoverageExplainsSourceFailureWithoutLeakingOutput(t *testing.T) {
	base := json.RawMessage(`{"steps":{"passive":"partial"},"tools":{},"details":{}}`)
	source := []byte(`{"domains":18,"sources":[{"name":"source-subfinder","status":"success"},{"name":"source-crtsh","status":"failed","returncode":22},{"name":"untrusted-secret","status":"failed"}]}`)
	coverage := passiveCoverage(base, source, []byte("curl: (22) The requested URL returned error: 502\nhttps://private.invalid/?token=secret"))
	summary := coverageSummary(coverage)
	if !strings.Contains(summary, "被动证书查询返回 HTTP 502") || !strings.Contains(string(coverage), `"domains":18`) || strings.Contains(string(coverage), "secret") || strings.Contains(string(coverage), "private.invalid") {
		t.Fatalf("incorrect passive coverage: %s; %s", coverage, summary)
	}
}
