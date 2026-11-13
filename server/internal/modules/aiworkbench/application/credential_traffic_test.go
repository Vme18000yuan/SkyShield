package application

import (
	"strings"
	"testing"

	artexdb "github.com/Autumn-27/artex/db"
)

func TestCredentialTrafficRequiresHostAndActualCredential(t *testing.T) {
	hosts := credentialTrafficHosts([]CredentialTrafficAsset{{Type: "root_domain", Label: "example.com"}, {Type: "service", Label: "https://api.other.example:8443/"}}, "", "https://exact.test/login")
	for _, raw := range []string{"https://example.com/", "https://a.example.com/", "https://api.other.example:8443/", "https://exact.test/"} {
		if !credentialHostMatches(raw, hosts) {
			t.Fatalf("expected host %s", raw)
		}
	}
	for _, raw := range []string{"https://badexample.com/", "https://x.api.other.example/", "https://x.exact.test/"} {
		if credentialHostMatches(raw, hosts) {
			t.Fatalf("unexpected host %s", raw)
		}
	}
	account := artexdb.CredentialAccount{Username: "alice", Password: "p@ssword"}
	if !credentialValueMatches(`username=alice&password=p%40ssword`, account) {
		t.Fatal("URL-encoded credential not matched")
	}
	if credentialValueMatches(`username=bob&password=p%40ssword`, account) {
		t.Fatal("password alone matched a named account")
	}
	if credentialValueMatches("token=short", artexdb.CredentialAccount{Token: "short"}) {
		t.Fatal("short token matched")
	}
	excerpt := credentialTrafficExcerpt(strings.Repeat("x", 200)+"p@ssword"+strings.Repeat("y", 1000), []artexdb.CredentialAccount{account})
	if !strings.Contains(excerpt, "p@ssword") || len([]rune(excerpt)) > 600 {
		t.Fatalf("bad excerpt: %q", excerpt)
	}
}
