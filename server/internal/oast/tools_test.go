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
	closed    bool
	after     string
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
	decodeResult := func(id string, out any) error {
		text, err := result(id)
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(text), out)
	}
	callback := func(body string) int {
		w := httptest.NewRecorder()
		p.collector.ServeHTTP(w, httptest.NewRequest("POST", p.probe.HTTPURL, strings.NewReader(body)))
		return w.Code
	}
	name, id, input := "", "", ""
	switch p.turn {
	case 1:
		// The registry must append the native workflow despite a saved catalog
		// description. The user message does not name a particular tool.
		taught := false
		for _, schema := range req.Tools {
			if schema.Name == "oast_create" && strings.Contains(schema.Description, "【自主反连测试流程】") && strings.Contains(schema.Description, "当前部署配置") {
				taught = true
			}
		}
		if !taught {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("model did not receive OAST guidance")
		}
		name, id, input = "oast_status", "status", `{}`
	case 2:
		var status Capabilities
		if err := decodeResult("status", &status); err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		if !status.Enabled || status.Reachability != "not_verified" {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("invalid preflight")
		}
		name, id, input = "oast_list", "list", `{}`
	case 3:
		var page ProbePage
		if err := decodeResult("list", &page); err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		if !page.Enabled {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("collector disabled")
		}
		name, id, input = "oast_create", "create", `{"label":"AI fixture callback","ttl_seconds":60}`
	case 4:
		if err := decodeResult("create", &p.probe); err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		if code := callback("isolated fixture"); code != 204 {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("callback status %d", code)
		}
		name, id, input = "oast_poll", "poll", fmt.Sprintf(`{"probe_id":%q,"wait_seconds":0}`, p.probe.ID)
	case 5:
		var page EventPage
		if err := decodeResult("poll", &page); err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		if len(page.Items) != 1 || page.Items[0].Protocol != "http" {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("missing evidence")
		}
		p.confirmed, p.after = true, page.NextAfter
		// An event arriving after the last poll must still be read at closure.
		if callback("late evidence") != 204 {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("late callback failed")
		}
		name, id, input = "oast_revoke", "revoke", fmt.Sprintf(`{"probe_id":%q}`, p.probe.ID)
	case 6:
		var stopped struct {
			Revoked bool `json:"revoked"`
		}
		if err := decodeResult("revoke", &stopped); err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		if !stopped.Revoked || callback("must not be captured") != 404 {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("probe still receiving")
		}
		name, id, input = "oast_poll", "final_poll", fmt.Sprintf(`{"probe_id":%q,"after":%q}`, p.probe.ID, p.after)
	case 7:
		var page EventPage
		if err := decodeResult("final_poll", &page); err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		if page.Probe.State != "revoked" || len(page.Items) != 1 || page.HasMore || page.NextAfter == p.after {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("closure lost late evidence")
		}
		p.closed = true
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "Callback evidence received; probe closed"}}}, "end_turn", llm.Usage{}, nil
	default:
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("unexpected model turn")
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

func exerciseAutonomousTools(t *testing.T, s *Service, run string, scope Scope, role string) {
	t.Helper()
	ctx := context.Background()
	if scope.TaskID != 0 {
		ctx = airuntime.WithTaskExecution(ctx, airuntime.TaskExecution{ID: scope.TaskID, RunID: run, UserID: 7, TenantID: assets.DefaultTenantID})
	}
	sibling, err := s.Create(ctx, 7, scope, "", CreateInput{Label: "Unrelated test", RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	model := &callbackModel{collector: NewCollector(s)}
	catalog, _ := testCatalog(s)
	_, err = runner.Run(ctx, airuntime.ChatRun{TenantID: assets.DefaultTenantID, UserID: 7, SessionID: uuid.NewString(), ExecutionID: run, AgentKey: role, Message: "Check the authorized URL-import test fixture and report the evidence.", Model: "fixture", Provider: model, ToolLayer: catalog.Resolve, NonStreaming: true, MaxTurns: 8}, nil)
	if err != nil || !model.confirmed || !model.closed || model.probe.Scope != scope || model.probe.RunID != run {
		t.Fatalf("%s autonomous callback loop: confirmed=%v closed=%v scope=%+v err=%v", role, model.confirmed, model.closed, model.probe.Scope, err)
	}
	if p, err := s.get(ctx, 7, scope, sibling.ID); err != nil || p.State != "active" {
		t.Fatalf("closed unrelated probe: %+v %v", p, err)
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
