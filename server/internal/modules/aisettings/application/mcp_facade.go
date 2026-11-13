package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"golang.org/x/net/http/httpguts"
)

var ErrMCPUnavailable = errors.New("MCP server connection failed")
var mcpNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type MCPFacade struct {
	connector airuntime.MCPConnector
	store     MCPStore
	codec     SecretCodec
}

func NewMCPFacade(store MCPStore, codec SecretCodec) *MCPFacade {
	return &MCPFacade{store: store, codec: codec}
}
func (f *MCPFacade) SetConnector(connect airuntime.MCPConnector) { f.connector = connect }
func mcpScope(id string) string                                  { return assetapp.DefaultTenantID + "/mcp/" + id }
func (f *MCPFacade) List(ctx context.Context, user int) ([]MCPServer, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	return f.store.ListMCP(ctx, user)
}
func (f *MCPFacade) Save(ctx context.Context, user int, id string, input MCPInput) (*MCPServer, error) {
	create := id == ""
	if create {
		id = uuid.NewString()
	}
	parsed, err := uuid.Parse(id)
	input.Name = strings.TrimSpace(input.Name)
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	input.Command = strings.TrimSpace(input.Command)
	if input.Transport == "" {
		input.Transport = "http"
	}
	if user <= 0 {
		return nil, ErrForbidden
	}
	if err != nil || parsed.String() != id || !mcpNamePattern.MatchString(input.Name) || len(input.Agents) > 200 || len(input.Env) > 128 || len(input.Args) > 200 || len(input.Command) > 2048 || strings.ContainsAny(input.Command, "\r\n\x00") {
		return nil, ErrInvalidProfile
	}
	if input.Transport == "stdio" {
		if input.Command == "" || len(input.Headers) > 0 {
			return nil, ErrInvalidProfile
		}
		input.Endpoint = ""
	} else if input.Transport == "http" || input.Transport == "sse" {
		if input.Endpoint != "" || input.Enabled {
			endpoint, urlErr := url.Parse(input.Endpoint)
			if urlErr != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.Fragment != "" || endpoint.RawQuery != "" || len(input.Endpoint) > 2048 {
				return nil, ErrInvalidProfile
			}
		}
		if input.ReplaceEnv {
			input.Headers = input.Env
			input.ClearHeaders = len(input.Headers) == 0
		}
		input.Command = ""
		input.Args = []string{}
	} else {
		return nil, ErrInvalidProfile
	}
	for _, argument := range input.Args {
		if len(argument) > 8192 || strings.ContainsRune(argument, 0) {
			return nil, ErrInvalidProfile
		}
	}
	seen := map[string]bool{}
	for _, key := range input.Agents {
		if !agentCatalogKeyPattern.MatchString(key) || seen[key] {
			return nil, ErrInvalidProfile
		}
		seen[key] = true
	}
	headers := map[string]string{}
	if len(input.Headers) > 32 || input.ClearHeaders && len(input.Headers) > 0 {
		return nil, ErrInvalidProfile
	}
	for key, value := range input.Headers {
		key = http.CanonicalHeaderKey(strings.TrimSpace(key))
		if !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(value) || len(value) > 8192 || len(key) > 128 || key == "Host" || key == "Content-Length" || key == "Transfer-Encoding" || key == "Connection" || key == "Accept" || key == "Content-Type" || key == "Mcp-Session-Id" || key == "Mcp-Protocol-Version" {
			return nil, ErrInvalidProfile
		}
		if _, ok := headers[key]; ok {
			return nil, ErrInvalidProfile
		}
		headers[key] = value
	}
	var encryptedHeaders, encryptedEnv []byte
	if len(headers) > 0 {
		raw, _ := json.Marshal(headers)
		encryptedHeaders, err = f.codec.Seal(mcpScope(id), string(raw))
		if err != nil {
			return nil, err
		}
	}
	if input.Transport == "stdio" && len(input.Env) > 0 {
		for key, value := range input.Env {
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(key) || len(value) > 16384 || strings.ContainsRune(value, 0) {
				return nil, ErrInvalidProfile
			}
		}
		raw, _ := json.Marshal(input.Env)
		encryptedEnv, err = f.codec.Seal(mcpScope(id)+"/env", string(raw))
		if err != nil {
			return nil, err
		}
	}
	return f.store.SaveMCP(ctx, user, MCPServer{ID: id, Name: input.Name, Endpoint: input.Endpoint, Enabled: input.Enabled, Insecure: input.Insecure, EncryptedHeaders: encryptedHeaders, Agents: input.Agents, Transport: input.Transport, Command: input.Command, Args: input.Args, EncryptedEnv: encryptedEnv, ReplaceEnv: input.ReplaceEnv, PreserveAgents: input.PreserveAgents}, create, input.ClearHeaders)
}

