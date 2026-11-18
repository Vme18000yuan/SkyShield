package repository

import (
	"strings"
	"testing"
)

func TestTimelineLabelsUseRecordedFieldsWithoutExposingCommandOrCredentials(t *testing.T) {
	action, subject, kind := stepAction("Bash", `{"command":"curl https://inventory.example/items?token=fixture-secret"}`)
	if action != "" || kind != "http" || !strings.Contains(subject, "inventory.example") || strings.Contains(subject, "fixture-secret") {
		t.Fatalf("unsafe caption %q %q %q", action, subject, kind)
	}
	if value, _ := stepOutcome("Authorization: Bearer fixture-secret", ""); value != "" {
		t.Fatal("credential exposed")
	}
	text, n := stepOutcome(`{"count":7}`, `{"count":7,"message":"Inventory loaded"}`)
	if n == nil || *n != 7 || text != "Inventory loaded" {
		t.Fatal("lost structured outcome")
	}
	if text := userRequestText(`{"text":"Review inventory","attachments":[{"path":"unlisted"}]}`, ""); text != "Review inventory" {
		t.Fatal(text)
	}
}
