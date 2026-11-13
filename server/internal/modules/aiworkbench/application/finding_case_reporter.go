package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

var ErrIncompleteFindingCaseReport = errors.New("统一报告不完整")

// ReporterCaseScopeStore binds case reads to the currently leased Reporter job
// and its source task, rather than allowing an Agent to choose another task.
type ReporterCaseScopeStore interface {
	ReporterCaseScope(context.Context, int, string, int64, int64) (int64, error)
	ReporterReviewSelection(context.Context, int, string) ([]int64, bool, error)
	MarkReporterReviewRead(context.Context, int, string, int64, string) error
}

type ReporterCaseReportStore interface {
	UpdateReporterFindingCaseReport(context.Context, int, string, string, int64, FindingCaseReport) (bool, error)
}

type ReporterCaseMergeStore interface {
	MergeReporterFindingCase(context.Context, int, string, string, int64, []int64, string, string) (int64, bool, error)
}

func (f *TaskFacade) reporterCaseScope(ctx context.Context, user int, run string, findingID, caseID int64) (ReporterCaseScopeStore, int64, error) {
	if f == nil || user <= 0 || !validConversationID(run) || (findingID > 0) == (caseID > 0) || findingID < 0 || caseID < 0 {
		return nil, 0, ErrInvalidInput
	}
	store, ok := f.store.(ReporterCaseScopeStore)
	if !ok {
		return nil, 0, ErrTaskNotFound
	}
	taskID, err := store.ReporterCaseScope(ctx, user, run, findingID, caseID)
	return store, taskID, err
}

func (f *TaskFacade) ReporterFindingDuplicates(ctx context.Context, user int, run string, findingID int64, page, size int) (FindingDuplicatePage, error) {
	store, taskID, err := f.reporterCaseScope(ctx, user, run, findingID, 0)
	if err != nil {
		return FindingDuplicatePage{}, err
	}
	selection, review, err := store.ReporterReviewSelection(ctx, user, run)
	if err != nil {
		return FindingDuplicatePage{}, err
	}
	if page < 0 || page > 10000 || size < 1 || size > 100 {
		return FindingDuplicatePage{}, ErrInvalidInput
	}
	candidates, ok := f.store.(FindingDuplicateStore)
	if !ok {
		return FindingDuplicatePage{}, ErrTaskNotFound
	}
	var allowed []int64
	if review {
		allowed = selection
	}
	result, err := candidates.ListFindingDuplicateCandidates(ctx, user, taskID, findingID, page, size, allowed)
	if err != nil {
		return FindingDuplicatePage{}, err
	}
	current, err := store.ReporterCaseScope(ctx, user, run, findingID, 0)
	if err != nil || current != taskID {
		if err != nil {
			return FindingDuplicatePage{}, err
		}
		return FindingDuplicatePage{}, ErrRunLeaseLost
	}
	return result, nil
}

func (f *TaskFacade) ReporterFindingRecord(ctx context.Context, user int, run string, findingID int64) (*TaskFinding, error) {
	store, taskID, err := f.reporterCaseScope(ctx, user, run, findingID, 0)
	if err != nil {
		return nil, err
	}
	result, err := f.Findings(ctx, user, taskID, TaskFindingFilter{ID: findingID, Page: 0, Size: 1, OwnOnly: true}, true)
	if err != nil {
		return nil, err
	}
	if len(result.Items) != 1 {
		return nil, ErrTaskNotFound
	}
	if err = store.MarkReporterReviewRead(ctx, user, run, findingID, result.Items[0].Fingerprint); err != nil {
		return nil, err
	}
	current, err := store.ReporterCaseScope(ctx, user, run, findingID, 0)
	if err != nil || current != taskID {
		if err != nil {
			return nil, err
		}
		return nil, ErrRunLeaseLost
	}
	return &result.Items[0], nil
}

func (f *TaskFacade) ReporterFindingCase(ctx context.Context, user int, run string, caseID int64) (*TaskFindingCase, error) {
	store, taskID, err := f.reporterCaseScope(ctx, user, run, 0, caseID)
	if err != nil {
		return nil, err
	}
	result, err := f.FindingCases(ctx, user, taskID, caseID, 0, 1)
	if err != nil {
		return nil, err
	}
	if len(result.Items) != 1 || result.Items[0].Inherited {
		return nil, ErrTaskNotFound
	}
	current, err := store.ReporterCaseScope(ctx, user, run, 0, caseID)
	if err != nil || current != taskID {
		if err != nil {
			return nil, err
		}
		return nil, ErrRunLeaseLost
	}
	return &result.Items[0], nil
}

