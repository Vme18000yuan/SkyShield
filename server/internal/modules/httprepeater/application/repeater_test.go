package application

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func input() Input {
	return Input{RequestID: uuid.NewString(), TargetURL: "http://127.0.0.1:8089/", RawRequest: "POST /a HTTP/1.1\nHost: virtual.test\nX-Proof: one\nX-Proof: two\nContent-Length: 999\n\n中"}
}
func TestPreparePreservesHeadersAndRepairsByteLength(t *testing.T) {
	p, err := Prepare(input())
	if err != nil {
		t.Fatal(err)
	}
	if string(p.Raw) != "POST /a HTTP/1.1\r\nHost: virtual.test\r\nX-Proof: one\r\nX-Proof: two\r\nContent-Length: 3\r\n\r\n中" {
		t.Fatalf("wrong raw: %q", p.Raw)
	}
	if p.Target.Host != "127.0.0.1:8089" || p.Request.Host != "virtual.test" || !p.VerifyTLS {
		t.Fatalf("target/Host/TLS: %#v", p)
	}
	if urls := p.PolicyURLs(); len(urls) != 2 || urls[1] != "http://virtual.test/a" {
		t.Fatal(urls)
	}
}

func TestPreparePreservesDestinationAndHostPorts(t *testing.T) {
	for _, protocol := range []string{"HTTP/1.1", "HTTP/2.0"} {
		for _, target := range []struct{ scheme, host string }{
			{"http", "example.test"},
			{"https", "example.test"},
			{"http", "example.test:80"},
			{"https", "example.test:443"},
			{"https", "example.test:00443"},
			{"https", "example.test:80"},
			{"http", "192.0.2.1"},
			{"https", "192.0.2.1:8443"},
			{"http", "[2001:db8::1]"},
			{"https", "[2001:db8::1]"},
			{"https", "[2001:db8::1]:443"},
		} {
			t.Run(protocol+"/"+target.scheme+"/"+target.host, func(t *testing.T) {
				raw := "GET /path HTTP/1.1\r\nHost: " + target.host + "\r\n\r\n"
				raw = strings.Replace(raw, "HTTP/1.1", protocol, 1)
				p, err := Prepare(Input{RequestID: uuid.NewString(), TargetURL: target.scheme + "://" + target.host + "/", RawRequest: raw})
				if err != nil {
					t.Fatal(err)
				}
				if p.Target.Host != target.host || p.Request.Host != target.host || string(p.Raw) != raw {
					t.Fatalf("authority changed: target=%q Host=%q raw=%q", p.Target.Host, p.Request.Host, p.Raw)
				}
				if got := p.PolicyURLs()[0]; got != target.scheme+"://"+target.host+"/path" {
					t.Fatalf("policy URL authority changed: %q", got)
				}
			})
		}
	}
}

func TestPrepareRejectsInvalidDestinationPorts(t *testing.T) {
	for _, target := range []string{"http://example.test:0/", "https://example.test:65536/", "http://example.test:/", "https://[::1]:/"} {
		in := input()
		in.TargetURL = target
		if _, err := Prepare(in); err == nil {
			t.Fatalf("accepted invalid destination port: %q", target)
		}
	}
}

func TestHeaderOnlyPasteAfterTransportNormalization(t *testing.T) {
	raw := "GET /container?restype=container&comp=list HTTP/1.1\nHost: storage.skyshield.example\nAccept: application/xml"
	p, err := Prepare(Input{RequestID: uuid.NewString(), TargetURL: "http://storage.skyshield.example/", RawRequest: raw + "\n\n"})
	if err != nil {
		t.Fatal(err)
	}
	if string(p.Raw) != strings.ReplaceAll(raw, "\n", "\r\n")+"\r\n\r\n" || p.Request.ContentLength != 0 || p.Request.URL.RawQuery != "restype=container&comp=list" {
		t.Fatalf("header-only GET changed: %q", p.Raw)
	}
}
func TestPrepareRejectsMalformedOrMultipleMessages(t *testing.T) {
	for _, raw := range []string{"GET / HTTP/3\nHost: example.test\n\n", "GET / HTTP/1.1\nHost: example.test", "GET / HTTP/1.1\nHost: example.test\nContent-Length: 0\nContent-Length: 0\n\n", "POST / HTTP/1.1\nHost: example.test\nContent-Length: 0\nTransfer-Encoding: chunked\n\n0\r\n\r\n", "CONNECT localhost:25 HTTP/1.1\nHost: localhost\n\n", "GET / HTTP/1.1\nHost: a\n\nGET /second HTTP/1.1\nHost: b\n\n"} {
		in := input()
		off := false
		in.AutoContentLength = &off
		in.RawRequest = raw
		if _, err := Prepare(in); err == nil {
			t.Fatalf("accepted malformed: %q", raw)
		}
	}
	in := input()
	in.TargetURL = "http://user:password@example.test/"
	if _, err := Prepare(in); err == nil {
		t.Fatal("credentials accepted in target")
	}
	in = input()
	in.RawRequest = strings.Repeat("x", MaxRequestBytes+1)
	if _, err := Prepare(in); err == nil {
		t.Fatal("unbounded request")
	}
	in = input()
	in.RawRequest = "POST / HTTP/1.1\r\nHost: example.test\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n\r\n"
	p, err := Prepare(in)
	if err != nil || string(p.Raw) != in.RawRequest {
		t.Fatalf("valid chunked body changed: %v", err)
	}
}

type accessFixture struct{ err error }

func (a accessFixture) Permit(context.Context, int, []string) (AddressGuard, error) {
	return func(string) error { return nil }, a.err
}

type transportFixture struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (t *transportFixture) Send(ctx context.Context, p Prepared, _ AddressGuard) Result {
	t.calls.Add(1)
	if t.started != nil {
		t.started <- struct{}{}
		select {
		case <-t.release:
		case <-ctx.Done():
		}
	}
	return Result{RequestRaw: string(p.Raw), Sent: true}
}

func TestServicePermissionReceiptsAndConcurrency(t *testing.T) {
	transport := &transportFixture{}
	denied := NewService(accessFixture{ErrForbidden}, transport)
	if _, err := denied.Send(context.Background(), 7, input()); !errors.Is(err, ErrForbidden) || transport.calls.Load() != 0 {
		t.Fatalf("unauthorized send: %v", err)
	}
	svc := NewService(accessFixture{}, transport)
	in := input()
	if _, err := svc.Send(context.Background(), 7, in); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(context.Background(), 7, in); !errors.Is(err, ErrDuplicate) || transport.calls.Load() != 1 {
		t.Fatalf("duplicate was sent: %v", err)
	}
	transport = &transportFixture{started: make(chan struct{}, 2), release: make(chan struct{})}
	svc = NewService(accessFixture{}, transport)
	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, err := svc.Send(context.Background(), 7, input()); err != nil {
				t.Error(err)
			}
		}()
	}
	<-transport.started
	<-transport.started
	if _, err := svc.Send(context.Background(), 7, input()); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrency: %v", err)
	}
	close(transport.release)
	<-done
	<-done
	if _, err := svc.Send(context.Background(), 7, input()); err != nil {
		t.Fatalf("slot was leaked: %v", err)
	}
}
