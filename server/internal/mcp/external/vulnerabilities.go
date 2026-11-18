package external

import (
	"context"
	"errors"
	"time"

	"github.com/lib/pq"
	"github.com/yyhuni/lunafox/server/internal/aivuln"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	security "github.com/yyhuni/lunafox/server/internal/modules/security/application"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

type vulnerability struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Source    string    `json:"source"`
	Severity  string    `json:"severity"`
	URL       string    `json:"url"`
	Reviewed  bool      `json:"reviewed"`
	CreatedAt time.Time `json:"createdAt"`
}
type vulnID struct {
	Org int `json:"organization_id,omitempty"`
	ID  int `json:"vulnerability_id"`
}
type vulnList struct {
	Org int `json:"organization_id,omitempty"`
	Page
}
type evidenceInput struct {
	vulnID
	Page int `json:"page,omitempty" jsonschema:"Pages of two recorded exchanges, starting at one."`
}
type reviewInput struct {
	vulnID
	Reviewed *bool `json:"reviewed"`
}

const vulnFields = `v.id,COALESCE(NULLIF(v.display_name,''),NULLIF(v.raw_output->'info'->>'name',''),v.vuln_type) AS name,
 v.vuln_type AS type,v.source,v.severity,v.url,v.reviewed,v.created_at`
const vulnFrom = ` FROM vulnerability v JOIN target x ON x.id=v.target_id AND x.deleted_at IS NULL
 WHERE (? OR EXISTS(SELECT 1 FROM organization_target ot JOIN organization o ON o.id=ot.organization_id AND o.deleted_at IS NULL
 WHERE ot.target_id=v.target_id AND ot.organization_id=ANY(?::integer[]))) `

func (c Client) scope(org int) (bool, pq.Int64Array, error) {
	if org == 0 {
		return c.AllOrganizations, c.OrganizationIDs, nil
	}
	if !c.permitsOrganization(org) {
		return false, nil, errDenied
	}
	return false, pq.Int64Array{int64(org)}, nil
}

func (s *Service) listVulnerabilities(ctx context.Context, c Client, in vulnList) (any, error) {
	all, ids, err := c.scope(in.Org)
	if err != nil {
		return nil, err
	}
	limit, offset, err := in.limits()
	if err != nil {
		return nil, err
	}
	rows := []vulnerability{}
	err = s.query(ctx).Raw("SELECT "+vulnFields+vulnFrom+" ORDER BY v.id DESC LIMIT ? OFFSET ?", all, ids, limit+1, offset).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].URL = cleanURL(rows[i].URL)
	}
	return listPage(rows, limit), nil
}
func (s *Service) readVulnerability(ctx context.Context, c Client, in vulnID) (*vulnerability, error) {
	if in.ID <= 0 {
		return nil, errInput
	}
	all, ids, err := c.scope(in.Org)
	if err != nil {
		return nil, err
	}
	var row vulnerability
	res := s.query(ctx).Raw("SELECT "+vulnFields+vulnFrom+" AND v.id=?", all, ids, in.ID).Scan(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, errDenied
	}
	row.URL = cleanURL(row.URL)
	return &row, nil
}
func (s *Service) getVulnerability(ctx context.Context, c Client, in vulnID) (any, error) {
	return s.readVulnerability(ctx, c, in)
}

func (s *Service) review(ctx context.Context, c Client, in reviewInput) (any, error) {
	if in.Reviewed == nil || in.ID <= 0 {
		return nil, errInput
	}
	// Recheck the live grant, target membership and role within the write.
	// Hold the authorization rows until the review state has committed.
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var grant Client
		r := tx.Raw(`SELECT c.owner_user_id,c.all_organizations,c.organization_ids,c.tools,m.role FROM mcp_client c `+activeMember+
			` WHERE c.id=? AND c.owner_user_id=? AND c.enabled AND NOT c.legacy FOR SHARE OF c,m,u,tn`, assets.DefaultTenantID, c.ID, c.OwnerUserID).Scan(&grant)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 || !grant.permitsTool(toolReview) {
			return errDenied
		}
		all, ids, err := grant.scope(in.Org)
		if err != nil {
			return err
		}
		query := `SELECT v.id FROM vulnerability v JOIN target x ON x.id=v.target_id AND x.deleted_at IS NULL `
		args := []any{in.ID}
		if all {
			query += " WHERE v.id=? FOR UPDATE OF v FOR SHARE OF x"
		} else {
			query += `JOIN organization_target ot ON ot.target_id=x.id JOIN organization o ON o.id=ot.organization_id AND o.deleted_at IS NULL
              WHERE v.id=? AND o.id=ANY(?::integer[]) ORDER BY o.id LIMIT 1 FOR UPDATE OF v FOR SHARE OF x,ot,o`
			args = append(args, ids)
		}
		var found int
		r = tx.Raw(query, args...).Scan(&found)
		if r.Error != nil {
			return r.Error
		}
		if found == 0 {
			return errDenied
		}
		txCtx := dbtx.WithTransaction(ctx, tx)
		// The disposition store participates in the ambient transaction.
		_, err = s.vulnerabilities.ApplyMCPVulnerabilityDisposition(txCtx, reviewRequest(in.ID, *in.Reviewed))
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": in.ID, "reviewed": *in.Reviewed}, nil
}

