package agent

import (
	"context"

	actool "github.com/Autumn-27/norma/tool"
)

// DeferredInfo carries the deferred-tools wiring an agent needs to build its
// Options: the MCP tool names whose schemas are withheld, the subset listed in the
// global system-prompt block (non-skill-gated), and the shared session unlock set.
// UnlockSkill unlocks a named skill's MCPs — hosts call it to rebuild the unlock set
// from history on a resumed session (design doc C2).
type DeferredInfo struct {
	IntelligenceGuidance string
	CaseGuidance         string // independent of optional traffic binding

	FindingGuidance string            // derived from the final permitted tools, including DB overrides
	Deferred        []string          // all MCP tool names (schema withheld)
	GlobalNames     []string          // MCP names to list in the system-prompt block
	Unlock          *actool.UnlockSet // shared call-gate; nil when no MCP tools
	UnlockSkill     func(skillName string)
}

// ToolAugment, if set, returns the EXTRA tools an agent should see beyond its
// built-in base set — the agent's visible skills (packed into one Skill meta-tool)
// and visible MCP servers (expanded to mcp__server__tool). It also returns the
// DeferredInfo describing how those MCP tools are deferred/gated. The server wires
// it to the PG agent_visibility table. cleanup releases any spawned MCP clients.
//
// When nil, agents run with only their built-in tools — behavior is unchanged
// until a user assigns a skill/MCP to the agent in the UI.
var ToolAugment func(ctx context.Context, agentKey string) (extra []actool.CoreTool, def DeferredInfo, cleanup func())

// AugmentTools returns base plus the agent's visible skill/MCP tools, the
// DeferredInfo, and a cleanup func the caller must defer (closes MCP clients).
// Built-in base tools are kept as-is — never filtered (内置工具留代码层，不做可见性过滤).
func AugmentTools(ctx context.Context, agentKey string, base []actool.CoreTool) ([]actool.CoreTool, DeferredInfo, func()) {
	var (
		def     DeferredInfo
		cleanup = func() {}
		out     = base
	)
	if ToolAugment != nil {
		var extra []actool.CoreTool
		var cl func()
		extra, def, cl = ToolAugment(ctx, agentKey)
		if cl != nil {
			cleanup = cl
		}
		if len(extra) > 0 {
			out = append(append([]actool.CoreTool{}, base...), extra...)
		}
	}
	// DB tools table has the final say on the built-in tools: drop the ones this
	// agent isn't bound to (or that are disabled) and swap in overridden
	// descriptions/schemas + default injection. MCP/skill/host tools have no row
	// and pass through untouched, so deferred/unlock wiring stays consistent.
	if ToolResolve != nil {
		out = ToolResolve(ctx, agentKey, out)
	}
	if agentKey == "reporter" && isFindingCaseReview(ctx) {
		out = historicalFindingCaseTools(out)
	}
	_, hosted := ctx.Value(hostPolicyKey{}).(HostPolicy)
	if !hosted {
		out, def.FindingGuidance = findingWorkflowTools(ctx, agentKey, out)
	}
	out, def.CaseGuidance = findingCaseWorkflow(agentKey, out)
	out, def.IntelligenceGuidance = intelligenceWorkflow(ctx, out)
	if agentKey == "reporter" && isFindingCaseReview(ctx) {
		def.FindingGuidance = ""
		def.CaseGuidance += historicalFindingReviewGuidance
	}
	if policy, ok := ctx.Value(hostPolicyKey{}).(HostPolicy); ok {
		if policy.TransformTools != nil {
			out = policy.TransformTools(ctx, agentKey, out)
		}
		if policy.AugmentTools != nil {
			extra, additional, closeHost := policy.AugmentTools(ctx, agentKey)
			out = append(out, extra...)
			def.Deferred = append(def.Deferred, additional.Deferred...)
			def.GlobalNames = append(def.GlobalNames, additional.GlobalNames...)
			if def.Unlock == nil {
				def.Unlock = additional.Unlock
			} else if additional.Unlock != nil {
				def.Unlock.Add(additional.Unlock.List()...)
			}
			if additional.UnlockSkill != nil {
				if def.UnlockSkill == nil {
					def.UnlockSkill = additional.UnlockSkill
				} else {
					previous := def.UnlockSkill
					next := additional.UnlockSkill
					def.UnlockSkill = func(name string) { previous(name); next(name) }
				}
			}
			def.FindingGuidance += additional.FindingGuidance
			def.CaseGuidance += additional.CaseGuidance
			def.IntelligenceGuidance += additional.IntelligenceGuidance
			if closeHost != nil {
				previous := cleanup
				cleanup = func() { closeHost(); previous() }
			}
		}
	}
	if hosted {
		var guidance string
		out, guidance = findingWorkflowTools(ctx, agentKey, out)
		def.FindingGuidance += guidance
		if agentKey == "reporter" && isFindingCaseReview(ctx) {
			def.FindingGuidance = ""
		}
	}
	// Host augmentation happens after the first review filter. Apply it again
	// so MCP or other host tools cannot escape a historical review's allowlist.
	if agentKey == "reporter" && isFindingCaseReview(ctx) {
		out = historicalFindingCaseTools(out)
	}
	// The host may intercept every final tool, including MCP and Skill tools.
	if policy, ok := ctx.Value(hostPolicyKey{}).(HostPolicy); ok && policy.DecorateTool != nil {
		for i, item := range out {
			out[i] = policy.DecorateTool(ctx, agentKey, item)
		}
	}
	// Hosted catalogs can mark deferred custom tools without process-global hooks.
	for _, t := range out {
		if marker, ok := t.(interface{ IsDeferredTool() bool }); ok && marker.IsDeferredTool() {
			def.Deferred = append(def.Deferred, t.Name())
			def.GlobalNames = append(def.GlobalNames, t.Name())
			if def.Unlock == nil {
				def.Unlock = actool.NewUnlockSet()
			}
			def.Unlock.Add(t.Name())
		}
	}
	for i, t := range out {
		out[i] = guardPanic(t)
	}
	return out, def, cleanup
}
