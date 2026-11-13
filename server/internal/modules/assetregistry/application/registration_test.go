package application

import (
	"context"
	"testing"

	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
)

type registrationStoreStub struct{ commands []RegistrationCommand }

func (s *registrationStoreStub) RegisterAsset(_ context.Context, command RegistrationCommand) (RegisteredAsset, error) {
	s.commands = append(s.commands, command)
	return RegisteredAsset{ID: int64(len(s.commands)), Type: command.Asset.Input.Type, Projection: command.Asset.Projection}, nil
}

func TestApplicationBoundDomainsAreNormalizedAndRespectTarget(t *testing.T) {
	asset, err := prepareRegistration(AssetInput{Type: "app", AppName: "Sales", BoundDomains: []string{"API.Example.COM."}})
	if err != nil || len(asset.Input.BoundDomains) != 1 || asset.Input.BoundDomains[0] != "api.example.com" {
		t.Fatalf("normalized application domains: %+v %v", asset, err)
	}
	if !RegistrationMatchesTarget(asset, assetdomain.TargetRef{Name: "example.com", Type: "domain"}) {
		t.Fatal("matching application domain rejected")
	}
	if RegistrationMatchesTarget(asset, assetdomain.TargetRef{Name: "other.example.com", Type: "domain"}) {
		t.Fatal("application domain widened target scope")
	}
	if _, err = prepareRegistration(AssetInput{Type: "app", AppName: "Sales", BoundDomains: []string{"https://example.com/path"}}); err == nil {
		t.Fatal("invalid bound domain accepted")
	}
}

func TestRegistrationPreservesRawURLAndUnknownMethod(t *testing.T) {
	const raw = "https://Example.com/Case%2FPath?z=2&a=1"
	asset, err := prepareRegistration(AssetInput{Type: "endpoint", URL: raw})
	if err != nil || asset.Label != raw || asset.Method != nil {
		t.Fatalf("unknown method observation: %+v %v", asset, err)
	}
	get, err := prepareRegistration(AssetInput{Type: "endpoint", URL: raw, Method: "get"})
	if err != nil || get.Method == nil || *get.Method != "GET" || string(get.Identity) == string(asset.Identity) {
		t.Fatalf("method identity: %+v %v", get, err)
	}
}

func TestRootDomainRegistrationUsesPublicSuffixBoundaries(t *testing.T) {
	for _, domain := range []string{"skyshield.example", "example.co.uk", "account.github.io"} {
		if _, err := prepareRegistration(AssetInput{Type: "root_domain", Domain: domain}); err != nil {
			t.Fatalf("valid root %q rejected: %v", domain, err)
		}
	}
	for _, domain := range []string{"co.uk", "x.skyshield.example", "api.account.github.io"} {
		if _, err := prepareRegistration(AssetInput{Type: "root_domain", Domain: domain}); err == nil {
			t.Fatalf("invalid root %q accepted", domain)
		}
	}
}

func TestRegistrationKeepsPartialSuccessAndInputIndexes(t *testing.T) {
	store := &registrationStoreStub{}
	facade := NewRegistrationFacade(store, nil)
	result, err := facade.Register(context.Background(), BatchRegisterInput{UserID: 7, Origin: "manual", RequestID: "request", Assets: []AssetInput{
		{Type: "ip", IP: "2001:0db8::1"},
		{Type: "endpoint", URL: "not-a-url"},
		{Type: "app", AppName: "应用", BundleID: "com.example.app"},
	}})
	if err != nil || len(result.Results) != 2 || len(result.Errors) != 1 || result.Errors[0].Index != 1 || result.Results[1].Index != 2 {
		t.Fatalf("partial results: %+v %v", result, err)
	}
	if len(store.commands) != 2 || store.commands[0].Asset.Label != "2001:db8::1" {
		t.Fatalf("canonical IP identity: %+v", store.commands)
	}
}
