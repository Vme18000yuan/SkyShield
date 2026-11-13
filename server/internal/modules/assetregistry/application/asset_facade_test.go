package application

import (
	"context"
	"errors"
	"testing"
)

type assetStoreStub struct {
	member bool
	items  []AssetEntity
	query  AssetQuery
}

func (s *assetStoreStub) IsActiveTenantMember(context.Context, string, int) (bool, error) {
	return s.member, nil
}

func (s *assetStoreStub) ListAssets(_ context.Context, q AssetQuery) ([]AssetEntity, error) {
	s.query = q
	return s.items, nil
}

func (s *assetStoreStub) BackfillBatch(context.Context, int) (bool, error) { return true, nil }
func (s *assetStoreStub) CountAssets(context.Context, AssetQuery) (int64, error) {
	return int64(len(s.items)), nil
}
func (s *assetStoreStub) ListPorts(context.Context, PortQuery) ([]PortAsset, int64, error) {
	return nil, 0, nil
}
func (s *assetStoreStub) ListApplicationWebsites(context.Context, ApplicationWebsiteQuery) ([]ApplicationWebsite, int64, error) {
	return nil, 0, nil
}

func TestListBindsCursorToCallerAndFilters(t *testing.T) {
	store := &assetStoreStub{member: true, items: []AssetEntity{{ID: 9}, {ID: 8}, {ID: 7}}}
	facade := NewFacade(store)
	input := ListInput{UserID: 3, AssetType: "service", Query: "example", PageSize: 2}
	result, err := facade.List(context.Background(), input)
	if err != nil || len(result.Items) != 2 || result.NextPageToken == "" || store.query.Limit != 3 {
		t.Fatalf("first page: result %+v, query %+v, err %v", result, store.query, err)
	}
	input.PageToken = result.NextPageToken
	store.items = []AssetEntity{{ID: 7}}
	if _, err := facade.List(context.Background(), input); err != nil || store.query.BeforeID != 8 {
		t.Fatalf("next page before=%d err=%v", store.query.BeforeID, err)
	}
	input.UserID = 4
	if _, err := facade.List(context.Background(), input); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("cursor reused by different user: %v", err)
	}
	input.UserID = 3
	input.Query = "other"
	if _, err := facade.List(context.Background(), input); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("cursor reused with different filter: %v", err)
	}
}

func TestListRejectsMissingMembershipAndUnboundedInput(t *testing.T) {
	store := &assetStoreStub{}
	facade := NewFacade(store)
	if _, err := facade.List(context.Background(), ListInput{UserID: 7}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing tenant membership: %v", err)
	}
	store.member = true
	for _, input := range []ListInput{
		{UserID: 7, PageSize: 101},
		{UserID: 7, AssetType: "not-an-asset"},
		{UserID: 7, AssetCategory: "not-a-category"},
		{UserID: 7, PageToken: "invalid"},
		{UserID: 7, OrganizationID: 2147483648},
		{UserID: 7, Page: 1000001},
	} {
		if _, err := facade.List(context.Background(), input); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("input %+v: %v", input, err)
		}
	}
}

func TestPortServiceNamesPreferEvidenceAndLabelEstimates(t *testing.T) {
	items := []PortAsset{{Port: 22}, {Port: 21}, {Port: 1883}, {Port: 3306}, {Port: 6379}, {Port: 5432}, {Port: 9999}, {Port: 22, Service: "custom-ssh"}}
	classifyPorts(items)
	for i, want := range []string{"SSH", "FTP", "MQTT", "MYSQL", "REDIS", "POSTGRESQL", "UNKNOWN", "CUSTOM-SSH"} {
		if items[i].Service != want {
			t.Fatalf("port %d: %+v, want %s", items[i].Port, items[i], want)
		}
	}
	if items[0].ServiceSource != "port" || items[6].ServiceSource != "unknown" || items[7].ServiceSource != "observed" {
		t.Fatalf("service evidence: %+v", items)
	}
}

func TestDisplayClassificationAndCategoryCursor(t *testing.T) {
	for _, tc := range []struct{ kind, label, want string }{
		{"service", "http://site.example/", "web"},
		{"service", "HTTPS://site.example/Case%2FPath", "web"},
		{"endpoint", "https://site.example/api", "web"},
		{"service", "ssh.example:22", "service"},
		{"ip", "1.1.1.1", "ip"},
		{"endpoint", "non-http-legacy-value", "endpoint"},
	} {
		if got := AssetDisplayType(tc.kind, tc.label); got != tc.want {
			t.Errorf("%s %q: got %q, want %q", tc.kind, tc.label, got, tc.want)
		}
	}
	store := &assetStoreStub{member: true, items: []AssetEntity{{ID: 9, AssetType: "service", Label: "http://site.example/"}, {ID: 8, AssetType: "endpoint", Label: "https://site.example/api"}}}
	facade := NewFacade(store)
	first, err := facade.List(context.Background(), ListInput{UserID: 3, AssetCategory: "web", PageSize: 1})
	if err != nil || first.NextPageToken == "" || first.Items[0].DisplayType != "web" || store.query.AssetCategory != "web" {
		t.Fatalf("web page: %+v, query %+v, %v", first, store.query, err)
	}
	if _, err := facade.List(context.Background(), ListInput{UserID: 3, AssetCategory: "non_web_service", PageSize: 1, PageToken: first.NextPageToken}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("web cursor accepted for other services: %v", err)
	}
}