// Existing evidence tools remain usable for normal runs. For a historical
// review they may read only the selected findings while the Reporter lease is
// still active.
func (f *TaskFacade) EnsureFindingReviewRead(ctx context.Context, user int, run string, findingID int64) error {
	if run == "" {
		return nil
	}
	if f == nil || user <= 0 || !validConversationID(run) || findingID <= 0 {
		return ErrInvalidInput
	}
	store, ok := f.store.(ReporterCaseScopeStore)
	if !ok {
		return ErrTaskNotFound
	}
	selection, review, err := store.ReporterReviewSelection(ctx, user, run)
	if err != nil || !review {
		return err
	}
	if !slices.Contains(selection, findingID) {
		return ErrRunLeaseLost
	}
	_, err = store.ReporterCaseScope(ctx, user, run, findingID, 0)
	return err
}

func (f *TaskFacade) SaveReporterFindingCaseReport(ctx context.Context, user int, run, toolUseID string, caseID int64, input FindingCaseReport) (bool, error) {
	if f == nil || user <= 0 || !validConversationID(run) || caseID <= 0 || toolUseID == "" || len(toolUseID) > 256 || !utf8.ValidString(toolUseID) || strings.ContainsRune(toolUseID, '\x00') || !normalizeFindingCaseReport(&input) {
		return false, ErrInvalidInput
	}
	if err := validateUnifiedFindingCaseReport(input.Report); err != nil {
		return false, err
	}
	store, ok := f.store.(ReporterCaseReportStore)
	if !ok {
		return false, ErrTaskNotFound
	}
	return store.UpdateReporterFindingCaseReport(ctx, user, run, toolUseID, caseID, input)
}

func (f *TaskFacade) ReporterMergeFindingCase(ctx context.Context, user int, run, toolUseID string, taskID int64, ids []int64, title, reason string) (int64, bool, error) {
	title, reason = strings.TrimSpace(title), strings.TrimSpace(reason)
	if f == nil || user <= 0 || !validConversationID(run) || toolUseID == "" || len(toolUseID) > 256 || !utf8.ValidString(toolUseID) || strings.ContainsRune(toolUseID, '\x00') || taskID <= 0 || len(ids) < 2 || len(ids) > 100 || !validCaseText(title, 300) || !validCaseText(reason, 2000) {
		return 0, false, ErrInvalidInput
	}
	seen := make(map[int64]bool, len(ids))
	ordered := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return 0, false, ErrInvalidInput
		}
		if !seen[id] {
			seen[id] = true
			ordered = append(ordered, id)
		}
	}
	if len(ordered) < 2 {
		return 0, false, ErrInvalidInput
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	store, ok := f.store.(ReporterCaseMergeStore)
	if !ok {
		return 0, false, ErrTaskNotFound
	}
	return store.MergeReporterFindingCase(ctx, user, run, toolUseID, taskID, ordered, title, reason)
}

// Reporter-generated unified reports must contain the same substantive
// sections as ARTEX. Human edits can be saved independently of this rule.
func validateUnifiedFindingCaseReport(report string) error {
	aliases := [][]string{{"概述", "overview", "summary"}, {"影响", "impact"}, {"前置", "prerequisite", "precondition"}, {"复现", "reproduction", "reproduce"}, {"证据", "evidence"}, {"poc", "完整脚本", "验证脚本", "proof of concept"}, {"根因", "root cause"}, {"修复", "remediation", "recommendation", "fix"}}
	names := []string{"概述", "影响", "前置条件", "复现步骤", "证据", "PoC", "根因分析", "修复建议"}
	found := make([]bool, len(aliases))
	current, hasContent, fence := -1, false, ""
	finish := func() {
		if current >= 0 && hasContent {
			found[current] = true
		}
	}
	for _, line := range strings.Split(report, "\n") {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "```") || strings.HasPrefix(text, "~~~") {
			marker := text[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence != "" {
			if text != "" {
				hasContent = true
			}
			continue
		}
		if strings.HasPrefix(text, "#") {
			heading := strings.TrimLeft(text, "#")
			if len(heading) == len(text) || !strings.HasPrefix(heading, " ") {
				continue
			}
			next, lower := -1, strings.ToLower(heading)
			for i, words := range aliases {
				for _, word := range words {
					if strings.Contains(lower, word) {
						next = i
						break
					}
				}
				if next >= 0 {
					break
				}
			}
			if next >= 0 {
				finish()
				current, hasContent = next, false
			}
			continue
		}
		if text != "" && text != "---" && text != "***" && text != ">" {
			hasContent = true
		}
	}
	finish()
	if fence != "" {
		return fmt.Errorf("%w：代码块未闭合", ErrIncompleteFindingCaseReport)
	}
	missing := []string{}
	for i, ok := range found {
		if !ok {
			missing = append(missing, names[i])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w：缺少有内容的章节：%s", ErrIncompleteFindingCaseReport, strings.Join(missing, "、"))
	}
	return nil
}
