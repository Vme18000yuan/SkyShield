package application

import "context"

type CoverageGraphNode struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Tested      bool   `json:"tested"`
	InScope     bool   `json:"in_scope"`
	AssetID     int64  `json:"asset_id,omitempty,string"`
	CompanyID   int64  `json:"company_id,omitempty,string"`
	Domain      string `json:"domain,omitempty"`
	RootDomain  string `json:"root_domain,omitempty"`
	IP          string `json:"ip,omitempty"`
	URL         string `json:"url,omitempty"`
	Port        *int   `json:"port,omitempty"`
	ServiceType string `json:"service_type,omitempty"`
	AppName     string `json:"app_name,omitempty"`
	PageTitle   string `json:"page_title,omitempty"`
	StatusCode  *int   `json:"status_code,omitempty"`
}
type CoverageGraphEdge struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}
type CoverageGraph struct {
	Nodes     []CoverageGraphNode `json:"nodes"`
	Edges     []CoverageGraphEdge `json:"edges"`
	Truncated bool                `json:"truncated"`
}
type CoverageAssetRef struct {
	ID           int64  `json:"id,string"`
	Kind         string `json:"kind"`
	State        string `json:"state"`
	Summary      string `json:"summary"`
	SourceTaskID int64  `json:"source_task_id,omitempty,string"`
	Inherited    bool   `json:"inherited,omitempty"`
}
type CoverageAssetRefs struct {
	Intents  []CoverageAssetRef `json:"intents"`
	Facts    []CoverageAssetRef `json:"facts"`
	Findings []CoverageAssetRef `json:"findings"`
}
type CoverageGraphStore interface {
	TaskCoverageGraph(context.Context, int, int64) (CoverageGraph, error)
	TaskAssetRefs(context.Context, int, int64, int64) (CoverageAssetRefs, error)
}

func (f *TaskFacade) CoverageGraph(ctx context.Context, user int, taskID int64) (CoverageGraph, error) {
	if user <= 0 || taskID <= 0 {
		return CoverageGraph{}, ErrInvalidInput
	}
	store, ok := f.store.(CoverageGraphStore)
	if !ok {
		return CoverageGraph{}, ErrTaskNotFound
	}
	return store.TaskCoverageGraph(ctx, user, taskID)
}
func (f *TaskFacade) AssetRefs(ctx context.Context, user int, taskID, assetID int64) (CoverageAssetRefs, error) {
	if user <= 0 || taskID <= 0 || assetID <= 0 {
		return CoverageAssetRefs{}, ErrInvalidInput
	}
	store, ok := f.store.(CoverageGraphStore)
	if !ok {
		return CoverageAssetRefs{}, ErrTaskNotFound
	}
	return store.TaskAssetRefs(ctx, user, taskID, assetID)
}
