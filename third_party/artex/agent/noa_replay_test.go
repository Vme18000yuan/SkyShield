package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/noa"
	"github.com/Autumn-27/norma/noaadapter"
	"github.com/Autumn-27/norma/tool"
)

func compressionReplayHistory() []llm.Message {
	messages := []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextBlock("Review the local fixture and preserve its observations.")}}}
	for i := range 18 {
		id := fmt.Sprintf("probe-%d", i)
		messages = append(messages,
			llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: llm.BlockThinking, Thinking: fmt.Sprintf("Check fixture %d.", i)},
				{Type: llm.BlockToolUse, ID: id, Name: "probe", Input: json.RawMessage(fmt.Sprintf(`{ "fixture": %d }`, i))},
			}},
			llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: id, Content: []llm.ContentBlock{llm.TextBlock(strings.Repeat("fixture observation. ", 1500))}}}},
		)
	}
	return messages
}

func TestNoaCompressionReplaysOriginalToolArguments(t *testing.T) {
	ctx := context.Background()
	opts := agentcore.Options{}
	session, err := noaadapter.EnableWithSession(&opts, noaadapter.Options{ArchiveBaseDir: t.TempDir(), SessionID: "fixture", ModelContextLimit: 800_000})
	if err != nil {
		t.Fatal(err)
	}
	history := compressionReplayHistory()
	session.View(history)
	cores, _ := noaadapter.Project(history)
	refs := session.State().MessageRefs.ByRaw
	start, end := "", ""
	for _, core := range cores {
		if core.ContentType != noa.CTToolResult {
			continue
		}
		if core.ToolCallID == "probe-1" {
			start = refs[core.ID]
		}
		if core.ToolCallID == "probe-8" {
			end = refs[core.ID]
		}
	}
	if start == "" || end == "" {
		t.Fatal("compression refs missing")
	}
	input, _ := json.Marshal(map[string]any{"content": []map[string]string{{"startId": start, "endId": end, "summary": strings.Repeat("Reviewed fixture observations and retained their evidence. ", 20)}}})
	result, err := opts.Tools[len(opts.Tools)-1].Call(ctx, input, &tool.ToolContext{ToolUseID: "compress-1"})
	if err != nil || result.IsError || noa.PanelBlockCount(result.Content[0].Text) == 0 {
		t.Fatalf("compression did not create a block: %v", err)
	}
	history = append(history,
		llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "Preserve the observations before continuing."}, {Type: llm.BlockToolUse, ID: "compress-1", Name: "Compress", Input: input}}},
		llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: "compress-1", Content: result.Content}}},
	)
	originalJSON, _ := json.Marshal(history)
	view := opts.Compactor.(harness.ContextView).View(ctx, history)
	guard := &replaySafeNoaCompactor{Compactor: opts.Compactor}
	safe := guard.View(ctx, history)
	findInput := func(messages []llm.Message, id string) string {
		for _, m := range messages {
			for _, b := range m.Content {
				if b.Type == llm.BlockToolUse && b.ID == id {
					return string(b.Input)
				}
			}
		}
		return ""
	}
	if findInput(view, "compress-1") == string(input) {
		t.Fatal("fixture no longer reproduces noa's argument rewriting")
	}
	if findInput(safe, "compress-1") != string(input) {
		t.Fatal("model-generated Compress input was changed")
	}
	if findInput(safe, "probe-5") != "" {
		t.Fatal("compressed-away tool exchange was restored")
	}
	afterJSON, _ := json.Marshal(history)
	if string(originalJSON) != string(afterJSON) {
		t.Fatal("raw transcript was mutated")
	}
	if len(safe) >= len(history) {
		t.Fatal("compression no longer reduces the history")
	}

	// A strict gateway rejects altered call history with the same terminal code
	// as the report. This uses an in-memory transport, never an external model.
	provider, err := llm.NewProvider(llm.Config{Format: llm.FormatOpenAI, BaseURL: "http://offline.invalid", APIKey: "fixture", Model: "fixture", MaxRetries: -1, HTTPClient: &http.Client{Transport: strictCompressReplay{expected: string(input)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := provider.Complete(ctx, llm.CompletionRequest{Messages: view}); err == nil || !strings.Contains(err.Error(), "model_rejected_request") {
		t.Fatal("strict fixture should reject modified call history")
	}
	if _, _, _, err := provider.Complete(ctx, llm.CompletionRequest{Messages: safe}); err != nil {
		t.Fatalf("safe replay rejected: %v", err)
	}
}

type strictCompressReplay struct{ expected string }

func (s strictCompressReplay) RoundTrip(request *http.Request) (*http.Response, error) {
	var body struct {
		Messages []struct {
			ToolCalls []struct {
				Function struct{ Name, Arguments string } `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return nil, err
	}
	status, response := 400, `{"error":{"type":"upstream_error","code":"model_rejected_request"}}`
	for _, m := range body.Messages {
		for _, call := range m.ToolCalls {
			if call.Function.Name == "Compress" && call.Function.Arguments == s.expected {
				status, response = 200, `{"choices":[{"message":{"content":"fixture complete"},"finish_reason":"stop"}]}`
			}
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func TestNoaUsesConfiguredContextWindow(t *testing.T) {
	for _, window := range []int{200_000, 800_000} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			opts := agentcore.Options{Compaction: &compaction.Config{ContextWindow: window}}
			enableNoa(&opts, func() bool { return true }, t.TempDir(), "fixture", nil)
			view := opts.Compactor.(harness.ContextView).View(context.Background(), compressionReplayHistory())
			last := view[len(view)-1]
			nudge := last.Role == llm.RoleUser && len(last.Content) == 1 && last.Content[0].Type == llm.BlockText
			if nudge != (window == 200_000) {
				t.Fatalf("window=%d nudge=%v", window, nudge)
			}
		})
	}
}
