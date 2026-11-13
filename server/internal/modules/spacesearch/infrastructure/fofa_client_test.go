package infrastructure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	spaceapp "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/application"
)

func TestFofaClientUsesExplicitFieldsAndPreservesObservedValues(t *testing.T) {
	const query = `domain="example.com" && title="测试"`
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/search/all" || q.Get("email") != "ops@example.test" || q.Get("key") != "fixture-key" || q.Get("qbase64") != base64.StdEncoding.EncodeToString([]byte(query)) || q.Get("fields") != fofaFields || q.Get("page") != "2" || q.Get("size") != "2" || q.Get("full") != "true" {
			t.Errorf("unexpected FOFA request shape")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"error": false, "size": 3, "results": [][]any{{"https://api.example.com/Case%2FPath?a=1", "192.0.2.1", "443", "https", "example.com", "测试标题", "nginx", "CN", "中国", "上海", "上海", "ICP fixture", "tcp"}, {"192.0.2.2:22", "192.0.2.2", 22, "ssh", "", "", "OpenSSH", "", "", "", "", "", "tcp"}}})
	}))
	defer server.Close()
	client := NewFofaClient(server.URL, server.Client())
	result, err := client.Search(context.Background(), spaceapp.Credentials{Email: "ops@example.test", APIKey: "fixture-key"}, spaceapp.SearchInput{Query: query, Page: 2, PageSize: 2, Full: true})
	if err != nil || result.Total != 3 || len(result.Results) != 2 || result.Results[0].Host != "https://api.example.com/Case%2FPath?a=1" || result.Results[0].Title != "测试标题" || result.Results[1].Port != 22 {
		t.Fatalf("FOFA result: %+v %v", result, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider query was retried: %d", calls.Load())
	}
}

func TestFofaClientRedactsCredentialErrorsAndRejectsRedirects(t *testing.T) {
	var hits atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer destination.Close()
	for _, redirect := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if redirect {
				http.Redirect(w, r, destination.URL, http.StatusFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"error": true, "errmsg": "invalid key fixture-secret for ops@example.test"})
		}))
		client := NewFofaClient(server.URL, server.Client())
		err := client.Test(context.Background(), spaceapp.Credentials{Email: "ops@example.test", APIKey: "fixture-secret"})
		if err == nil || !errors.Is(err, spaceapp.ErrProvider) || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "ops@example.test") {
			t.Fatalf("unsafe provider error: %v", err)
		}
		server.Close()
	}
	if hits.Load() != 0 {
		t.Fatal("FOFA credentials were forwarded to redirect destination")
	}
}

func TestFofaClientRejectsMalformedFieldSets(t *testing.T) {
	for _, body := range []string{`{"error":false,"size":1,"results":[["host","ip"]]}`, `{"size":0,"results":[]}`, `{"error":false,"size":1,"results":[[{},{},0,0,0,0,0,0,0,0,0,0,0]]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, err := NewFofaClient(server.URL, server.Client()).Search(context.Background(), spaceapp.Credentials{APIKey: "key"}, spaceapp.SearchInput{Query: "q", Page: 1, PageSize: 20})
		if !errors.Is(err, spaceapp.ErrProvider) {
			t.Fatalf("malformed response accepted: %v", err)
		}
		server.Close()
	}
}
