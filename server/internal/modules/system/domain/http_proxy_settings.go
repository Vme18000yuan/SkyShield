package domain

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTPProxySettings is the singleton, installation-wide outbound proxy policy.
type HTTPProxySettings struct {
	Enabled           bool
	EncryptedProxyURL []byte
	UpdatedAt         time.Time
}

type HTTPProxySettingsRepository interface {
	GetHTTPProxySettings(ctx context.Context) (*HTTPProxySettings, error)
	SaveHTTPProxySettings(ctx context.Context, settings HTTPProxySettings, updatedBy int) error
}

func NormalizeHTTPProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\t\x00 ") {
		return "", fmt.Errorf("proxy URL is invalid")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("proxy URL must be an http or https proxy address without a path, query, or fragment")
	}
	if port := parsed.Port(); port != "" {
		portNumber, parseErr := strconv.Atoi(port)
		if parseErr != nil || portNumber < 1 || portNumber > 65535 {
			return "", fmt.Errorf("proxy URL port is invalid")
		}
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Path = ""
	parsed.RawPath = ""
	return parsed.String(), nil
}

func SafeHTTPProxyAddress(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}
