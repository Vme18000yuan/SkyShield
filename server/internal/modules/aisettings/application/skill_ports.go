package application

import (
	"context"
	"github.com/Autumn-27/norma/skill"
	"time"
)

type SkillRecord struct {
	License       string
	Compatibility string
	MCPs          []string
	ID            string
	BuiltinKey    string
	Name          string
	Description   string
	Instructions  string
	Enabled       bool
	Agents        []string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type SkillRuntimeFileStore interface {
	RuntimeSkillFiles(context.Context, int, string, string) ([]SkillFile, error)
}
type SkillMaterializer func(context.Context, int, string, []skill.Skill) ([]skill.Skill, error)
type SkillStore interface {
	ListSkills(context.Context, int) ([]SkillRecord, error)
	SaveSkill(context.Context, int, SkillRecord, bool) (*SkillRecord, error)
	DeleteSkill(context.Context, int, string) error
	ResolveSkills(context.Context, int, string) ([]skill.Skill, error)
}

type BuiltinSkillSeeder interface {
	SeedBuiltinSkills(context.Context) error
}
