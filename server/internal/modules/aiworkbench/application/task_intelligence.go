package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

type IntelligenceCodec interface {
	Seal(string, string) ([]byte, error)
	Open(string, []byte) (string, error)
	Fingerprint(string, []byte) (string, error)
}
type TaskIntelligence struct {
	ID                 int64                       `json:"id,string"`
	TaskName           string                      `json:"task_name,omitempty"`
	TaskID             int64                       `json:"task_id,string"`
	NodeID             *int64                      `json:"node_id,omitempty,string"`
	Kind               string                      `json:"kind"`
	Title              string                      `json:"title"`
	Content            string                      `json:"content"`
	Evidence           string                      `json:"evidence"`
	Confidence         string                      `json:"confidence"`
	Status             string                      `json:"status"`
	AssetIDs           []int64                     `json:"asset_ids"`
	Worker             string                      `json:"worker"`
	CreatedAt          time.Time                   `json:"created_at"`
	UpdatedAt          time.Time                   `json:"updated_at"`
	Inherited          bool                        `json:"inherited"`
	SourceSession      string                      `json:"source_session,omitempty"`
	Fingerprint        string                      `json:"-"`
	Sealed             []byte                      `json:"-"`
	Credentials        *artexdb.CredentialAnalysis `json:"credentials,omitempty"`
	AnalysisSealed     []byte                      `json:"-"`
	CredentialStatus   string                      `json:"-"`
	CredentialMetadata []byte                      `json:"-"`
	LegacyAnalysis     []byte                      `json:"-"`
}
type IntelligenceFilter struct {
	Kind, Status, Query string
	Limit, Offset       int
	ID                  int64
}
type IntelligencePage struct {
	Items                  []TaskIntelligence `json:"items"`
	Total                  int                `json:"total"`
	Counts                 map[string]int     `json:"counts"`
	Enabled                bool               `json:"enabled"`
	CredentialAgentEnabled bool               `json:"credential_agent_enabled"`
}
type IntelligenceStore interface {
	Enabled(context.Context, int) (bool, error)
	CredentialEnabled(context.Context, int) (bool, error)
	Record(context.Context, airuntime.TaskExecution, int64, string, artexdb.IntelligenceInput, string, []byte) (int64, error)
	Stream(context.Context, int, int64, int64, func(TaskIntelligence) error) error
	UpdateStatus(context.Context, int, int64, int64, string) error
}
type IntelligenceFacade struct {
	store     IntelligenceStore
	codec     IntelligenceCodec
	assetGate airuntime.TaskAssetIDsGate
	assets    interface {
		List(context.Context, assets.ListInput) (*assets.ListResult, error)
	}
}

func NewIntelligenceFacade(store IntelligenceStore, codec IntelligenceCodec) *IntelligenceFacade {
	return &IntelligenceFacade{store: store, codec: codec}
}
func (f *IntelligenceFacade) SetAssetReader(reader interface {
	List(context.Context, assets.ListInput) (*assets.ListResult, error)
}) {
	f.assets = reader
}

type AssetIntelligenceStore interface {
	StreamAsset(context.Context, int, int64, int64, func(TaskIntelligence) error) error
}

func (f *IntelligenceFacade) SetAssetGate(gate airuntime.TaskAssetIDsGate) { f.assetGate = gate }
func (f *IntelligenceFacade) Enabled(ctx context.Context, user int) bool {
	if user <= 0 || f == nil || f.store == nil {
		return false
	}
	enabled, err := f.store.Enabled(ctx, user)
	return err == nil && enabled
}
func intelligenceScope(task int64, kind string) string {
	return fmt.Sprintf("00000000-0000-0000-0000-000000000001/task-%d/intelligence/%s", task, kind)
}

// The model never chooses the task or execution identity; those come from the
// server-owned runtime context. Duplicate content is keyed with an HMAC.
func (f *IntelligenceFacade) Record(ctx context.Context, taskID, nodeID int64, worker string, input artexdb.IntelligenceInput) (int64, error) {
	task, ok := airuntime.TaskExecutionFromContext(ctx)
	if !ok || task.ID != taskID || task.UserID <= 0 || task.RunID == "" || f == nil || f.store == nil || f.codec == nil {
		return 0, ErrRunLeaseLost
	}
	in, err := artexdb.NormalizeIntelligence(input)
	if err != nil || !utf8.ValidString(in.Title+in.Content+in.Evidence) || strings.ContainsRune(in.Title+in.Content+in.Evidence, '\x00') {
		return 0, ErrInvalidInput
	}
	if f.assetGate != nil && len(in.AssetIDs) > 0 {
		if err = f.assetGate(ctx, task, in.AssetIDs); err != nil {
			return 0, err
		}
	}
	canonical := in.Content
	var parsed any
	if json.Unmarshal([]byte(canonical), &parsed) == nil {
		if value, e := json.Marshal(parsed); e == nil {
			canonical = string(value)
		}
	}
	keyData, err := json.Marshal([]any{canonical, in.AssetIDs})
	if err != nil {
		return 0, err
	}
	scope := intelligenceScope(task.ID, in.Kind)
	fingerprint, err := f.codec.Fingerprint(scope, keyData)
	if err != nil {
		return 0, err
	}
	secret, err := json.Marshal(map[string]string{"content": in.Content, "evidence": in.Evidence})
	if err != nil {
		return 0, err
	}
	sealed, err := f.codec.Seal(scope+"/"+fingerprint, string(secret))
	if err != nil {
		return 0, err
	}
	return f.store.Record(ctx, task, nodeID, worker, in, fingerprint, sealed)
}

