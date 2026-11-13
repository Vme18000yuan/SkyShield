package repository

import (
	"database/sql"
	"errors"

	"github.com/lib/pq"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

// enqueueManualApplicationSites replays prior manual associations and expands
// each association into bounded, resumable website-recheck events.
func enqueueManualApplicationSites(tx *gorm.DB, limit int) (bool, error) {
	worked := false
	var lastObservation int64
	err := tx.Raw(`SELECT last_observation_id FROM asset_application_projection_progress WHERE id=TRUE AND observations_completed=FALSE FOR UPDATE SKIP LOCKED`).Row().Scan(&lastObservation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil {
		worked = true
		var observations []struct{ ID int64 }
		if e := tx.Raw(`SELECT id FROM asset_observation WHERE id>? AND payload->>'type'='app' AND jsonb_array_length(CASE WHEN jsonb_typeof(payload->'boundDomains')='array' THEN payload->'boundDomains' ELSE '[]'::jsonb END)>0 ORDER BY id LIMIT ?`, lastObservation, limit).Scan(&observations).Error; e != nil {
			return false, e
		}
		next := lastObservation
		for _, item := range observations {
			next = item.ID
			if e := tx.Exec(`INSERT INTO asset_application_manual_queue(observation_id) VALUES(?) ON CONFLICT(observation_id) DO NOTHING`, item.ID).Error; e != nil {
				return false, e
			}
		}
		if e := tx.Exec(`UPDATE asset_application_projection_progress SET last_observation_id=?,observations_completed=?,updated_at=CURRENT_TIMESTAMP WHERE id=TRUE`, next, len(observations) < limit).Error; e != nil {
			return false, e
		}
	}
	var event struct{ ID, ObservationID, LastWebsiteID int64 }
	err = tx.Raw(`SELECT id,observation_id,last_website_id FROM asset_application_manual_queue ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`).Row().Scan(&event.ID, &event.ObservationID, &event.LastWebsiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return worked, nil
	}
	if err != nil {
		return false, err
	}
	worked = true
	var domains []string
	if err := tx.Raw(`SELECT jsonb_array_elements_text(CASE WHEN jsonb_typeof(payload->'boundDomains')='array' THEN payload->'boundDomains' ELSE '[]'::jsonb END) FROM asset_observation WHERE id=?`, event.ObservationID).Scan(&domains).Error; err != nil {
		return false, err
	}
	seen := map[string]bool{}
	roots := make([]string, 0, len(domains))
	for _, domain := range domains {
		if root, ok := assetapp.RegistrableRootDomain("subdomain", domain); ok && !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	var sites []struct{ ID int64 }
	if len(roots) > 0 {
		if err := tx.Raw(`SELECT DISTINCT w.id FROM asset_observation obs JOIN asset_entity a ON a.id=obs.asset_id AND a.tenant_id=obs.tenant_id AND a.asset_type='app'
	 JOIN website w ON w.id>? JOIN target t ON t.id=w.target_id AND t.deleted_at IS NULL
	 LEFT JOIN target original_target ON original_target.id=obs.target_id
	 WHERE obs.id=? AND (obs.original_target_id IS NULL OR original_target.id IS NOT NULL AND original_target.deleted_at IS NULL)
	 AND (obs.target_id=w.target_id OR EXISTS(SELECT 1 FROM organization_target ot JOIN organization org ON org.id=ot.organization_id AND org.deleted_at IS NULL WHERE ot.organization_id=obs.organization_id AND ot.target_id=w.target_id))
	 AND EXISTS(SELECT 1 FROM unnest(?::text[]) r(value) WHERE lower(rtrim(w.host,'.'))=r.value OR right(lower(rtrim(w.host,'.')),length(r.value)+1)='.'||r.value)
	 ORDER BY w.id LIMIT ?`, event.LastWebsiteID, event.ObservationID, pq.StringArray(roots), limit).Scan(&sites).Error; err != nil {
			return false, err
		}
	}
	if len(sites) > 0 {
		ids := make([]int64, 0, len(sites))
		for _, site := range sites {
			ids = append(ids, site.ID)
		}
		if err := tx.Exec(`INSERT INTO asset_application_projection_queue(website_id) SELECT unnest(?::bigint[])`, pq.Int64Array(ids)).Error; err != nil {
			return false, err
		}
	}
	if len(sites) < limit {
		if err := tx.Exec(`DELETE FROM asset_application_manual_queue WHERE id=?`, event.ID).Error; err != nil {
			return false, err
		}
	} else if err := tx.Exec(`UPDATE asset_application_manual_queue SET last_website_id=? WHERE id=?`, sites[len(sites)-1].ID, event.ID).Error; err != nil {
		return false, err
	}
	return worked, nil
}

// An explicit application wins only when its name, bound domain and target or
// organization all agree. Ambiguous matches remain separate candidates.
func explicitApplicationForWebsite(tx *gorm.DB, targetID int, scope, name string) (int64, int64, error) {
	var rows []struct{ ID, ObservationID int64 }
	err := tx.Raw(`SELECT a.id, MIN(obs.id) AS observation_id FROM asset_entity a JOIN asset_observation obs ON obs.tenant_id=a.tenant_id AND obs.asset_id=a.id
	 LEFT JOIN target original_target ON original_target.id=obs.target_id
	 WHERE a.tenant_id=?::uuid AND a.asset_type='app' AND lower(a.label)=? AND obs.payload->>'type'='app'
	 AND (obs.original_target_id IS NULL OR original_target.id IS NOT NULL AND original_target.deleted_at IS NULL)
	 AND (obs.target_id=? OR EXISTS(SELECT 1 FROM organization_target ot JOIN organization org ON org.id=ot.organization_id AND org.deleted_at IS NULL WHERE ot.organization_id=obs.organization_id AND ot.target_id=?))
	 AND EXISTS(SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(obs.payload->'boundDomains')='array' THEN obs.payload->'boundDomains' ELSE '[]'::jsonb END) d(value)
	 WHERE ?=lower(rtrim(d.value,'.')) OR right(lower(rtrim(d.value,'.')),length(?)+1)='.'||?) GROUP BY a.id ORDER BY a.id LIMIT 2`, assetapp.DefaultTenantID, name, targetID, targetID, scope, scope, scope).Scan(&rows).Error
	if err != nil {
		return 0, 0, err
	}
	if len(rows) != 1 {
		return 0, 0, nil
	}
	return rows[0].ID, rows[0].ObservationID, nil
}

// enqueueTargetApplicationSites rechecks affected websites in bounded batches
// when a target's organization or visibility changes.
func enqueueTargetApplicationSites(tx *gorm.DB, limit int) (bool, error) {
	var event struct{ ID, TargetID, LastWebsiteID int64 }
	err := tx.Raw(`SELECT id,target_id,last_website_id FROM asset_application_target_queue ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`).Row().Scan(&event.ID, &event.TargetID, &event.LastWebsiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var sites []struct{ ID int64 }
	if err := tx.Raw(`SELECT id FROM website WHERE target_id=? AND id>? ORDER BY id LIMIT ?`, event.TargetID, event.LastWebsiteID, limit).Scan(&sites).Error; err != nil {
		return false, err
	}
	if len(sites) > 0 {
		ids := make([]int64, 0, len(sites))
		for _, site := range sites {
			ids = append(ids, site.ID)
		}
		if err := tx.Exec(`INSERT INTO asset_application_projection_queue(website_id) SELECT unnest(?::bigint[])`, pq.Int64Array(ids)).Error; err != nil {
			return false, err
		}
	}
	if len(sites) < limit {
		err = tx.Exec(`DELETE FROM asset_application_target_queue WHERE id=?`, event.ID).Error
	} else {
		err = tx.Exec(`UPDATE asset_application_target_queue SET last_website_id=? WHERE id=?`, sites[len(sites)-1].ID, event.ID).Error
	}
	return true, err
}
