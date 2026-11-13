package application

import "testing"

func TestRegistrableRootDomain(t *testing.T) {
	for _, tc := range []struct {
		kind, value, root string
		valid             bool
	}{
		{"subdomain", "z.skyshield.example", "skyshield.example", true},
		{"target", "SKYSHIELD.EXAMPLE.", "skyshield.example", true},
		{"subdomain", "api.shop.example.co.uk", "example.co.uk", true},
		{"subdomain", "api.account.github.io", "account.github.io", true},
		{"service", "https://A.Skyshield.example:443/path?x=1", "skyshield.example", true},
		{"endpoint", "https://u.example.co.uk/A%2Fv", "example.co.uk", true},
		{"service", "ssh.example.com:22", "example.com", true},
		{"subdomain", "x.食狮.com.cn", "xn--85x722f.com.cn", true},
		{"target", "co.uk", "", false},
		{"subdomain", "localhost", "", false},
		{"subdomain", "8.8.8.8", "", false},
		{"service", "https://8.8.8.8/", "", false},
		{"service", "[2001:db8::1]:443", "", false},
		{"endpoint", "not-a-url", "", false},
		{"ip", "1.2.3.4", "", false},
	} {
		t.Run(tc.kind+"/"+tc.value, func(t *testing.T) {
			root, ok := RegistrableRootDomain(tc.kind, tc.value)
			if ok != tc.valid || root != tc.root {
				t.Fatalf("root = %q, %v; want %q, %v", root, ok, tc.root, tc.valid)
			}
		})
	}
}
