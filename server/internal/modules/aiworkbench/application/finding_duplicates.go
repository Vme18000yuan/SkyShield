package application

import "context"

// FindingDuplicateCandidate is a retrieval hint, not a determination that two
// findings share a root cause. Human decisions remain authoritative.
type FindingDuplicateCandidate struct {
	ID         int64              `json:"id,string"`
	Name       string             `json:"name"`
	VulnClass  string             `json:"vulnclass"`
	Severity   string             `json:"severity"`
	Summary    string             `json:"summary"`
	CaseID     *int64             `json:"case_id,omitempty,string"`
	Score      int                `json:"score"`
	Tier       string             `json:"tier"`
	HitReasons []string           `json:"hit_reasons"`
	Assets     []TaskFindingAsset `json:"assets"`
}

type FindingDuplicatePage struct {
	Items       []FindingDuplicateCandidate `json:"items"`
	Total       int64                       `json:"total"`
	StrongTotal int64                       `json:"strong_total"`
	Page        int                         `json:"page"`
	Size        int                         `json:"size"`
}

type FindingDuplicateStore interface {
	ListFindingDuplicateCandidates(context.Context, int, int64, int64, int, int, []int64) (FindingDuplicatePage, error)
}

func (f *TaskFacade) FindingDuplicates(ctx context.Context, user int, taskID, findingID int64, page, size int) (FindingDuplicatePage, error) {
	if f == nil || user <= 0 || taskID <= 0 || findingID <= 0 || page < 0 || page > 10000 || size < 1 || size > 100 {
		return FindingDuplicatePage{}, ErrInvalidInput
	}
	store, ok := f.store.(FindingDuplicateStore)
	if !ok {
		return FindingDuplicatePage{}, ErrTaskNotFound
	}
	return store.ListFindingDuplicateCandidates(ctx, user, taskID, findingID, page, size, nil)
}
