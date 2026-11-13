package infogather

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeRiskSupportsReviewableFindingsAndRejectsUnsafeAddresses(t *testing.T) {
	for _, tc := range []struct {
		category, value string
		dns, valid      bool
	}{
		{"ANONYMOUS_DATA", "https://app.example.test/api", false, true},
		{"REVIEW_TOKEN_DIFFERENCE", "https://app.example.test/api", false, true},
		{"PUBLIC_LISTING", "https://bucket.example.test/", false, true},
		{"DNS_ZONE_TRANSFER", "Sub.Example.Test.", true, true},
		{"DNS_ZONE_TRANSFER", "https://example.test/", true, false},
		{"PUBLIC_LISTING", "https://user:password@example.test/", false, false},
		{"OTHER", "https://example.test/", false, false},
	} {
		raw, _ := json.Marshal(Observation{ID: strings.Repeat("a", 64), Category: tc.category, Value: tc.value})
		risk, err := decodeRisk("observation", raw)
		if (err == nil) != tc.valid || err == nil && (risk.DNS != tc.dns || risk.Title == "" || risk.Description == "") {
			t.Fatalf("%s %q: risk=%+v err=%v", tc.category, tc.value, risk, err)
		}
	}
}

func TestEnvelopeDropsOnlyExactlyDuplicatedRiskObservations(t *testing.T) {
	id := strings.Repeat("a", 64)
	scope, err := parseScope("example.test")
	if err != nil {
		t.Fatal(err)
	}
	report := Envelope{SchemaVersion: "1.0", Run: ReportRun{ID: "run", Target: scope.Name, Kind: scope.Kind, Status: "partial", Mode: "normal"},
		Assets: []Asset{}, Vulnerabilities: []Vulnerability{{ID: id, Type: "ANONYMOUS_DATA", URL: "https://example.test/api", Title: "candidate", Severity: "info", Confidence: "candidate", Status: "needs_review", Scope: "in_scope", Evidence: []string{"recon.json"}}},
		Observations: []Observation{{ID: id, Category: "ANONYMOUS_DATA", Value: "https://example.test/api", Scope: "in_scope", Status: "needs_review", Evidence: []string{"recon.json"}},
			{ID: strings.Repeat("b", 64), Category: "DNS_ZONE_TRANSFER", Value: "example.test", Scope: "in_scope", Status: "needs_review", Evidence: []string{"axfr.json"}}}}
	body, _ := json.Marshal(report)
	decoded, err := decodeEnvelope(body, "run", scope, "normal")
	if err != nil || len(decoded.Vulnerabilities) != 1 || len(decoded.Observations) != 1 || decoded.Observations[0].Category != "DNS_ZONE_TRANSFER" {
		t.Fatalf("incorrect deduplication: %+v %v", decoded, err)
	}
	report.Observations[0].Value = "https://example.test/other"
	body, _ = json.Marshal(report)
	decoded, err = decodeEnvelope(body, "run", scope, "normal")
	if err != nil || len(decoded.Observations) != 2 {
		t.Fatalf("different evidence was discarded: %+v %v", decoded, err)
	}
}
