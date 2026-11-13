package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

type LLMRecordingStore interface {
	StartLLMRecording(context.Context, airuntime.LLMUsageRecord) (bool, airuntime.LLMRecordingLease, error)
	InsertLLMRecording(context.Context, string, airuntime.LLMUsageRecord, airuntime.LLMRecordingLease, bool, []byte) error
}
type LLMRecordingEntry struct {
	ID               string    `json:"id"`
	TaskID           *int64    `json:"task_id,omitempty,string"`
	TaskName         string    `json:"task_name,omitempty"`
	ConversationID   string    `json:"conversation_id,omitempty"`
	SessionID        string    `json:"session_id"`
	Worker           string    `json:"worker"`
	Model            string    `json:"model"`
	ProfileName      string    `json:"profile_name"`
	DurationMS       int64     `json:"duration_ms"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	Status           string    `json:"status"`
	Truncated        bool      `json:"truncated"`
	CreatedAt        time.Time `json:"created_at"`
	Sealed           []byte    `json:"-"`
}
type LLMRecordingFilter struct {
	TaskID         int64
	Model, Session string
	Page, Size     int
}
type LLMRecordingPage struct {
	Items []LLMRecordingEntry `json:"items"`
	Total int64               `json:"total"`
	Page  int                 `json:"page"`
	Size  int                 `json:"size"`
}
type LLMRecordingDetail struct {
	LLMRecordingEntry
	Payload airuntime.LLMRecordingPayload `json:"payload"`
}
type LLMRecordingTask struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
}
type LLMRecordingTasks struct {
	Items   []LLMRecordingTask `json:"items"`
	HasMore bool               `json:"has_more"`
}
type LLMRecordingReader interface {
	ListLLMRecordings(context.Context, int, LLMRecordingFilter) (LLMRecordingPage, error)
	GetLLMRecording(context.Context, int, string) (LLMRecordingEntry, error)
	LLMRecordingVisible(context.Context, int, string) (bool, error)
	DeleteTaskLLMRecordings(context.Context, int, int64) (int64, error)
	ListLLMRecordingTasks(context.Context, int, string) (LLMRecordingTasks, error)
}
type LLMRecordingFacade struct {
	store LLMRecordingReader
	codec SecretCodec
}

func NewLLMRecordingFacade(store LLMRecordingReader, codec SecretCodec) *LLMRecordingFacade {
	return &LLMRecordingFacade{store: store, codec: codec}
}
func (f *LLMRecordingFacade) List(ctx context.Context, user int, filter LLMRecordingFilter) (LLMRecordingPage, error) {
	if f == nil || f.store == nil || user <= 0 || filter.TaskID < 0 || filter.Page < 0 || filter.Page > 10000 || filter.Size < 1 || filter.Size > 100 {
		return LLMRecordingPage{}, ErrInvalidUsageQuery
	}
	for _, value := range []string{filter.Model, filter.Session} {
		if len(value) > 500 || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
			return LLMRecordingPage{}, ErrInvalidUsageQuery
		}
	}
	return f.store.ListLLMRecordings(ctx, user, filter)
}
func (f *LLMRecordingFacade) Detail(ctx context.Context, user int, id string) (LLMRecordingDetail, error) {
	parsed, err := uuid.Parse(id)
	if f == nil || f.store == nil || f.codec == nil || user <= 0 || err != nil || parsed.String() != id {
		return LLMRecordingDetail{}, ErrInvalidUsageQuery
	}
	entry, err := f.store.GetLLMRecording(ctx, user, id)
	if err != nil {
		return LLMRecordingDetail{}, err
	}
	plain, err := f.codec.Open(llmRecordingScope(user, id), entry.Sealed)
	if err != nil {
		return LLMRecordingDetail{}, err
	}
	var payload airuntime.LLMRecordingPayload
	if err = json.Unmarshal([]byte(plain), &payload); err != nil {
		return LLMRecordingDetail{}, err
	}
	visible, err := f.store.LLMRecordingVisible(ctx, user, id)
	if err != nil {
		return LLMRecordingDetail{}, err
	}
	if !visible {
		return LLMRecordingDetail{}, ErrUsageTaskNotFound
	}
	entry.Sealed = nil
	return LLMRecordingDetail{LLMRecordingEntry: entry, Payload: payload}, nil
}
func (f *LLMRecordingFacade) DeleteTask(ctx context.Context, user int, task int64) (int64, error) {
	if f == nil || f.store == nil || user <= 0 || task <= 0 {
		return 0, ErrInvalidUsageQuery
	}
	return f.store.DeleteTaskLLMRecordings(ctx, user, task)
}
func (f *LLMRecordingFacade) Tasks(ctx context.Context, user int, query string) (LLMRecordingTasks, error) {
	if f == nil || f.store == nil || user <= 0 || len(query) > 500 || !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') {
		return LLMRecordingTasks{}, ErrInvalidUsageQuery
	}
	return f.store.ListLLMRecordingTasks(ctx, user, strings.TrimSpace(query))
}

type LLMRecordingRecorder struct {
	usage airuntime.LLMUsageSink
	store LLMRecordingStore
	codec SecretCodec
}

func NewLLMRecordingRecorder(usage airuntime.LLMUsageSink, store LLMRecordingStore, codec SecretCodec) *LLMRecordingRecorder {
	return &LLMRecordingRecorder{usage: usage, store: store, codec: codec}
}
func (r *LLMRecordingRecorder) RecordLLMUsage(ctx context.Context, entry airuntime.LLMUsageRecord) error {
	if r == nil || r.usage == nil {
		return ErrInvalidUsageQuery
	}
	return r.usage.RecordLLMUsage(ctx, entry)
}
func (r *LLMRecordingRecorder) StartLLMRecording(ctx context.Context, entry airuntime.LLMUsageRecord) (bool, airuntime.LLMRecordingLease, error) {
	if r == nil || r.store == nil || entry.UserID <= 0 {
		return false, airuntime.LLMRecordingLease{}, ErrInvalidUsageQuery
	}
	return r.store.StartLLMRecording(ctx, entry)
}
func llmRecordingScope(user int, id string) string {
	return fmt.Sprintf("%s/user-%d/llm-record/%s", assets.DefaultTenantID, user, id)
}
func (r *LLMRecordingRecorder) RecordLLMRecording(ctx context.Context, entry airuntime.LLMUsageRecord, lease airuntime.LLMRecordingLease, payload airuntime.LLMRecordingPayload) error {
	if r == nil || r.store == nil || r.codec == nil || entry.UserID <= 0 {
		return ErrInvalidUsageQuery
	}
	id := uuid.NewString()
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	sealed, err := r.codec.Seal(llmRecordingScope(entry.UserID, id), string(raw))
	if err != nil {
		return err
	}
	return r.store.InsertLLMRecording(ctx, id, entry, lease, payload.Truncated, sealed)
}
