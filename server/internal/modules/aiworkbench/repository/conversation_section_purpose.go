package repository

import (
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	ai "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

// Only the presentation reads this vocabulary. Matches produce a topic key,
// never command text or a new instruction for the agent.
var sectionTopics = [...]struct {
	key     string
	pattern *regexp.Regexp
}{
	{"accountRecovery", regexp.MustCompile(`(?i)\b(?:forgot|recover(?:y)?|reset(?:password|pwd)?|password reset)\b|找回|重置密码|账号恢复`)},
	{"captcha", regexp.MustCompile(`(?i)\b(?:captcha|recaptcha|human verification)\b|图形验证码|人机校验|人机验证`)},
	{"codeDelivery", regexp.MustCompile(`(?i)\b(?:sendcode|sms|(?:email|mail|send) code|send (?:email|mail)|mail delivery)\b|验证码发送|发送验证码|短信投递|邮件投递`)},
	{"verification", regexp.MustCompile(`(?i)\b(?:otp|verification|verifycode|verify code|verify email|email verify|verificationcode)\b|验证码|邮箱验证`)},
	{"access", regexp.MustCompile(`(?i)\b(?:permission[s]?|roles?|ownership|access control|access rights?|acl|entitlements?|forbidden)\b|权限|访问控制|访问边界|资源归属`)},
	{"session", regexp.MustCompile(`(?i)\b(?:sessions?|oauth2?|sso|log ?in|sign ?in|logout|authentication|refresh token|bearer token)\b|登录|会话|身份认证`)},
	{"accounts", regexp.MustCompile(`(?i)\b(?:accounts?|usernames?|user ?info|profiles?|register|registration|sign ?up|email address)\b|账户|账号|用户资料|注册信息|账号注册`)},
	{"serviceLimits", regexp.MustCompile(`(?i)\b(?:quota|throttl[a-z]*|rate limit[a-z]*|retry after|request limit)\b|限流|频率|配额`)},
	{"assetInventory", regexp.MustCompile(`(?i)\b(?:inventor(?:y|ies)|assets?|dns|subdomains?|cname|nameserver|hostnames?)\b|资产|台账|域名|解析记录`)},
	{"storage", regexp.MustCompile(`(?i)\b(?:storage|buckets?|containers?|blob[s]?|object store|object listing)\b|对象存储|存储桶|存储容器`)},
	{"frontend", regexp.MustCompile(`(?i)\b(?:source ?maps?|bundles?|front ?end|javascript|webpack|static resources?)\b|前端|静态资源|脚本资源`)},
	{"interfaces", regexp.MustCompile(`(?i)\b(?:openapi|swagger|endpoints?|routes?|schema|parameters?|api docs?)\b|接口|路由|请求字段`)},
	{"records", regexp.MustCompile(`(?i)\b(?:exports?|datasets?|metadata|csv|records?|spreadsheets?|invoices?)\b|导出|数据记录|元数据|账单|清单比对`)},
	{"configuration", regexp.MustCompile(`(?i)\b(?:configuration|config|settings|versions?|manifests?)\b|配置|版本信息`)},
	{"certificates", regexp.MustCompile(`(?i)\b(?:x509|getpeercert|peer certificate|certificate (?:chain|issuer|expiry|validity)|(?:tls|ssl) (?:versions?|policy|settings|certificates?))\b|\.(?:crt|pem)\b|证书|加密连接`)},
	{"comparison", regexp.MustCompile(`(?i)\b(?:baseline|compar(?:e|ing|ison)|diff(?:erences?)?|control group)\b|基线|对照|差异|比对`)},
	{"media", regexp.MustCompile(`(?i)\b(?:pillow|pil|image\.open|exif|tesseract|ocr)\b|\.(?:png|jpe?g|tiff?)\b|图像|图片材料`)},
	{"formats", regexp.MustCompile(`(?i)\b(?:struct\.(?:unpack|pack)|unpack_from|b64decode|zipfile|zlib|file format|binary format|serializ[a-z]+|decode\()|编码格式|文件结构|二进制格式`)},
	{"trafficRecords", regexp.MustCompile(`(?i)\b(?:pcap(?:ng)?|wireshark|http archive|captured traffic)\b|\.har\b|抓包记录|流量记录|已采集流量`)},
	{"evidence", regexp.MustCompile(`(?i)\b(?:evidence|receipts?|findings?|reports?|conclusions?)\b|证据|报告|复核结果|登记发现`)},
	{"responses", regexp.MustCompile(`(?i)\b(?:http[s]? response|status code|response body|response status|api)\b|响应状态|接口返回`)},
}
var topicURL = regexp.MustCompile(`(?i)https?://[^[:space:]"'<>\\]+`)
var workspaceSession = regexp.MustCompile(`(?i)(?:[a-z0-9_.~/\-]+/)?sessions/[a-z0-9_\-]{8,}/?`)
var runtimeBoilerplate = regexp.MustCompile(`(?i)\b(?:requests?\.session|aiohttp\.clientsession|session\.(?:post|get|headers|request|cookies))\b|\buser-agent\b`)
var wordBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
var topicSeparators = strings.NewReplacer("_", " ", "-", " ")
var taskLead = regexp.MustCompile(`(?i)^(?:(?:当前(?:子)?任务|本段(?:任务|目的)|子任务|任务目标|目的|objective|current task|task|purpose)\s*[:：]\s*|(?:接下来(?:我会|将)?|下一步(?:我会|将)?|now i(?:'ll| will)|next i(?:'ll| will))[,，:\s：]*)(.+)$`)
var proseAction = regexp.MustCompile(`(?i)^(?:核对|检查|梳理|统计|整理|比对|确认|分析|汇总|验证|收集|查找|review\b|compare\b|reconcile\b|check\b|inspect\b|collect\b|validate\b|summari[sz]e\b|verify\b|identify\b)`)
var taskCode = regexp.MustCompile(`(?i)(?:https?://|[\x00-\x1f{};<>\x60$|\\]|(?:^|\s)--?[a-z]|(?:^|\s)/(?:tmp|home|var|etc)/|^[a-zA-Z0-9_./+-]+\.(?:py|js|sh)\b|^(?:bash|sh|python[0-9.]*|curl|wget|node|ls|cat|cd|grep|echo|printf)\s)`)
var privateTaskText = regexp.MustCompile(`(?i)(?:\b(?:token|passphrase|passwd|password|api[_-]?key|secret|session[_-]?id)\s*[:=]|[a-z0-9._+-]+@[a-z0-9.-]+\.[a-z]{2,}|[a-z0-9_+/=-]{40,})`)
var genericTask = regexp.MustCompile(`(?i)^(?:(?:task|step|phase|任务|步骤)\s*[#0-9–— -]*|执行命令|运行脚本|继续执行|run (?:a |the )?(?:script|command)|bash|python[0-9.]*|shell|continue)[.!。！ ]*$`)
var wordTitle = regexp.MustCompile(`^[a-zA-Z]{3,36}$`)
var namedArtifact = regexp.MustCompile(`(?i)(?:^|[/\s"'=])([\p{L}0-9_][\p{L}0-9_.-]{1,54}\.(?:png|jpe?g|pdf|csv|xlsx?|json|xml|har|pcap|md))\b`)
var opaqueArtifact = regexp.MustCompile(`(?i)[a-f0-9]{14,}|[a-z0-9]{25,}|passw|credential|token|secret|api[_-]?key|session[_-]?id`)

type purposeSignal struct {
	active                            *ai.TimelinePurpose
	nextTodo                          *ai.TimelinePurpose
	note                              string
	subject                           string
	action, actionSubject, actionKind string
	scores                            [len(sectionTopics)]uint8
	resultScores                      [len(sectionTopics)]uint8
}

func taskProse(raw string) string {
	raw = strings.TrimSpace(raw)
	raw, _, _ = strings.Cut(raw, "\n")
	if n := utf8.RuneCountInString(raw); n < 2 || n > 2048 || taskCode.MatchString(raw) || privateTaskText.MatchString(raw) || genericTask.MatchString(raw) {
		return ""
	}
	text := previewText(raw, 150)
	if !strings.ContainsAny(text, " \t") && utf8.RuneCountInString(text) == len(text) && !wordTitle.MatchString(text) {
		return ""
	}
	return text
}

func declaredTask(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	first = strings.TrimLeft(first, "#* \t")
	if m := taskLead.FindStringSubmatch(first); m != nil {
		return taskProse(m[1])
	}
	return ""
}

func taskStatement(source, text string, id int64, step int) *ai.TimelinePurpose {
	if text == "" {
		return nil
	}
	return &ai.TimelinePurpose{Source: source, Text: text, Basis: []ai.TimelineSource{{ActivityID: positiveActivityID(id), Step: step}}}
}
func activeTodo(input string, id int64, step int) *ai.TimelinePurpose {
	var body struct {
		Todos []struct{ Content, Status string }
	}
	if json.Unmarshal([]byte(input), &body) != nil {
		return nil
	}
	var active string
	for _, v := range body.Todos {
		if v.Status != "in_progress" {
			continue
		}
		text := taskProse(v.Content)
		if text == "" || (active != "" && active != text) {
			return nil
		}
		active = text
	}
	return taskStatement("todo", active, id, step)
}

// Remove hosts, query strings and header boilerplate before matching. A host
// called "account" or a common Authorization header is not a task description.
func topicText(raw string) string {
	text := topicURL.ReplaceAllStringFunc(raw, func(v string) string {
		u, e := url.Parse(v)
		if e != nil {
			return " "
		}
		return " " + u.EscapedPath() + " "
	})
	text = workspaceSession.ReplaceAllString(text, " ")
	text = runtimeBoilerplate.ReplaceAllString(text, " ")
	return topicSeparators.Replace(wordBoundary.ReplaceAllString(text, "${1} ${2}"))
}
func scoreTopics(scores *[len(sectionTopics)]uint8, raw string, weight uint8) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	text := topicText(raw)
	for i, rule := range sectionTopics {
		if weight > scores[i] && rule.pattern.MatchString(text) {
			scores[i] = weight
		}
	}
}

