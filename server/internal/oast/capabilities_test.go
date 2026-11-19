package oast

import (
	"slices"
	"strings"
	"testing"
)

func TestCapabilitiesReportLimitsWithoutClaimingReachability(t *testing.T) {
	for _, tt := range []struct{ address, scope string }{
		{"", "unconfigured"}, {"http://127.0.0.1:3000", "loopback"},
		{"http://[::1]:3000", "loopback"}, {"http://LOCALHOST.:8080", "loopback"},
		{"http://sub.localhost:8080", "loopback"}, {"http://0.0.0.0:8080", "unusable"},
		{"http://192.168.1.10:8080", "private"}, {"https://callback.example.test", "hostname"},
		{"https://192.0.2.10", "public_ip"},
	} {
		s, err := New(nil, Config{PublicURL: tt.address}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		status := s.capabilities()
		if status.AddressScope != tt.scope || status.Reachability != "not_verified" || status.Enabled != (tt.address != "") {
			t.Fatalf("%s: %+v", tt.address, status)
		}
		if status.DefaultTTLSeconds != 3600 || status.MaxTTLSeconds != 86400 || status.MaxWaitSeconds != 20 {
			t.Fatalf("incorrect limits: %+v", status)
		}
		if status.Enabled && len(status.Protocols) != 1 {
			t.Fatalf("invented DNS capability: %+v", status)
		}
		if tt.scope == "loopback" && !strings.Contains(strings.Join(status.Notes, " "), "不能用它验证远程目标") {
			t.Fatal("loopback warning missing")
		}
	}
	s, err := New(nil, Config{PublicURL: "https://callback.example.test", DNSListen: "0.0.0.0:5353", DNSZone: "cb.example.test", DNSAnswerIP: "192.0.2.10"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	status := s.capabilities()
	if !slices.Equal(status.Protocols, []string{"https", "dns"}) || status.DNSZone != "cb.example.test" || status.Reachability != "not_verified" {
		t.Fatalf("DNS status: %+v", status)
	}
}
