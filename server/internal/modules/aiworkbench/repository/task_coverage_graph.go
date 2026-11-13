// Adapted from ARTEX's coverage graph; AGPL-3.0-only.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/lib/pq"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"golang.org/x/net/publicsuffix"
)

func (s *TaskStore) TaskCoverageGraph(ctx context.Context, user int, taskID int64) (aiapp.CoverageGraph, error) {
	out := aiapp.CoverageGraph{Nodes: []aiapp.CoverageGraphNode{}, Edges: []aiapp.CoverageGraphEdge{}}
	task, err := s.GetTask(ctx, user, taskID)
	if err != nil {
		return out, err
	}
	db, err := s.db.DB()
	if err != nil {
		return out, err
	}
	assets, truncated, err := airuntime.TaskCoverageAssets(ctx, db, user, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, aiapp.ErrTaskNotFound
	}
	if err != nil {
		return out, err
	}
	out.Truncated = truncated
	byKey := map[string]int{}
	roots, domains, ips, services, servicesByHost := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	companySet := map[int64]bool{}
	for _, asset := range assets {
		key := fmt.Sprintf("a:%d", asset.ID)
		node := aiapp.CoverageGraphNode{Key: key, Kind: asset.Type, Label: asset.Label, Tested: task.CoverageEnabled && asset.Tested, InScope: true, AssetID: asset.ID, Domain: asset.Domain, RootDomain: asset.RootDomain, IP: asset.IP, URL: asset.URL, Port: asset.Port, ServiceType: asset.ServiceType, AppName: asset.AppName, PageTitle: asset.PageTitle, StatusCode: asset.StatusCode}
		node.Domain = strings.ToLower(strings.TrimSuffix(node.Domain, "."))
		if net.ParseIP(node.Domain) != nil {
			if node.IP == "" {
				node.IP = node.Domain
			}
			node.Domain = ""
		}
		if node.RootDomain == "" && node.Domain != "" {
			if root, e := publicsuffix.EffectiveTLDPlusOne(node.Domain); e == nil {
				node.RootDomain = root
			}
		}
		byKey[key] = len(out.Nodes)
		out.Nodes = append(out.Nodes, node)
		for _, company := range asset.CompanyIDs {
			if company > 0 {
				companySet[company] = true
			}
		}
		switch node.Kind {
		case "root_domain":
			roots[node.Label] = key
		case "subdomain":
			domains[node.Domain] = key
		case "ip":
			ips[node.IP] = key
		case "service":
			host := node.Domain
			if host == "" {
				host = node.IP
			}
			if host != "" {
				servicesByHost[host] = key
				if node.Port != nil {
					services[fmt.Sprintf("%s|%d", host, *node.Port)] = key
				}
			}
		}
	}
	companyIDs := make([]int64, 0, len(companySet))
	for id := range companySet {
		companyIDs = append(companyIDs, id)
	}
	if len(companyIDs) > 0 {
		var companies []struct {
			ID   int64
			Name string
		}
		if err = s.db.WithContext(ctx).Raw(`SELECT id,name FROM organization WHERE id=ANY(?::bigint[]) AND deleted_at IS NULL ORDER BY id`, pq.Int64Array(companyIDs)).Scan(&companies).Error; err != nil {
			return out, err
		}
		for _, company := range companies {
			key := fmt.Sprintf("c:%d", company.ID)
			byKey[key] = len(out.Nodes)
			out.Nodes = append(out.Nodes, aiapp.CoverageGraphNode{Key: key, Kind: "company", Label: company.Name, CompanyID: company.ID})
		}
	}
	for _, asset := range assets {
		index := byKey[fmt.Sprintf("a:%d", asset.ID)]
		node := out.Nodes[index]
		if node.Kind == "subdomain" && node.RootDomain != "" && roots[node.RootDomain] == "" {
			key := "r:" + node.RootDomain
			roots[node.RootDomain] = key
			byKey[key] = len(out.Nodes)
			out.Nodes = append(out.Nodes, aiapp.CoverageGraphNode{Key: key, Kind: "root_domain", Label: node.RootDomain, Domain: node.RootDomain})
		}
	}
	edgeSet := map[string]bool{}
	link := func(child, parent string) {
		if child == "" || parent == "" || child == parent {
			return
		}
		if _, ok := byKey[parent]; !ok {
			return
		}
		pair := child + "\x00" + parent
		if !edgeSet[pair] {
			edgeSet[pair] = true
			out.Edges = append(out.Edges, aiapp.CoverageGraphEdge{Src: child, Dst: parent})
		}
	}
	for _, asset := range assets {
		node := out.Nodes[byKey[fmt.Sprintf("a:%d", asset.ID)]]
		for _, company := range asset.CompanyIDs {
			companyKey := fmt.Sprintf("c:%d", company)
			if node.Kind == "root_domain" || node.Kind == "ip" || node.Kind == "app" {
				link(node.Key, companyKey)
			}
			if node.Kind == "subdomain" && node.RootDomain != "" {
				link(roots[node.RootDomain], companyKey)
			}
		}
		switch node.Kind {
		case "subdomain":
			link(node.Key, roots[node.RootDomain])
		case "service", "endpoint":
			host := node.Domain
			if host == "" {
				host = node.IP
			}
			parent := ""
			if node.Kind == "endpoint" {
				if node.Port != nil {
					parent = services[fmt.Sprintf("%s|%d", host, *node.Port)]
				}
				if parent == "" {
					parent = servicesByHost[host]
				}
			}
			if parent == "" {
				parent = domains[host]
			}
			if parent == "" {
				parent = ips[host]
			}
			if parent == "" {
				parent = roots[node.RootDomain]
			}
			link(node.Key, parent)
		}
	}
	return out, nil
}

