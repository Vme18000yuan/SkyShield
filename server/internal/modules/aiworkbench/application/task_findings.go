package application

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type TaskFindingAsset struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
}
type TaskFinding struct {
	ID                    int64              `json:"id,string"`
	TaskID                int64              `json:"task_id,string"`
	NodeID                *int64             `json:"node_id,omitempty,string"`
	TaskName              string             `json:"task_name"`
	Name                  string             `json:"name"`
	VulnClass             string             `json:"vulnclass"`
	Severity              string             `json:"severity"`
	Status                string             `json:"status"`
	Summary               string             `json:"summary"`
	Evidence              string             `json:"evidence,omitempty"`
	Report                string             `json:"report,omitempty"`
	EvidenceVersion       int64              `json:"evidence_version"`
	ReportEvidenceVersion int64              `json:"report_evidence_version"`
	HasReport             bool               `json:"has_report"`
	TrafficCount          int                `json:"traffic_count"`
	RiskID                *int64             `json:"risk_id,omitempty,string"`
	CaseID                *int64             `json:"case_id,omitempty,string"`
	Assets                []TaskFindingAsset `json:"assets"`
	Inherited             bool               `json:"inherited"`
	CreatedAt             time.Time          `json:"created_at"`
	Fingerprint           string             `json:"-"`
}
type TaskFindingFilter struct {
	ID              int64
	Query, Severity string
	Page, Size      int
	OwnOnly         bool
}
type TaskFindingPage struct {
	Items []TaskFinding `json:"items"`
	Total int64         `json:"total"`
	Page  int           `json:"page"`
	Size  int           `json:"size"`
}
type TaskFindingStore interface {
	ListTaskFindings(context.Context, int, int64, TaskFindingFilter, bool) (TaskFindingPage, error)
	UpdateTaskFindingStatus(context.Context, int, int64, int64, string) error
}

func (f *TaskFacade) Findings(ctx context.Context, user int, taskID int64, filter TaskFindingFilter, detail bool) (TaskFindingPage, error) {
	if f == nil || user <= 0 || taskID <= 0 || filter.ID < 0 || filter.Page < 0 || filter.Page > 10000 || filter.Size < 1 || filter.Size > 100 || len(filter.Query) > 500 || !utf8.ValidString(filter.Query) || strings.ContainsRune(filter.Query, '\x00') {
		return TaskFindingPage{}, ErrInvalidInput
	}
	if filter.Severity != "" && filter.Severity != "critical" && filter.Severity != "high" && filter.Severity != "medium" && filter.Severity != "low" && filter.Severity != "info" {
		return TaskFindingPage{}, ErrInvalidInput
	}
	store, ok := f.store.(TaskFindingStore)
	if !ok {
		return TaskFindingPage{}, ErrTaskNotFound
	}
	return store.ListTaskFindings(ctx, user, taskID, filter, detail)
}
func (f *TaskFacade) SetFindingStatus(ctx context.Context, user int, taskID, findingID int64, status string) error {
	if f == nil || user <= 0 || taskID <= 0 || findingID <= 0 {
		return ErrInvalidInput
	}
	switch status {
	case "pending", "in_progress", "confirmed", "resolved", "fixed", "false_positive", "ignored", "duplicate", "risk_accepted":
	default:
		return ErrInvalidInput
	}
	store, ok := f.store.(TaskFindingStore)
	if !ok {
		return ErrTaskNotFound
	}
	return store.UpdateTaskFindingStatus(ctx, user, taskID, findingID, status)
}
