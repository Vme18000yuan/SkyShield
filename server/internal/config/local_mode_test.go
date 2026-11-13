package config

import "testing"

func TestLocalModeAllowsOnlyLoopbackHTTPAndListeners(t *testing.T) {
	for _, test := range []struct {
		name, url, host, grpcHost string
		local, valid              bool
	}{
		{"native loopback", "http://127.0.0.1:3000", "127.0.0.1", "127.0.0.1", true, true},
		{"native ipv6", "http://[::1]:3000", "::1", "::1", true, true},
		{"native nonloopback URL", "http://192.0.2.1:3000", "127.0.0.1", "127.0.0.1", true, false},
		{"native public listener", "http://127.0.0.1:3000", "0.0.0.0", "127.0.0.1", true, false},
		{"container HTTP", "http://127.0.0.1:3000", "", "", false, false},
		{"container HTTPS", "https://example.com", "", "", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := GetDefaults()
			cfg.JWT.Secret = "local-mode-config-test-secret"
			cfg.PublicURL, cfg.LocalMode = test.url, test.local
			cfg.LocalDataRoot = "/tmp/skyshield-local-test"
			cfg.Server.Host, cfg.Server.GRPCHost = test.host, test.grpcHost
			if err := validateConfig(cfg); (err == nil) != test.valid {
				t.Fatalf("validateConfig() error = %v, want valid %v", err, test.valid)
			}
		})
	}
}
