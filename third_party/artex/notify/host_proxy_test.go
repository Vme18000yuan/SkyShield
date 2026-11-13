package notify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestHostProxyPolicyForNotificationHTTP(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "")
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "8.8.8.8" {
			t.Errorf("unexpected proxy destination %q", r.URL.Host)
		}
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	defer SetHTTPProxyResolver(nil)
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	SetHTTPProxyResolver(func(*http.Request) (*url.URL, error) { return proxyURL, nil })
	channel, _ := Get(KindWebhook)
	message := Message{Items: []Item{{FindingID: 1, Name: "test", Severity: "high"}}}
	if _, err = channel.Send(context.Background(), map[string]any{"url": "http://8.8.8.8/finding"}, message); err != nil {
		t.Fatalf("send via local proxy: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("proxy received %d requests", requests.Load())
	}
	if _, err = channel.Send(context.Background(), map[string]any{"url": "http://localhost/finding"}, message); err == nil {
		t.Fatal("proxied local destination was accepted")
	}
	if requests.Load() != 1 {
		t.Fatal("blocked destination reached proxy")
	}
	SetHTTPProxyResolver(func(*http.Request) (*url.URL, error) { return nil, errors.New("proxy policy unavailable") })
	if _, err = channel.Send(context.Background(), map[string]any{"url": "http://8.8.8.8/finding"}, message); err == nil {
		t.Fatal("changed proxy policy was ignored")
	}
	if requests.Load() != 1 {
		t.Fatal("request used the old proxy after policy change")
	}
}
