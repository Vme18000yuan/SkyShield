package application

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type FindingReviewCandidate struct {
	ID         int64     `json:"id,string"`
	TaskID     int64     `json:"task_id,string"`
	TaskName   string    `json:"task_name"`
	TargetName string    `json:"target_name"`
	Name       string    `json:"name"`
	VulnClass  string    `json:"vulnclass"`
	Severity   string    `json:"severity"`
	Status     string    `json:"status"`
	Summary    string    `json:"summary"`
	InGroup    bool      `json:"in_group"`
	CaseID     *int64    `json:"case_id,omitempty,string"`
	CreatedAt  time.Time `json:"created_at"`
}

type FindingReviewCandidatePage struct {
	Items []FindingReviewCandidate `json:"items"`
	Total int64                    `json:"total"`
	Page  int                      `json:"page"`
	Size  int                      `json:"size"`
}

type FindingReviewCandidateStore interface {
	ListFindingReviewCandidates(context.Context, int, string, string, int, int) (FindingReviewCandidatePage, error)
}

func (f *TaskFacade) FindingReviewCandidates(ctx context.Context, user int, query, severity string, page, size int) (FindingReviewCandidatePage, error) {
	query, severity = strings.TrimSpace(query), strings.TrimSpace(severity)
	if f == nil || user <= 0 || page < 0 || page > 10000 || size < 1 || size > 100 || len(query) > 500 || !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') {
		return FindingReviewCandidatePage{}, ErrInvalidInput
	}
	if severity != "" && severity != "critical" && severity != "high" && severity != "medium" && severity != "low" && severity != "info" {
		return FindingReviewCandidatePage{}, ErrInvalidInput
	}
	store, ok := f.store.(FindingReviewCandidateStore)
	if !ok {
		return FindingReviewCandidatePage{}, ErrTaskNotFound
	}
	return store.ListFindingReviewCandidates(ctx, user, query, severity, page, size)
}
