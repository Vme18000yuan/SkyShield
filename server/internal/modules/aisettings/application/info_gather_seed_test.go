package application

import (
	"encoding/json"
	"testing"

	"github.com/Autumn-27/norma/tool"
)

func TestInfoGatherToolSeedIsRestrictedToItsAgent(t *testing.T) {
	tools := []tool.CoreTool{}
	want := map[string]bool{"run_info_gather": false, "prepare_info_gather": false, "run_info_gather_phase": false, "finish_info_gather": false}
	for name := range want {
		tools = append(tools, tool.Build(tool.Spec{Name: name, Description: "test", Schema: map[string]any{"type": "object"}, ReadOnly: func(json.RawMessage) bool { return false }}))
	}
	for _, seed := range NativeToolSeeds(tools) {
		if _, ok := want[seed.Key]; !ok {
			continue
		}
		if !seed.System || !seed.Enabled || len(seed.Agents) != 1 || seed.Agents[0] != "info_gatherer" {
			t.Fatalf("unexpected tool seed: %+v", seed)
		}
		want[seed.Key] = true
	}
	for name, found := range want {
		if !found {
			t.Fatalf("information collection tool %s missing", name)
		}
	}
}
