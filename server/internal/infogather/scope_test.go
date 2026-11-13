package infogather

import (
	"strings"
	"testing"

	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
)

func TestScopeNormalizationAndContainment(t *testing.T) {
	cases := []struct{ input, name, kind string }{
		{"https://APP.example.com:8443/path", "app.example.com", "HOST"},
		{"2001:db8::123/120", "2001:db8::100/120", "CIDR"},
		{"https://[2001:db8::1]/", "2001:db8::1", "IP"},
	}
	for _, tc := range cases {
		got, err := parseScope(tc.input)
		if err != nil || got.Name != tc.name || got.Kind != tc.kind {
			t.Fatalf("%s: %#v %v", tc.input, got, err)
		}
	}
	for _, invalid := range []string{"-bad", "https://user:secret@example.com/", "https://example.com/?token=secret", "example.com;touch /tmp/x", "127.0.0.1/99"} {
		if _, err := parseScope(invalid); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
	domain := assetdomain.TargetRef{Name: "example.com", Type: "domain"}
	if !targetContainsScope(domain, Scope{Name: "sub.example.com", Kind: "HOST"}) || targetContainsScope(domain, Scope{Name: "example.com.other", Kind: "HOST"}) {
		t.Fatal("domain boundary")
	}
	network := assetdomain.TargetRef{Name: "2001:db8::/120", Type: "cidr"}
	if !targetContainsScope(network, Scope{Name: "2001:db8::/124", Kind: "CIDR"}) || targetContainsScope(network, Scope{Name: "2001:db8::/112", Kind: "CIDR"}) {
		t.Fatal("network containment")
	}
}

func TestAssetMappingDoesNotTrustReportScope(t *testing.T) {
	target := assetdomain.TargetRef{ID: 7, Name: "example.com", Type: "domain"}
	domain, err := toAssetInput(Asset{Type: "domain", Value: "example.com", Scope: "out_of_scope"}, target, false, "run")
	if err != nil || domain.Type != "root_domain" {
		t.Fatalf("root mapping: %#v %v", domain, err)
	}
	if _, err = toAssetInput(Asset{Type: "domain", Value: "evil.example.net", Scope: "in_scope"}, target, false, "run"); err == nil {
		t.Fatal("trusted report scope")
	}
	ip := Asset{Type: "ip", Value: "203.0.113.9", BoundDomains: []string{"app.example.com"}, CDN: true}
	if _, err = toAssetInput(ip, target, false, "run"); err == nil {
		t.Fatal("automatic CDN association")
	}
	if _, err = toAssetInput(ip, target, true, "run"); err != nil {
		t.Fatal(err)
	}
	ip.BoundDomains = []string{"evil.example.net"}
	if _, err = toAssetInput(ip, target, true, "run"); err == nil {
		t.Fatal("unrelated DNS association")
	}
	service := Asset{Type: "service", Value: "203.0.113.9", Port: 22, Protocol: "tcp", ServiceName: "ssh", BoundDomains: []string{"app.example.com"}}
	mapped, err := toAssetInput(service, target, false, "run")
	if err != nil || mapped.Domain != "app.example.com" || mapped.IP != "203.0.113.9" || mapped.Port != 22 {
		t.Fatalf("service mapping: %#v %v", mapped, err)
	}
	if _, err = toAssetInput(Asset{Type: "endpoint", Value: "https://example.com/api?secret=x"}, target, false, "run"); err == nil {
		t.Fatal("query-containing URL imported")
	}
	web, err := toAssetInput(Asset{Type: "service", Value: "https://app.example.com/", PageTitle: "Console", Technologies: []string{"nginx", "PHP:8.3"}}, target, false, "run")
	if err != nil || web.PageTitle != "Console" || len(web.Technologies) != 2 {
		t.Fatalf("web metadata missing: %+v %v", web, err)
	}
	ipv6Target := assetdomain.TargetRef{ID: 8, Name: "2001:db8::1", Type: "ip"}
	if _, err = toAssetInput(Asset{Type: "endpoint", Value: "https://[2001:db8::1]/api"}, ipv6Target, false, "run"); err == nil || !strings.Contains(err.Error(), "IPv6 URL") {
		t.Fatalf("IPv6 limitation not explained: %v", err)
	}
}

func TestEvidencePaths(t *testing.T) {
	for _, s := range []string{"/etc/passwd", "../secret", "raw/../../secret", "a\\b", "a//b", "a/./b", ""} {
		if validEvidence(s) {
			t.Errorf("accepted %q", s)
		}
	}
	if !validEvidence("raw/ports.json") {
		t.Fatal("valid evidence rejected")
	}
	if recordIDPattern.MatchString(strings.Repeat("g", 64)) {
		t.Fatal("non-hex id accepted")
	}
}
