package infrastructure

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDNSResolverUsesPublicAnswerAndRejectsUnsafeNames(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("name") != "assets.example.com" || r.URL.Query().Get("type") != "A" || r.Header.Get("Accept") != "application/dns-json" {
			t.Errorf("unexpected DNS request: %s", r.URL.String())
		}
		fmt.Fprint(w, `{"Status":0,"Answer":[{"type":5,"data":"alias.example.com."},{"type":1,"data":"198.18.0.2"},{"type":1,"data":"8.8.8.8"}]}`)
	}))
	defer server.Close()
	resolver := NewDNSResolver(server.Client())
	resolver.endpoint = server.URL
	ip, err := resolver.ResolveHost(context.Background(), "Assets.Example.Com.")
	if err != nil || ip != "8.8.8.8" {
		t.Fatalf("public result = %q, %v", ip, err)
	}
	for _, host := range []string{"198.18.0.2", "localhost", "server.internal", "x.local", "bad host.example.com", "user@example.com"} {
		if _, err := resolver.ResolveHost(context.Background(), host); err == nil {
			t.Fatalf("accepted unsafe host %q", host)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("invalid names generated network requests: %d", calls.Load())
	}
}

func TestDNSResolverHandlesFailuresAndBoundsResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"no public IP", `{"Status":0,"Answer":[{"type":1,"data":"198.18.0.2"}]}`, 200},
		{"lookup failure", `{"Status":3}`, 200},
		{"bad json", `{`, 200},
		{"oversized", strings.Repeat("x", 65537), 200},
		{"http failure", `{}`, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			resolver := NewDNSResolver(server.Client())
			resolver.endpoint = server.URL
			if _, err := resolver.ResolveHost(context.Background(), "assets.example.com"); err == nil {
				t.Fatal("accepted unsuccessful response")
			}
		})
	}
}