func (f *MCPFacade) Delete(ctx context.Context, user int, id string) error {
	parsed, err := uuid.Parse(id)
	if user <= 0 {
		return ErrForbidden
	}
	if err != nil || parsed.String() != id {
		return ErrInvalidProfile
	}
	return f.store.DeleteMCP(ctx, user, id)
}
func (f *MCPFacade) decode(item MCPServer) (airuntime.MCPConfig, error) {
	config := airuntime.MCPConfig{ID: item.ID, Name: item.Name, Endpoint: item.Endpoint, Insecure: item.Insecure, Headers: map[string]string{}, Transport: item.Transport, Command: item.Command, Args: item.Args, Env: map[string]string{}, SkillGated: item.SkillGated}
	if len(item.EncryptedHeaders) > 0 {
		raw, err := f.codec.Open(mcpScope(item.ID), item.EncryptedHeaders)
		if err != nil {
			return config, err
		}
		if err := json.Unmarshal([]byte(raw), &config.Headers); err != nil {
			return config, err
		}
	}
	if len(item.EncryptedEnv) > 0 {
		raw, err := f.codec.Open(mcpScope(item.ID)+"/env", item.EncryptedEnv)
		if err != nil {
			return config, err
		}
		if err = json.Unmarshal([]byte(raw), &config.Env); err != nil {
			return config, err
		}
	}
	return config, nil
}
func (f *MCPFacade) Resolve(ctx context.Context, user int, key string) ([]airuntime.MCPConfig, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	if !agentCatalogKeyPattern.MatchString(key) {
		return nil, ErrInvalidProfile
	}
	items, err := f.store.ResolveMCP(ctx, user, key)
	if err != nil {
		return nil, err
	}
	out := make([]airuntime.MCPConfig, 0, len(items))
	for _, item := range items {
		config, err := f.decode(item)
		if err != nil {
			return nil, err
		}
		out = append(out, config)
	}
	return out, nil
}
func (f *MCPFacade) Test(ctx context.Context, user int, id string) ([]MCPTool, error) {
	parsed, err := uuid.Parse(id)
	if user <= 0 {
		return nil, ErrForbidden
	}
	if err != nil || parsed.String() != id {
		return nil, ErrInvalidProfile
	}
	item, err := f.store.GetMCPForAdmin(ctx, user, id)
	if err != nil {
		return nil, err
	}
	config, err := f.decode(*item)
	if err != nil {
		return nil, err
	}
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	connect := f.connector
	if connect == nil {
		connect = airuntime.NewMCPConnector("")
	}
	client, err := connect(testCtx, user, config)
	if err != nil {
		return nil, ErrMCPUnavailable
	}
	defer client.Close()
	tools, err := client.Tools(testCtx)
	if err != nil {
		return nil, ErrMCPUnavailable
	}
	if len(tools) > 200 {
		return nil, ErrMCPUnavailable
	}
	out := make([]MCPTool, 0, len(tools))
	for _, tool := range tools {
		if !airuntime.ValidMCPTool(tool) {
			continue
		}
		name, desc := tool.Name(), tool.Description()
		if len(name) > 256 {
			name = name[:256]
		}
		if len(desc) > 1024 {
			desc = desc[:1024]
		}
		out = append(out, MCPTool{Name: name, Description: desc, Schema: tool.InputSchema()})
	}
	if control, ok := f.store.(MCPControlStore); ok {
		if err := control.CacheMCPTools(ctx, user, id, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}
