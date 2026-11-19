package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"reflect"
	"regexp"
	"strings"
)

var ErrToolCatalogInvalid = errors.New("工具配置无效")
var ErrToolCatalogNotFound = errors.New("工具不存在")

type ToolRecord = artexdb.Tool
type ToolCatalogStore interface {
	ListToolCatalog(context.Context, int, bool) ([]ToolRecord, error)
	SaveToolCatalog(context.Context, int, ToolRecord, bool) error
	DeleteToolCatalog(context.Context, int, string) error
	SeedToolCatalog(context.Context, []ToolRecord) error
}
type CustomToolBuilder func(context.Context, int, ToolRecord, []tool.CoreTool) tool.CoreTool
type ToolCatalogFacade struct {
	tester  func(context.Context, int, ToolRecord, map[string]any) (tool.Result, error)
	store   ToolCatalogStore
	catalog *AgentCatalogFacade
	domain  func(int) []tool.CoreTool
	custom  CustomToolBuilder
	seeds   []ToolRecord
}

func NewToolCatalogFacade(store ToolCatalogStore, catalog *AgentCatalogFacade, domain func(int) []tool.CoreTool, builder CustomToolBuilder) *ToolCatalogFacade {
	return &ToolCatalogFacade{store: store, catalog: catalog, domain: domain, custom: builder}
}
func (f *ToolCatalogFacade) SetSeeds(seeds []ToolRecord) { f.seeds = seeds }
func (f *ToolCatalogFacade) SetTester(test func(context.Context, int, ToolRecord, map[string]any) (tool.Result, error)) {
	f.tester = test
}
func (f *ToolCatalogFacade) Test(ctx context.Context, user int, kind string, exec json.RawMessage, params map[string]any) (tool.Result, error) {
	if _, err := f.List(ctx, user); err != nil {
		return tool.Result{}, err
	}
	if f.tester == nil || kind != "command" && kind != "script" && kind != "http" || len(exec) > 1<<20 {
		return tool.Result{}, ErrToolCatalogInvalid
	}
	return f.tester(ctx, user, ToolRecord{Key: "test_custom_tool", Kind: kind, Exec: exec, Schema: json.RawMessage("{}")}, params)
}
func (f *ToolCatalogFacade) Seed(ctx context.Context) error {
	return f.store.SeedToolCatalog(ctx, f.seeds)
}
func (f *ToolCatalogFacade) List(ctx context.Context, user int) ([]ToolRecord, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	return f.store.ListToolCatalog(ctx, user, true)
}
func NativeToolSeeds(extra []tool.CoreTool) []ToolRecord {
	seeds := []ToolRecord{}
	seen := map[string]bool{}
	for _, seed := range agent.BuiltinToolSeeds() {
		schema, _ := json.Marshal(seed.Schema)
		seeds = append(seeds, ToolRecord{Key: seed.Key, System: true, Description: seed.Desc, Schema: schema, Agents: seed.Agents, Enabled: true, Kind: "builtin", Exec: json.RawMessage("{}")})
		seen[seed.Key] = true
	}
	for _, item := range extra {
		if seen[item.Name()] {
			continue
		}
		seen[item.Name()] = true
		schema, _ := json.Marshal(item.InputSchema())
		agents := []string{"auto"}
		if strings.HasPrefix(item.Name(), "get_task_") || strings.HasPrefix(item.Name(), "list_task_") || strings.HasPrefix(item.Name(), "search_task_") {
			agents = append(agents, "reporter")
		}
		if item.Name() == "get_finding_retest_context" || item.Name() == "record_finding_retest_result" {
			agents = []string{"retester"}
		}
		if item.Name() == "search_finding_duplicates" || item.Name() == "get_finding_record" || item.Name() == "get_finding_case" || item.Name() == "update_finding_case_report" || item.Name() == "suggest_finding_merge" || item.Name() == "merge_finding_records" || item.Name() == "complete_finding_case_review" {
			agents = []string{"reporter"}
		}
		if item.Name() == "traffic_search" || item.Name() == "traffic_get" || item.Name() == "traffic_blob" {
			agents = []string{"worker", "reporter", "mainagent", "auto", "pentest"}
		}
		if item.Name() == "bind_finding_traffic" {
			agents = []string{"reporter"}
		}
		if item.Name() == "get_finding_traffic" {
			agents = []string{"auto", "reporter", "worker", "planner", "mainagent", "pentest"}
		} else if item.Name() == "update_finding_report" {
			agents = []string{"auto", "reporter"}
		}
		if item.Name() == "search_platform_assets" {
			agents = []string{"mainagent", "planner", "worker", "auto", "pentest"}
		}
		if item.Name() == "report_vulnerability" {
			agents = []string{"mainagent", "auto", "pentest"}
		}
		if strings.HasPrefix(item.Name(), "oast_") {
			agents = []string{"mainagent", "auto", "pentest", "planner", "worker", "retester"}
			if item.Name() == "oast_list" || item.Name() == "oast_poll" {
				agents = append(agents, "reporter")
			}
		}
		if item.Name() == "run_info_gather" || item.Name() == "prepare_info_gather" || item.Name() == "run_info_gather_phase" || item.Name() == "finish_info_gather" {
			agents = []string{"info_gatherer"}
		}
		seeds = append(seeds, ToolRecord{Key: item.Name(), System: true, Description: item.Description(), Schema: schema, Agents: agents, Enabled: true, Kind: "builtin", Exec: json.RawMessage("{}")})
	}
	return seeds
}

var customToolKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)

func toolSchema(raw json.RawMessage) (map[string]any, error) {
	var value map[string]any
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if len(raw) > 256*1024 || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, ErrToolCatalogInvalid
	}
	return value, nil
}
func stripToolProse(value any) any {
	switch item := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, value := range item {
			if key == "properties" || key == "$defs" || key == "definitions" || key == "patternProperties" {
				entries, ok := value.(map[string]any)
				if ok {
					preserved := map[string]any{}
					for name, entry := range entries {
						preserved[name] = stripToolProse(entry)
					}
					out[key] = preserved
					continue
				}
			}
			if key != "description" && key != "default" {
				out[key] = stripToolProse(value)
			}
		}
		return out
	case []any:
		out := make([]any, len(item))
		for i, value := range item {
			out[i] = stripToolProse(value)
		}
		return out
	default:
		return value
	}
}
func (f *ToolCatalogFacade) Save(ctx context.Context, user int, key string, input ToolRecord, create bool) (ToolRecord, error) {
	if user <= 0 {
		return input, ErrForbidden
	}
	if !customToolKey.MatchString(key) || len(input.Description) > 64*1024 || len(input.Exec) > 1<<20 || len(input.Agents) > 200 {
		return input, ErrToolCatalogInvalid
	}
	input.Key = key
	schema, err := toolSchema(input.Schema)
	if err != nil {
		return input, err
	}
	input.Schema, _ = json.Marshal(schema)
	agents, err := f.catalog.List(ctx, user)
	if err != nil {
		return input, err
	}
	valid := map[string]bool{}
	for _, agent := range agents {
		valid[agent.Key] = true
	}
	seen := map[string]bool{}
	unique := []string{}
	for _, key := range input.Agents {
		if !valid[key] {
			return input, ErrToolCatalogInvalid
		}
		if !seen[key] {
			seen[key] = true
			unique = append(unique, key)
		}
	}
	input.Agents = unique
	if !create {
		rows, err := f.store.ListToolCatalog(ctx, user, true)
		if err != nil {
			return input, err
		}
		found := false
		for _, row := range rows {
			if row.Key != key {
				continue
			}
			found = true
			input.System = row.System
			if row.System {
				original, err := toolSchema(row.Schema)
				if err != nil {
					return input, err
				}
				if !reflect.DeepEqual(stripToolProse(original), stripToolProse(schema)) {
					return input, ErrToolCatalogInvalid
				}
				input.Kind = row.Kind
				input.Exec = row.Exec
				input.Deferred = row.Deferred
			}
			break
		}
		if !found {
			return input, ErrToolCatalogNotFound
		}
	} else {
		input.System = false
	}
	if input.System {
		input.Kind = "builtin"
	} else {
		switch input.Kind {
		case "shell":
			input.Exec = json.RawMessage("{}")
		case "command", "script", "http":
			var spec map[string]any
			if json.Unmarshal(input.Exec, &spec) != nil || spec == nil {
				return input, ErrToolCatalogInvalid
			}
			field := map[string]string{"command": "command", "script": "code", "http": "url"}[input.Kind]
			value, _ := spec[field].(string)
			if strings.TrimSpace(value) == "" {
				return input, ErrToolCatalogInvalid
			}
			if input.Kind == "http" {
				props, _ := schema["properties"].(map[string]any)
				if len(props) == 0 {
					return input, ErrToolCatalogInvalid
				}
			}
		default:
			return input, ErrToolCatalogInvalid
		}
	}
	if err = f.store.SaveToolCatalog(ctx, user, input, create); err != nil {
		return input, err
	}
	return input, nil
}
func (f *ToolCatalogFacade) Delete(ctx context.Context, user int, key string) error {
	if user <= 0 {
		return ErrForbidden
	}
	return f.store.DeleteToolCatalog(ctx, user, key)
}
func (f *ToolCatalogFacade) Reset(ctx context.Context, user int, key string) error {
	for _, seed := range f.seeds {
		if seed.Key == key {
			return f.store.SaveToolCatalog(ctx, user, seed, false)
		}
	}
	return ErrToolCatalogNotFound
}
func containsAgent(keys []string, key string) bool {
	for _, candidate := range keys {
		if candidate == key {
			return true
		}
	}
	return false
}
func (f *ToolCatalogFacade) Resolve(ctx context.Context, user int, key string, base []tool.CoreTool) ([]tool.CoreTool, error) {
	if key == "assistant" {
		key = "mainagent"
	}
	rows, err := f.store.ListToolCatalog(ctx, user, false)
	if err != nil {
		return nil, err
	}
	byName := map[string]ToolRecord{}
	for _, row := range rows {
		byName[row.Key] = row
	}
	out := []tool.CoreTool{}
	baseNames := map[string]bool{}
	for _, item := range base {
		if item != nil {
			baseNames[item.Name()] = true
		}
	}
	_, taskContext := airuntime.TaskExecutionFromContext(ctx)
	seen := map[string]bool{}
	add := func(item tool.CoreTool) {
		if item == nil || seen[item.Name()] {
			return
		}
		if item.Name() == "report_vulnerability" && taskContext {
			return
		}
		if airuntime.RequiresTaskContext(item.Name()) && (!taskContext || !baseNames[item.Name()]) {
			return
		}
		if item.Name() == "list_assets" && !airuntime.AssetSearchAllowed(ctx) {
			return
		}
		row, ok := byName[item.Name()]
		if ok {
			if !row.Enabled || !containsAgent(row.Agents, key) || row.Kind == "shell" {
				return
			}
			schema, _ := toolSchema(row.Schema)
			if item.Name() == "record_fact" {
				liveProps, _ := item.InputSchema()["properties"].(map[string]any)
				storedProps, _ := schema["properties"].(map[string]any)
				if storedProps != nil {
					if dynamic, ok := liveProps["intelligence"]; ok {
						storedProps["intelligence"] = dynamic
					} else {
						delete(storedProps, "intelligence")
					}
				}
			}
			item = agent.DecorateTool(item, row.Description, schema)
			if row.Deferred {
				item = airuntime.WithDeferredTool(item)
			}
		}
		seen[item.Name()] = true
		out = append(out, item)
	}
	for _, item := range base {
		add(item)
	}
	if f.domain != nil {
		for _, item := range f.domain(user) {
			// A pre-existing custom tool owns its key. A newly introduced host
			// tool must not silently replace the user's handler on upgrade.
			if row, ok := byName[item.Name()]; ok && !row.System {
				continue
			}
			if _, taskContext := airuntime.TaskExecutionFromContext(ctx); taskContext && item.Name() == "list_assets" && !baseNames[item.Name()] {
				continue
			}
			if (item.Name() == "search_platform_assets" || item.Name() == "insert_assets") && !baseNames[item.Name()] {
				continue
			}
			add(item)
		}
	}
	for _, row := range rows {
		if !row.System && row.Kind != "shell" && row.Enabled && containsAgent(row.Agents, key) && f.custom != nil {
			add(f.custom(ctx, user, row, base))
		}
	}
	hints := []string{}
	for _, row := range rows {
		if row.Kind == "shell" && row.Enabled && containsAgent(row.Agents, key) {
			hints = append(hints, "- "+row.Key+": "+row.Description)
		}
	}
	if len(hints) > 0 {
		for i, item := range out {
			if item.Name() == "Bash" {
				out[i] = agent.DecorateTool(item, item.Description()+"\n以下工具已安装，可通过 Bash 调用：\n"+strings.Join(hints, "\n"), item.InputSchema())
			}
		}
	}
	return out, nil
}

var _ airuntime.ToolLayerResolver = (*ToolCatalogFacade)(nil).Resolve