func resultPurposeText(summary string) string {
	lower := strings.ToLower(summary)
	if strings.Contains(lower, "warning:") || strings.Contains(lower, "insecurerequestwarning") || strings.Contains(lower, "traceback (") || privateTaskText.MatchString(summary) {
		return ""
	}
	return summary
}

// Only a short, explicit human heading can become display text. Other input
// fragments are used exclusively for fixed, localized topic labels.
func artifactHeading(body string) string {
	for _, line := range strings.SplitN(body, "\n", 10) {
		line = strings.TrimSpace(line)
		var heading string
		switch {
		case strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#!"):
			heading = strings.TrimLeft(line, "# ")
		case strings.HasPrefix(line, "//"):
			heading = strings.TrimSpace(strings.TrimPrefix(line, "//"))
		case strings.HasPrefix(line, "\"\"\""):
			heading = strings.Trim(line, "\" ")
		case strings.HasPrefix(line, "echo "):
			heading = strings.Trim(strings.TrimPrefix(line, "echo "), "\"'= -")
		case strings.HasPrefix(line, "print(\"") || strings.HasPrefix(line, "print('") || strings.HasPrefix(line, "console.log(\""):
			_, literal, _ := strings.Cut(line, "(")
			quote := literal[0]
			end := strings.IndexByte(literal[1:], quote)
			if end >= 0 {
				heading = strings.Trim(literal[1:end+1], "= -")
			}
		default:
			continue
		}
		if text := declaredTask(heading); text != "" {
			return text
		}
		if proseAction.MatchString(heading) {
			if text := taskProse(heading); text != "" {
				return text
			}
		}
	}
	return ""
}

