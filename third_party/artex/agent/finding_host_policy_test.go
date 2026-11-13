package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/norma/tool"
)

func TestFindingTrafficPolicyIsRunScoped(t *testing.T) {
	makeTool := func(name string) tool.CoreTool {
		return tool.Build(tool.Spec{Name: name, Schema: map[string]any{"type": "object", "properties": map[string]any{"traffic_refs": map[string]any{"type": "array"}}}})
	}
	base := []tool.CoreTool{makeTool("report_finding"), makeTool("traffic_search"), makeTool("traffic_get"), makeTool("bind_finding_traffic")}
	policy := func(enabled bool) context.Context {
		return WithHostPolicy(context.Background(), HostPolicy{FindingTrafficEnabled: func(context.Context) bool { return enabled }})
	}
	check := func(ctx context.Context, want bool) {
		t.Helper()
		items, _, close := AugmentTools(ctx, "reporter", base)
		defer close()
		present := map[string]tool.CoreTool{}
		for _, item := range items {
			present[item.Name()] = item
		}
		if (present["bind_finding_traffic"] != nil) != want || (present["traffic_search"] != nil) != want || (present["traffic_get"] != nil) != want {
			t.Fatalf("traffic tools enabled=%v: %v", want, present)
		}
		raw, _ := json.Marshal(present["report_finding"].InputSchema())
		var schema map[string]any
		_ = json.Unmarshal(raw, &schema)
		properties := schema["properties"].(map[string]any)
		if _, ok := properties["traffic_refs"]; ok != want {
			t.Fatalf("traffic schema enabled=%v: %s", want, raw)
		}
	}
	check(policy(true), true)
	check(policy(false), false)
	check(policy(true), true)
}
