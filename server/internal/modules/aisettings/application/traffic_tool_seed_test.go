package application

import (
	"slices"
	"testing"

	"github.com/Autumn-27/norma/tool"
)

func TestTrafficToolDefaultBindings(t *testing.T) {
	item := func(name string) tool.CoreTool {
		return tool.Build(tool.Spec{Name: name, Schema: map[string]any{"type": "object", "properties": map[string]any{}}})
	}
	seeds := NativeToolSeeds([]tool.CoreTool{item("traffic_search"), item("traffic_get"), item("traffic_blob"), item("bind_finding_traffic"), item("report_vulnerability")})
	found := map[string][]string{}
	for _, seed := range seeds {
		found[seed.Key] = seed.Agents
	}
	for _, name := range []string{"traffic_search", "traffic_get", "traffic_blob"} {
		if !slices.Equal(found[name], []string{"worker", "reporter", "mainagent", "auto", "pentest"}) {
			t.Fatalf("%s defaults: %v", name, found[name])
		}
	}
	if !slices.Equal(found["report_vulnerability"], []string{"mainagent", "auto", "pentest"}) {
		t.Fatalf("independent report defaults: %v", found["report_vulnerability"])
	}
	if !slices.Equal(found["bind_finding_traffic"], []string{"reporter"}) {
		t.Fatalf("bind defaults: %v", found["bind_finding_traffic"])
	}
}

func TestReporterFindingCaseToolDefaultBindings(t *testing.T) {
	item := func(name string) tool.CoreTool {
		return tool.Build(tool.Spec{Name: name, Schema: map[string]any{"type": "object", "properties": map[string]any{}}})
	}
	seeds := NativeToolSeeds([]tool.CoreTool{item("search_finding_duplicates"), item("get_finding_record"), item("get_finding_case"), item("update_finding_case_report"), item("suggest_finding_merge"), item("merge_finding_records"), item("complete_finding_case_review")})
	found := map[string][]string{}
	for _, seed := range seeds {
		found[seed.Key] = seed.Agents
	}
	for _, name := range []string{"search_finding_duplicates", "get_finding_record", "get_finding_case", "update_finding_case_report", "suggest_finding_merge", "merge_finding_records", "complete_finding_case_review"} {
		if !slices.Equal(found[name], []string{"reporter"}) {
			t.Fatalf("%s defaults: %v", name, found[name])
		}
	}
}
