package application

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrFindingCaseReviewConclusion = errors.New("审查结论未通过验收")

type FindingCaseReview struct {
	ID             int64                   `json:"id,string"`
	TaskID         int64                   `json:"task_id,string"`
	ConversationID *string                 `json:"conversation_id,omitempty"`
	State          string                  `json:"state"`
	Error          string                  `json:"error"`
	FindingCount   int                     `json:"finding_count"`
	FindingIDs     []string                `json:"finding_ids,omitempty"`
	Findings       []TaskFindingCaseMember `json:"findings,omitempty"`
	Conclusion     json.RawMessage         `json:"conclusion,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
	StartedAt      *time.Time              `json:"started_at,omitempty"`
	FinishedAt     *time.Time              `json:"finished_at,omitempty"`
}

type FindingCaseReviewPage struct {
	Items []FindingCaseReview `json:"items"`
	Total int64               `json:"total"`
	Page  int                 `json:"page"`
	Size  int                 `json:"size"`
}

type FindingCaseReviewBatch struct {
	TaskID     int64
	FindingIDs []int64
}

type FindingCaseReviewDecision struct {
	FindingID         int64  `json:"finding_id,string"`
	Verdict           string `json:"verdict"`
	Reason            string `json:"reason"`
	RootCauseEvidence string `json:"root_cause_evidence"`
}

type FindingCaseReviewConclusion struct {
	Summary   string                      `json:"summary"`
	Decisions []FindingCaseReviewDecision `json:"decisions"`
}

type FindingCaseReviewStore interface {
	CreateFindingCaseReview(context.Context, int, int64, []int64) (*FindingCaseReview, error)
	ListFindingCaseReviews(context.Context, int, int64, int64, int, int) (FindingCaseReviewPage, error)
	CancelQueuedFindingCaseReview(context.Context, int, int64, int64) error
}

func (f *TaskFacade) CancelQueuedCaseReview(ctx context.Context, user int, taskID, reviewID int64) error {
	if f == nil || user <= 0 || taskID <= 0 || reviewID <= 0 {
		return ErrInvalidInput
	}
	store, ok := f.store.(FindingCaseReviewStore)
	if !ok {
		return ErrTaskNotFound
	}
	return store.CancelQueuedFindingCaseReview(ctx, user, taskID, reviewID)
}

type FindingCaseReviewConclusionStore interface {
	CompleteFindingCaseReview(context.Context, int, string, string, FindingCaseReviewConclusion) (bool, error)
}

type FindingCaseReviewBatchStore interface {
	CreateFindingCaseReviewBatch(context.Context, int, []FindingCaseReviewBatch) ([]FindingCaseReview, error)
}

func (f *TaskFacade) CreateCaseReviewBatch(ctx context.Context, user int, batches []FindingCaseReviewBatch) ([]FindingCaseReview, error) {
	if f == nil || user <= 0 || len(batches) < 1 || len(batches) > 20 {
		return nil, ErrInvalidInput
	}
	ordered := make([]FindingCaseReviewBatch, 0, len(batches))
	tasks, findings := map[int64]bool{}, map[int64]bool{}
	for _, batch := range batches {
		if batch.TaskID <= 0 || tasks[batch.TaskID] || len(batch.FindingIDs) < 1 || len(batch.FindingIDs) > 200 {
			return nil, ErrInvalidInput
		}
		tasks[batch.TaskID] = true
		item := FindingCaseReviewBatch{TaskID: batch.TaskID, FindingIDs: append([]int64(nil), batch.FindingIDs...)}
		for _, id := range item.FindingIDs {
			if id <= 0 || findings[id] {
				return nil, ErrInvalidInput
			}
			findings[id] = true
		}
		sort.Slice(item.FindingIDs, func(i, j int) bool { return item.FindingIDs[i] < item.FindingIDs[j] })
		ordered = append(ordered, item)
	}
	if len(findings) > 200 {
		return nil, ErrInvalidInput
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].TaskID < ordered[j].TaskID })
	store, ok := f.store.(FindingCaseReviewBatchStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.CreateFindingCaseReviewBatch(ctx, user, ordered)
}

func (f *TaskFacade) CreateCaseReview(ctx context.Context, user int, taskID int64, ids []int64) (*FindingCaseReview, error) {
	if f == nil || user <= 0 || taskID <= 0 || len(ids) < 1 || len(ids) > 200 {
		return nil, ErrInvalidInput
	}
	seen := make(map[int64]bool, len(ids))
	ordered := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrInvalidInput
		}
		if !seen[id] {
			seen[id] = true
			ordered = append(ordered, id)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	store, ok := f.store.(FindingCaseReviewStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.CreateFindingCaseReview(ctx, user, taskID, ordered)
}

func (f *TaskFacade) CaseReviews(ctx context.Context, user int, taskID, reviewID int64, page, size int) (FindingCaseReviewPage, error) {
	if f == nil || user <= 0 || taskID <= 0 || reviewID < 0 || page < 0 || page > 10000 || size < 1 || size > 100 {
		return FindingCaseReviewPage{}, ErrInvalidInput
	}
	store, ok := f.store.(FindingCaseReviewStore)
	if !ok {
		return FindingCaseReviewPage{}, ErrTaskNotFound
	}
	return store.ListFindingCaseReviews(ctx, user, taskID, reviewID, page, size)
}

func (f *TaskFacade) CompleteCaseReview(ctx context.Context, user int, run, toolUseID string, conclusion FindingCaseReviewConclusion) (bool, error) {
	conclusion.Summary = strings.TrimSpace(conclusion.Summary)
	if f == nil || user <= 0 || !validConversationID(run) || toolUseID == "" || len(toolUseID) > 256 || !utf8.ValidString(toolUseID) || strings.ContainsRune(toolUseID, '\x00') || !validCaseText(conclusion.Summary, 4000) || len(conclusion.Decisions) < 1 || len(conclusion.Decisions) > 200 {
		return false, ErrInvalidInput
	}
	conclusion.Decisions = append([]FindingCaseReviewDecision(nil), conclusion.Decisions...)
	seen := map[int64]bool{}
	for index := range conclusion.Decisions {
		item := &conclusion.Decisions[index]
		item.Reason, item.RootCauseEvidence = strings.TrimSpace(item.Reason), strings.TrimSpace(item.RootCauseEvidence)
		if item.FindingID <= 0 || seen[item.FindingID] || !validCaseText(item.Reason, 2000) || !validCaseText(item.RootCauseEvidence, 8000) {
			return false, ErrInvalidInput
		}
		seen[item.FindingID] = true
		switch item.Verdict {
		case "independent", "merged", "suggested":
		default:
			return false, ErrInvalidInput
		}
	}
	sort.Slice(conclusion.Decisions, func(i, j int) bool { return conclusion.Decisions[i].FindingID < conclusion.Decisions[j].FindingID })
	store, ok := f.store.(FindingCaseReviewConclusionStore)
	if !ok {
		return false, ErrTaskNotFound
	}
	return store.CompleteFindingCaseReview(ctx, user, run, toolUseID, conclusion)
}
