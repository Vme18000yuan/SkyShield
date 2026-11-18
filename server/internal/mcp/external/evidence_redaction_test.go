package external

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedactedPacketPreservesBoundariesAndHidesCredentials(t *testing.T) {
	raw := "POST /check?access_token=private-fixture HTTP/1.1\r\nHost: example.test\r\nAuthorization: Bearer private-fixture\r\nCookie: sid=private-fixture\r\nX-Custom: private-fixture\r\n continuation: private-fixture\r\nContent-Type: application/json\r\n\r\n" + `{"data":{"refresh_token":"private-fixture","ok":true}}`
	out, partial := redactHTTP(raw, false)
	if partial || strings.Contains(out, "private-fixture") || !strings.Contains(out, "Host: example.test\r\n") || !strings.Contains(out, "\r\n\r\n") || !strings.Contains(out, `"ok":true`) {
		t.Fatal(out, partial)
	}
	for _, raw := range []string{"opaque secret", "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\nprivate-fixture", "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n\"private-fixture\""} {
		value, hidden := redactHTTP(raw, true)
		if !hidden || strings.Contains(value, "private-fixture") {
			t.Fatal(value)
		}
	}
}
func TestBrowserOriginMatchesIngress(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/mcp/clients", nil)
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("Origin", "http://frontend.example:3300")
	req.Header.Set("X-Forwarded-Host", "frontend.example:3300")
	req.Header.Set("X-Forwarded-Proto", "http")
	if !browserOriginAllowed(req) {
		t.Fatal("rejected same-origin diagnostic")
	}
	req.Header.Set("Origin", "https://another.example")
	if browserOriginAllowed(req) {
		t.Fatal("accepted unrelated origin")
	}
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if browserOriginAllowed(req) {
		t.Fatal("accepted cross-site browser request")
	}
}
