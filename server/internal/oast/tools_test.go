package oast

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

type callbackModel struct {
	turn      int
	collector *Collector
	probe     Probe
	confirmed bool
}

func (p *callbackModel) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, fmt.Errorf("unexpected streaming request"))
	}
}
func (p *callbackModel) Complete(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.turn++
	result := func(id string) (string, error) {
		for _, m := range req.Messages {
			for _, b := range m.Content {
				if b.Type == llm.BlockToolResult && b.ToolUseID == id {
					var text strings.Builder
					for _, item := range b.Content {
						text.WriteString(item.Text)
					}
					if b.IsError {
						return "", fmt.Errorf("tool error: %s", text.String())
					}
					return text.String(), nil
				}
			}
		}
		return "", fmt.Errorf("missing result for %s", id)
	}
	name, id, input := "oast_create", "create", `{"label":"AI fixture callback","ttl_seconds":60}`
	if p.turn == 2 {
		text, err := result("create")
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		if err = json.Unmarshal([]byte(text), &p.probe); err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		w := httptest.NewRecorder()
		p.collector.ServeHTTP(w, httptest.NewRequest("POST", p.probe.HTTPURL, strings.NewReader("isolated fixture")))
		if w.Code != 204 {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("fixture callback status %d", w.Code)
		}
		name, id, input = "oast_poll", "poll", fmt.Sprintf(`{"probe_id":%q,"wait_seconds":0}`, p.probe.ID)
	} else if p.turn == 3 {
		text, err := result("poll")
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		var page EventPage
		if err = json.Unmarshal([]byte(text), &page); err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		if len(page.Items) != 1 || page.Items[0].Protocol != "http" {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("missing callback evidence")
		}
		p.confirmed = true
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "Callback evidence received"}}}, "end_turn", llm.Usage{}, nil
	}
	found := false
	for _, item := range req.Tools {
		if item.Name == name {
			found = true
		}
	}
	if !found {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("tool not exposed: %s", name)
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: id, Name: name, Input: json.RawMessage(input)}}}, "tool_use", llm.Usage{}, nil
}

func exerciseChatTools(t *testing.T, s *Service, run, conversation string) {
	t.Helper()
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	model := &callbackModel{collector: NewCollector(s)}
	_, err = runner.Run(context.Background(), airuntime.ChatRun{TenantID: assets.DefaultTenantID, UserID: 7, SessionID: uuid.NewString(), ExecutionID: run, AgentKey: "auto", Message: "Validate an isolated callback fixture", Model: "fixture", Provider: model, Tools: s.Tools(7), NonStreaming: true, MaxTurns: 4}, nil)
	if err != nil || !model.confirmed || model.probe.ConversationID != conversation || model.probe.RunID != run {
		t.Fatalf("AI callback loop: confirmed=%v scope=%+v err=%v", model.confirmed, model.probe.Scope, err)
	}
}

func TestToolsRejectCallsWithoutHostExecutionContext(t *testing.T) {
	s, err := New(nil, Config{PublicURL: "https://callback.example.test"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range s.Tools(7) {
		result, err := item.Call(context.Background(), json.RawMessage(`{}`), &tool.ToolContext{ToolUseID: "untrusted-call"})
		if err != nil || !result.IsError {
			t.Fatalf("%s accepted missing run: %+v %v", item.Name(), result, err)
		}
	}
}

func TestManagementRoutesRequireIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, _ := New(nil, Config{PublicURL: "https://callback.example.test"}, nil, nil)
	router := gin.New()
	s.RegisterRoutes(router.Group("/v1"))
	for _, method := range []string{"GET", "POST", "DELETE"} {
		path := "/v1/aiConversations/" + uuid.NewString() + "/oastProbes"
		if method == "DELETE" {
			path += "/" + uuid.NewString()
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != 401 {
			t.Fatalf("anonymous %s: %d", method, w.Code)
		}
	}
}
