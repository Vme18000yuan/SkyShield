package application

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	artexdb "github.com/Autumn-27/artex/db"
	contractresults "github.com/yyhuni/lunafox/contracts/results"
	"golang.org/x/net/publicsuffix"
)

type TaskScope struct {
	ID             int64     `json:"id,string"`
	TaskID         int64     `json:"task_id,string"`
	Kind           string    `json:"kind"`
	OrganizationID *int      `json:"company_id,omitempty"`
	CompanyName    string    `json:"company_name,omitempty"`
	Domain         string    `json:"domain,omitempty"`
	Net            string    `json:"net,omitempty"`
	Value          string    `json:"value,omitempty"`
	Source         string    `json:"source"`
	Reason         string    `json:"reason,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	Inherited      bool      `json:"inherited,omitempty"`
	SourceTaskID   int64     `json:"source_task_id,omitempty,string"`
	Key            string    `json:"-"`
}

type TaskScopeInput struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

type TaskScopeStore interface {
	ListTaskScopes(context.Context, int, int64) ([]TaskScope, error)
	AddTaskScope(context.Context, int, int64, string, TaskScope) (TaskScope, error)
	DeleteTaskScope(context.Context, int, int64, int64) error
	TaskCoverage(context.Context, int, int64) (TaskCoverage, error)
}

type TaskCoverageByType struct {
	Type   string `json:"type"`
	Total  int    `json:"total"`
	Tested int    `json:"tested"`
}

type TaskCoverage struct {
	Enabled     bool                 `json:"enabled"`
	ScopeRows   int                  `json:"scope_rows"`
	Denominator int                  `json:"denominator"`
	Tested      int                  `json:"tested"`
	Pct         *float64             `json:"pct"`
	ByType      []TaskCoverageByType `json:"by_type"`
}

func normalizeTaskScope(input TaskScopeInput) (TaskScope, error) {
	item := TaskScope{Kind: strings.ToLower(strings.TrimSpace(input.Kind)), Reason: strings.TrimSpace(input.Reason)}
	raw := strings.TrimSpace(input.Value)
	if raw == "" || len(raw) > 8192 || len(item.Reason) > 16000 || !utf8.ValidString(raw+item.Reason) || strings.ContainsRune(raw+item.Reason, '\x00') {
		return TaskScope{}, ErrInvalidInput
	}
	switch item.Kind {
	case "company":
		item.Value = raw // resolved to the existing organization in the repository
	case "root_domain", "subdomain":
		host := raw
		if strings.Contains(host, "://") {
			parsed, err := url.Parse(host)
			if err != nil || parsed.Hostname() == "" {
				return TaskScope{}, ErrInvalidInput
			}
			host = parsed.Hostname()
		} else if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		domain, ok := contractresults.NormalizeSubdomainDNSName(host)
		if !ok {
			return TaskScope{}, ErrInvalidInput
		}
		if item.Kind == "root_domain" {
			root, err := publicsuffix.EffectiveTLDPlusOne(domain)
			if err != nil {
				return TaskScope{}, ErrInvalidInput
			}
			domain = root
		}
		item.Domain, item.Key = domain, domain
	case "ip", "cidr":
		if strings.Contains(raw, "/") {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil {
				return TaskScope{}, ErrInvalidInput
			}
			item.Kind, item.Net = "cidr", prefix.Masked().String()
		} else {
			address := raw
			if host, _, err := net.SplitHostPort(address); err == nil {
				address = host
			}
			addr, err := netip.ParseAddr(strings.Trim(address, "[]"))
			if err != nil {
				return TaskScope{}, ErrInvalidInput
			}
			addr = addr.Unmap()
			item.Kind, item.Net = "ip", netip.PrefixFrom(addr, addr.BitLen()).String()
		}
		item.Key = item.Net
	case "icp", "keyword":
		parsed, err := artexdb.ParseScopeInput(artexdb.ScopeInput{Kind: item.Kind, Value: raw})
		if err != nil {
			return TaskScope{}, ErrInvalidInput
		}
		item.Value, item.Key = parsed.Value, parsed.Value
	default:
		return TaskScope{}, ErrInvalidInput
	}
	return item, nil
}

func (f *TaskFacade) Scopes(ctx context.Context, user int, taskID int64) ([]TaskScope, error) {
	if user <= 0 || taskID <= 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(TaskScopeStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.ListTaskScopes(ctx, user, taskID)
}
func (f *TaskFacade) Coverage(ctx context.Context, user int, taskID int64) (TaskCoverage, error) {
	if user <= 0 || taskID <= 0 {
		return TaskCoverage{}, ErrInvalidInput
	}
	store, ok := f.store.(TaskScopeStore)
	if !ok {
		return TaskCoverage{}, ErrTaskNotFound
	}
	return store.TaskCoverage(ctx, user, taskID)
}
func (f *TaskFacade) AddScope(ctx context.Context, user int, taskID int64, input TaskScopeInput) (TaskScope, error) {
	return f.addScope(ctx, user, taskID, "", input)
}
func (f *TaskFacade) AddAgentScope(ctx context.Context, user int, taskID int64, runID string, input TaskScopeInput) (TaskScope, error) {
	if !validConversationID(runID) {
		return TaskScope{}, ErrInvalidInput
	}
	return f.addScope(ctx, user, taskID, runID, input)
}
func (f *TaskFacade) addScope(ctx context.Context, user int, taskID int64, runID string, input TaskScopeInput) (TaskScope, error) {
	if user <= 0 || taskID <= 0 {
		return TaskScope{}, ErrInvalidInput
	}
	item, err := normalizeTaskScope(input)
	if err != nil {
		return TaskScope{}, err
	}
	store, ok := f.store.(TaskScopeStore)
	if !ok {
		return TaskScope{}, ErrTaskNotFound
	}
	item, err = store.AddTaskScope(ctx, user, taskID, runID, item)
	if err == nil && f.controller != nil {
		f.controller.Notify()
	}
	return item, err
}
func (f *TaskFacade) DeleteScope(ctx context.Context, user int, taskID, id int64) error {
	if user <= 0 || taskID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	store, ok := f.store.(TaskScopeStore)
	if !ok {
		return ErrTaskNotFound
	}
	return store.DeleteTaskScope(ctx, user, taskID, id)
}
