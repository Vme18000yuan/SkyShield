package traffic

import (
	"strings"
	"testing"

	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

func TestJoinHTTPMessagePreservesBodyAndBoundary(t *testing.T) {
	body := []byte("\r\n{\"line\":1}\x00")
	for _, head := range []string{"HTTP/1.1 200 OK\r\nX-Proof: one", "HTTP/1.1 200 OK\r\nX-Proof: one\r\n", "HTTP/1.1 200 OK\r\nX-Proof: one\r\n\r\n"} {
		if got := JoinHTTPMessage(head, body); got != "HTTP/1.1 200 OK\r\nX-Proof: one\r\n\r\n"+string(body) {
			t.Fatalf("body/boundary changed: %q", got)
		}
	}
	if got := JoinHTTPMessage("", body); got != string(body) {
		t.Fatalf("invented header: %q", got)
	}
	if got := JoinHTTPMessage("HTTP 200\nX-Proof: one", body); got != "HTTP 200\nX-Proof: one\n\n"+string(body) {
		t.Fatalf("legacy header changed: %q", got)
	}
}

func TestRecordPreservesResponseProtocolAndReason(t *testing.T) {
	tr, _ := openTraffic(t)
	tr.record(newFlow("raw.example", "POST", "/inspect", []byte("request body"), []byte("\r\nresponse body"), func(f *mproxy.Flow) {
		f.Response.Proto = "HTTP/2.0"
		f.Response.Status = "201 Custom reason"
		f.Response.StatusCode = 201
		f.Response.Header.Add("X-Proof", "one")
		f.Response.Header.Add("X-Proof", "two")
	}))
	request, response, err := tr.Get(onlyExchangeID(t, tr))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(request, "POST /inspect HTTP/1.1\r\n") || !strings.HasSuffix(request, "\r\n\r\nrequest body") {
		t.Fatalf("bad request: %q", request)
	}
	if !strings.HasPrefix(response, "HTTP/2.0 201 Custom reason\r\n") || !strings.Contains(response, "X-Proof: one\r\nX-Proof: two\r\n") || !strings.HasSuffix(response, "\r\n\r\n\r\nresponse body") {
		t.Fatalf("bad response: %q", response)
	}
	if got := responseStatusLine(&mproxy.Response{StatusCode: 200}); strings.HasPrefix(got, "HTTP/") {
		t.Fatalf("invented old protocol: %q", got)
	}
}
