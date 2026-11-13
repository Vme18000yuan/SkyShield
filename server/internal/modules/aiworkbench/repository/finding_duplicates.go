package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

// Match ARTEX's retrieval score: type + asset overlap is strong, while name,
// type, or asset alone is only a weak candidate. A previous human veto always
// excludes a pair. Every candidate still passes the task's target scope.
const duplicateFindingScope = taskFindingScope + `,anchor AS (
 SELECT * FROM eligible WHERE id=?
),hits AS (
 SELECT e.*,
 (btrim(a.vulnclass)<>'' AND lower(e.vulnclass)=lower(a.vulnclass)) AS type_hit,
 (btrim(a.name)<>'' AND lower(e.name)=lower(a.name)) AS name_hit,
 EXISTS(SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(e.asset_ids)='array' THEN e.asset_ids ELSE '[]'::jsonb END) x
        WHERE x.value IN (SELECT value FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(a.asset_ids)='array' THEN a.asset_ids ELSE '[]'::jsonb END))) AS asset_hit
 FROM eligible e JOIN anchor a ON e.task_id=a.task_id AND e.id<>a.id
 WHERE (?::jsonb IS NULL OR e.id IN (SELECT value::bigint FROM jsonb_array_elements_text(?::jsonb)))
 AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_blocks b WHERE b.left_id=LEAST(e.id,a.id) AND b.right_id=GREATEST(e.id,a.id))
),candidates AS (
 SELECT h.*,((CASE WHEN type_hit THEN 2 ELSE 0 END)+(CASE WHEN asset_hit THEN 2 ELSE 0 END)+
  (CASE WHEN name_hit THEN 1 ELSE 0 END)+(CASE WHEN type_hit AND asset_hit THEN 3 ELSE 0 END)) AS score
 FROM hits h WHERE type_hit OR asset_hit OR name_hit
) `

func duplicateScopeArgs(user int, taskID int64, tenant string, findingID int64, allowed []int64) []any {
	var selected any
	if allowed != nil {
		selected = findingIDsJSON(allowed)
	}
	return []any{taskID, tenant, user, false, 0, 0, "", "", "", "", findingID, selected, selected}
}

func (s *TaskStore) ListFindingDuplicateCandidates(ctx context.Context, user int, taskID, findingID int64, page, size int, allowed []int64) (aiapp.FindingDuplicatePage, error) {
	out := aiapp.FindingDuplicatePage{Items: []aiapp.FindingDuplicateCandidate{}, Page: page, Size: size}
	tenant, err := s.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return out, err
	}
	sources, err := s.findingSources(ctx, user, taskID, tenant)
	if err != nil {
		return out, err
	}
	args := duplicateScopeArgs(user, taskID, tenant, findingID, allowed)
	var sourceTask int64
	err = s.db.WithContext(ctx).Raw(duplicateFindingScope+`SELECT source_task_id FROM anchor`, args...).Row().Scan(&sourceTask)
	if errors.Is(err, sql.ErrNoRows) {
		return out, aiapp.ErrTaskNotFound
	}
	if err != nil {
		return out, err
	}
	if err = s.db.WithContext(ctx).Raw(duplicateFindingScope+`SELECT count(*),count(*) FILTER(WHERE type_hit AND asset_hit) FROM candidates`, args...).Row().Scan(&out.Total, &out.StrongTotal); err != nil {
		return out, err
	}
	values := append(append([]any{}, args...), size, page*size)
	rows, err := s.db.WithContext(ctx).Raw(duplicateFindingScope+`SELECT f.id,f.name,f.vulnclass,f.severity,left(f.summary,400),f.score,f.type_hit,f.asset_hit,f.name_hit,
 (SELECT m.case_id FROM tianqiong_ai.finding_case_members m JOIN tianqiong_ai.finding_cases c ON c.id=m.case_id AND c.active
  WHERE m.finding_id=f.id AND NOT EXISTS(SELECT 1 FROM tianqiong_ai.finding_case_members hidden WHERE hidden.case_id=c.id AND NOT EXISTS(SELECT 1 FROM eligible visible WHERE visible.id=hidden.finding_id))),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id::text,'type',a.asset_type,'label',a.label) ORDER BY a.id) FROM asset_entity a WHERE a.tenant_id=f.context_tenant AND a.id::text IN (SELECT jsonb_array_elements_text(CASE WHEN jsonb_typeof(f.asset_ids)='array' THEN f.asset_ids ELSE '[]'::jsonb END))),'[]'::jsonb)
 FROM candidates f ORDER BY f.score DESC,f.id DESC LIMIT ? OFFSET ?`, values...).Rows()
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item aiapp.FindingDuplicateCandidate
		var typeHit, assetHit, nameHit bool
		var raw []byte
		if err = rows.Scan(&item.ID, &item.Name, &item.VulnClass, &item.Severity, &item.Summary, &item.Score, &typeHit, &assetHit, &nameHit, &item.CaseID, &raw); err != nil {
			rows.Close()
			return out, err
		}
		item.HitReasons = []string{}
		if typeHit {
			item.HitReasons = append(item.HitReasons, "same_type")
		}
		if assetHit {
			item.HitReasons = append(item.HitReasons, "asset_overlap")
		}
		if nameHit {
			item.HitReasons = append(item.HitReasons, "same_name")
		}
		item.Tier = "weak"
		if typeHit && assetHit {
			item.Tier = "strong"
		}
		if err = json.Unmarshal(raw, &item.Assets); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	current, err := s.TrafficTaskTenant(ctx, user, taskID)
	if err != nil || current != tenant {
		if err != nil {
			return aiapp.FindingDuplicatePage{}, err
		}
		return aiapp.FindingDuplicatePage{}, aiapp.ErrTaskNotFound
	}
	currentSources, err := s.findingSources(ctx, user, taskID, tenant)
	if err != nil {
		return aiapp.FindingDuplicatePage{}, err
	}
	if !slices.Equal(sources, currentSources) {
		return aiapp.FindingDuplicatePage{}, aiapp.ErrTaskNotFound
	}
	if sourceTask != taskID {
		if _, err = s.TrafficTaskTenant(ctx, user, sourceTask); err != nil {
			return aiapp.FindingDuplicatePage{}, err
		}
	}
	var currentSource int64
	err = s.db.WithContext(ctx).Raw(duplicateFindingScope+`SELECT source_task_id FROM anchor`, args...).Row().Scan(&currentSource)
	if errors.Is(err, sql.ErrNoRows) || err == nil && currentSource != sourceTask {
		return aiapp.FindingDuplicatePage{}, aiapp.ErrTaskNotFound
	}
	if err != nil {
		return aiapp.FindingDuplicatePage{}, err
	}
	return out, nil
}
