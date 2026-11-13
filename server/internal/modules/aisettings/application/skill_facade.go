package application

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Autumn-27/norma/skill"
	"github.com/google/uuid"
)

type SkillFacade struct {
	store       SkillStore
	materialize SkillMaterializer
}

func (f *SkillFacade) SetMaterializer(materialize SkillMaterializer) { f.materialize = materialize }

var skillAgentKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func NewSkillFacade(store SkillStore) *SkillFacade { return &SkillFacade{store: store} }
func (f *SkillFacade) List(ctx context.Context, user int) ([]SkillRecord, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	return f.store.ListSkills(ctx, user)
}

func (f *SkillFacade) SeedBuiltinSkills(ctx context.Context) error {
	seeder, ok := f.store.(BuiltinSkillSeeder)
	if !ok {
		return errors.New("AI skill store does not support built-in skill bootstrap")
	}
	return seeder.SeedBuiltinSkills(ctx)
}

func (f *SkillFacade) Save(ctx context.Context, user int, id string, item SkillRecord) (*SkillRecord, error) {
	create := id == ""
	if create {
		id = uuid.NewString()
	}
	parsed, err := uuid.Parse(id)
	item.Name = strings.TrimSpace(item.Name)
	item.Description = strings.TrimSpace(item.Description)
	item.Instructions = strings.TrimSpace(item.Instructions)
	if user <= 0 {
		return nil, ErrForbidden
	}
	if err != nil || parsed.String() != id || !validSkillPackageName(item.Name) || utf8.RuneCountInString(item.Name) > 120 || strings.ContainsAny(item.Name, "\r\n") || len(item.Description) > 4096 || item.Instructions == "" || len(item.Instructions) > 65536 || len(item.Agents) > 12 {
		return nil, ErrInvalidProfile
	}
	seen := map[string]bool{}
	for _, agent := range item.Agents {
		if !skillAgentKeyPattern.MatchString(agent) || seen[agent] {
			return nil, ErrInvalidProfile
		}
		seen[agent] = true
	}
	if !create {
		current, listErr := f.store.ListSkills(ctx, user)
		if listErr != nil {
			return nil, listErr
		}
		found := false
		for _, existing := range current {
			if existing.ID != id {
				continue
			}
			found = true
			item.BuiltinKey = existing.BuiltinKey
			item.License, item.Compatibility, item.MCPs = existing.License, existing.Compatibility, existing.MCPs
			break
		}
		if !found {
			return nil, ErrProfileNotFound
		}
	}
	item.ID = id
	return f.store.SaveSkill(ctx, user, item, create)
}
func (f *SkillFacade) Delete(ctx context.Context, user int, id string) error {
	parsed, err := uuid.Parse(id)
	if user <= 0 {
		return ErrForbidden
	}
	if err != nil || parsed.String() != id {
		return ErrInvalidProfile
	}
	return f.store.DeleteSkill(ctx, user, id)
}
func (f *SkillFacade) Resolve(ctx context.Context, user int, key string) ([]skill.Skill, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	if !skillAgentKeyPattern.MatchString(key) {
		return nil, ErrInvalidProfile
	}
	items, err := f.store.ResolveSkills(ctx, user, key)
	if err != nil {
		return nil, err
	}
	if f.materialize != nil {
		return f.materialize(ctx, user, key, items)
	}
	return items, nil
}
