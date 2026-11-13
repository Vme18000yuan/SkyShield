package repository

import (
	"context"
	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *SkillStore) RuntimeSkillFiles(ctx context.Context, user int, key, name string) ([]settings.SkillFile, error) {
	if err := requireAgentCatalogMember(ctx, s.db, user); err != nil {
		return nil, err
	}
	var id string
	err := s.db.WithContext(ctx).Raw(`SELECT s.id::text FROM ai_skill s JOIN ai_skill_agent a ON a.skill_id=s.id AND a.tenant_id=s.tenant_id WHERE s.tenant_id=? AND s.name=? AND a.agent_key=? AND s.enabled`, assets.DefaultTenantID, name, key).Row().Scan(&id)
	if err != nil {
		return nil, settings.ErrProfileNotFound
	}
	return readSkillPackageFiles(s.db.WithContext(ctx), id)
}