func (f *IntelligenceFacade) List(ctx context.Context, user int, taskID int64, filter IntelligenceFilter) (IntelligencePage, error) {
	if taskID <= 0 {
		return IntelligencePage{}, ErrInvalidInput
	}
	return f.collect(ctx, user, filter, func(visit func(TaskIntelligence) error) error {
		return f.store.Stream(ctx, user, taskID, filter.ID, visit)
	})
}
func (f *IntelligenceFacade) ListAsset(ctx context.Context, user int, assetID int64, filter IntelligenceFilter) (IntelligencePage, error) {
	if f == nil || f.store == nil || f.assets == nil || user <= 0 || assetID <= 0 {
		return IntelligencePage{}, ErrInvalidInput
	}
	visible, err := f.assets.List(ctx, assets.ListInput{UserID: user, AssetID: assetID, PageSize: 1})
	if errors.Is(err, assets.ErrForbidden) {
		return IntelligencePage{}, ErrTaskNotFound
	}
	if errors.Is(err, assets.ErrInvalidQuery) {
		return IntelligencePage{}, ErrInvalidInput
	}
	if err != nil {
		return IntelligencePage{}, err
	}
	if visible == nil || len(visible.Items) != 1 || visible.Items[0].ID != assetID {
		return IntelligencePage{}, ErrTaskNotFound
	}
	store, ok := f.store.(AssetIntelligenceStore)
	if !ok {
		return IntelligencePage{}, ErrTaskNotFound
	}
	return f.collect(ctx, user, filter, func(visit func(TaskIntelligence) error) error {
		return store.StreamAsset(ctx, user, assetID, filter.ID, visit)
	})
}
func (f *IntelligenceFacade) collect(ctx context.Context, user int, filter IntelligenceFilter, stream func(func(TaskIntelligence) error) error) (IntelligencePage, error) {
	page := IntelligencePage{Items: []TaskIntelligence{}, Counts: map[string]int{"credential": 0, "lead": 0, "sensitive_data": 0}}
	if f == nil || f.store == nil || user <= 0 || filter.Offset < 0 || filter.Offset > 100000 || filter.Limit < 0 || filter.Limit > 100 || len(filter.Query) > 500 || !utf8.ValidString(filter.Query) || strings.ContainsRune(filter.Query, '\x00') {
		return page, ErrInvalidInput
	}
	if filter.Limit == 0 {
		filter.Limit = 25
	}
	if filter.Kind != "" && filter.Kind != "credential" && filter.Kind != "lead" && filter.Kind != "sensitive_data" {
		return page, ErrInvalidInput
	}
	page.Enabled = f.Enabled(ctx, user)
	page.CredentialAgentEnabled, _ = f.store.CredentialEnabled(ctx, user)
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	err := stream(func(item TaskIntelligence) error {
		page.Counts[item.Kind]++
		if filter.ID > 0 && item.ID != filter.ID || filter.Kind != "" && item.Kind != filter.Kind || filter.Status != "" && item.Status != filter.Status {
			return nil
		}
		if len(item.Sealed) > 0 {
			if f.codec == nil {
				return ErrTaskNotFound
			}
			value, err := f.codec.Open(intelligenceScope(item.TaskID, item.Kind)+"/"+item.Fingerprint, item.Sealed)
			if err != nil {
				return err
			}
			var secret struct{ Content, Evidence string }
			if err = json.Unmarshal([]byte(value), &secret); err != nil {
				return err
			}
			item.Content, item.Evidence = secret.Content, secret.Evidence
		}
		if len(item.LegacyAnalysis) > 0 && string(item.LegacyAnalysis) != "null" {
			var legacy artexdb.CredentialAnalysis
			if err := json.Unmarshal(item.LegacyAnalysis, &legacy); err != nil {
				return err
			}
			item.Credentials = &legacy
		}
		if err := f.openCredentialAnalysis(&item); err != nil {
			return err
		}
		if item.CredentialStatus != "" {
			if item.Credentials == nil {
				item.Credentials = &artexdb.CredentialAnalysis{}
			}
			item.Credentials.State = item.CredentialStatus
			if len(item.CredentialMetadata) > 0 {
				var metadata struct {
					Error, Note, Method, Model string
					InputTokens                int `json:"input_tokens"`
					OutputTokens               int `json:"output_tokens"`
				}
				if json.Unmarshal(item.CredentialMetadata, &metadata) == nil {
					item.Credentials.Error = metadata.Error
					item.Credentials.Note = metadata.Note
					item.Credentials.Method = metadata.Method
					item.Credentials.Model = metadata.Model
					item.Credentials.InputTokens = metadata.InputTokens
					item.Credentials.OutputTokens = metadata.OutputTokens
				}
			}
		}
		if item.Credentials != nil {
			item.Credentials.Lease = ""
			item.Credentials.LeaseUntil = time.Time{}
		}
		item.Sealed = nil
		item.AnalysisSealed = nil
		item.CredentialMetadata = nil
		item.LegacyAnalysis = nil
		item.Fingerprint = ""
		if query != "" && !strings.Contains(strings.ToLower(item.Title+"\n"+item.Content+"\n"+item.Evidence), query) {
			return nil
		}
		page.Total++
		if page.Total > filter.Offset && len(page.Items) < filter.Limit {
			page.Items = append(page.Items, item)
		}
		return nil
	})
	return page, err
}
func (f *IntelligenceFacade) UpdateStatus(ctx context.Context, user int, taskID, id int64, status string) error {
	if user <= 0 || taskID <= 0 || id <= 0 || len(status) > 30 {
		return ErrInvalidInput
	}
	return f.store.UpdateStatus(ctx, user, taskID, id, status)
}
