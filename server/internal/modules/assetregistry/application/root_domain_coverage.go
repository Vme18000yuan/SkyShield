package application

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrRootDomainNotFound = errors.New("root domain not found")

type RootCoverageNode struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Tested      bool   `json:"tested"`
	InScope     bool   `json:"in_scope"`
	AssetID     int64  `json:"asset_id,omitempty,string"`
	Domain      string `json:"domain,omitempty"`
	RootDomain  string `json:"root_domain,omitempty"`
	IP          string `json:"ip,omitempty"`
	URL         string `json:"url,omitempty"`
	Port        int    `json:"port,omitempty"`
	ServiceType string `json:"service_type,omitempty"`
	AppName     string `json:"app_name,omitempty"`
}
type RootCoverageEdge struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}
type RootCoverageGraph struct {
	Nodes     []RootCoverageNode `json:"nodes"`
	Edges     []RootCoverageEdge `json:"edges"`
	Truncated bool               `json:"truncated"`
}
type RootCoverageRef struct {
	ID           int64  `json:"id,string"`
	Kind         string `json:"kind"`
	State        string `json:"state"`
	Summary      string `json:"summary"`
	SourceTaskID int64  `json:"source_task_id,string"`
	TaskName     string `json:"task_name"`
}
type RootCoverageRefs struct {
	Intents   []RootCoverageRef `json:"intents"`
	Facts     []RootCoverageRef `json:"facts"`
	Findings  []RootCoverageRef `json:"findings"`
	Truncated bool              `json:"truncated"`
}
type RootCoverageStore interface {
	ListRootCoverageAssets(context.Context, AssetQuery) ([]AssetEntity, error)
	ListRootCoveragePorts(context.Context, PortQuery) ([]PortAsset, error)
	RootCoverageTested(context.Context, string, int, []int64) (map[int64]bool, error)
	RootCoverageReferences(context.Context, string, int, int64) (RootCoverageRefs, error)
}

func (f *Facade) rootCoverageRoot(ctx context.Context, user int, root int64, org int) (AssetEntity, error) {
	if f == nil || f.store == nil || user <= 0 {
		return AssetEntity{}, ErrForbidden
	}
	if root <= 0 || org < 0 || org > 2147483647 {
		return AssetEntity{}, ErrInvalidQuery
	}
	member, err := f.store.IsActiveTenantMember(ctx, DefaultTenantID, user)
	if err != nil {
		return AssetEntity{}, err
	}
	if !member {
		return AssetEntity{}, ErrForbidden
	}
	store, ok := f.store.(RootCoverageStore)
	if !ok {
		return AssetEntity{}, errors.New("root coverage unavailable")
	}
	result, err := store.ListRootCoverageAssets(ctx, AssetQuery{TenantID: DefaultTenantID, AssetType: "root_domain", AssetID: root, OrganizationID: org, Limit: 1})
	if err != nil {
		return AssetEntity{}, err
	}
	if len(result) == 0 {
		return AssetEntity{}, ErrRootDomainNotFound
	}
	return result[0], nil
}

