package repository

import (
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	ai "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
)

var secretText = regexp.MustCompile(`(?i)(authorization\s*:|(?:set-)?cookie\s*:|\b(?:bearer|basic)\s+\S+|(?:password|secret|api[_-]?key|(?:access|refresh)[_-]?token)\s*["']?\s*[:=]|-----BEGIN .+PRIVATE KEY)`)
var programName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.+-]{0,35}$`)
var recordedHTTPHost = regexp.MustCompile(`https?://[a-zA-Z0-9._:\[\]-]+(?:[/\s'"]|$)`)
var scriptPath = regexp.MustCompile(`^[a-zA-Z0-9_./-]+\.(?:py|js|mjs|sh)$`)
var requirementLine = regexp.MustCompile(`^\s*(?:[-*]\s+|\d{1,3}[.)、]\s*)(?:\[([xX ])\]\s*)?(.+)$`)
var selectedAsset = regexp.MustCompile(`@\[(?:企业|资产|接口|IP|应用|域名|子域名|服务)#[0-9]+(?: [^\]\r\n]*)?\]`)

func userRequestText(raw, fallback string) string {
	var envelope struct {
		Text        string
		Attachments json.RawMessage
	}
	if strings.HasPrefix(strings.TrimSpace(raw), "{") && json.Unmarshal([]byte(raw), &envelope) == nil && envelope.Attachments != nil {
		if strings.TrimSpace(envelope.Text) != "" {
			return envelope.Text
		}
		return fallback
	}
	return raw
}

func previewText(raw string, limit int) string {
	if secretText.MatchString(raw) {
		return ""
	}
	clean := strings.Join(strings.Fields(raw), " ")
	if utf8.RuneCountInString(clean) > limit {
		return string([]rune(clean)[:limit]) + "…"
	}
	return clean
}

func objectInput(raw string) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	start := strings.IndexByte(raw, '{')
	if start < 0 || json.Unmarshal([]byte(raw[start:]), &fields) != nil {
		return nil
	}
	return fields
}
func stringField(fields map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(fields[key], &value)
	return strings.TrimSpace(value)
}

func stepAction(tool, input string) (action, subject, kind string) {
	return stepActionFields(tool, objectInput(input))
}
func stepActionFields(tool string, fields map[string]json.RawMessage) (action, subject, kind string) {
	action = previewText(stringField(fields, "description"), 170)
	if tool == "report_vulnerability" {
		subject = previewText(stringField(fields, "name"), 100)
		return
	}
	if u, err := url.Parse(stringField(fields, "url")); err == nil && u.Hostname() != "" {
		subject = u.Hostname()
		return
	}
	for _, key := range []string{"file_path", "path"} {
		if v := stringField(fields, key); v != "" {
			subject = previewText(path.Base(strings.ReplaceAll(v, "\\", "/")), 90)
			return
		}
	}
	if tool == "Bash" {
		command := stringField(fields, "command")
		words := strings.Fields(command)
		if len(words) > 0 && programName.MatchString(path.Base(words[0])) {
			program := path.Base(words[0])
			subject = program
			switch program {
			case "curl", "wget":
				kind = "http"
			case "python", "python3", "node", "bash", "sh":
				kind = "script"
			case "cat", "head", "tail", "wc":
				kind = "fileRead"
			case "grep", "rg", "find":
				kind = "fileSearch"
			case "ls":
				kind = "directory"
			}
			if len(words) > 1 && scriptPath.MatchString(words[1]) {
				subject = program + " · " + path.Base(words[1])
			}
		}
		if literal := recordedHTTPHost.FindString(command); literal != "" {
			if u, err := url.Parse(strings.TrimRight(literal, "/'\" \n\t")); err == nil && u.Hostname() != "" {
				if subject != "" {
					subject += " · "
				}
				subject += u.Hostname()
			}
		}
	}
	return
}
func stepOutcome(summary, detail string) (string, *int) {
	fields := objectInput(detail)
	var count *int
	for _, key := range []string{"totalSize", "total_size", "count", "total"} {
		var n int
		if v := fields[key]; len(v) > 0 && string(v) != "null" && json.Unmarshal(v, &n) == nil && n >= 0 {
			count = &n
			break
		}
	}
	for _, key := range []string{"message", "summary", "description"} {
		if v := stringField(fields, key); v != "" {
			return previewText(v, 185), count
		}
	}
	// Raw JSON, packet headers, shell output blocks and arguments belong in
	// the authenticated detail drawer, not in a human-readable heading.
	value := strings.TrimSpace(summary)
	if strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[") || strings.HasPrefix(value, "<") ||
		strings.HasPrefix(value, "HTTP/") || strings.HasPrefix(value, "===") || strings.Contains(value, "://") || strings.Contains(value, "?") {
		return "", count
	}
	return previewText(value, 185), count
}

func addTodoChecks(out *ai.ConversationTimeline, id int64, detail string) {
	var entries []struct{ Content, Status string }
	if json.Unmarshal(objectInput(detail)["todos"], &entries) != nil {
		return
	}
	for _, item := range entries {
		state := "unverified"
		switch item.Status {
		case "pending", "in_progress", "completed", "blocked":
			state = item.Status
		}
		addCheck(out, ai.TimelineCheck{Text: previewText(item.Content, 200), State: state, Source: "todo", ActivityID: positiveActivityID(id)})
	}
}
func addCheck(out *ai.ConversationTimeline, item ai.TimelineCheck) {
	if item.Text == "" {
		return
	}
	if len(out.Checks) >= 64 {
		out.ChecksTruncated = true
		return
	}
	out.Checks = append(out.Checks, item)
}
func addUserChecks(out *ai.ConversationTimeline) {
	todo := out.Checks
	out.Checks = []ai.TimelineCheck{}
	known := map[string]bool{}
	addRequested := func(text, state, source string) {
		name := previewText(text, 200)
		if name == "" || known[name] {
			return
		}
		known[name] = true
		check := ai.TimelineCheck{Text: name, State: state, Source: source, ActivityID: out.Request.ActivityID}
		for _, item := range todo {
			if item.Text == name {
				check.State = item.State
				check.StatusActivityID = item.ActivityID
				break
			}
		}
		addCheck(out, check)
	}
	for _, line := range strings.Split(out.Request.Text, "\n") {
		parts := requirementLine.FindStringSubmatch(line)
		if len(parts) > 0 {
			state := "unverified"
			if strings.EqualFold(parts[1], "x") {
				state = "completed"
			}
			addRequested(parts[2], state, "user")
		}
	}
	for _, ref := range selectedAsset.FindAllString(out.Request.Text, 65) {
		addRequested(ref, "unverified", "asset")
	}
	for _, item := range todo {
		if !known[item.Text] {
			addCheck(out, item)
		}
	}
	if utf8.RuneCountInString(out.Request.Text) > 16000 {
		out.ChecksTruncated = true
	}
}
