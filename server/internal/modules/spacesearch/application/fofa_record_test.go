package application

import "testing"

func TestFofaRecordMappingPreservesURLAndAvoidsInventedDNS(t *testing.T) {
	const raw = "https://Api.Example.com/Case%2FPath?b=2&a=1"
	record := NormalizeRecord(Record{Host: raw, IP: "192.0.2.8", Port: 443, Protocol: "https", Title: "原始标题", Server: "nginx", BaseProtocol: "tcp"})
	if !record.Importable || record.URL != raw || record.Hostname != "api.example.com" || record.Target != "example.com" {
		t.Fatalf("observed URL changed: %+v", record)
	}
	assets := RecordAssets(ImportCommand{Record: record, Query: `domain="example.com"`, Selection: ImportSelection{OrganizationID: 4, SearchID: "fixture"}}, 3)
	if len(assets) != 3 || assets[1].RecordType != "" || len(assets[1].RecordValue) != 0 || assets[2].URL != raw || assets[2].Webserver != "nginx" || assets[2].Extra["provider"] != "fofa" {
		t.Fatalf("incorrect FOFA field mapping: %+v", assets)
	}
}

func TestFofaAutomaticTargetUsesRegistrableRoot(t *testing.T) {
	for _, tc := range []struct{ hostname, target string }{
		{"a.example.com", "example.com"},
		{"example.com", "example.com"},
		{"api.shop.example.co.uk", "example.co.uk"},
		{"api.account.github.io", "account.github.io"},
		{"www.xn--85x722f.com.cn", "xn--85x722f.com.cn"},
		{"192.0.2.4", "192.0.2.4"},
		{"2001:db8::4", "2001:db8::4"},
		{"localhost", "localhost"},
	} {
		t.Run(tc.hostname, func(t *testing.T) {
			if got := AutomaticTarget(tc.hostname); got != tc.target {
				t.Fatalf("AutomaticTarget(%q) = %q, want %q", tc.hostname, got, tc.target)
			}
		})
	}
}

func TestFofaRecordHTTPAndOtherServiceNormalization(t *testing.T) {
	for _, test := range []struct {
		host, ip, protocol, url string
		port                    int
		valid                   bool
	}{
		{"api.example.com:8443", "192.0.2.4", "https", "https://api.example.com:8443", 8443, true},
		{"192.0.2.4:22", "192.0.2.4", "ssh", "", 22, true},
		{"2001:db8::4", "2001:db8::4", "http", "http://[2001:db8::4]", 80, true},
		{"https://u:p@example.com/", "192.0.2.4", "https", "", 443, false},
		{"example.com", "invalid", "https", "", 443, false},
		{"example.com", "192.0.2.4", "https", "", 0, false},
	} {
		r := NormalizeRecord(Record{Host: test.host, IP: test.ip, Port: test.port, Protocol: test.protocol})
		if r.Importable != test.valid || r.URL != test.url {
			t.Errorf("normalize %+v: %+v", test, r)
		}
	}
}
