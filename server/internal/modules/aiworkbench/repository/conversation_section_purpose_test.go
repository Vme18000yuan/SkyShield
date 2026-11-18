package repository

import (
	"encoding/json"
	"strings"
	"testing"

	ai "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

func purposeTestCalls(inputs ...string) []timelineMeta {
	calls := make([]timelineMeta, len(inputs))
	for i, input := range inputs {
		calls[i] = timelineMeta{id: int64(i + 1), tool: "Bash", purpose: taskSignal("Bash", input, int64(i+1), i+1)}
	}
	return calls
}

func TestSectionPurposeUsesRecordsAcrossTheRange(t *testing.T) {
	calls := purposeTestCalls(`{"command":"pwd"}`, `{"command":"ls"}`, `{"command":"echo ok","description":"核对两份台账的归属差异"}`, `{"command":"date"}`)
	result := purposeOf(calls, 11)
	if result.Source != "step" || result.Text != "核对两份台账的归属差异" || len(result.Basis) != 1 || result.Basis[0].Step != 13 || result.Basis[0].ActivityID != "3" {
		t.Fatalf("missing recorded objective: %+v", result)
	}
}

func TestSectionPurposeCanDescribeOlderRunsWithoutInventingIntent(t *testing.T) {
	calls := purposeTestCalls(`{"command":"cd /work/sessions/12345678-fixture && python3 -c 'import PIL; print(1)'"}`, `{"command":"python3 -c 'from PIL import Image'"}`)
	p := purposeOf(calls, 1)
	if p.Source != "observed" || len(p.Topics) == 0 || p.Topics[0] != "media" || p.SupportingSteps != 2 || len(p.Basis) != 2 {
		t.Fatalf("unexpected topics: %+v", p)
	}
	opaque := purposeOf(purposeTestCalls(`{"command":"cd /work/sessions/12345678-fixture && date"}`, `{"command":"pwd"}`), 1)
	if opaque.Source != "unknown" {
		t.Fatalf("runtime paths were treated as purpose: %+v", opaque)
	}
	if resultPurposeText("InsecureRequestWarning: certificate verification") != "" {
		t.Fatal("library warning was used as intent")
	}
	if activeTodo(`{"todos":[{"content":"Count inventory entries","status":"in_progress"},{"content":"Review invoices","status":"in_progress"}]}`, 2, 1) != nil {
		t.Fatal("assigned one of multiple active checklist entries")
	}
}

func TestSectionTitlesDoNotCopySecretsOrCommandLines(t *testing.T) {
	for _, input := range []string{`{"description":"curl --header fixture-private","command":"pwd"}`, `{"description":"当前任务：核查 token=fixture-private","command":"ls"}`, `{"description":"run command","command":"date"}`} {
		p := purposeOf(purposeTestCalls(input), 1)
		raw, _ := json.Marshal(p)
		if p.Text != "" || strings.Contains(string(raw), "fixture-private") {
			t.Fatal("invalid task caption", p)
		}
	}
	note := declaredTask("当前任务：比对采购记录\nextra")
	if note == "" {
		t.Fatal("lost stated task")
	}
}

func TestDeclaredTaskBoundariesIgnoreMechanicalPageSize(t *testing.T) {
	a := taskStatement("todo", "核对资产归属", 101, 1)
	b := taskStatement("statement", "整理核查结果", 200, 0)
	calls := make([]timelineMeta, 42)
	for i := range calls {
		calls[i].purpose.active = a
		if i >= 35 {
			calls[i].purpose.active = b
		}
	}
	sections := timelineSections(calls)
	if len(sections) != 2 || sections[0].First != 1 || sections[0].Last != 35 || sections[1].First != 36 || sections[1].Last != 42 || sections[1].Purpose.Source != "statement" {
		t.Fatalf("wrong task boundaries: %+v", sections)
	}
	// A finding is an outcome. Its existence must not replace the declared goal.
	calls[12].finding = &ai.TimelineFinding{Title: "Stored finding"}
	if timelineSections(calls)[0].Purpose.Text != a.Text {
		t.Fatal("finding replaced the recorded task")
	}
}
