package application

import (
	"html"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	contractresults "github.com/yyhuni/lunafox/contracts/results"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/text/unicode/norm"
)

// ApplicationCandidate is an evidence-based, reviewable identification, not
// proof that two websites are the same product or a new scan authorization.
type ApplicationCandidate struct {
	Name          string
	IdentityName  string
	Scope         string
	Hostname      string
	EvidenceKind  string
	EvidenceValue string
}

var genericApplicationTitles = map[string]bool{
	"login": true, "log in": true, "sign in": true, "signin": true, "home": true, "homepage": true, "home page": true, "index": true, "welcome": true, "welcome back": true, "dashboard": true, "portal": true, "administrator": true, "admin": true,
	"首页": true, "主页": true, "登录": true, "登录页": true, "用户登录": true, "系统登录": true, "欢迎": true, "欢迎使用": true, "欢迎访问": true, "管理后台": true, "后台管理": true, "控制台": true, "管理系统": true, "网站首页": true,
	"access denied": true, "forbidden": true, "not found": true, "error": true, "bad gateway": true, "service unavailable": true, "just a moment...": true, "just a moment": true, "untitled": true, "无标题": true, "ok": true, "default page": true,
}
var genericErrorPrefix = regexp.MustCompile(`(?i)^(?:http\s*)?(?:error\s*)?[45][0-9]{2}(?:\b|\s|[\-:])|^3[0-9]{2}\s+(?:moved|found|redirect|see other|temporary|permanent)|^(access denied|access forbidden|forbidden|not found|bad gateway|service unavailable|internal server error|page not found|just a moment)(?:\b|\s|[\-:：.!！])`)
var numericTitle = regexp.MustCompile(`^[\p{N}\p{P}\p{Z}\s]+$`)
var recognizedProducts = map[string]string{
	"jenkins": "Jenkins", "grafana": "Grafana", "gitlab": "GitLab", "jira": "Jira", "confluence": "Confluence", "wordpress": "WordPress", "zabbix": "Zabbix", "kibana": "Kibana", "phpmyadmin": "phpMyAdmin", "harbor": "Harbor", "sonarqube": "SonarQube", "nacos": "Nacos", "minio": "MinIO", "nexus repository manager": "Nexus Repository Manager", "prometheus": "Prometheus", "nextcloud": "Nextcloud", "gitbucket": "GitBucket", "gitea": "Gitea",
}

func RecognizeWebApplication(rawURL, title string, technologies []string, status *int64) (ApplicationCandidate, bool) {
	var candidate ApplicationCandidate
	if status != nil && (*status < 200 || *status >= 400) {
		return candidate, false
	}
	u, err := url.Parse(rawURL)
	if err != nil || (strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https") || u.Hostname() == "" {
		return candidate, false
	}
	host := strings.TrimSpace(u.Hostname())
	if address, err := netip.ParseAddr(host); err == nil {
		candidate.Scope = address.Unmap().String()
		candidate.Hostname = candidate.Scope
	} else {
		domain, ok := contractresults.NormalizeSubdomainDNSName(host)
		if !ok {
			return candidate, false
		}
		root, err := publicsuffix.EffectiveTLDPlusOne(domain)
		if err != nil || root == "" {
			return candidate, false
		}
		candidate.Scope = root
		candidate.Hostname = domain
	}
	clean := cleanApplicationText(title)
	if isApplicationErrorTitle(clean) {
		return ApplicationCandidate{}, false
	}
	if clean != "" {
		for _, separator := range []string{" | ", "｜", " - ", " – ", " — ", " · "} {
			clean = strings.ReplaceAll(clean, separator, "\x00")
		}
		for _, part := range strings.Split(clean, "\x00") {
			part = strings.TrimSpace(part)
			if meaningfulApplicationName(part) {
				candidate.Name, candidate.EvidenceKind, candidate.EvidenceValue = part, "title", cleanApplicationText(title)
				break
			}
		}
	}
	if candidate.Name == "" && status != nil {
		for _, tech := range technologies {
			key := strings.ToLower(strings.TrimSpace(strings.SplitN(tech, ":", 2)[0]))
			if name, ok := recognizedProducts[key]; ok {
				candidate.Name, candidate.EvidenceKind, candidate.EvidenceValue = name, "technology", name
				break
			}
		}
	}
	if candidate.Name == "" {
		return ApplicationCandidate{}, false
	}
	candidate.IdentityName = strings.ToLower(candidate.Name)
	return candidate, true
}

func cleanApplicationText(value string) string {
	if len(value) > 4096 || !utf8.ValidString(value) {
		return ""
	}
	value = norm.NFKC.String(html.UnescapeString(value))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u200b' || r == '\ufeff' {
			return ' '
		}
		return r
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func meaningfulApplicationName(name string) bool {
	if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 120 || len(name) > 500 || numericTitle.MatchString(name) || isApplicationErrorTitle(name) {
		return false
	}
	lower := strings.ToLower(strings.Trim(strings.TrimSpace(name), " .!！:：-—"))
	if genericApplicationTitles[lower] || strings.HasPrefix(lower, "welcome to nginx") || strings.HasPrefix(lower, "index of ") {
		return false
	}
	return true
}

func isApplicationErrorTitle(value string) bool {
	if genericErrorPrefix.MatchString(value) {
		return true
	}
	for _, prefix := range []string{"网站无法访问", "访问被拒绝", "拒绝访问", "系统错误", "页面不存在", "安全拦截", "请求被拒绝"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
