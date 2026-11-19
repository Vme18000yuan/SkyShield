// Package oast owns the built-in out-of-band callback collector. Callback
// capabilities only allow appending bounded evidence; reads require ownership.
package oast

import (
	"errors"
	"time"
)

var (
	ErrInvalid  = errors.New("invalid OAST input")
	ErrNotFound = errors.New("OAST resource not found")
	ErrDisabled = errors.New("OAST public URL is not configured")
	ErrQuota    = errors.New("OAST probe quota reached")
	ErrConflict = errors.New("OAST request ID was already used with different input")
)

const (
	PageSize       = 50
	MaxEvents      = 200
	MaxBodyBytes   = 8192
	MaxHeaderBytes = 8192
	Retention      = 7 * 24 * time.Hour
)

type Scope struct {
	ConversationID string `json:"conversationId,omitempty"`
	TaskID         int64  `json:"taskId,omitempty,string"`
}

type CreateInput struct {
	Label      string `json:"label"`
	TTLSeconds int    `json:"ttlSeconds"`
	RequestID  string `json:"requestId"`
}

type Probe struct {
	ID       string `json:"id"`
	Sequence int64  `json:"-"`
	Scope
	UserID      int        `json:"-"`
	Token       string     `json:"-"`
	RunID       string     `json:"runId,omitempty"`
	Label       string     `json:"label"`
	HTTPURL     string     `json:"httpUrl"`
	DNSName     string     `json:"dnsName,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
	EventCount  int        `json:"eventCount"`
	LastEventAt *time.Time `json:"lastEventAt,omitempty"`
	State       string     `json:"state"`
}

type Event struct {
	ID         int64     `json:"id,string"`
	ReceivedAt time.Time `json:"receivedAt"`
	Protocol   string    `json:"protocol"`
	RemoteAddr string    `json:"remoteAddr"`
	Method     string    `json:"method,omitempty"`
	Host       string    `json:"host,omitempty"`
	Path       string    `json:"path,omitempty"`
	Headers    string    `json:"headers,omitempty"`
	BodyBase64 string    `json:"bodyBase64,omitempty"`
	QueryName  string    `json:"queryName,omitempty"`
	QueryType  string    `json:"queryType,omitempty"`
	Truncated  bool      `json:"truncated"`
}

type ProbePage struct {
	Items      []Probe `json:"items"`
	NextBefore string  `json:"nextBefore"`
	Enabled    bool    `json:"enabled"`
	DNSEnabled bool    `json:"dnsEnabled"`
}

type EventPage struct {
	Probe     Probe   `json:"probe"`
	Items     []Event `json:"items"`
	NextAfter string  `json:"nextAfter"`
	HasMore   bool    `json:"hasMore"`
}
