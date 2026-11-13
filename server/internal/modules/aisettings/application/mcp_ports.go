package application

import (
	"context"
	"time"
)

type MCPServer struct {
	Transport        string
	Command          string
	Args             []string
	EncryptedEnv     []byte
	ReplaceEnv       bool
	PreserveAgents   bool
	HasEnv           bool
	Tools            []MCPTool
	SkillGated       bool
	ID               string
	Name             string
	Endpoint         string
	Enabled          bool
	Insecure         bool
	EncryptedHeaders []byte
	HasHeaders       bool
	Agents           []string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
type MCPInput struct {
	Transport      string
	Command        string
	Args           []string
	Env            map[string]string
	ReplaceEnv     bool
	PreserveAgents bool
	Name           string
	Endpoint       string
	Enabled        bool
	Insecure       bool
	Headers        map[string]string
	ClearHeaders   bool
	Agents         []string
}
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema,omitempty"`
}

type MCPControlStore interface {
	CacheMCPTools(context.Context, int, string, []MCPTool) error
	MCPVisibility(context.Context, int, string) ([]string, error)
	SetMCPVisible(context.Context, int, string, string, bool) error
	SeedMCPDefaults(context.Context) error
}
type MCPStore interface {
	ListMCP(context.Context, int) ([]MCPServer, error)
	GetMCPForAdmin(context.Context, int, string) (*MCPServer, error)
	SaveMCP(context.Context, int, MCPServer, bool, bool) (*MCPServer, error)
	DeleteMCP(context.Context, int, string) error
	ResolveMCP(context.Context, int, string) ([]MCPServer, error)
}
