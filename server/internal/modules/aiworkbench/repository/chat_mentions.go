package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type ChatMentionStore struct{ db *gorm.DB }

func NewChatMentionStore(db *gorm.DB) *ChatMentionStore { return &ChatMentionStore{db: db} }
func (s *ChatMentionStore) AuthorizeMentions(ctx context.Context, user int) error {
	var ok bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=? AND m.user_id=? AND t.status='active' AND u.is_active=TRUE)`, assets.DefaultTenantID, user).Scan(&ok).Error
	if err != nil {
		return err
	}
	if !ok {
		return assets.ErrForbidden
	}
	return nil
}
func (s *ChatMentionStore) SearchMentionRecords(ctx context.Context, user int, kind, query string, before int64) ([]aiapp.ChatMention, error) {
	if err := s.AuthorizeMentions(ctx, user); err != nil {
		return nil, err
	}
	out := []aiapp.ChatMention{}
	if kind == "" || kind == "finding" {
		items := []aiapp.ChatMention{}
		err := s.db.WithContext(ctx).Raw(`SELECT 'finding' AS kind,f.id,COALESCE(NULLIF(f.name,''),NULLIF(f.vulnclass,''),'未分类') AS label,concat_ws(' · ',f.severity,f.status,left(f.summary,160)) AS description FROM tianqiong_ai.findings f JOIN ai_task task ON task.id=f.task_id JOIN tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE task.tenant_id=? AND task.owner_user_id=? AND task.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE AND (?::bigint=0 OR f.id<=?::bigint) AND (?='' OR f.id::text=? OR strpos(lower(concat_ws(' ',f.name,f.vulnclass,f.summary)),lower(?))>0) ORDER BY (f.id::text=?) DESC,f.id DESC LIMIT 22`, assets.DefaultTenantID, user, before, before, query, query, query, query).Scan(&items).Error
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	if kind == "" || kind == "risk" {
		items := []aiapp.ChatMention{}
		err := s.db.WithContext(ctx).Raw(`SELECT 'risk' AS kind,v.id,COALESCE(NULLIF(v.vuln_type,''),'未分类') AS label,concat_ws(' · ',v.severity,v.url,left(v.description,160)) AS description FROM vulnerability v JOIN target t ON t.id=v.target_id AND t.deleted_at IS NULL WHERE (?::bigint=0 OR v.id::bigint<=?::bigint) AND (?='' OR v.id::text=? OR strpos(lower(concat_ws(' ',v.vuln_type,v.url,v.description)),lower(?))>0) ORDER BY (v.id::text=?) DESC,v.id DESC LIMIT 22`, before, before, query, query, query, query).Scan(&items).Error
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	if kind == "" || kind == "company" {
		items := []aiapp.ChatMention{}
		err := s.db.WithContext(ctx).Raw(`SELECT 'company' AS kind,o.id,o.name AS label,o.description FROM organization o WHERE o.deleted_at IS NULL AND (?::bigint=0 OR o.id::bigint<=?::bigint) AND (?='' OR o.id::text=? OR strpos(lower(concat_ws(' ',o.name,o.description)),lower(?))>0) ORDER BY (o.id::text=?) DESC,o.id DESC LIMIT 22`, before, before, query, query, query, query).Scan(&items).Error
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}
func (s *ChatMentionStore) ReadMentionRecord(ctx context.Context, user int, kind string, id int64) (map[string]any, error) {
	if err := s.AuthorizeMentions(ctx, user); err != nil {
		return nil, err
	}
	var raw []byte
	var err error
	switch kind {
	case "finding":
		err = s.db.WithContext(ctx).Raw(`SELECT jsonb_build_object('finding_id',f.id::text,'finding_node_id',f.node_id::text,'task_id',task.id::text,'finding',to_jsonb(f),'assets',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',asset.id::text,'type',asset.asset_type,'label',asset.label,'attributes',asset.attributes) ORDER BY asset.id) FROM tianqiong_ai.exploration_anchors anchor JOIN asset_entity asset ON asset.id=anchor.asset_id AND asset.tenant_id=task.tenant_id WHERE anchor.node_id=f.node_id),'[]'::jsonb)) FROM tianqiong_ai.findings f JOIN ai_task task ON task.id=f.task_id JOIN tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id JOIN tenant tenant ON tenant.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE f.id=? AND task.tenant_id=? AND task.owner_user_id=? AND task.deleted_at IS NULL AND tenant.status='active' AND u.is_active=TRUE`, id, assets.DefaultTenantID, user).Row().Scan(&raw)
	case "risk":
		err = s.db.WithContext(ctx).Raw(`SELECT jsonb_build_object('risk',jsonb_build_object('id',v.id::text,'target_id',v.target_id,'target',t.name,'url',v.url,'vuln_type',v.vuln_type,'severity',v.severity,'source',v.source,'description',v.description,'raw_output',v.raw_output,'reviewed',v.reviewed,'created_at',v.created_at),'ai_findings',COALESCE((SELECT jsonb_agg(jsonb_build_object('finding_id',f.id::text,'finding_node_id',f.node_id::text,'task_id',task.id::text,'summary',f.summary,'report',f.report) ORDER BY f.id) FROM ai_finding_risk_link link JOIN tianqiong_ai.findings f ON f.id=link.finding_id JOIN ai_task task ON task.id=link.task_id WHERE link.vulnerability_id=v.id AND task.tenant_id=? AND task.owner_user_id=? AND task.deleted_at IS NULL),'[]'::jsonb)) FROM vulnerability v JOIN target t ON t.id=v.target_id AND t.deleted_at IS NULL WHERE v.id::bigint=?::bigint`, assets.DefaultTenantID, user, id).Row().Scan(&raw)
	case "asset":
		err = s.db.WithContext(ctx).Raw(`SELECT jsonb_build_object('asset',jsonb_build_object('id',id::text,'type',asset_type,'label',label,'method',method,'attributes',attributes,'first_seen_at',first_seen_at,'last_seen_at',last_seen_at)) FROM asset_entity WHERE tenant_id=? AND id=?`, assets.DefaultTenantID, id).Row().Scan(&raw)
	case "company":
		err = s.db.WithContext(ctx).Raw(`SELECT jsonb_build_object('company',jsonb_build_object('id',o.id,'name',o.name,'description',o.description),'targets',COALESCE((SELECT jsonb_agg(x.item ORDER BY x.id) FROM (SELECT t.id,jsonb_build_object('id',t.id,'name',t.name,'type',t.type) AS item FROM organization_target ot JOIN target t ON t.id=ot.target_id AND t.deleted_at IS NULL WHERE ot.organization_id=o.id ORDER BY t.id LIMIT 101) x),'[]'::jsonb)) FROM organization o WHERE o.id::bigint=?::bigint AND o.deleted_at IS NULL`, id).Row().Scan(&raw)
	default:
		return nil, aiapp.ErrInvalidInput
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ aiapp.ChatMentionStore = (*ChatMentionStore)(nil)
