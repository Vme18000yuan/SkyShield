package oast

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/time/rate"
)

type Recorder interface {
	Record(context.Context, string, Event) error
}

// Collector never interprets forwarded headers as peer identity. They are
// evidence only: the public reverse proxy must overwrite them if desired.
type Collector struct {
	recorder Recorder
	limiter  *rate.Limiter
	slots    chan struct{}
}

func NewCollector(recorder Recorder) *Collector {
	return &Collector{recorder: recorder, limiter: rate.NewLimiter(50, 100), slots: make(chan struct{}, 16)}
}

func (h *Collector) admit() bool {
	if !h.limiter.Allow() {
		return false
	}
	select {
	case h.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func boundedText(value string, limit int) (string, bool) {
	truncated := len(value) > limit
	if truncated {
		value = value[:limit]
	}
	value = strings.ToValidUTF8(value, "�")
	value = strings.ReplaceAll(value, "\x00", `\x00`)
	for len(value) > limit {
		_, n := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-n]
		truncated = true
	}
	return value, truncated
}

func callbackToken(path string) string {
	if !strings.HasPrefix(path, "/oast/") {
		return ""
	}
	token := strings.SplitN(strings.TrimPrefix(path, "/oast/"), "/", 2)[0]
	if !tokenPattern.MatchString(token) {
		return ""
	}
	return token
}

func (h *Collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	token := callbackToken(r.URL.Path)
	if token == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !h.admit() {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	defer func() { <-h.slots }()
	// Limit both duration and bytes even for chunked or slow uploads.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer controller.SetReadDeadline(time.Time{})
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	e := Event{Protocol: "http", RemoteAddr: r.RemoteAddr, Method: r.Method, Truncated: len(body) > MaxBodyBytes}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		e.RemoteAddr = host
	}
	if len(body) > MaxBodyBytes {
		body = body[:MaxBodyBytes]
	}
	e.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	var cut bool
	e.Host, cut = boundedText(r.Host, 512)
	e.Truncated = e.Truncated || cut
	e.Path, cut = boundedText(r.URL.RequestURI(), 2048)
	e.Truncated = e.Truncated || cut
	keys := make([]string, 0, len(r.Header))
	for k := range r.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var head strings.Builder
	for _, key := range keys {
		value := strings.Join(r.Header.Values(key), ", ")
		switch strings.ToLower(key) {
		case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key":
			value = "[REDACTED]"
		}
		line, truncated := boundedText(key+": "+value+"\r\n", MaxHeaderBytes-head.Len())
		head.WriteString(line)
		e.Truncated = e.Truncated || truncated
		if head.Len() >= MaxHeaderBytes {
			e.Truncated = true
			break
		}
	}
	e.Headers = head.String()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err = h.recorder.Record(ctx, token, e); err != nil {
		if err == ErrNotFound || err == ErrInvalid {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
