package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

type AssetRegistrationStore struct {
	db       *gorm.DB
	creators assetapp.LegacyAssetCreators
}

func NewAssetRegistrationStore(db *gorm.DB, creators assetapp.LegacyAssetCreators) *AssetRegistrationStore {
	return &AssetRegistrationStore{db: db, creators: creators}
}

func (s *AssetRegistrationStore) RegisterAsset(ctx context.Context, command assetapp.RegistrationCommand) (assetapp.RegisteredAsset, error) {
	result := assetapp.RegisteredAsset{Type: command.Asset.Input.Type, Projection: command.Asset.Projection}
	err := dbtx.WithinTransaction(ctx, s.db, func(_ context.Context, tx *gorm.DB) error {
		var role string
		err := tx.Raw(`SELECT m.role FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
			WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE FOR SHARE OF m,u`, assetapp.DefaultTenantID, command.UserID).Row().Scan(&role)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && role != "admin" && role != "analyst") {
			return assetapp.ErrAssetWriteForbidden
		}
		if err != nil {
			return err
		}
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?,0))`, assetapp.DefaultTenantID+"/"+command.Origin+"/"+command.SourceKey).Error; err != nil {
			return err
		}
		var previousID int64
		var previousHash string
		err = tx.Raw(`SELECT asset_id,request_fingerprint FROM asset_observation WHERE tenant_id=? AND origin=? AND source_key=?`, assetapp.DefaultTenantID, command.Origin, command.SourceKey).Row().Scan(&previousID, &previousHash)
		if err == nil {
			if previousHash != command.Fingerprint {
				return assetapp.ErrAssetRequestConflict
			}
			result.ID = previousID
			return linkTaskAsset(tx, command, previousID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		input := command.Asset.Input
		if input.TargetID > 0 {
			var target assetdomain.TargetRef
			err := tx.Raw(`SELECT id,name,type FROM target WHERE id=? AND deleted_at IS NULL FOR UPDATE`, input.TargetID).Row().Scan(&target.ID, &target.Name, &target.Type)
			if errors.Is(err, sql.ErrNoRows) {
				return assetapp.ErrAssetTargetNotFound
			}
			if err != nil {
				return err
			}
			if !assetapp.RegistrationMatchesTarget(command.Asset, target) {
				return assetapp.ErrAssetOutsideTarget
			}
		}
		if input.OrganizationID > 0 {
			var id int
			if err := tx.Raw(`SELECT id FROM organization WHERE id=? AND deleted_at IS NULL FOR SHARE`, input.OrganizationID).Row().Scan(&id); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return assetapp.ErrAssetTargetNotFound
				}
				return err
			}
		}
		txCtx := dbtx.WithTransaction(ctx, tx)
		legacyID, err := s.projectLegacyAsset(txCtx, tx, command.Asset)
		if err != nil {
			return err
		}
		if err := tx.Raw(`SELECT tq_register_asset_v2(?::uuid,?,?::jsonb::text,?,?,?,CURRENT_TIMESTAMP)`,
			assetapp.DefaultTenantID, input.Type, string(command.Asset.Identity), command.Asset.Label, command.Asset.Method, command.Asset.RecordType).Row().Scan(&result.ID); err != nil {
			return err
		}
		if input.TargetID == 0 {
			if err := tx.Exec(`UPDATE asset_entity SET is_manual=TRUE WHERE tenant_id=? AND id=?`, assetapp.DefaultTenantID, result.ID).Error; err != nil {
				return err
			}
		}
		if command.Asset.Projection == "endpoint" && command.Asset.Method != nil {
			if err := tx.Exec(`INSERT INTO asset_endpoint_variant_binding(endpoint_id,asset_id) VALUES(?,?) ON CONFLICT DO NOTHING`, legacyID, result.ID).Error; err != nil {
				return err
			}
		}
		if command.Asset.Projection == "subdomain" && command.Asset.RecordType != nil {
			if err := tx.Exec(`INSERT INTO asset_subdomain_variant_binding(subdomain_id,asset_id) VALUES(?,?) ON CONFLICT DO NOTHING`, legacyID, result.ID).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec(`INSERT INTO asset_observation(tenant_id,asset_id,target_id,original_target_id,organization_id,created_by,origin,source_key,request_fingerprint,payload,encrypted_auth)
			VALUES(?,?,NULLIF(?,0),NULLIF(?,0),NULLIF(?,0),?,?,?,?,?::jsonb,?)`, assetapp.DefaultTenantID, result.ID, input.TargetID, input.TargetID, input.OrganizationID, command.UserID,
			command.Origin, command.SourceKey, command.Fingerprint, string(command.Payload), command.EncryptedAuth).Error; err != nil {
			return err
		}
		return linkTaskAsset(tx, command, result.ID)
	})
	return result, err
}

// Link the canonical asset and its original projection in the same transaction.
// The database fence checks membership, target, active run and lease again;
// retries must pass that fence too, even when the observation already exists.
func linkTaskAsset(tx *gorm.DB, command assetapp.RegistrationCommand, assetID int64) error {
	scope := command.Task
	if scope == nil {
		return nil
	}
	if err := tx.Exec(`SELECT set_config('tianqiong.task_id',?,TRUE),set_config('tianqiong.owner_user_id',?,TRUE),set_config('tianqiong.execution_id',?,TRUE),set_config('tianqiong.execution_mode',tianqiong_ai.task_run_mode(?::bigint,?::uuid),TRUE)`,
		strconv.FormatInt(scope.TaskID, 10), strconv.Itoa(command.UserID), scope.ExecutionID, scope.TaskID, scope.ExecutionID).Error; err != nil {
		return err
	}
	var checked int64
	if err := tx.Raw(`SELECT id FROM tianqiong_ai.assert_task_write(?,?)`, scope.ExplorationID, assetID).Row().Scan(&checked); err != nil {
		return err
	}
	if checked != scope.TaskID {
		return errors.New("task attribution mismatch")
	}
	var node int64
	query := `SELECT id FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND kind='fact' AND state='origin' ORDER BY id LIMIT 1`
	args := []any{scope.ExplorationID}
	if scope.IntentID > 0 {
		query = `SELECT id FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=? AND kind='intent' AND state='running'`
		args = append(args, scope.IntentID)
	}
	if err := tx.Raw(query, args...).Row().Scan(&node); err != nil {
		return err
	}
	if err := tx.Exec(`INSERT INTO ai_task_asset(tenant_id,task_id,asset_id,source) VALUES(?,?,?,'discovered') ON CONFLICT DO NOTHING`, assetapp.DefaultTenantID, scope.TaskID, assetID).Error; err != nil {
		return err
	}
	if err := tx.Exec(`INSERT INTO tianqiong_ai.exploration_anchors(node_id,asset_id) VALUES(?,?) ON CONFLICT DO NOTHING`, node, assetID).Error; err != nil {
		return err
	}
	return addDiscoveredTaskScope(tx, command)
}

func (s *AssetRegistrationStore) projectLegacyAsset(ctx context.Context, tx *gorm.DB, asset assetapp.PreparedAsset) (int, error) {
	input := asset.Input
	var creator assetapp.LegacyAssetCreator
	var table, column, value string
	switch asset.Projection {
	case "website":
		creator = s.creators.Websites
		table, column, value = "website", "url", input.URL
	case "endpoint":
		creator = s.creators.Endpoints
		table, column, value = "endpoint", "url", input.URL
	case "subdomain":
		creator = s.creators.Subdomains
		table, column, value = "subdomain", "dns_name", asset.Label
	default:
		return 0, nil
	}
	if creator == nil {
		return 0, errors.New("legacy asset projection is unavailable")
	}
	if _, err := creator.BatchCreateContext(ctx, input.TargetID, []string{value}); err != nil {
		return 0, err
	}
	if asset.Projection == "website" || asset.Projection == "endpoint" {
		observation := assetdomain.SparseWebObservation{Title: input.PageTitle, Webserver: input.Webserver, Technologies: input.Technologies, StatusCode: input.StatusCode, ContentLength: input.ContentLength}
		if asset.Projection == "endpoint" && asset.Method != nil {
			observation.Title = ""
			observation.Webserver = ""
			observation.StatusCode = nil
			observation.ContentLength = nil
		}
		if observation.Title != "" || observation.Webserver != "" || len(observation.Technologies) > 0 || observation.StatusCode != nil || observation.ContentLength != nil {
			merger, ok := creator.(assetapp.LegacyWebObservationMerger)
			if !ok {
				return 0, errors.New("legacy web observation merge is unavailable")
			}
			if err := merger.MergeWebObservationContext(ctx, input.TargetID, value, observation); err != nil {
				return 0, err
			}
		}
	}
	var id int
	err := tx.Raw(fmt.Sprintf(`SELECT id FROM %s WHERE target_id=? AND %s=?`, table, column), input.TargetID, value).Row().Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, assetapp.ErrAssetOutsideTarget
	}
	return id, err
}
