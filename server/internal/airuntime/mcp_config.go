package airuntime

import "context"

type MCPConfig struct {
	Transport  string
	Command    string
	Args       []string
	Env        map[string]string
	SkillGated bool
	ID         string
	Name       string
	Endpoint   string
	Insecure   bool
	Headers    map[string]string
}
type MCPResolver func(context.Context, int, string) ([]MCPConfig, error)
