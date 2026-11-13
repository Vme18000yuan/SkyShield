package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrRetestUnavailable = errors.New("复测 Agent 或必需工具未启用")
var ErrRetestNotRunning = errors.New("复测未运行或结论已保存")

type FindingRetest struct {
	ID             int64      `json:"id,string"`
	FindingID      int64      `json:"finding_id,string"`
	ConversationID *string    `json:"conversation_id"`
	Status         string     `json:"status"`
	Verdict        string     `json:"verdict"`
	Notes          string     `json:"notes"`
	Summary        string     `json:"summary"`
	Evidence       string     `json:"evidence"`
	Error          string     `json:"error"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
}
type RetestFinding struct {
	ID     int64  `json:"id,string"`
	NodeID int64  `json:"node_id,string"`
	TaskID int64  `json:"task_id,string"`
	Name   string `json:"name"`
	Status string `json:"status"`
}
type FindingRetestContext struct {
	Retest             FindingRetest   `json:"retest"`
	Snapshot           json.RawMessage `json:"snapshot"`
	CurrentConstraints json.RawMessage `json:"current_constraints"`
}
type FindingRetestStore interface {
	CreateFindingRetest(context.Context, int, int64, string) (*FindingRetest, bool, error)
	ListFindingRetests(context.Context, int, int64) ([]FindingRetest, error)
	ListActiveFindingRetests(context.Context, int) ([]FindingRetest, error)
	ListVulnerabilityFindings(context.Context, int, int64) ([]RetestFinding, error)
	ReadRetestContext(context.Context, int, string) (*FindingRetestContext, error)
	RecordRetestResult(context.Context, int, string, string, string, string) error
}

func (f *TaskFacade) StartRetest(ctx context.Context, user int, findingID int64, notes string) (*FindingRetest, bool, error) {
	notes = strings.TrimSpace(notes)
	if user <= 0 || findingID <= 0 || utf8.RuneCountInString(notes) > 4000 || strings.ContainsRune(notes, '\x00') {
		return nil, false, ErrInvalidInput
	}
	store, ok := f.store.(FindingRetestStore)
	if !ok {
		return nil, false, ErrRetestUnavailable
	}
	return store.CreateFindingRetest(ctx, user, findingID, notes)
}
func (f *TaskFacade) Retests(ctx context.Context, user int, findingID int64) ([]FindingRetest, error) {
	if user <= 0 || findingID <= 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(FindingRetestStore)
	if !ok {
		return nil, ErrRetestUnavailable
	}
	return store.ListFindingRetests(ctx, user, findingID)
}
func (f *TaskFacade) VulnerabilityFindings(ctx context.Context, user int, id int64) ([]RetestFinding, error) {
	if user <= 0 || id <= 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(FindingRetestStore)
	if !ok {
		return nil, ErrRetestUnavailable
	}
	return store.ListVulnerabilityFindings(ctx, user, id)
}
func (f *TaskFacade) RetestContext(ctx context.Context, user int, runID string) (*FindingRetestContext, error) {
	if user <= 0 || !validConversationID(runID) {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(FindingRetestStore)
	if !ok {
		return nil, ErrRetestUnavailable
	}
	return store.ReadRetestContext(ctx, user, runID)
}
func (f *TaskFacade) SaveRetestResult(ctx context.Context, user int, runID, verdict, summary, evidence string) error {
	summary, evidence = strings.TrimSpace(summary), strings.TrimSpace(evidence)
	if user <= 0 || !validConversationID(runID) || (verdict != "reproduced" && verdict != "fixed" && verdict != "inconclusive") || summary == "" || evidence == "" || len(summary) > 16000 || len(evidence) > 128000 || !utf8.ValidString(summary) || !utf8.ValidString(evidence) || strings.ContainsRune(summary, '\x00') || strings.ContainsRune(evidence, '\x00') {
		return ErrInvalidInput
	}
	store, ok := f.store.(FindingRetestStore)
	if !ok {
		return ErrRetestUnavailable
	}
	return store.RecordRetestResult(ctx, user, runID, verdict, summary, evidence)
}

func (f *TaskFacade) ActiveRetests(ctx context.Context, user int) ([]FindingRetest, error) {
	if user <= 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(FindingRetestStore)
	if !ok {
		return nil, ErrRetestUnavailable
	}
	return store.ListActiveFindingRetests(ctx, user)
}
