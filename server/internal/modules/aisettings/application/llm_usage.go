package application

import (
	"context"
	"errors"
)

var ErrUsageTaskNotFound = errors.New("AI task not found")
var ErrInvalidUsageQuery = errors.New("invalid model usage query")

type LLMUsageTotals struct {
	Calls            int64 `json:"calls"`
	Tasks            int64 `json:"tasks"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}
type LLMUsageProfile struct {
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
	LLMUsageTotals
}
type LLMUsageDay struct {
	Date        string `json:"date"`
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
	LLMUsageTotals
}
type LLMUsageSummary struct {
	Totals   LLMUsageTotals    `json:"totals"`
	Profiles []LLMUsageProfile `json:"profiles"`
	Daily    []LLMUsageDay     `json:"daily"`
	Days     int               `json:"days"`
}
type LLMModelUsage struct {
	Model string `json:"model"`
	LLMUsageTotals
}
type LLMUsageReader interface {
	UsageSummary(context.Context, int, int) (LLMUsageSummary, error)
	UsageByTaskModel(context.Context, int, int64) ([]LLMModelUsage, error)
}
type LLMUsageFacade struct{ store LLMUsageReader }

func NewLLMUsageFacade(store LLMUsageReader) *LLMUsageFacade { return &LLMUsageFacade{store: store} }
func (f *LLMUsageFacade) Summary(ctx context.Context, user, days int) (LLMUsageSummary, error) {
	if f == nil || f.store == nil || user <= 0 || days < 1 || days > 366 {
		return LLMUsageSummary{}, ErrInvalidUsageQuery
	}
	return f.store.UsageSummary(ctx, user, days)
}
func (f *LLMUsageFacade) TaskModels(ctx context.Context, user int, task int64) ([]LLMModelUsage, error) {
	if f == nil || f.store == nil || user <= 0 || task <= 0 {
		return nil, ErrInvalidUsageQuery
	}
	return f.store.UsageByTaskModel(ctx, user, task)
}