func reviewRequest(id int, reviewed bool) security.MCPVulnerabilityDispositionRequest {
	action := "unreview_vulnerability"
	if reviewed {
		action = "review_vulnerability"
	}
	return security.MCPVulnerabilityDispositionRequest{IDs: []int{id}, Action: action, Reviewed: reviewed}
}

type packet struct {
	Source    string `json:"source"`
	Request   string `json:"request,omitempty"`
	Response  string `json:"response,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (s *Service) httpEvidence(ctx context.Context, c Client, in evidenceInput) (any, error) {
	if in.Page < 0 || in.Page > 1000000 {
		return nil, errInput
	}
	row, err := s.readVulnerability(ctx, c, in.vulnID)
	if err != nil {
		return nil, err
	}
	items := []aivuln.Evidence{}
	if row.Source != "ai" && row.Source != "info-gath" {
		var captured aivuln.Evidence
		err = s.query(ctx).Raw(`SELECT left(COALESCE(raw_output->>'request',''),131072) AS request,
          left(COALESCE(raw_output->>'response',''),131072) AS response,
          (length(COALESCE(raw_output->>'request',''))>131072 OR length(COALESCE(raw_output->>'response',''))>131072) AS truncated
          FROM vulnerability WHERE id=?`, in.ID).Scan(&captured).Error
		if err != nil {
			return nil, err
		}
		captured.Source = "scanner"
		items = append(items, captured)
	} else {
		items, err = s.evidence.EvidenceForVulnerability(ctx, c.OwnerUserID, in.ID)
		if errors.Is(err, aivuln.ErrNotFound) {
			return nil, errDenied
		}
		if err != nil {
			return nil, err
		}
	}
	packets := []packet{}
	page := max(in.Page, 1)
	offset := (page - 1) * 2
	count := 0
	add := func(item aivuln.Evidence) {
		if item.Request == "" && item.Response == "" {
			return
		}
		if count >= offset && len(packets) < 2 {
			req, tr := redactHTTP(item.Request, false)
			resp, ts := redactHTTP(item.Response, true)
			packets = append(packets, packet{item.Source, req, resp, item.Truncated || tr || ts})
		}
		count++
	}
	for _, item := range items {
		add(item)
	}
	// Task snapshots have a separate owner boundary from the inventory row.
	const bindings = ` FROM ai_finding_risk_link l JOIN vulnerability v ON v.id=l.vulnerability_id
      JOIN ai_task t ON t.id=l.task_id AND t.target_id=v.target_id AND t.deleted_at IS NULL
      JOIN tianqiong_ai.finding_traffic_bindings b ON b.finding_id=l.finding_id
      JOIN tianqiong_ai.traffic_evidence_snapshots sn ON sn.id=b.snapshot_id
      WHERE l.vulnerability_id=? AND t.tenant_id=? AND t.owner_user_id=? `
	var total int
	args := []any{in.ID, assets.DefaultTenantID, c.OwnerUserID}
	err = s.query(ctx).Raw("SELECT count(*)"+bindings, args...).Scan(&total).Error
	if err != nil {
		return nil, err
	}
	if remaining := 2 - len(packets); remaining > 0 && total > max(0, offset-count) {
		var snapshots []aivuln.Evidence
		err = s.query(ctx).Raw(`SELECT 'task_snapshot' AS source,COALESCE(sn.req_head,'') AS request,COALESCE(sn.resp_head,'') AS response,
          (sn.req_len>octet_length(COALESCE(sn.req_head,'')) OR sn.resp_len>octet_length(COALESCE(sn.resp_head,''))) AS truncated`+bindings+
			" ORDER BY l.finding_id,b.position,b.id LIMIT ? OFFSET ?", append(args, remaining, max(0, offset-count))...).Scan(&snapshots).Error
		if err != nil {
			return nil, err
		}
		for _, item := range snapshots {
			req, tr := redactHTTP(item.Request, false)
			res, ts := redactHTTP(item.Response, true)
			packets = append(packets, packet{item.Source, req, res, item.Truncated || tr || ts})
		}
	}
	return map[string]any{"vulnerability": row, "items": packets, "page": page, "hasMore": page*2 < count+total, "redacted": true}, nil
}
