package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestSecretFileAcceptsOneTrailingNewline(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		valid             bool
	}{
		{"no newline", "fixture-value", "fixture-value", true},
		{"LF", "fixture-value\n", "fixture-value", true},
		{"CRLF", "fixture-value\r\n", "fixture-value", true},
		{"embedded newline", "first\nsecond", "", false},
		{"repeated newline", "fixture-value\n\n", "", false},
		{"bare carriage return", "fixture-value\r", "", false},
		{"NUL", "fixture\x00value", "", false},
		{"empty line", "\n", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			v := viper.New()
			v.Set("FIXTURE_SECRET_FILE", path)
			err := resolveSecretFile(v, "FIXTURE_SECRET", "FIXTURE_SECRET_FILE")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if tc.valid && v.GetString("FIXTURE_SECRET") != tc.want {
				t.Fatal("secret did not preserve the expected value")
			}
			original, readErr := os.ReadFile(path)
			if readErr != nil || string(original) != tc.input {
				t.Fatalf("secret file was modified: %v", readErr)
			}
		})
	}
}
