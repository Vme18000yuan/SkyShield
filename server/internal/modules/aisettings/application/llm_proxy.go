package application

import (
	"net/url"
	"strconv"
	"strings"
)

func validateLLMProxy(raw string) error {
	if len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\t\x00 ") {
		return ErrInvalidProfile
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return ErrInvalidProfile
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return ErrInvalidProfile
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return ErrInvalidProfile
		}
	}
	return nil
}
