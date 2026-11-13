package airuntime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
)

func TestModelProviderUsesLiveSystemProxy(t *testing.T) {
	var first, second atomic.Int64
	proxy := func(count *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}))
	}
	proxyA, proxyB := proxy(&first), proxy(&second)
	defer proxyA.Close()
	defer proxyB.Close()
	controller := httpoutbound.NewProxyController()
	config := agent.ConfigFrom("openai", "fixture", "http://model.example.invalid/v1", "fixture-key", proxyA.URL)
	config.ProxyResolver = controller.WithFallback(config.Proxy)
	provider, err := config.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	request := llm.CompletionRequest{Messages: []llm.Message{llm.UserText("ping")}}
	call := func() {
		t.Helper()
		for _, err := range provider.Stream(context.Background(), request) {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	call() // explicit per-provider fallback
	if err := controller.Set(true, proxyB.URL); err != nil {
		t.Fatal(err)
	}
	call() // same provider observes the new system proxy
	if first.Load() != 1 || second.Load() != 1 {
		t.Fatalf("proxy calls: %d %d", first.Load(), second.Load())
	}
	if err := controller.Set(false, ""); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://model.example.invalid/", nil)
	resolved, err := config.ProxyResolver(req)
	if err != nil || resolved != nil {
		t.Fatalf("disabled system proxy did not override provider fallback: %v %v", resolved, err)
	}
}
