package llmrec

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestBoundedCaptureLimitsBothSides(t *testing.T) {
	_, capture := NewBoundedCapture(context.Background(), 5)
	capture.SetRequest("123456789")
	first := capture.TeeResponse(200, io.NopCloser(strings.NewReader("abc")))
	second := capture.TeeResponse(500, io.NopCloser(strings.NewReader("defghi")))
	_, _ = io.ReadAll(first)
	_, _ = io.ReadAll(second)
	if capture.RawRequest() != "12345" || !capture.Truncated() {
		t.Fatalf("bounded request: %q truncated=%t", capture.RawRequest(), capture.Truncated())
	}
	attempts := capture.Attempts()
	if len(attempts) != 2 || attempts[0].Body != "abc" || attempts[1].Body != "de" {
		t.Fatalf("bounded response attempts: %+v", attempts)
	}
}
