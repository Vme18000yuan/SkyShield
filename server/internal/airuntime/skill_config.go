package airuntime

import (
	"context"
	"github.com/Autumn-27/norma/skill"
	"github.com/Autumn-27/norma/tool"
)

type SkillResolver func(context.Context, int, string) ([]skill.Skill, error)

// AppendSkills preserves Norma's Skill invocation and separate guidance message.
func AppendSkills(tools []tool.CoreTool, skills []skill.Skill) []tool.CoreTool {
	if len(skills) == 0 {
		return tools
	}
	return append(tools, skill.NewRegistry(skills...).Tool())
}
