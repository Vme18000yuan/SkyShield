package airuntime

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSecretBoxBindsCredentialToProfile(t *testing.T) {
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))))
	box := SecretBox{}
	ciphertext, err := box.Seal("tenant/profile-a", "example-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), "example-secret") {
		t.Fatal("credential stored in plaintext")
	}
	value, err := box.Open("tenant/profile-a", ciphertext)
	if err != nil || value != "example-secret" {
		t.Fatalf("round trip: %q, %v", value, err)
	}
	if _, err := box.Open("tenant/profile-b", ciphertext); err == nil {
		t.Fatal("credential opened under another profile")
	}
	first, err := box.Fingerprint("tenant/observation-a", []byte("synthetic-secret"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := box.Fingerprint("tenant/observation-a", []byte("synthetic-secret"))
	if err != nil || first != again {
		t.Fatal("credential request fingerprint is unstable")
	}
	other, err := box.Fingerprint("tenant/observation-b", []byte("synthetic-secret"))
	if err != nil || first == other {
		t.Fatal("credential fingerprint is not scope-bound")
	}
}
