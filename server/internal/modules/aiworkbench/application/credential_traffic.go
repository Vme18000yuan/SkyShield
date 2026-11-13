// Credential traffic matching is adapted from ARTEX; AGPL-3.0-only.
package application

import (
	"context"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Autumn-27/artex/agent"
	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
)

type ScopedCredentialTrafficMatcher struct {
	store   CredentialTrafficStore
	manager TaskTrafficManager
}

func NewScopedCredentialTrafficMatcher(store CredentialTrafficStore, manager TaskTrafficManager) *ScopedCredentialTrafficMatcher {
	return &ScopedCredentialTrafficMatcher{store: store, manager: manager}
}

var credentialURLs = regexp.MustCompile(`https?://[^\s<>"'，；）)]+`)
var credentialTrafficIDs = regexp.MustCompile(`\b[0-9]{8,}-[0-9]{3,}(?:-[a-f0-9]{32})?\b`)

func credentialTrafficHosts(assets []CredentialTrafficAsset, content, evidence string) map[string]bool {
	hosts := map[string]bool{}
	add := func(raw string, children bool) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		u, err := url.Parse(raw)
		if err == nil && u.Hostname() != "" {
			host := strings.ToLower(u.Hostname())
			hosts[host] = hosts[host] || children
		}
	}
	for _, item := range assets {
		switch item.Type {
		case "root_domain":
			add(item.Label, true)
		case "subdomain", "ip", "service", "endpoint":
			add(item.Label, false)
		}
	}
	for _, raw := range credentialURLs.FindAllString(content+"\n"+evidence, 16) {
		add(raw, false)
	}
	return hosts
}
func credentialHostMatches(raw string, hosts map[string]bool) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for known, children := range hosts {
		if host == known || children && strings.HasSuffix(host, "."+known) {
			return true
		}
	}
	return false
}
func credentialValueMatches(raw string, account artexdb.CredentialAccount) bool {
	source := agent.CredentialSourceText(raw)
	for _, secret := range []string{account.Token, account.Cookie} {
		if len(secret) >= 8 && strings.Contains(source, secret) {
			return true
		}
	}
	if len(account.Password) < 6 || !strings.Contains(source, account.Password) {
		return false
	}
	if account.Username != "" {
		return strings.Contains(source, account.Username)
	}
	return len(account.Password) >= 8
}
func credentialTrafficExcerpt(raw string, accounts []artexdb.CredentialAccount) string {
	index := 0
	found := false
	for _, account := range accounts {
		for _, secret := range []string{account.Token, account.Cookie, account.Password} {
			if secret != "" {
				if pos := strings.Index(raw, secret); pos >= 0 {
					index = pos
					found = true
					break
				}
			}
		}
		if found {
			break
		}
	}
	start := len([]rune(raw[:index])) - 100
	if start < 0 {
		start = 0
	}
	all := []rune(raw)
	end := min(start+600, len(all))
	return string(all[start:end])
}
func (m *ScopedCredentialTrafficMatcher) Match(ctx context.Context, job *CredentialJob, content, evidence string, accounts []artexdb.CredentialAccount) ([]artexdb.CredentialTraffic, string, string, *int64, error) {
	bound := []artexdb.CredentialTraffic{}
	if m == nil || m.store == nil || m.manager == nil || job == nil {
		return bound, "unavailable", "流量存储不可用", nil, nil
	}
	on, epoch, err := m.store.TrafficState(ctx, job)
	if err != nil {
		return bound, "error", "流量核对权限不可用", nil, err
	}
	if !on {
		return bound, "off", "", nil, nil
	}
	check := func() error {
		enabled, current, err := m.store.TrafficState(ctx, job)
		if err != nil {
			return err
		}
		if !enabled || current != epoch {
			return ErrCredentialBindingStateChanged
		}
		return nil
	}
	assets, err := m.store.TrafficAssets(ctx, job)
	if err != nil {
		return bound, "error", "无法读取关联资产", &epoch, err
	}
	hosts := credentialTrafficHosts(assets, content, evidence)
	if len(hosts) == 0 {
		return bound, "unavailable", "没有可核对的关联资产或 URL，未猜测流量归属", &epoch, nil
	}
	names := make([]string, 0, len(hosts))
	for host := range hosts {
		names = append(names, host)
	}
	sort.Strings(names)
	if len(names) > 8 {
		names = names[:8]
	}
	found, err := m.manager.Read(ctx, aitraffic.Scope{TenantID: job.TenantID, UserID: job.UserID, TaskID: job.TaskID}, func(store *traffic.Traffic) error {
		seen := map[string]bool{}
		inspected, queries := 0, 0
		var interrupted error
		inspect := func(id string) {
			if seen[id] || inspected >= 32 || len(bound) >= 12 || ctx.Err() != nil || interrupted != nil {
				return
			}
			if interrupted = check(); interrupted != nil {
				return
			}
			seen[id] = true
			inspected++
			_ = store.ReadEvidence(ctx, []string{id}, func(exchange traffic.EvidenceExchange) error {
				if !credentialHostMatches(exchange.URL, hosts) {
					return nil
				}
				request, err := io.ReadAll(io.LimitReader(exchange.Request, 128<<10))
				if err != nil {
					return err
				}
				response, err := io.ReadAll(io.LimitReader(exchange.Response, 128<<10))
				if err != nil {
					return err
				}
				head := func(value string) string {
					if len(value) > 8192 {
						return value[:8192]
					}
					return value
				}
				for _, side := range []struct{ name, raw string }{{"request", head(exchange.ReqHead) + "\n" + string(request)}, {"response", head(exchange.RespHead) + "\n" + string(response)}} {
					matches := []int{}
					for i, account := range accounts {
						if credentialValueMatches(side.raw, account) {
							matches = append(matches, i)
						}
					}
					if len(matches) > 0 {
						bound = append(bound, artexdb.CredentialTraffic{ID: exchange.ID, URL: exchange.URL, Method: exchange.Method, Status: exchange.Status, Side: side.name, Excerpt: credentialTrafficExcerpt(side.raw, accounts), Accounts: matches})
						break
					}
				}
				return nil
			})
		}
		for _, id := range credentialTrafficIDs.FindAllString(evidence, 8) {
			inspect(id)
		}
		for _, account := range accounts {
			for _, secret := range []string{account.Token, account.Cookie, account.Password} {
				if len(secret) < 6 {
					continue
				}
				for _, host := range names {
					if queries >= 24 || len(bound) >= 12 || ctx.Err() != nil || interrupted != nil {
						break
					}
					if interrupted = check(); interrupted != nil {
						break
					}
					queries++
					rows, _, err := store.Page(traffic.PageQuery{Host: host, Body: secret, RespMin: -1, RespMax: -1}, 0, 4)
					if err != nil {
						continue
					}
					for _, row := range rows {
						inspect(row.ID)
					}
				}
			}
		}
		if interrupted != nil {
			return interrupted
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return check()
	})
	if err != nil {
		return bound, "error", "流量核对已中断，可手动重试", &epoch, err
	}
	if !found {
		return bound, "unavailable", "没有可用的流量记录", &epoch, nil
	}
	if len(bound) == 0 {
		return bound, "none", "未找到包含匹配凭据的已抓取流量；不会改变凭据验证状态", &epoch, nil
	}
	return bound, "ready", "已核对资产域名及凭据原文，保存了流量引用和片段", &epoch, nil
}
