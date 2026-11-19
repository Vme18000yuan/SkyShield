package oast

import (
	"testing"

	"github.com/yyhuni/lunafox/server/internal/config"
)

func TestDeploymentConfigSeparatesWebAndCallbackOrigins(t *testing.T) {
	defaultListen := config.GetDefaults().OAST.HTTPListen
	if defaultListen != "0.0.0.0:8899" {
		t.Fatalf("default listener: %q", defaultListen)
	}
	for _, tt := range []struct{ site, listen, explicit, want string }{
		{"http://127.0.0.1:3000", defaultListen, "", "http://127.0.0.1:8899"},
		{"https://skyshield.example.test:8443", defaultListen, "", "http://skyshield.example.test:8899"},
		{"http://[::1]:3000", defaultListen, "", "http://[::1]:8899"},
		{"https://web.example.test", "127.0.0.1:9901", "", "http://web.example.test:9901"},
		{"http://127.0.0.1:3000", defaultListen, "https://callback.example.test", "https://callback.example.test"},
		{"https://web.example.test:8443", "off", "", "https://web.example.test:8443"},
	} {
		got, err := DeploymentConfig(tt.site, Config{HTTPListen: tt.listen, PublicURL: tt.explicit})
		if err != nil || got.PublicURL != tt.want {
			t.Fatalf("%+v => %+v %v", tt, got, err)
		}
		if tt.listen == "off" && got.HTTPListen != "" {
			t.Fatal("off kept listener enabled")
		}
	}
	for _, listen := range []string{"localhost", ":0", ":99999"} {
		if _, err := DeploymentConfig("http://localhost:3000", Config{HTTPListen: listen}); err == nil {
			t.Fatalf("accepted invalid listen %q", listen)
		}
	}
}
