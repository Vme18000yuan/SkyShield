package repository

import (
	"context"
	"encoding/json"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

func (s *TaskIntelligenceStore) StreamAsset(ctx context.Context, user int, assetID, intelID int64, visit func(aiapp.TaskIntelligence) error) error {
	rows, err := s.db.WithContext(ctx).Raw(`SELECT i.id,i.task_id,i.node_id,i.kind,i.title,i.content,i.evidence,i.confidence,i.status,i.asset_ids,i.worker,i.created_at,i.updated_at,i.fingerprint,i.sealed_payload,FALSE AS inherited,job.sealed_analysis,COALESCE(job.status,''),COALESCE(job.metadata,'{}'::jsonb),i.credential_analysis,
      COALESCE((SELECT 'intent:'||n.id::text FROM tianqiong_ai.exploration_nodes n WHERE n.id=i.node_id AND n.exploration_id=t.exploration_id AND n.kind='intent'),(SELECT 'intent:'||min(n.id)::text FROM tianqiong_ai.exploration_edges edge JOIN tianqiong_ai.exploration_nodes n ON n.id=edge.src_id AND n.kind='intent' WHERE edge.dst_id=i.node_id AND edge.rel='yields' AND edge.exploration_id=t.exploration_id AND n.exploration_id=t.exploration_id HAVING count(*)=1),CASE WHEN i.worker='planner' THEN 'plan' ELSE '' END),t.name
      FROM tianqiong_ai.task_intelligence i JOIN ai_task t ON t.id=i.task_id JOIN tenant_membership m ON m.tenant_id=t.tenant_id AND m.user_id=t.owner_user_id JOIN auth_user u ON u.id=m.user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN asset_entity asset ON asset.id=? AND asset.tenant_id=t.tenant_id LEFT JOIN ai_credential_job job ON job.intelligence_id=i.id
      WHERE t.tenant_id=?::uuid AND t.owner_user_id=? AND t.deleted_at IS NULL AND u.is_active AND tenant.status='active' AND (t.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target x WHERE x.id=t.target_id AND x.deleted_at IS NULL)) AND i.asset_ids @> jsonb_build_array(?::bigint) AND (?=0 OR i.id=?)
      AND (t.target_id IS NULL OR
       EXISTS(SELECT 1 FROM asset_website_binding b JOIN website x ON x.id=b.website_id WHERE b.asset_id=asset.id AND x.target_id=t.target_id) OR
       EXISTS(SELECT 1 FROM asset_endpoint_binding b JOIN endpoint x ON x.id=b.endpoint_id WHERE b.asset_id=asset.id AND x.target_id=t.target_id) OR
       EXISTS(SELECT 1 FROM asset_subdomain_binding b JOIN subdomain x ON x.id=b.subdomain_id WHERE b.asset_id=asset.id AND x.target_id=t.target_id) OR
       EXISTS(SELECT 1 FROM asset_host_port_binding b JOIN host_port_mapping x ON x.id=b.host_port_id WHERE b.asset_id=asset.id AND x.target_id=t.target_id) OR
       EXISTS(SELECT 1 FROM asset_observation o WHERE o.asset_id=asset.id AND o.tenant_id=t.tenant_id AND o.target_id=t.target_id) OR
       EXISTS(SELECT 1 FROM asset_application_visible_website app JOIN website x ON x.id=app.website_id WHERE app.application_id=asset.id AND app.tenant_id=t.tenant_id AND x.target_id=t.target_id) OR
       EXISTS(SELECT 1 FROM asset_root_domain_target root JOIN target x ON x.id=root.target_id AND x.deleted_at IS NULL WHERE root.tenant_id=t.tenant_id AND root.root_asset_id=asset.id AND root.target_id=t.target_id AND x.type='domain' AND x.name=asset.label AND asset.asset_type='root_domain'))
      ORDER BY i.id DESC`, assetID, assets.DefaultTenantID, user, assetID, intelID, intelID).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item aiapp.TaskIntelligence
		var assetJSON []byte
		if err = rows.Scan(&item.ID, &item.TaskID, &item.NodeID, &item.Kind, &item.Title, &item.Content, &item.Evidence, &item.Confidence, &item.Status, &assetJSON, &item.Worker, &item.CreatedAt, &item.UpdatedAt, &item.Fingerprint, &item.Sealed, &item.Inherited, &item.AnalysisSealed, &item.CredentialStatus, &item.CredentialMetadata, &item.LegacyAnalysis, &item.SourceSession, &item.TaskName); err != nil {
			return err
		}
		if err = json.Unmarshal(assetJSON, &item.AssetIDs); err != nil {
			return err
		}
		if err = visit(item); err != nil {
			return err
		}
	}
	return rows.Err()
}