func (s *TaskStore) TaskAssetRefs(ctx context.Context, user int, taskID, assetID int64) (aiapp.CoverageAssetRefs, error) {
	out := aiapp.CoverageAssetRefs{Intents: []aiapp.CoverageAssetRef{}, Facts: []aiapp.CoverageAssetRef{}, Findings: []aiapp.CoverageAssetRef{}}
	task, err := s.GetTask(ctx, user, taskID)
	if err != nil {
		return out, err
	}
	var allowed bool
	if err = s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.task_scope_assets(?,?) WHERE asset_id=?)`, task.ID, user, assetID).Scan(&allowed).Error; err != nil {
		return out, err
	}
	if !allowed {
		return out, aiapp.ErrTaskNotFound
	}
	refs := []aiapp.CoverageAssetRef{}
	err = s.db.WithContext(ctx).Raw(`WITH scopes AS (
	 SELECT ?::bigint AS exploration_id,0::bigint AS source_task_id,FALSE AS inherited
	 UNION ALL SELECT source.exploration_id,source.id,TRUE FROM ai_task_relation r JOIN ai_task source ON source.id=r.source_task_id AND source.tenant_id=r.tenant_id WHERE r.task_id=? AND r.tenant_id=? AND source.owner_user_id=? AND source.deleted_at IS NULL AND (source.original_target_id IS NULL OR EXISTS(SELECT 1 FROM target WHERE id=source.target_id AND deleted_at IS NULL))
	) SELECT n.id,n.kind,n.state,COALESCE(NULLIF(n.payload->>'summary',''),n.payload->>'text','') AS summary,scopes.source_task_id,scopes.inherited FROM scopes JOIN tianqiong_ai.exploration_nodes n ON n.exploration_id=scopes.exploration_id JOIN tianqiong_ai.exploration_anchors a ON a.node_id=n.id WHERE a.asset_id=? AND n.kind IN ('intent','fact','finding') AND (NOT scopes.inherited OR n.kind<>'intent' OR n.state IN ('done','blocked','exhausted','stopped')) ORDER BY scopes.inherited,n.id DESC LIMIT 1000`, task.ExplorationID, task.ID, task.TenantID, user, assetID).Scan(&refs).Error
	if err != nil {
		return out, err
	}
	for _, ref := range refs {
		switch ref.Kind {
		case "intent":
			out.Intents = append(out.Intents, ref)
		case "fact":
			out.Facts = append(out.Facts, ref)
		case "finding":
			out.Findings = append(out.Findings, ref)
		}
	}
	return out, nil
}

var _ aiapp.CoverageGraphStore = (*TaskStore)(nil)