func taskSignal(tool, raw string, id int64, number int) purposeSignal {
	var h purposeSignal
	if tool == "TodoWrite" {
		h.nextTodo = activeTodo(raw, id, number)
		return h
	}
	fields := objectInput(raw)
	h.action, h.actionSubject, h.actionKind = stepActionFields(tool, fields)
	for _, key := range []string{"objective", "task", "description"} {
		if note := taskProse(stringField(fields, key)); note != "" {
			h.note = note
			break
		}
	}
	if tool == "traffic_get" || tool == "traffic_search" {
		scoreTopics(&h.scores, "captured traffic", 5)
	}
	target := stringField(fields, "url")
	var material string
	var file string
	switch tool {
	case "Bash":
		material = stringField(fields, "command")
	case "Read", "Write", "Edit", "MultiEdit", "Grep", "Glob", "LS":
		for _, key := range []string{"file_path", "path"} {
			if name := stringField(fields, key); name != "" {
				file = path.Base(strings.ReplaceAll(name, "\\", "/"))
				scoreTopics(&h.scores, file, 5)
				break
			}
		}
		if tool == "Write" {
			material = stringField(fields, "content")
		}
	}
	if fields == nil && (tool == "Write" || tool == "Bash") {
		// The stored prefix may end inside a long JSON string. Never quote it.
		scoreTopics(&h.scores, raw, 1)
	}
	if len(material) > 3072 {
		material = material[:3072]
	}
	if h.note == "" {
		h.note = artifactHeading(material)
	}
	scoreTopics(&h.scores, h.note, 7)
	scoreTopics(&h.scores, target+" "+material, 1)
	if target == "" {
		target = topicURL.FindString(material)
	}
	if u, err := url.Parse(target); err == nil && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") {
		host := u.Hostname()
		if len(host) <= 100 && !secretText.MatchString(host) && !privateTaskText.MatchString(host) {
			h.subject = host
		}
	}
	if h.subject == "" {
		match := namedArtifact.FindStringSubmatch(file)
		if len(match) == 0 && !secretText.MatchString(material) && !privateTaskText.MatchString(material) {
			match = namedArtifact.FindStringSubmatch(topicURL.ReplaceAllString(material, " "))
		}
		if len(match) > 1 && !opaqueArtifact.MatchString(match[1]) {
			h.subject = match[1]
		}
	}
	return h
}

