package application

import (
	"context"
	"strings"
	"unicode/utf8"

	artexdb "github.com/Autumn-27/artex/db"
)

type AutoTaskHint struct {
	Text            string
	AssetIDs        []int64
	TrafficRefs     []artexdb.TrafficRef
	ValidationError string
}
type AutoTaskHintResult struct {
	IDs    []int64           `json:"ids"`
	Errors map[string]string `json:"errors,omitempty"`
}
type AutoTaskHintStore interface {
	CreateAutoTaskHints(context.Context, int, int64, AutoTaskMutation, []AutoTaskHint) (AutoTaskHintResult, error)
}

func (f *TaskFacade) AddAutoTaskHints(ctx context.Context, user int, taskID int64, mutation AutoTaskMutation, items []AutoTaskHint) (AutoTaskHintResult, error) {
	if f == nil || user <= 0 || taskID <= 0 || !validAutoTaskMutation(mutation) || len(items) < 1 || len(items) > 100 {
		return AutoTaskHintResult{}, ErrInvalidInput
	}
	items = append([]AutoTaskHint(nil), items...)
	for i := range items {
		item := &items[i]
		if item.ValidationError != "" {
			if item.ValidationError == "traffic_disabled" {
				item.ValidationError = "Agent 自动绑定流量已关闭"
			} else {
				item.ValidationError = "提示参数无效"
			}
			continue
		}
		item.Text = strings.TrimSpace(item.Text)
		if item.Text == "" || len(item.Text) > 8000 || !utf8.ValidString(item.Text) || strings.ContainsRune(item.Text, '\x00') || len(item.AssetIDs) > 100 || len(item.TrafficRefs) > 100 {
			item.ValidationError = "提示参数无效"
			continue
		}
		seen := map[int64]bool{}
		ids := make([]int64, 0, len(item.AssetIDs))
		for _, id := range item.AssetIDs {
			if id <= 0 {
				item.ValidationError = "资产编号无效"
				break
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		item.AssetIDs = ids
		for _, ref := range item.TrafficRefs {
			if len(ref.TrafficID) > 128 || len(ref.Role) > 32 || len(ref.Note) > 4000 || !utf8.ValidString(ref.TrafficID+ref.Role+ref.Note) || strings.ContainsRune(ref.TrafficID+ref.Role+ref.Note, '\x00') {
				item.ValidationError = "流量引用无效"
				break
			}
		}
		refs, err := artexdb.NormalizeTrafficRefs(item.TrafficRefs)
		if err != nil {
			item.ValidationError = "流量引用无效"
		} else {
			item.TrafficRefs = refs
		}
	}
	store, ok := f.store.(AutoTaskHintStore)
	if !ok {
		return AutoTaskHintResult{}, ErrTaskNotFound
	}
	result, err := store.CreateAutoTaskHints(ctx, user, taskID, mutation, items)
	if err == nil && f.controller != nil {
		f.controller.Notify()
	}
	return result, err
}
