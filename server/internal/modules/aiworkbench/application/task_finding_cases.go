package application

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// TaskFindingCase preserves the original findings while presenting a group as
// one item. A report is current only when ReportVersion equals Version.
type TaskFindingCase struct {
	ID              int64                   `json:"id,string"`
	TaskID          int64                   `json:"task_id,string"`
	TaskName        string                  `json:"task_name"`
	Title           string                  `json:"title"`
	Reason          string                  `json:"reason"`
	Severity        string                  `json:"severity"`
	SeverityReason  string                  `json:"severity_reason,omitempty"`
	Report          string                  `json:"report,omitempty"`
	Version         int64                   `json:"version"`
	ReportVersion   int64                   `json:"report_version"`
	HasReport       bool                    `json:"has_report"`
	MemberCount     int                     `json:"member_count"`
	Inherited       bool                    `json:"inherited"`
	CreatedAt       time.Time               `json:"created_at"`
	Members         []TaskFindingCaseMember `json:"members,omitempty"`
	Events          []TaskFindingCaseEvent  `json:"events,omitempty"`
	EventsTruncated bool                    `json:"events_truncated,omitempty"`
}

type TaskFindingCaseMember struct {
	ID        int64  `json:"id,string"`
	Name      string `json:"name"`
	VulnClass string `json:"vulnclass"`
	Severity  string `json:"severity"`
	Status    string `json:"status"`
	Summary   string `json:"summary"`
}

type TaskFindingCaseEvent struct {
	ID         int64     `json:"id,string"`
	Action     string    `json:"action"`
	Actor      string    `json:"actor"`
	Reason     string    `json:"reason"`
	FindingIDs []string  `json:"finding_ids"`
	CreatedAt  time.Time `json:"created_at"`
}

type TaskFindingCasePage struct {
	Items []TaskFindingCase `json:"items"`
	Total int64             `json:"total"`
	Page  int               `json:"page"`
	Size  int               `json:"size"`
}

type TaskFindingCaseFilter struct {
	CaseID          int64
	Severity, Query string
	Page, Size      int
}

type FindingCaseReport struct {
	Version        int64  `json:"version"`
	Title          string `json:"title"`
	Severity       string `json:"severity"`
	SeverityReason string `json:"severity_reason"`
	Report         string `json:"report"`
}

type TaskFindingCaseStore interface {
	ListTaskFindingCases(context.Context, int, int64, TaskFindingCaseFilter) (TaskFindingCasePage, error)
	MergeTaskFindingCase(context.Context, int, int64, []int64, string, string) (int64, error)
	DetachTaskFindingCaseMember(context.Context, int, int64, int64, int64, string) error
	UpdateTaskFindingCaseReport(context.Context, int, int64, int64, FindingCaseReport) error
}

func (f *TaskFacade) FindingCases(ctx context.Context, user int, taskID, caseID int64, page, size int) (TaskFindingCasePage, error) {
	return f.FindingCasesFiltered(ctx, user, taskID, TaskFindingCaseFilter{CaseID: caseID, Page: page, Size: size})
}

func (f *TaskFacade) FindingCasesFiltered(ctx context.Context, user int, taskID int64, filter TaskFindingCaseFilter) (TaskFindingCasePage, error) {
	if f == nil || user <= 0 || taskID <= 0 || filter.CaseID < 0 || filter.Page < 0 || filter.Page > 10000 || filter.Size < 1 || filter.Size > 100 || len(filter.Query) > 500 || !utf8.ValidString(filter.Query) || strings.ContainsRune(filter.Query, '\x00') {
		return TaskFindingCasePage{}, ErrInvalidInput
	}
	if filter.Severity != "" && filter.Severity != "critical" && filter.Severity != "high" && filter.Severity != "medium" && filter.Severity != "low" && filter.Severity != "info" {
		return TaskFindingCasePage{}, ErrInvalidInput
	}
	store, ok := f.store.(TaskFindingCaseStore)
	if !ok {
		return TaskFindingCasePage{}, ErrTaskNotFound
	}
	return store.ListTaskFindingCases(ctx, user, taskID, filter)
}

func validCaseText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max*4 && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && !strings.ContainsRune(value, '\x00')
}

func (f *TaskFacade) MergeFindingCase(ctx context.Context, user int, taskID int64, ids []int64, title, reason string) (int64, error) {
	title, reason = strings.TrimSpace(title), strings.TrimSpace(reason)
	if f == nil || user <= 0 || taskID <= 0 || len(ids) < 2 || len(ids) > 100 || !validCaseText(title, 300) || !validCaseText(reason, 2000) {
		return 0, ErrInvalidInput
	}
	unique := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return 0, ErrInvalidInput
		}
		unique[id] = struct{}{}
	}
	if len(unique) < 2 {
		return 0, ErrInvalidInput
	}
	ordered := make([]int64, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	store, ok := f.store.(TaskFindingCaseStore)
	if !ok {
		return 0, ErrTaskNotFound
	}
	return store.MergeTaskFindingCase(ctx, user, taskID, ordered, title, reason)
}

func (f *TaskFacade) DetachFindingCaseMember(ctx context.Context, user int, taskID, caseID, findingID int64, reason string) error {
	reason = strings.TrimSpace(reason)
	if f == nil || user <= 0 || taskID <= 0 || caseID <= 0 || findingID <= 0 || !validCaseText(reason, 2000) {
		return ErrInvalidInput
	}
	store, ok := f.store.(TaskFindingCaseStore)
	if !ok {
		return ErrTaskNotFound
	}
	return store.DetachTaskFindingCaseMember(ctx, user, taskID, caseID, findingID, reason)
}

func (f *TaskFacade) SaveFindingCaseReport(ctx context.Context, user int, taskID, caseID int64, input FindingCaseReport) error {
	if f == nil || user <= 0 || taskID <= 0 || caseID <= 0 || !normalizeFindingCaseReport(&input) {
		return ErrInvalidInput
	}
	store, ok := f.store.(TaskFindingCaseStore)
	if !ok {
		return ErrTaskNotFound
	}
	return store.UpdateTaskFindingCaseReport(ctx, user, taskID, caseID, input)
}

func normalizeFindingCaseReport(input *FindingCaseReport) bool {
	input.Title, input.SeverityReason, input.Report = strings.TrimSpace(input.Title), strings.TrimSpace(input.SeverityReason), strings.TrimSpace(input.Report)
	if input.Version <= 0 || !validCaseText(input.Title, 300) || !validCaseText(input.SeverityReason, 2000) || !validCaseText(input.Report, 200000) {
		return false
	}
	switch input.Severity {
	case "critical", "high", "medium", "low":
		return true
	default:
		return false
	}
}
