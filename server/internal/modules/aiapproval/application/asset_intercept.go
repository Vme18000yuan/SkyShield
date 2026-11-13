package application

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	artexdb "github.com/Autumn-27/artex/db"
	contractresults "github.com/yyhuni/lunafox/contracts/results"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	blacklist "github.com/yyhuni/lunafox/server/internal/modules/blacklist/domain"
	"golang.org/x/net/publicsuffix"
)

type AssetRule struct {
	ID        int64     `json:"id,string"`
	Enabled   bool      `json:"enabled"`
	Action    string    `json:"action"`
	Kind      string    `json:"kind"`
	Pattern   string    `json:"pattern"`
	Note      string    `json:"note"`
	Builtin   bool      `json:"builtin"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AssetCandidate struct {
	ID     int64
	Type   string
	Label  string
	Inputs []assets.AssetInput `gorm:"-"`
}

type AssetRuleStore interface {
	SeedAssetRules(context.Context) error
	ListAssetRules(context.Context, int, int64) ([]AssetRule, error)
	SaveAssetRule(context.Context, int, int64, AssetRule, bool) (AssetRule, error)
	DeleteAssetRule(context.Context, int, int64, int64) error
	ToggleAssetRule(context.Context, int, int64, int64, bool) error
	EffectiveAssetRules(context.Context, int, int64, string) ([]AssetRule, error)
	AssetCandidates(context.Context, int, int64, string, []int64) ([]AssetCandidate, error)
}

type PlatformBlacklistResolver func(context.Context, int) ([]string, error)

func (s *Service) SetPlatformBlacklistResolver(resolve PlatformBlacklistResolver) {
	s.blacklist = resolve
}

func (s *Service) platformMatcher(ctx context.Context, targetID int) (*blacklist.Matcher, error) {
	if s.blacklist == nil {
		return nil, nil
	}
	patterns, err := s.blacklist(ctx, targetID)
	if err != nil {
		return nil, err
	}
	return blacklist.CompileMatcher(patterns)
}

func platformBlocks(matcher *blacklist.Matcher, domains, ips, urls []string) bool {
	if matcher == nil {
		return false
	}
	for _, raw := range domains {
		if host, ok := contractresults.NormalizeSubdomainDNSName(raw); ok && matcher.MatchesHostname(host) {
			return true
		}
	}
	for _, raw := range ips {
		if address, err := netip.ParseAddr(strings.TrimSpace(raw)); err == nil && matcher.MatchesHostname(address.Unmap().String()) {
			return true
		}
	}
	for _, raw := range urls {
		if matcher.MatchesURL(raw) {
			return true
		}
	}
	return false
}

func (s *Service) assetRules() (AssetRuleStore, error) {
	store, ok := s.store.(AssetRuleStore)
	if !ok {
		return nil, ErrNotFound
	}
	return store, nil
}
func (s *Service) AssetRules(ctx context.Context, user int, taskID int64) ([]AssetRule, error) {
	if user <= 0 || taskID < 0 {
		return nil, ErrInvalidInput
	}
	store, err := s.assetRules()
	if err != nil {
		return nil, err
	}
	return store.ListAssetRules(ctx, user, taskID)
}

// NormalizeAssetRule applies the same validation to rule editing and atomic task creation.
func NormalizeAssetRule(taskID int64, rule AssetRule) (AssetRule, error) {
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	if taskID == 0 {
		rule.Action = "block"
	} else if rule.Action == "" {
		rule.Action = "block"
	}
	if taskID < 0 || rule.Pattern == "" || len(rule.Pattern) > 8192 || len(rule.Note) > 16000 || !utf8.ValidString(rule.Pattern+rule.Note) || strings.ContainsRune(rule.Pattern+rule.Note, '\x00') || rule.Action != "block" && rule.Action != "allow" {
		return AssetRule{}, ErrInvalidInput
	}
	switch rule.Kind {
	case "exact_domain", "exact_url", "fuzzy_domain", "fuzzy_ip", "fuzzy_url":
	case "exact_ip":
		if net.ParseIP(rule.Pattern) == nil {
			return AssetRule{}, ErrInvalidInput
		}
	case "cidr":
		if _, _, err := net.ParseCIDR(rule.Pattern); err != nil {
			return AssetRule{}, ErrInvalidInput
		}
	default:
		return AssetRule{}, ErrInvalidInput
	}
	return rule, nil
}
func (s *Service) SaveAssetRule(ctx context.Context, user int, taskID int64, rule AssetRule, create bool) (AssetRule, error) {
	if user <= 0 || !create && rule.ID <= 0 {
		return AssetRule{}, ErrInvalidInput
	}
	rule, err := NormalizeAssetRule(taskID, rule)
	if err != nil {
		return AssetRule{}, err
	}
	store, err := s.assetRules()
	if err != nil {
		return AssetRule{}, err
	}
	return store.SaveAssetRule(ctx, user, taskID, rule, create)
}
func (s *Service) DeleteAssetRule(ctx context.Context, user int, taskID, id int64) error {
	if user <= 0 || taskID < 0 || id <= 0 {
		return ErrInvalidInput
	}
	store, err := s.assetRules()
	if err != nil {
		return err
	}
	return store.DeleteAssetRule(ctx, user, taskID, id)
}
func (s *Service) ToggleAssetRule(ctx context.Context, user int, taskID, id int64, enabled bool) error {
	if user <= 0 || taskID < 0 || id <= 0 {
		return ErrInvalidInput
	}
	store, err := s.assetRules()
	if err != nil {
		return err
	}
	return store.ToggleAssetRule(ctx, user, taskID, id, enabled)
}

func splitAssetRules(rules []AssetRule) (block, allow []artexdb.AssetInterceptRule) {
	for _, item := range rules {
		rule := artexdb.AssetInterceptRule{ID: item.ID, Enabled: item.Enabled, Kind: item.Kind, Pattern: item.Pattern, Note: item.Note, Builtin: item.Builtin, Action: item.Action}
		if item.Action == "allow" {
			allow = append(allow, rule)
		} else {
			block = append(block, rule)
		}
	}
	return block, allow
}
func assetCandidates(input assets.AssetInput, label, typ string, deriveRoot bool) (domains, ips, urls []string) {
	addHost := func(host string) {
		host = strings.TrimSpace(host)
		if host == "" {
			return
		}
		if net.ParseIP(host) != nil {
			ips = append(ips, host)
			return
		}
		domains = append(domains, host)
		if root, err := publicsuffix.EffectiveTLDPlusOne(strings.ToLower(host)); deriveRoot && err == nil && root != host {
			domains = append(domains, root)
		}
	}
	addURL := func(raw string) {
		if raw = strings.TrimSpace(raw); raw == "" {
			return
		}
		urls = append(urls, raw)
		if parsed, err := url.Parse(raw); err == nil {
			addHost(parsed.Hostname())
		}
	}
	addHost(input.Domain)
	for _, host := range input.BoundDomains {
		addHost(host)
	}
	addHost(input.IP)
	addHost(input.ServiceIP)
	addURL(input.URL)
	switch typ {
	case "root_domain", "subdomain", "ip":
		addHost(label)
	case "service", "endpoint":
		if strings.Contains(label, "://") {
			addURL(label)
		} else if host, _, err := net.SplitHostPort(label); err == nil {
			addHost(host)
		}
	}
	return
}

func (s *Service) CheckAssetInputs(ctx context.Context, user int, taskID int64, runID string, inputs []assets.AssetInput) ([]string, error) {
	store, err := s.assetRules()
	if err != nil {
		return nil, err
	}
	rules, err := store.EffectiveAssetRules(ctx, user, taskID, runID)
	if err != nil {
		return nil, err
	}
	block, allow := splitAssetRules(rules)
	reasons := make([]string, len(inputs))
	matchers := map[int]*blacklist.Matcher{}
	for i, input := range inputs {
		domains, ips, urls := assetCandidates(input, "", input.Type, true)
		platformDomains, platformIPs, platformURLs := assetCandidates(input, "", input.Type, false)
		matcher, ok := matchers[input.TargetID]
		if !ok {
			matcher, err = s.platformMatcher(ctx, input.TargetID)
			if err != nil {
				return nil, err
			}
			matchers[input.TargetID] = matcher
		}
		if platformBlocks(matcher, platformDomains, platformIPs, platformURLs) {
			reasons[i] = "命中系统扫描黑名单"
		} else if result := artexdb.EvaluateAssetGate(block, allow, domains, ips, urls); !result.Allowed {
			reasons[i] = result.Reason
		}
	}
	return reasons, nil
}
func (s *Service) CheckAssetIDs(ctx context.Context, user int, taskID int64, runID string, ids []int64, targetID int) error {
	if len(ids) == 0 {
		return nil
	}
	store, err := s.assetRules()
	if err != nil {
		return err
	}
	rules, err := store.EffectiveAssetRules(ctx, user, taskID, runID)
	if err != nil {
		return err
	}
	items, err := store.AssetCandidates(ctx, user, taskID, runID, ids)
	if err != nil {
		return err
	}
	block, allow := splitAssetRules(rules)
	matcher, err := s.platformMatcher(ctx, targetID)
	if err != nil {
		return err
	}
	for _, item := range items {
		domains, ips, urls := assetCandidates(assets.AssetInput{}, item.Label, item.Type, true)
		platformDomains, platformIPs, platformURLs := assetCandidates(assets.AssetInput{}, item.Label, item.Type, false)
		for _, input := range item.Inputs {
			d, i, u := assetCandidates(input, "", input.Type, true)
			domains, ips, urls = append(domains, d...), append(ips, i...), append(urls, u...)
			d, i, u = assetCandidates(input, "", input.Type, false)
			platformDomains, platformIPs, platformURLs = append(platformDomains, d...), append(platformIPs, i...), append(platformURLs, u...)
		}
		if platformBlocks(matcher, platformDomains, platformIPs, platformURLs) {
			return fmt.Errorf("资产 #%d [%s] %s：命中系统扫描黑名单", item.ID, item.Type, item.Label)
		}
		if result := artexdb.EvaluateAssetGate(block, allow, domains, ips, urls); !result.Allowed {
			return fmt.Errorf("资产 #%d [%s] %s：%s", item.ID, item.Type, item.Label, result.Reason)
		}
	}
	return nil
}
