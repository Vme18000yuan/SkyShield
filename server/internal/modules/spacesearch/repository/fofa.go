package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	spaceapp "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/application"
	"github.com/yyhuni/lunafox/server/internal/modules/spacesearch/repository/persistence"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

type FofaStore struct{ db *gorm.DB }

func NewFofaStore(db *gorm.DB) *FofaStore { return &FofaStore{db: db} }
func fofaAccess(tx *gorm.DB, user int) (spaceapp.Access, error) {
	var role string
	var superuser bool
	err := tx.Raw(`SELECT m.role,u.is_superuser FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id
 WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE FOR SHARE OF m,u`, registryapp.DefaultTenantID, user).Row().Scan(&role, &superuser)
	if errors.Is(err, sql.ErrNoRows) {
		return spaceapp.Access{}, spaceapp.ErrForbidden
	}
	if err != nil {
		return spaceapp.Access{}, err
	}
	return spaceapp.Access{CanSearch: role == "admin" || role == "analyst", CanManage: role == "admin" && superuser}, nil
}
func (s *FofaStore) Access(ctx context.Context, user int) (spaceapp.Access, error) {
	return fofaAccess(s.db.WithContext(ctx), user)
}
func (s *FofaStore) GetSettings(ctx context.Context, user int) (*spaceapp.Settings, error) {
	var settings *spaceapp.Settings
	err := dbtx.WithinTransaction(ctx, s.db, func(_ context.Context, tx *gorm.DB) error {
		if _, err := fofaAccess(tx, user); err != nil {
			return err
		}
		item := persistence.FofaSetting{}
		err := tx.Raw(`SELECT email,encrypted_api_key,updated_at FROM fofa_setting WHERE tenant_id=?`, registryapp.DefaultTenantID).Row().Scan(&item.Email, &item.EncryptedAPIKey, &item.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err == nil {
			settings = &spaceapp.Settings{Email: item.Email, EncryptedKey: item.EncryptedAPIKey, UpdatedAt: item.UpdatedAt}
		}
		return err
	})
	return settings, err
}
func (s *FofaStore) SaveSettings(ctx context.Context, user int, item spaceapp.Settings, clear bool) error {
	return dbtx.WithinTransaction(ctx, s.db, func(_ context.Context, tx *gorm.DB) error {
		access, err := fofaAccess(tx, user)
		if err != nil {
			return err
		}
		if !access.CanManage {
			return spaceapp.ErrForbidden
		}
		if clear {
			return tx.Exec(`DELETE FROM fofa_setting WHERE tenant_id=?`, registryapp.DefaultTenantID).Error
		}
		if len(item.EncryptedKey) == 0 {
			result := tx.Exec(`UPDATE fofa_setting SET email=?,updated_by=?,updated_at=clock_timestamp() WHERE tenant_id=?`, item.Email, user, registryapp.DefaultTenantID)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return spaceapp.ErrNotConfigured
			}
			return nil
		}
		return tx.Exec(`INSERT INTO fofa_setting(tenant_id,email,encrypted_api_key,updated_by) VALUES(?,?,?,?)
 ON CONFLICT(tenant_id) DO UPDATE SET email=EXCLUDED.email,encrypted_api_key=EXCLUDED.encrypted_api_key,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()`, registryapp.DefaultTenantID, item.Email, item.EncryptedKey, user).Error
	})
}
func (s *FofaStore) SaveSearch(ctx context.Context, user int, page spaceapp.SearchPage) error {
	return dbtx.WithinTransaction(ctx, s.db, func(_ context.Context, tx *gorm.DB) error {
		access, err := fofaAccess(tx, user)
		if err != nil {
			return err
		}
		if !access.CanSearch {
			return spaceapp.ErrForbidden
		}
		raw, err := json.Marshal(page.Results)
		if err != nil {
			return err
		}
		if err = tx.Exec(`INSERT INTO fofa_search(id,tenant_id,owner_user_id,query,page,page_size,full_history,total,records,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?::jsonb,?,?)`, page.ID, registryapp.DefaultTenantID, user, page.Query, page.Page, page.PageSize, page.Full, page.Total, string(raw), page.CreatedAt, page.ExpiresAt).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM fofa_search WHERE id IN (SELECT id FROM fofa_search WHERE tenant_id=? AND expires_at<clock_timestamp()-INTERVAL '24 hours' ORDER BY expires_at LIMIT 100)`, registryapp.DefaultTenantID).Error
	})
}
func (s *FofaStore) ImportRecord(ctx context.Context, selection spaceapp.ImportSelection, writer func(context.Context, spaceapp.Record, string) (spaceapp.ImportRowResult, error)) (spaceapp.ImportRowResult, error) {
	var result spaceapp.ImportRowResult
	err := dbtx.WithinTransaction(ctx, s.db, func(txCtx context.Context, tx *gorm.DB) error {
		access, err := fofaAccess(tx, selection.UserID)
		if err != nil {
			return err
		}
		if !access.CanSearch {
			return spaceapp.ErrForbidden
		}
		var query string
		var raw []byte
		err = tx.Raw(`SELECT query,records FROM fofa_search WHERE id=? AND tenant_id=? AND owner_user_id=? AND expires_at>clock_timestamp() FOR SHARE`, selection.SearchID, registryapp.DefaultTenantID, selection.UserID).Row().Scan(&query, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return spaceapp.ErrSearchNotFound
		}
		if err != nil {
			return err
		}
		var records []spaceapp.Record
		if err = json.Unmarshal(raw, &records); err != nil {
			return err
		}
		if selection.RowID < 0 || selection.RowID >= len(records) || !records[selection.RowID].Importable {
			return spaceapp.ErrInvalidInput
		}
		var id int
		if err = tx.Raw(`SELECT id FROM organization WHERE id=? AND deleted_at IS NULL FOR SHARE`, selection.OrganizationID).Row().Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return spaceapp.ErrDestination
			}
			return err
		}
		if selection.TargetID > 0 {
			if err = tx.Raw(`SELECT id FROM target WHERE id=? AND deleted_at IS NULL FOR SHARE`, selection.TargetID).Row().Scan(&id); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return spaceapp.ErrDestination
				}
				return err
			}
		}
		lock := fmt.Sprintf("fofa/%s/%d/%d/%d", selection.SearchID, selection.RowID, selection.OrganizationID, selection.TargetID)
		if err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?,0))`, lock).Error; err != nil {
			return err
		}
		var previous []byte
		err = tx.Raw(`SELECT result FROM fofa_import WHERE search_id=? AND row_index=? AND organization_id=? AND requested_target_id=?`, selection.SearchID, selection.RowID, selection.OrganizationID, selection.TargetID).Row().Scan(&previous)
		if err == nil {
			if err = json.Unmarshal(previous, &result); err != nil {
				return err
			}
			if result.TargetID > 0 {
				var active bool
				if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM target WHERE id=? AND deleted_at IS NULL)`, result.TargetID).Row().Scan(&active); err != nil {
					return err
				}
				if !active {
					return spaceapp.ErrDestination
				}
			}
			result.Reused = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		result, err = writer(txCtx, records[selection.RowID], query)
		if err != nil {
			return err
		}
		result.RowID = selection.RowID
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO fofa_import(tenant_id,search_id,owner_user_id,row_index,organization_id,requested_target_id,result) VALUES(?,?,?,?,?,?,?::jsonb)`, registryapp.DefaultTenantID, selection.SearchID, selection.UserID, selection.RowID, selection.OrganizationID, selection.TargetID, string(encoded)).Error
	})
	return result, err
}
