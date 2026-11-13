package httpoutbound

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/url"
	"os"
)

type recordingKey struct{}
type recordingRoute struct {
	proxy   *url.URL
	bundle  string
	enabled func(context.Context) (bool, error)
}

// WithRecordingRoute supplies an authenticated loopback recorder for the
// lifetime of one server-authorized agent run. The recorder's own upstream
// always follows the system proxy policy.
func WithRecordingRoute(ctx context.Context, proxyURL, bundle string, enabled func(context.Context) (bool, error)) (context.Context, error) {
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed == nil || parsed.Scheme != "http" || parsed.User == nil || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || bundle == "" || enabled == nil {
		return ctx, errors.New("invalid recording route")
	}
	return context.WithValue(ctx, recordingKey{}, recordingRoute{proxy: parsed, bundle: bundle, enabled: enabled}), nil
}
func RecordingProxy(ctx context.Context, fallback func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	route, ok := ctx.Value(recordingKey{}).(recordingRoute)
	if !ok {
		return fallback
	}
	return func(req *http.Request) (*url.URL, error) {
		enabled, err := route.enabled(ctx)
		if err != nil {
			return nil, err
		}
		if !enabled {
			return fallback(req)
		}
		return route.proxy, nil
	}
}
func RecordingCABundle(ctx context.Context) string {
	route, _ := ctx.Value(recordingKey{}).(recordingRoute)
	return route.bundle
}
func ApplyRecordingTrust(ctx context.Context, transport *http.Transport) error {
	bundle := RecordingCABundle(ctx)
	if bundle == "" {
		return nil
	}
	data, err := os.ReadFile(bundle)
	if err != nil {
		return err
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(data) {
		return errors.New("invalid recording CA bundle")
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.RootCAs = roots
	return nil
}
