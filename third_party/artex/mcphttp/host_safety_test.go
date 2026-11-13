package mcphttp

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHostDoesNotForwardMCPHeadersOnRedirect(t *testing.T) {
	var hits atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(http.StatusNoContent) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	if _, err := New(context.Background(), "fixture", source.URL, map[string]string{"Authorization": "secret"}, false); err == nil {
		t.Fatal("MCP redirect was accepted")
	}
	if hits.Load() != 0 {
		t.Fatal("MCP redirect forwarded credentials")
	}
}
func TestLegacySSEEndpointCannotChangeOrigin(t *testing.T) {
	_, err := readSSEEndpoint(bufio.NewReader(strings.NewReader("data: https://other.example/message\n\n")), "https://source.example/sse")
	if err == nil {
		t.Fatal("cross-origin MCP endpoint accepted")
	}
}

func TestMCPTransportErrorsOmitResponseBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fixture-secret", http.StatusUnauthorized)
	}))
	defer server.Close()
	_, err := New(context.Background(), "fixture", server.URL, nil, false)
	if err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("response body leaked through transport error: %v", err)
	}
}
