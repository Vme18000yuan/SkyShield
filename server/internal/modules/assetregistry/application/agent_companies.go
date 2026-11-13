package application

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

type AgentCompany struct {
	ID             int      `json:"id"`
	Name           string   `json:"name"`
	AssetCount     int64    `json:"asset_count"`
	Scope          []string `json:"scope"`
	ScopeTruncated bool     `json:"scope_truncated,omitempty"`
}
type AgentCompanyPage struct {
	Companies []AgentCompany `json:"companies"`
	Count     int            `json:"count"`
	Truncated bool           `json:"truncated"`
}
type AgentCompanyStore interface {
	ListAgentCompanies(context.Context, int, int64, string, string) (AgentCompanyPage, error)
}

// AgentCompanies resolves organization names and inventory counts under a live
// conversation or task lease. The task's target limits both scope and count.
func (f *Facade) AgentCompanies(ctx context.Context, user int, taskID int64, run, search string) (AgentCompanyPage, error) {
	if f == nil || f.store == nil || user <= 0 || taskID < 0 {
		return AgentCompanyPage{}, ErrInvalidQuery
	}
	id, err := uuid.Parse(run)
	search = strings.TrimSpace(search)
	if err != nil || id.String() != run || len(search) > 800 || utf8.RuneCountInString(search) > 200 || !utf8.ValidString(search) || strings.ContainsRune(search, '\x00') {
		return AgentCompanyPage{}, ErrInvalidQuery
	}
	store, ok := f.store.(AgentCompanyStore)
	if !ok {
		return AgentCompanyPage{}, ErrForbidden
	}
	return store.ListAgentCompanies(ctx, user, taskID, run, search)
}
