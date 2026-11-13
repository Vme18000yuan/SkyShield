package application

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type FindingCaseSuggestion struct {
	ID        int64                   `json:"id,string"`
	TaskID    int64                   `json:"task_id,string"`
	TaskName  string                  `json:"task_name"`
	Title     string                  `json:"title"`
	Reason    string                  `json:"reason"`
	Inherited bool                    `json:"inherited"`
	CreatedAt time.Time               `json:"created_at"`
	Members   []TaskFindingCaseMember `json:"members"`
}

type FindingCaseSuggestionPage struct {
	Items []FindingCaseSuggestion `json:"items"`
	Total int64                   `json:"total"`
	Page  int                     `json:"page"`
	Size  int                     `json:"size"`
}

type FindingCaseSuggestionStore interface {
	ListFindingCaseSuggestions(context.Context, int, int64, int, int) (FindingCaseSuggestionPage, error)
	ResolveFindingCaseSuggestion(context.Context, int, int64, int64, bool, []int64) (int64, error)
}

type ReporterCaseSuggestionStore interface {
	CreateReporterFindingCaseSuggestion(context.Context, int, string, string, []int64, string, string) (int64, bool, error)
}

func (f *TaskFacade) FindingCaseSuggestions(ctx context.Context, user int, taskID int64, page, size int) (FindingCaseSuggestionPage, error) {
	if f == nil || user <= 0 || taskID <= 0 || page < 0 || page > 10000 || size < 1 || size > 100 {
		return FindingCaseSuggestionPage{}, ErrInvalidInput
	}
	store, ok := f.store.(FindingCaseSuggestionStore)
	if !ok {
		return FindingCaseSuggestionPage{}, ErrTaskNotFound
	}
	return store.ListFindingCaseSuggestions(ctx, user, taskID, page, size)
}

func (f *TaskFacade) ResolveCaseSuggestion(ctx context.Context, user int, taskID, suggestionID int64, accept bool, exclude []int64) (int64, error) {
	if f == nil || user <= 0 || taskID <= 0 || suggestionID <= 0 || len(exclude) > 20 || !accept && len(exclude) > 0 {
		return 0, ErrInvalidInput
	}
	ordered := append([]int64(nil), exclude...)
	seen := make(map[int64]bool, len(ordered))
	for _, id := range ordered {
		if id <= 0 || seen[id] {
			return 0, ErrInvalidInput
		}
		seen[id] = true
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	store, ok := f.store.(FindingCaseSuggestionStore)
	if !ok {
		return 0, ErrTaskNotFound
	}
	return store.ResolveFindingCaseSuggestion(ctx, user, taskID, suggestionID, accept, ordered)
}

func (f *TaskFacade) ReporterSuggestFindingCase(ctx context.Context, user int, run, toolUseID string, ids []int64, title, reason string) (int64, bool, error) {
	title, reason = strings.TrimSpace(title), strings.TrimSpace(reason)
	if f == nil || user <= 0 || !validConversationID(run) || toolUseID == "" || len(toolUseID) > 256 || !utf8.ValidString(toolUseID) || strings.ContainsRune(toolUseID, '\x00') || len(ids) < 2 || len(ids) > 20 || !validCaseText(title, 300) || !validCaseText(reason, 2000) {
		return 0, false, ErrInvalidInput
	}
	seen := make(map[int64]bool, len(ids))
	ordered := append([]int64(nil), ids...)
	for _, id := range ordered {
		if id <= 0 || seen[id] {
			return 0, false, ErrInvalidInput
		}
		seen[id] = true
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	store, ok := f.store.(ReporterCaseSuggestionStore)
	if !ok {
		return 0, false, ErrTaskNotFound
	}
	return store.CreateReporterFindingCaseSuggestion(ctx, user, run, toolUseID, ordered, title, reason)
}
