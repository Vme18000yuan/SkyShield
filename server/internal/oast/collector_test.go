package oast

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

type recordingSink struct {
	mu     sync.Mutex
	events []Event
	tokens []string
	err    error
}

func (s *recordingSink) Record(_ context.Context, token string, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !tokenPattern.MatchString(token) {
		return ErrNotFound
	}
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, event)
	s.tokens = append(s.tokens, token)
	return nil
}

func TestHTTPCollectorBoundsAndPreservesEvidence(t *testing.T) {
	sink := &recordingSink{}
	collector := NewCollector(sink)
	token := strings.Repeat("a", 40)
	body := append([]byte{0, 0xff}, bytes.Repeat([]byte("x"), MaxBodyBytes+10)...)
	req := httptest.NewRequest("PATCH", "http://callback.test/oast/"+token+"/nested/?q=%00", bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.8:1234"
	req.Header.Set("Cookie", "session=secret")
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Probe", strings.Repeat("x", MaxHeaderBytes+100))
	w := httptest.NewRecorder()
	collector.ServeHTTP(w, req)
	if w.Code != 204 || w.Body.Len() != 0 || len(sink.events) != 1 {
		t.Fatalf("response=%d %q events=%d", w.Code, w.Body.String(), len(sink.events))
	}
	e := sink.events[0]
	decoded, err := base64.StdEncoding.DecodeString(e.BodyBase64)
	if err != nil || !bytes.Equal(decoded, body[:MaxBodyBytes]) {
		t.Fatal("body bytes were corrupted")
	}
	if !e.Truncated || len(e.Headers) > MaxHeaderBytes || strings.Contains(e.Headers, "secret") || !strings.Contains(e.Headers, "[REDACTED]") {
		t.Fatalf("header capture/redaction: %+v", e)
	}
	if e.RemoteAddr != "192.0.2.8" || e.Method != "PATCH" || !strings.Contains(e.Path, "/nested/?q=%00") {
		t.Fatalf("wrong attribution: %+v", e)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("callback was cacheable")
	}
}

func TestHTTPCollectorRejectsUnknownCapabilitiesAndFailures(t *testing.T) {
	for _, tt := range []struct {
		path string
		err  error
		code int
	}{
		{"/oast/short", nil, 404}, {"/v1/oast/" + strings.Repeat("b", 40), nil, 404},
		{"/oast/" + strings.Repeat("b", 40), ErrNotFound, 404}, {"/oast/" + strings.Repeat("b", 40), errors.New("database unavailable"), 503},
	} {
		sink := &recordingSink{err: tt.err}
		w := httptest.NewRecorder()
		NewCollector(sink).ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
		if w.Code != tt.code || len(sink.events) != 0 {
			t.Fatalf("%s: %d events=%d", tt.path, w.Code, len(sink.events))
		}
	}
	sink := &recordingSink{}
	c := NewCollector(sink)
	for i := 0; i < cap(c.slots); i++ {
		c.slots <- struct{}{}
	}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("GET", "/oast/"+strings.Repeat("a", 40), nil))
	if w.Code != 429 || len(sink.events) != 0 {
		t.Fatal("collector admitted work beyond concurrency limit")
	}
}

func startTestDNS(t *testing.T, handler dns.Handler) string {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	if err != nil {
		_ = tcp.Close()
		t.Fatal(err)
	}
	ready := make(chan struct{}, 2)
	servers := []*dns.Server{{Listener: tcp, Handler: handler, NotifyStartedFunc: func() { ready <- struct{}{} }}, {PacketConn: udp, Handler: handler, NotifyStartedFunc: func() { ready <- struct{}{} }}}
	for _, server := range servers {
		go func() { _ = server.ActivateAndServe() }()
	}
	for range servers {
		select {
		case <-ready:
		case <-time.After(3 * time.Second):
			t.Fatal("DNS server did not start")
		}
	}
	t.Cleanup(func() {
		for _, server := range servers {
			_ = server.Shutdown()
		}
		_ = tcp.Close()
		_ = udp.Close()
	})
	return tcp.Addr().String()
}

func TestDNSCollectorUDPAndTCPAreAuthoritativeAndDoNotRecurse(t *testing.T) {
	sink := &recordingSink{}
	addr := startTestDNS(t, NewDNSCollector(NewCollector(sink), "cb.example.test", "192.0.2.10"))
	for _, transport := range []string{"udp", "tcp"} {
		for _, tt := range []struct {
			name           string
			qtype          uint16
			rcode, answers int
		}{
			{strings.Repeat("A", 40) + ".cb.example.test.", dns.TypeA, dns.RcodeSuccess, 1},
			{strings.Repeat("b", 40) + ".cb.example.test.", dns.TypeAAAA, dns.RcodeSuccess, 0},
			{"cb.example.test.", dns.TypeNS, dns.RcodeSuccess, 1},
			{"ns1.cb.example.test.", dns.TypeA, dns.RcodeSuccess, 1},
			{"missing.cb.example.test.", dns.TypeA, dns.RcodeNameError, 0},
			{"example.org.", dns.TypeA, dns.RcodeRefused, 0},
		} {
			m := new(dns.Msg)
			m.SetQuestion(tt.name, tt.qtype)
			response, _, err := (&dns.Client{Net: transport, Timeout: 2 * time.Second}).Exchange(m, addr)
			if err != nil {
				t.Fatal(err)
			}
			if response.Rcode != tt.rcode || response.RecursionAvailable || len(response.Answer) != tt.answers {
				t.Fatalf("%s %s: %+v", transport, tt.name, response)
			}
			if tt.rcode == dns.RcodeSuccess && !response.Authoritative {
				t.Fatal("not authoritative")
			}
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 4 || sink.tokens[0] != strings.Repeat("a", 40) || sink.events[0].Protocol != "dns" {
		t.Fatalf("DNS attribution: %+v", sink)
	}
}

func TestConfigRejectsIncompleteOrAmbiguousDeployment(t *testing.T) {
	for _, cfg := range []Config{
		{PublicURL: "file:///tmp/callback"}, {PublicURL: "https://user:secret@example.test"}, {PublicURL: "https://example.test/path"}, {PublicURL: "https://example.test?x=1"},
		{PublicURL: "https://example.test", DNSZone: "cb.example.test"}, {PublicURL: "https://example.test", DNSListen: invalidListen, DNSZone: "cb.example.test", DNSAnswerIP: "192.0.2.2"},
	} {
		if _, err := cfg.Normalize(); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	if _, err := (Config{PublicURL: "http://127.0.0.1:8080/", DNSListen: "127.0.0.1:5353", DNSZone: "CB.example.test.", DNSAnswerIP: "::1"}).Normalize(); err != nil {
		t.Fatal(err)
	}
}

const invalidListen = "localhost"

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenBody) Close() error             { return nil }
func TestIncompleteHTTPBodyIsNotClaimedAsEvidence(t *testing.T) {
	sink := &recordingSink{}
	req := httptest.NewRequest("POST", "/oast/"+strings.Repeat("a", 40), nil)
	req.Body = brokenBody{}
	w := httptest.NewRecorder()
	NewCollector(sink).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || len(sink.events) != 0 {
		t.Fatal("incomplete request was recorded as complete")
	}
}
