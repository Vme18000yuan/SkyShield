package agent

import (
	"context"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// HostPolicy is immutable run-scoped capability injection. An absent policy
// retains upstream behavior; hosts do not mutate package-global tool hooks.
type HostPolicy struct {
	StreamText     bool // emit bounded live previews independently of durable messages
	TransformTools func(context.Context, string, []actool.CoreTool) []actool.CoreTool
	DecorateTool   func(context.Context, string, actool.CoreTool) actool.CoreTool
	AllowWebFetch  bool
	// Local Bash and its task controls share a manager in the host worker.
	// Do not let the SDK replace those controls with a second manager.
	ExternalBackgroundTasks bool
	PromptTemplate          func(string) (string, bool)
	MaxTurns                func(string, int) int
	Wrapup                  func(string) WrapupPolicy
	AdditionalSystemPrompt  func(string) string
	AugmentTools            func(context.Context, string) ([]actool.CoreTool, DeferredInfo, func())
	CheckAssetIDs           func(context.Context, []int64) error
	IntelligenceEnabled     func(context.Context) bool
	IntelligenceRecord      func(context.Context, int64, int64, string, db.IntelligenceInput) (int64, error)
	FindingTrafficEnabled   func(context.Context) bool
	FindingTrafficRecord    func(context.Context, db.RecordFindingInput, []db.TrafficRef) (*db.RecordedFinding, error)
}

func hostCheckAssetIDs(ctx context.Context, ids []int64) error {
	policy, ok := ctx.Value(hostPolicyKey{}).(HostPolicy)
	if !ok || policy.CheckAssetIDs == nil {
		return nil
	}
	return policy.CheckAssetIDs(ctx, ids)
}

type WrapupPolicy struct {
	Prompt              string
	MaxTurns            int
	TaskTimeoutPrompt   string
	TaskTimeoutMaxTurns int
}

func hostWrapup(ctx context.Context, key string) WrapupPolicy {
	policy, ok := ctx.Value(hostPolicyKey{}).(HostPolicy)
	if !ok || policy.Wrapup == nil {
		return WrapupPolicy{}
	}
	return policy.Wrapup(key)
}

func hostMaxTurns(ctx context.Context, key string, fallback int) int {
	policy, ok := ctx.Value(hostPolicyKey{}).(HostPolicy)
	if !ok || policy.MaxTurns == nil {
		return fallback
	}
	return policy.MaxTurns(key, fallback)
}

func hostAdditionalSystemPrompt(ctx context.Context, key string) string {
	policy, ok := ctx.Value(hostPolicyKey{}).(HostPolicy)
	if !ok || policy.AdditionalSystemPrompt == nil {
		return ""
	}
	return policy.AdditionalSystemPrompt(key)
}

type hostPolicyKey struct{}

func WithHostPolicy(ctx context.Context, policy HostPolicy) context.Context {
	return context.WithValue(ctx, hostPolicyKey{}, policy)
}

func hostWebFetchEnabled(ctx context.Context, fallback bool) bool {
	policy, ok := ctx.Value(hostPolicyKey{}).(HostPolicy)
	if !ok {
		return fallback
	}
	return policy.AllowWebFetch
}