// RootDomainCoverage uses inventory membership, not a task's potentially narrower scope.
// Confirmation evidence is restricted to tasks the requesting user can read.
func (f *Facade) RootDomainCoverage(ctx context.Context, user int, rootID int64, org int) (RootCoverageGraph, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out := RootCoverageGraph{Nodes: []RootCoverageNode{}, Edges: []RootCoverageEdge{}}
	root, err := f.rootCoverageRoot(ctx, user, rootID, org)
	if err != nil {
		return out, err
	}
	evidence, ok := f.store.(RootCoverageStore)
	if !ok {
		return out, errors.New("root coverage unavailable")
	}
	assets, err := evidence.ListRootCoverageAssets(ctx, AssetQuery{TenantID: DefaultTenantID, RootDomainID: rootID, OrganizationID: org, Limit: 5000})
	if err != nil {
		return out, fmt.Errorf("root coverage assets: %w", err)
	}
	out.Truncated = len(assets) > 4999
	if out.Truncated {
		assets = assets[:4999]
	}
	ids := []int64{root.ID}
	for _, a := range assets {
		ids = append(ids, a.ID)
	}
	tested, err := evidence.RootCoverageTested(ctx, DefaultTenantID, user, ids)
	if err != nil {
		return out, err
	}
	assets = append([]AssetEntity{root}, assets...)
	hosts, services := map[string]string{}, map[string]string{}
	for _, asset := range assets {
		node := rootCoverageNode(asset, root.Label)
		node.Tested = tested[asset.ID]
		out.Nodes = append(out.Nodes, node)
		if node.Kind == "root_domain" || node.Kind == "subdomain" {
			hosts[node.Domain] = node.Key
		}
		if node.Kind == "ip" {
			hosts[node.IP] = node.Key
		}
		if node.Kind == "service" && node.Port > 0 {
			services[rootCoverageHostPort(node)] = node.Key
		}
	}
	// Port observations have no canonical asset ID and cannot imply tested status.
	if len(out.Nodes) < 5000 {
		ports, e := evidence.ListRootCoveragePorts(ctx, PortQuery{TenantID: DefaultTenantID, RootDomainID: rootID, OrganizationID: org, Limit: 5001})
		if e != nil {
			return out, fmt.Errorf("root coverage ports: %w", e)
		}
		if len(ports) > 5000 {
			out.Truncated = true
		}
		for _, port := range ports {
			node := rootCoverageNode(AssetEntity{AssetType: "service", Label: port.Label}, root.Label)
			node.Port = port.Port
			node.Key = "p:" + port.Label
			node.AssetID = 0
			node.ServiceType = port.Service
			hp := rootCoverageHostPort(node)
			if services[hp] != "" {
				continue
			}
			if len(out.Nodes) >= 5000 {
				out.Truncated = true
				break
			}
			services[hp] = node.Key
			out.Nodes = append(out.Nodes, node)
		}
	} else {
		out.Truncated = true
	}
	rootKey := fmt.Sprintf("a:%d", root.ID)
	for _, node := range out.Nodes {
		if node.Key == rootKey {
			continue
		}
		parent := ""
		if node.Kind == "endpoint" {
			parent = services[rootCoverageHostPort(node)]
		}
		if parent == "" && (node.Kind == "service" || node.Kind == "endpoint") {
			host := node.Domain
			if host == "" {
				host = node.IP
			}
			parent = hosts[host]
		}
		if parent == "" || parent == node.Key {
			parent = rootKey
		}
		out.Edges = append(out.Edges, RootCoverageEdge{Src: node.Key, Dst: parent})
	}
	return out, nil
}

func rootCoverageNode(asset AssetEntity, root string) RootCoverageNode {
	node := RootCoverageNode{Key: fmt.Sprintf("a:%d", asset.ID), Kind: asset.AssetType, Label: asset.Label, AssetID: asset.ID, InScope: true, RootDomain: root}
	host := ""
	switch asset.AssetType {
	case "root_domain", "subdomain":
		host = strings.ToLower(strings.TrimSuffix(asset.Label, "."))
	case "ip":
		node.IP = asset.Label
	case "app":
		node.AppName = asset.Label
	case "service", "endpoint":
		u, err := url.Parse(asset.Label)
		if err == nil && u.Hostname() != "" {
			host = strings.ToLower(u.Hostname())
			node.URL = asset.Label
			node.ServiceType = u.Scheme
			node.Port, _ = strconv.Atoi(u.Port())
			if node.Port == 0 {
				if u.Scheme == "https" {
					node.Port = 443
				} else if u.Scheme == "http" {
					node.Port = 80
				}
			}
		} else if h, p, e := net.SplitHostPort(asset.Label); e == nil {
			host = strings.ToLower(h)
			node.Port, _ = strconv.Atoi(p)
		} else if i := strings.LastIndex(asset.Label, ":"); i > 0 && net.ParseIP(asset.Label[:i]) != nil {
			host = asset.Label[:i]
			node.Port, _ = strconv.Atoi(asset.Label[i+1:])
		}
	}
	if net.ParseIP(host) != nil {
		node.IP = host
	} else {
		node.Domain = host
	}
	return node
}
func rootCoverageHostPort(node RootCoverageNode) string {
	host := node.Domain
	if host == "" {
		host = node.IP
	}
	return net.JoinHostPort(host, strconv.Itoa(node.Port))
}

func (f *Facade) RootDomainCoverageRefs(ctx context.Context, user int, rootID int64, org int, assetID int64) (RootCoverageRefs, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out := RootCoverageRefs{Intents: []RootCoverageRef{}, Facts: []RootCoverageRef{}, Findings: []RootCoverageRef{}}
	if assetID <= 0 {
		return out, ErrInvalidQuery
	}
	if _, err := f.rootCoverageRoot(ctx, user, rootID, org); err != nil {
		return out, err
	}
	store, ok := f.store.(RootCoverageStore)
	if !ok {
		return out, errors.New("root coverage unavailable")
	}
	if assetID != rootID {
		assets, err := store.ListRootCoverageAssets(ctx, AssetQuery{TenantID: DefaultTenantID, AssetID: assetID, RootDomainID: rootID, OrganizationID: org, Limit: 1})
		if err != nil {
			return out, err
		}
		if len(assets) == 0 {
			return out, ErrRootDomainNotFound
		}
	}
	return store.RootCoverageReferences(ctx, DefaultTenantID, user, assetID)
}