type topicVote struct {
	score, steps int
	basis        ai.TimelineSource
	last         ai.TimelineSource
	best         uint8
}

func purposeOf(calls []timelineMeta, first int) ai.TimelinePurpose {
	if active := calls[0].purpose.active; active != nil {
		return *active
	}
	var votes [len(sectionTopics)]topicVote
	type noteVote struct {
		text string
		hits int
		ref  ai.TimelineSource
	}
	var notes []noteVote
	noteIndex := map[string]int{}
	hosts := map[string]int{}
	commonHost := ""
	var receipt *ai.TimelineFinding
	for i, step := range calls {
		ref := ai.TimelineSource{ActivityID: positiveActivityID(step.id), Step: first + i}
		if step.finding != nil {
			receipt = step.finding
		}
		if host := step.purpose.subject; host != "" {
			hosts[host]++
			if hosts[host] > hosts[commonHost] {
				commonHost = host
			}
		}
		if value := step.purpose.note; value != "" {
			if j, ok := noteIndex[value]; ok {
				notes[j].hits++
			} else {
				noteIndex[value] = len(notes)
				notes = append(notes, noteVote{value, 1, ref})
			}
		}
		for j, score := range step.purpose.scores {
			source := ref
			if weight := step.purpose.resultScores[j]; weight > score {
				score = weight
				source.ActivityID = positiveActivityID(step.result)
			}
			if score == 0 {
				continue
			}
			v := &votes[j]
			v.score += int(score)
			v.steps++
			v.last = source
			if score > v.best {
				v.best = score
				v.basis = source
			}
		}
	}
	if hosts[commonHost] < 2 && len(calls) > 1 {
		commonHost = ""
	}
	var strongest noteVote
	for _, n := range notes {
		if n.hits > strongest.hits {
			strongest = n
		}
	}
	if strongest.hits > 0 && (len(notes) == 1 || strongest.hits*2 > len(calls)) {
		return ai.TimelinePurpose{Source: "step", Text: strongest.text, Subject: commonHost, SupportingSteps: strongest.hits, Basis: []ai.TimelineSource{strongest.ref}}
	}
	var topics []int
	for i, v := range votes {
		if v.score > 0 && (v.steps >= 2 || v.best >= 3 || len(calls) == 1) {
			topics = append(topics, i)
		}
	}
	slices.SortStableFunc(topics, func(a, b int) int { return votes[b].score - votes[a].score })
	if len(topics) > 0 {
		primary := votes[topics[0]]
		p := ai.TimelinePurpose{Source: "observed", Subject: commonHost, SupportingSteps: primary.steps}
		for _, j := range topics {
			if len(p.Topics) == 2 || votes[j].score*2 < votes[topics[0]].score {
				break
			}
			p.Topics = append(p.Topics, sectionTopics[j].key)
			b := votes[j].basis
			if !slices.Contains(p.Basis, b) {
				p.Basis = append(p.Basis, b)
			}
		}
		if !slices.Contains(p.Basis, primary.last) {
			p.Basis = append(p.Basis, primary.last)
		}
		return p
	}
	if receipt != nil {
		return ai.TimelinePurpose{Source: "receipt", Text: taskProse(receipt.Title), Topics: []string{"evidence"}, Basis: []ai.TimelineSource{{ActivityID: receipt.SourceActivityID, Step: receipt.Step}}}
	}
	if strongest.hits > 0 {
		return ai.TimelinePurpose{Source: "step", Text: strongest.text, Basis: []ai.TimelineSource{strongest.ref}}
	}
	return ai.TimelinePurpose{Source: "unknown"}
}

