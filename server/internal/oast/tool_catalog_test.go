package oast

import (
	"context"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/tool"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
)

type toolCatalogFixture struct {
	settings.ToolCatalogStore
	records []settings.ToolRecord
}

func (f *toolCatalogFixture) ListToolCatalog(context.Context, int, bool) ([]settings.ToolRecord, error) {
	return f.records, nil
}

// Use the real catalog decorator to exercise saved descriptions and bindings,
// including the independent-chat and worker tool-discovery entry point.
func testCatalog(s *Service) (*settings.ToolCatalogFacade, *toolCatalogFixture) {
	f := &toolCatalogFixture{records: settings.NativeToolSeeds(s.Tools(7))}
	for i := range f.records {
		if strings.HasPrefix(f.records[i].Key, "oast_") {
			f.records[i].Description = "Previously saved description: " + f.records[i].Key
		}
	}
	return settings.NewToolCatalogFacade(f, nil, s.Tools, nil), f
}

func TestCatalogPreservesRuntimeGuidanceAndRespectsPermissions(t *testing.T) {
	s, _ := New(nil, Config{PublicURL: "http://127.0.0.1:3000"}, nil, nil)
	catalog, store := testCatalog(s)
	for _, role := range []string{"pentest", "auto", "mainagent", "assistant", "worker", "planner", "retester"} {
		resolved, err := catalog.Resolve(context.Background(), 7, role, nil)
		if err != nil {
			t.Fatal(err)
		}
		schemas := tool.NewRegistry(resolved...).Schemas()
		if len(schemas) != 5 {
			t.Fatalf("%s tools: %+v", role, schemas)
		}
		for _, schema := range schemas {
			if !strings.HasPrefix(schema.Description, "Previously saved description:") {
				t.Fatal("overwrote saved description")
			}
			if schema.Name == "oast_create" && (!strings.Contains(schema.Description, "【自主反连测试流程】") || !strings.Contains(schema.Description, "loopback") || !strings.Contains(schema.Description, "oast_revoke")) {
				t.Fatalf("guidance missing for %s", role)
			}
		}
	}
	readOnly, err := catalog.Resolve(context.Background(), 7, "reporter", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(readOnly) != 3 {
		t.Fatalf("reporter tools: %d", len(readOnly))
	}
	for _, item := range readOnly {
		if !item.IsReadOnly(nil) {
			t.Fatalf("reporter got mutation %s", item.Name())
		}
	}
	for i := range store.records {
		if store.records[i].Key == "oast_create" {
			store.records[i].Enabled = false
		}
	}
	resolved, err := catalog.Resolve(context.Background(), 7, "pentest", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range tool.NewRegistry(resolved...).Schemas() {
		if schema.Name == "oast_create" || strings.Contains(schema.Description, "【自主反连测试流程】") {
			t.Fatal("disabled create was reintroduced")
		}
	}
	custom, err := catalog.Resolve(context.Background(), 7, "unbound_custom_agent", nil)
	if err != nil || len(custom) != 0 {
		t.Fatalf("unexpected custom binding: %d %v", len(custom), err)
	}
}
