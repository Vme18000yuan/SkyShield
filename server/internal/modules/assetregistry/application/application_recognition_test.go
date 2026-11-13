package application

import "testing"

func TestRecognizeWebApplicationUsesSpecificEvidence(t *testing.T) {
	ok, denied := int64(200), int64(403)
	for _, tc := range []struct {
		name, url, title  string
		tech              []string
		status            *int64
		want, scope, kind string
	}{
		{"title", "https://fleet.skyshield.example/", "ExampleFleet", []string{"Azure"}, &ok, "ExampleFleet", "skyshield.example", "title"},
		{"login title", "https://a.example.com/", "登录 - 销售管理平台", nil, &ok, "销售管理平台", "example.com", "title"},
		{"technology fallback", "https://b.example.com/", "登录", []string{"Nginx", "Grafana:10.2"}, &ok, "Grafana", "example.com", "technology"},
		{"unverified technology", "https://b.example.com/", "", []string{"WordPress"}, nil, "", "", ""},
		{"framework alone", "https://b.example.com/", "登录", []string{"Nginx", "Vue.js"}, &ok, "", "", ""},
		{"error status", "https://b.example.com/", "Sales App", nil, &denied, "", "", ""},
		{"error title", "https://b.example.com/", "Access denied - Company", []string{"Jenkins"}, nil, "", "", ""},
		{"chinese error title", "https://b.example.com/", "访问被拒绝，请联系管理员", []string{"Jenkins"}, nil, "", "", ""},
		{"redirect", "https://b.example.com/", "301 Moved Permanently", nil, nil, "", "", ""},
		{"ip host", "http://8.8.8.8:8080/", "Reports", nil, &ok, "Reports", "8.8.8.8", "title"},
		{"bad url", "not a URL", "Reports", nil, &ok, "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RecognizeWebApplication(tc.url, tc.title, tc.tech, tc.status)
			if (tc.want != "") != ok || got.Name != tc.want || got.Scope != tc.scope || got.EvidenceKind != tc.kind {
				t.Fatalf("candidate=%+v ok=%v", got, ok)
			}
		})
	}
}

func TestRecognizeWebApplicationNormalizesIdentity(t *testing.T) {
	a, ok := RecognizeWebApplication("https://a.example.com", "  Ｓａｌｅｓ　 Portal ", nil, nil)
	if !ok {
		t.Fatal("expected app")
	}
	b, ok := RecognizeWebApplication("https://b.example.com", "sales portal", nil, nil)
	if !ok || a.Scope != b.Scope || a.IdentityName != b.IdentityName {
		t.Fatalf("inconsistent identity: %+v %+v", a, b)
	}
}
