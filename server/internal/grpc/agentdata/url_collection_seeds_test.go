package agentdata

import (
	"context"
	execution "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/execution/v1"
	blacklist "github.com/yyhuni/lunafox/server/internal/modules/blacklist/domain"
	scanapp "github.com/yyhuni/lunafox/server/internal/modules/scan/application"
	scandomain "github.com/yyhuni/lunafox/server/internal/modules/scan/domain"
	"reflect"
	"testing"
)

type responsiveSeedsStub struct{}

func (responsiveSeedsStub) ForEachWebsiteURLByScanID(_ context.Context, _ int, visit func(string) error) error {
	for _, url := range []string{"https://example.com/ok", "https://example.com/bad"} {
		if err := visit(url); err != nil {
			return err
		}
	}
	return nil
}
func (responsiveSeedsStub) ForEachResponsiveWebsiteURLByScanID(_ context.Context, _ int, visit func(string) error) error {
	return visit("https://example.com/ok")
}

func TestURLCollectionResponsivePolicyDoesNotChangeOtherEngineInputs(t *testing.T) {
	for _, tc := range []struct {
		engine, policy string
		want           []string
	}{
		{"engine.lunafox.url_collection", "responsive", []string{"https://example.com/ok"}},
		{"engine.lunafox.url_collection", "all", []string{"https://example.com/ok", "https://example.com/bad"}},
		{"engine.lunafox.directory_scan", "responsive", []string{"https://example.com/ok", "https://example.com/bad"}},
	} {
		t.Run(tc.engine+tc.policy, func(t *testing.T) {
			plan := &execution.ResolvedEngineExecutionPlan{EngineRelease: &execution.EngineRelease{Engine: tc.engine}, Config: &execution.FinalEngineConfig{Sections: []*execution.EngineConfigSection{{SectionId: "katana", Enabled: true, Params: []*execution.EngineConfigParam{{Key: "seed-policy", Value: &execution.EngineConfigScalar{Value: &execution.EngineConfigScalar_StringValue{StringValue: tc.policy}}}}}}}}
			matcher, _ := blacklist.CompileMatcher(nil)
			filter, _ := scanapp.NewExecutionInputBlacklistFilter(matcher)
			resolver := serverExecutionArtifactResolver{websiteURLs: responsiveSeedsStub{}}
			producer, err := resolver.newWebsiteURLsProducer(context.Background(), authorizedExecutionPlan{scanID: 7, inputSource: scandomain.InputSourceScanSnapshot, plan: plan}, filter)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			if err := producer(func(url string) error { got = append(got, url); return nil }); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%v want %v", got, tc.want)
			}
		})
	}
}
