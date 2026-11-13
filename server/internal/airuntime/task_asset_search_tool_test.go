package airuntime

import (
	"strings"
	"testing"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/tool"
)

func TestTaskAssetDSLUsesParametersAndAllOrganizations(t *testing.T) {
	const hostile = `x' OR '1'='1`
	query, args, err := artexdb.CompileAssetDSL(`company_id==7 AND (domain=="`+hostile+`" OR port>=443)`, 3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query, hostile) || !strings.Contains(query, "$4 = ANY(company_ids)") || !strings.Contains(query, "domain = $5") || !strings.Contains(query, "unnest(ports) p(v) WHERE v >= $6") {
		t.Fatalf("unexpected compiled expression: %s", query)
	}
	if len(args) != 3 || args[0] != int64(7) || args[1] != hostile || args[2] != 443 {
		t.Fatalf("unexpected DSL parameters: %#v", args)
	}
	if _, _, err = artexdb.CompileAssetDSL("port>not-a-number", 3); err == nil {
		t.Fatal("invalid numeric DSL accepted")
	}
	if _, _, err = artexdb.CompileAssetDSL(strings.Repeat("a", 4097), 3); err == nil {
		t.Fatal("oversized DSL accepted")
	}
}

func TestAssetSearchGrantCoversTaskTools(t *testing.T) {
	config := DefaultAgentConfig("planner")
	config.SearchAssetsEnabled = false
	items := []tool.CoreTool{taskListAssetsTool(nil, TaskExecution{}), taskUntestedAssetsTool(nil, TaskExecution{})}
	if got := FilterAgentTools(config, items); len(got) != 0 {
		t.Fatalf("disabled asset search exposed %d tools", len(got))
	}
	config.SearchAssetsEnabled = true
	if got := FilterAgentTools(config, items); len(got) != 2 {
		t.Fatalf("enabled asset search returned %d tools", len(got))
	}
}
