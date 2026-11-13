package repository

import (
	"context"
	"github.com/lib/pq"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

// The graph only needs canonical identities. Avoid the inventory list's per-row
// provenance counts, timestamp aggregates and organization JSON for thousands of nodes.
func (s *AssetRegistryStore) ListRootCoverageAssets(ctx context.Context, q assetapp.AssetQuery) ([]assetapp.AssetEntity, error) {
	filter, args := assetFilters(q, false)
	var items []assetapp.AssetEntity
	// The expanded visibility views produce a high estimated cost even for a
	// small root. Avoid LLVM compilation for this bounded interactive read.
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SET LOCAL jit=off`).Error; err != nil {
			return err
		}
		return tx.Raw(organizationAssetCTE+`SELECT e.id,e.asset_type,e.label FROM asset_entity e WHERE `+filter+` ORDER BY e.id DESC LIMIT ?`, append(args, q.Limit)...).Scan(&items).Error
	})
	return items, err
}

var _ assetapp.RootCoverageStore = (*AssetRegistryStore)(nil)

func (s *AssetRegistryStore) ListRootCoveragePorts(ctx context.Context, q assetapp.PortQuery) ([]assetapp.PortAsset, error) {
	base, args := portSources(q, false)
	var rows []struct {
		Label   string
		Port    int
		Service string
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SET LOCAL jit=off`).Error; err != nil {
			return err
		}
		return tx.Raw(base+`SELECT label,substring(label FROM ':([0-9]+)$')::integer AS port,COALESCE(string_agg(DISTINCT NULLIF(observed_service,''), ', '),'') AS service FROM port_sources GROUP BY label ORDER BY label LIMIT ?`, append(args, q.Limit)...).Scan(&rows).Error
	})
	items := make([]assetapp.PortAsset, 0, len(rows))
	for _, row := range rows {
		items = append(items, assetapp.PortAsset{Label: row.Label, Port: row.Port, Service: row.Service})
	}
	return items, err
}

const rootCoverageTasks = `WITH readable_tasks AS (
 SELECT t.id,t.exploration_id,t.name,t.coverage_enabled FROM ai_task t
 JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id
 JOIN tenant tenant ON tenant.id=t.tenant_id AND tenant.status='active'
 JOIN auth_user u ON u.id=m.user_id AND u.is_active
 WHERE t.tenant_id=? AND t.owner_user_id=? AND t.deleted_at IS NULL
 AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=t.target_id AND deleted_at IS NULL))
 AND NOT EXISTS(SELECT 1 FROM ai_task_archive a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))
) `

func (s *AssetRegistryStore) RootCoverageTested(ctx context.Context, tenant string, user int, ids []int64) (map[int64]bool, error) {
	var rows []struct{ AssetID int64 }
	err := s.db.WithContext(ctx).Raw(rootCoverageTasks+`SELECT DISTINCT a.asset_id FROM readable_tasks t JOIN tianqiong_ai.exploration_nodes n ON n.exploration_id=t.exploration_id JOIN tianqiong_ai.exploration_anchors a ON a.node_id=n.id WHERE t.coverage_enabled AND n.kind='fact' AND n.state='confirmed' AND a.asset_id=ANY(?::bigint[])`, tenant, user, pq.Int64Array(ids)).Scan(&rows).Error
	out := map[int64]bool{}
	for _, row := range rows {
		out[row.AssetID] = true
	}
	return out, err
}
func (s *AssetRegistryStore) RootCoverageReferences(ctx context.Context, tenant string, user int, asset int64) (assetapp.RootCoverageRefs, error) {
	out := assetapp.RootCoverageRefs{Intents: []assetapp.RootCoverageRef{}, Facts: []assetapp.RootCoverageRef{}, Findings: []assetapp.RootCoverageRef{}}
	var rows []assetapp.RootCoverageRef
	err := s.db.WithContext(ctx).Raw(rootCoverageTasks+`SELECT n.id,n.kind,n.state,COALESCE(NULLIF(n.payload->>'summary',''),n.payload->>'text','') AS summary,t.id AS source_task_id,t.name AS task_name FROM readable_tasks t JOIN tianqiong_ai.exploration_nodes n ON n.exploration_id=t.exploration_id JOIN tianqiong_ai.exploration_anchors a ON a.node_id=n.id WHERE a.asset_id=? AND n.kind IN ('intent','fact','finding') AND NOT (n.kind='fact' AND n.state='origin') ORDER BY n.id DESC LIMIT 201`, tenant, user, asset).Scan(&rows).Error
	if err != nil {
		return out, err
	}
	if len(rows) > 200 {
		out.Truncated = true
		rows = rows[:200]
	}
	for _, row := range rows {
		switch row.Kind {
		case "intent":
			out.Intents = append(out.Intents, row)
		case "fact":
			out.Facts = append(out.Facts, row)
		case "finding":
			out.Findings = append(out.Findings, row)
		}
	}
	return out, nil
}
