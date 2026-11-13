package aivuln

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestEvidenceCannotEscapeAndReadsBoundedSnapshots(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("private"), 0600)
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "large"), []byte(strings.Repeat("x", maxEvidenceBytes+100)), 0600)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if item := readEvidence(root, "pipe"); item.Error == "" {
		t.Fatal("FIFO accepted as evidence")
	}
	for _, name := range []string{"../secret", "/etc/passwd", "escape", "a/../../secret", "a\\secret"} {
		if e := readEvidence(root, name); e.Error == "" || e.Text != "" {
			t.Fatalf("escaped %q: %#v", name, e)
		}
	}
	large := readEvidence(root, "large")
	if !large.Truncated || len(large.Text) != maxEvidenceBytes || large.SHA256 != "" {
		t.Fatalf("bad bound: %#v", large)
	}
	path := filepath.Join(dir, "raw", "fp-fixture", "raw", "http")
	os.MkdirAll(path, 0700)
	os.WriteFile(filepath.Join(path, "test.body"), []byte("saved body"), 0600)
	legacy := readLegacyInfoEvidence(root, "raw/http/test.body")
	if legacy.Text != "saved body" || legacy.SHA256 == "" {
		t.Fatalf("legacy evidence missing: %#v", legacy)
	}
	other := filepath.Join(dir, "raw", "fp-other", "raw", "http")
	os.MkdirAll(other, 0700)
	os.WriteFile(filepath.Join(other, "test.body"), []byte("other"), 0600)
	if found := readLegacyInfoEvidence(root, "raw/http/test.body"); found.Error == "" {
		t.Fatal("ambiguous legacy evidence accepted")
	}
}

func TestReportRequiresRealScopeAndEvidence(t *testing.T) {
	r := Report{URL: "https://example.test/api", VulnType: "Access control", Severity: "low", Summary: "Observed result", Evidence: "Saved observation"}
	if err := validateReport(r); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Report){func(r *Report) { r.URL = "file:///etc/passwd" }, func(r *Report) { r.Severity = "severe" }, func(r *Report) { r.Evidence = "" }, func(r *Report) { r.EvidencePaths = []string{"../other/report.txt"} }, func(r *Report) { r.Response = "a\x00b" }} {
		bad := r
		change(&bad)
		if validateReport(bad) == nil {
			t.Fatalf("accepted invalid report: %#v", bad)
		}
	}
}

func TestInfoGatherRawPacketPreservesBoundary(t *testing.T) {
	dir := t.TempDir()
	request := "POST /api HTTP/1.1\r\nHost: example.test\r\n\r\ninput"
	head := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	body := "\r\n{\"ok\":true}"
	for name, value := range map[string]string{"proof.body": body, "proof.headers": head, "proof.request": request} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	packet := readInfoPacket(root, "proof.body")
	if packet.Request != request || packet.Response != head+body {
		t.Fatalf("raw packet altered: %#v", packet)
	}
	if err := os.Remove(filepath.Join(dir, "proof.headers")); err != nil {
		t.Fatal(err)
	}
	packet = readInfoPacket(root, "proof.body")
	if packet.Source != "response_body" || packet.Response != body {
		t.Fatalf("invented missing response header: %#v", packet)
	}
}
