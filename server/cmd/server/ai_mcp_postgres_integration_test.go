package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	settingsapp "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	settingshandler "github.com/yyhuni/lunafox/server/internal/modules/aisettings/handler"
	settingsrepo "github.com/yyhuni/lunafox/server/internal/modules/aisettings/repository"
	settingsrouter "github.com/yyhuni/lunafox/server/internal/modules/aisettings/router"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type mcpFixtureProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *mcpFixtureProvider) Stream(_ context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	index := p.calls
	p.calls++
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Type: llm.SEMessageStart}, nil) {
			return
		}
		if index == 0 {
			present := false
			for _, item := range request.Tools {
				if item.Name == "SearchExtraTools" {
					present = true
				}
			}
			if !present {
				yield(llm.StreamEvent{}, errors.New("deferred MCP discovery unavailable"))
				return
			}
			yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "find-mcp", ToolName: "SearchExtraTools"}, nil)
			yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{"query":"echo"}`}, nil)
			yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"}, nil)
		} else if index == 1 {
			yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "call-mcp", ToolName: "ExecuteExtraTool"}, nil)
			yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{"tool_name":"mcp__fixture__echo","params":{"value":"hello"}}`}, nil)
			yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"}, nil)
		} else {
			yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "MCP 调用完成"}, nil)
			yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
		}
		yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
	}
}
func (p *mcpFixtureProvider) Complete(ctx context.Context, request llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, request) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyAIMCPPostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	t.Setenv("TIANQIONG_AI_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32))))
	var admin, analyst int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&analyst); err != nil {
		t.Fatal(err)
	}
	var callMu sync.Mutex
	calls := 0
	closed := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-mcp-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			callMu.Lock()
			closed++
			callMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if request.ID == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "fixture-session")
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "fixture echo", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}}}}
		case "tools/call":
			callMu.Lock()
			calls++
			callMu.Unlock()
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "fixture-mcp-result"}}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer fixture.Close()
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	settings := settingsapp.NewMCPFacade(settingsrepo.NewMCPStore(orm), airuntime.SecretBox{})
	input := settingsapp.MCPInput{Name: "fixture", Endpoint: fixture.URL, Enabled: true, Headers: map[string]string{"Authorization": "Bearer fixture-mcp-secret"}, Agents: []string{"assistant"}}
	if _, err := settings.Save(ctx, analyst, "", input); !errors.Is(err, settingsapp.ErrForbidden) {
		t.Fatalf("analyst saved MCP: %v", err)
	}
	item, err := settings.Save(ctx, admin, "", input)
	if err != nil {
		t.Fatal(err)
	}
	if !item.HasHeaders || len(item.EncryptedHeaders) != 0 {
		t.Fatalf("MCP exposed credentials: %+v", item)
	}
	var ciphertext []byte
	if err := db.QueryRowContext(ctx, `SELECT encrypted_headers FROM ai_mcp_server WHERE id=$1`, item.ID).Scan(&ciphertext); err != nil || strings.Contains(string(ciphertext), "fixture-mcp-secret") {
		t.Fatalf("MCP headers not encrypted: %v", err)
	}
	input.Headers = nil
	item, err = settings.Save(ctx, admin, item.ID, input)
	if err != nil || !item.HasHeaders {
		t.Fatalf("MCP update lost headers: %+v %v", item, err)
	}
	tools, err := settings.Test(ctx, admin, item.ID)
	if err != nil || len(tools) != 1 || tools[0].Name != "mcp__fixture__echo" {
		t.Fatalf("MCP discovery: %+v %v", tools, err)
	}
	invalid := input
	invalid.Endpoint = fixture.URL + "?token=secret"
	if _, err := settings.Save(ctx, admin, "", invalid); !errors.Is(err, settingsapp.ErrInvalidProfile) {
		t.Fatalf("credential-bearing URL accepted: %v", err)
	}
	invalid = input
	invalid.Headers = map[string]string{"Authorization": "good\r\nInjected: bad"}
	if _, err := settings.Save(ctx, admin, "", invalid); !errors.Is(err, settingsapp.ErrInvalidProfile) {
		t.Fatalf("invalid MCP header accepted: %v", err)
	}
	manager := auth.NewJWTManager("fixture-jwt-secret", time.Minute, time.Hour)
	adminToken, _, err := manager.GenerateAccessToken(admin, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	analystToken, _, err := manager.GenerateAccessToken(analyst, "ai-other", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	settingsrouter.RegisterAIMCPRoutes(group, settingshandler.NewMCPHandler(settings))
	request := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if r := request(http.MethodGet, "/v1/aiMcpServers", analystToken); r.Code != http.StatusForbidden {
		t.Fatalf("analyst listed MCP settings: %d", r.Code)
	}
	if r := request(http.MethodGet, "/v1/aiMcpServers", adminToken); r.Code != http.StatusOK || strings.Contains(r.Body.String(), "fixture-mcp-secret") {
		t.Fatalf("MCP list leaked secret or failed: %d %s", r.Code, r.Body.String())
	}
	if r := request(http.MethodPost, "/v1/aiMcpServers/"+item.ID+"/test", adminToken); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "mcp__fixture__echo") || strings.Contains(r.Body.String(), "fixture-mcp-secret") {
		t.Fatalf("MCP test endpoint: %d %s", r.Code, r.Body.String())
	}
	planner, err := settings.Resolve(ctx, analyst, "planner")
	if err != nil || len(planner) != 0 {
		t.Fatalf("unbound MCP visible: %+v %v", planner, err)
	}
	provider := &mcpFixtureProvider{}
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	facade := aiapp.NewConversationFacade(airepo.NewConversationStore(orm), runner, func(context.Context, int) (llm.Provider, string, bool, error) { return provider, "fixture", true, nil }, nil)
	facade.SetMCPAugmenter(airuntime.NewMCPAugmenter(settings.Resolve))
	jobCtx, cancel := context.WithCancel(ctx)
	facade.Start(jobCtx)
	defer func() { cancel(); <-facade.Done() }()
	conversation, err := facade.Create(ctx, admin, "MCP 测试")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facade.Send(ctx, admin, conversation.ID, "运行模拟工具"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		page, err := facade.Activities(ctx, admin, conversation.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !page.Running {
			found := false
			for _, activity := range page.Items {
				if strings.Contains(activity.Detail, "fixture-mcp-result") {
					found = true
				}
				if strings.Contains(activity.Detail, "fixture-mcp-secret") {
					t.Fatal("MCP credential leaked into activity")
				}
			}
			callMu.Lock()
			count, closes := calls, closed
			callMu.Unlock()
			if page.RunStatus != "completed" || !found || count != 1 || closes < 3 {
				t.Fatalf("MCP agent call: status=%s found=%v calls=%d closes=%d activity=%+v", page.RunStatus, found, count, closes, page.Items)
			}
			if err := settings.Delete(ctx, admin, item.ID); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("MCP run timed out for conversation %s", conversation.ID))
}
