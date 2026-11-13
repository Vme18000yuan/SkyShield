package application

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrEvidenceVersionConflict = errors.New("漏洞证据已更新，请重新读取后再生成报告")

func (f *TaskFacade) FindingTraffic(ctx context.Context, user int, findingID int64) (FindingTrafficRecord, error) {
	if user <= 0 || findingID <= 0 {
		return FindingTrafficRecord{}, ErrInvalidInput
	}
	store, ok := f.store.(FindingReportStore)
	if !ok {
		return FindingTrafficRecord{}, ErrTaskNotFound
	}
	return store.ReadFindingTraffic(ctx, user, findingID)
}
func (f *TaskFacade) SaveFindingReport(ctx context.Context, user int, executionID, toolUseID string, nodeID int64, report string, version *int64) (bool, error) {
	if user <= 0 || !validConversationID(executionID) || toolUseID == "" || len(toolUseID) > 256 || strings.ContainsRune(toolUseID, '\x00') || nodeID <= 0 || strings.TrimSpace(report) == "" || len(report) > 1<<20 || !utf8.ValidString(report) || strings.ContainsRune(report, '\x00') || version != nil && *version < 0 {
		return false, ErrInvalidInput
	}
	store, ok := f.store.(FindingReportStore)
	if !ok {
		return false, ErrTaskNotFound
	}
	return store.UpdateFindingReport(ctx, user, executionID, toolUseID, nodeID, report, version)
}
func (f *TaskFacade) SearchTrace(ctx context.Context, user int, taskID int64, query string, limit int) ([]TaskActivity, error) {
	query = strings.TrimSpace(query)
	if user <= 0 || taskID <= 0 || query == "" || len(query) > 1000 || limit < 1 || limit > 200 {
		return nil, ErrInvalidInput
	}
	if _, err := f.store.GetTask(ctx, user, taskID); err != nil {
		return nil, err
	}
	store, ok := f.store.(TaskTraceSearchStore)
	if !ok {
		return nil, ErrTaskNotFound
	}
	return store.SearchTaskTrace(ctx, user, taskID, query, limit)
}
