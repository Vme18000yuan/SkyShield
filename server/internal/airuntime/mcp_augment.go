package airuntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/skill"
	"github.com/Autumn-27/norma/tool"
	"regexp"
	"time"
)

type MCPAugmenter func(context.Context, int, string) ([]tool.CoreTool, agent.DeferredInfo, func())

var mcpToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func ValidMCPTool(item tool.CoreTool) bool {
	schema, err := json.Marshal(item.InputSchema())
	return mcpToolNamePattern.MatchString(item.Name()) && len(item.Description()) <= 4096 && err == nil && len(schema) <= 64*1024
}
func NewMCPAugmenter(resolve MCPResolver) MCPAugmenter {
	return NewMCPAugmenterWithConnector(resolve, NewMCPConnector(""), nil)
}

// Each run owns the clients and unlock set. Skill-gated MCPs are connected but
// become callable only when their visible Skill is loaded (including on resume).
func NewMCPAugmenterWithConnector(resolve MCPResolver, connect MCPConnector, skills SkillResolver) MCPAugmenter {
	return func(ctx context.Context, user int, key string) ([]tool.CoreTool, agent.DeferredInfo, func()) {
		if resolve == nil {
			return nil, agent.DeferredInfo{}, nil
		}
		configs, err := resolve(ctx, user, key)
		if err != nil {
			return nil, agent.DeferredInfo{}, nil
		}
		discovery, stop := context.WithTimeout(ctx, 90*time.Second)
		defer stop()
		clients := []MCPClient{}
		all := []tool.CoreTool{}
		deferred, global := []string{}, []string{}
		seen := map[string]bool{}
		byServer := map[string][]string{}
		for _, config := range configs {
			if discovery.Err() != nil || len(clients) >= 20 || len(deferred) >= 500 {
				break
			}
			timeout := 10 * time.Second
			if config.Transport == "stdio" {
				timeout = 60 * time.Second
			}
			attempt, cancel := context.WithTimeout(discovery, timeout)
			connection, release := context.WithCancel(ctx)
			stopExpiry := context.AfterFunc(attempt, release)
			client, err := connect(connection, user, config)
			if err != nil || attempt.Err() != nil {
				if errors.Is(err, ErrExecutionCapacity) {
					executionProgress(ctx, "MCP 执行通道已满，本轮部分连接暂不可用。可减少并发或调整执行容量。")
				}
				stopExpiry()
				cancel()
				release()
				if client != nil {
					_ = client.Close()
				}
				continue
			}
			tools, err := client.Tools(attempt)
			stopExpiry()
			cancel()
			if err != nil || len(tools) > 200 {
				release()
				_ = client.Close()
				continue
			}
			clients = append(clients, &ownedMCPClient{MCPClient: client, cleanup: release})
			for _, item := range tools {
				if !ValidMCPTool(item) || seen[item.Name()] || len(deferred) >= 500 {
					continue
				}
				seen[item.Name()] = true
				all = append(all, item)
				deferred = append(deferred, item.Name())
				byServer[config.Name] = append(byServer[config.Name], item.Name())
				if !config.SkillGated {
					global = append(global, item.Name())
				}
			}
		}
		unlock := tool.NewUnlockSet(global...)
		var visible []skill.Skill
		if skills != nil {
			visible, _ = skills(ctx, user, key)
		}
		skillNames := map[string][]string{}
		for _, item := range visible {
			for _, server := range item.MCPs {
				skillNames[item.Name] = append(skillNames[item.Name], byServer[server]...)
			}
		}
		unlockSkill := func(name string) { unlock.Add(skillNames[name]...) }
		if len(visible) > 0 {
			registry := skill.NewRegistry(visible...)
			registry.OnInvoke = func(item skill.Skill) string {
				unlockSkill(item.Name)
				return tool.RenderDeferredToolsBlock(skillNames[item.Name])
			}
			all = append(all, registry.Tool())
		}
		cleanup := func() {
			for _, client := range clients {
				_ = client.Close()
			}
		}
		return all, agent.DeferredInfo{Deferred: deferred, GlobalNames: global, Unlock: unlock, UnlockSkill: unlockSkill}, cleanup
	}
}
