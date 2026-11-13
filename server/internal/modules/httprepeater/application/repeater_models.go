package application

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

const MaxRequestBytes = 512 << 10
const MaxResponseBytes = 2 << 20

var ErrForbidden = errors.New("repeater permission required")
var ErrBlocked = errors.New("destination blocked by platform policy")
var ErrBusy = errors.New("repeater concurrency limit reached")
var ErrDuplicate = errors.New("this request ID was already accepted")

type Input struct {
	RequestID         string `json:"requestId"`
	TargetURL         string `json:"targetUrl"`
	RawRequest        string `json:"rawRequest"`
	TimeoutSeconds    int    `json:"timeoutSeconds"`
	VerifyTLS         *bool  `json:"verifyTls"`
	AutoContentLength *bool  `json:"autoContentLength"`
}
type Prepared struct {
	Target            *url.URL
	Raw               []byte
	Request           *http.Request
	VerifyTLS         bool
	Timeout           time.Duration
	Protocol          string
	Body              []byte
	NormalizedHeaders []string
}
type Result struct {
	RequestID         string   `json:"requestId"`
	RequestRaw        string   `json:"requestRaw"`
	ResponseRaw       string   `json:"responseRaw"`
	ResponseBytes     []byte   `json:"responseBase64"`
	StatusCode        int      `json:"statusCode"`
	ElapsedMS         int64    `json:"elapsedMs"`
	ByteCount         int      `json:"byteCount"`
	Sent              bool     `json:"sent"`
	Truncated         bool     `json:"truncated"`
	Binary            bool     `json:"binary"`
	ErrorCode         string   `json:"errorCode,omitempty"`
	RequestProtocol   string   `json:"requestProtocol,omitempty"`
	ResponseProtocol  string   `json:"responseProtocol,omitempty"`
	Representation    string   `json:"representation,omitempty"`
	NormalizedHeaders []string `json:"normalizedHeaders,omitempty"`
}
type AddressGuard func(string) error
type Access interface {
	Permit(context.Context, int, []string) (AddressGuard, error)
}
type Transport interface {
	Send(context.Context, Prepared, AddressGuard) Result
}
type InvalidInput struct{ Message string }

func (e *InvalidInput) Error() string { return e.Message }