func sameTask(a, b ai.TimelinePurpose) bool {
	if a.Source != b.Source {
		return false
	}
	if a.Source == "observed" {
		return len(a.Topics) > 0 && len(b.Topics) > 0 && a.Topics[0] == b.Topics[0]
	}
	return a.Text == b.Text
}
func sameDeclaration(a, b *ai.TimelinePurpose) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Source == b.Source && a.Text == b.Text
}

// Explicit task changes form exact boundaries. For older, unlabelled traces,
// aggregate small consecutive windows and coalesce matching topics. Detail
// pagination stays independent of these presentation-only boundaries.
func timelineSections(calls []timelineMeta) []ai.TimelineSection {
	sections := []ai.TimelineSection{}
	window := min(sectionWidth(len(calls))/2, max(2, (len(calls)+11)/12))
	for start := 0; start < len(calls); {
		end := start + 1
		for end < len(calls) && sameDeclaration(calls[start].purpose.active, calls[end].purpose.active) {
			end++
		}
		for i := start; i < end; {
			stop := end
			if calls[start].purpose.active == nil {
				stop = min(end, i+window)
			}
			p := purposeOf(calls[i:stop], i+1)
			if i > start && sameTask(sections[len(sections)-1].Purpose, p) {
				sections[len(sections)-1].Last = stop
			} else {
				sections = append(sections, ai.TimelineSection{First: i + 1, Last: stop, Tool: calls[i].tool, Purpose: p})
			}
			i = stop
		}
		start = end
	}
	for i := range sections {
		s := &sections[i]
		s.Purpose = purposeOf(calls[s.First-1:s.Last], s.First)
	}
	return sections
}
