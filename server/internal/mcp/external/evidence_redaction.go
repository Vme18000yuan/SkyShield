package external

import (
	"encoding/json"
	"mime"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

const masked = "[redacted]"
const packetLimit = 24 << 10

func sensitive(name string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(name))
	for _, part := range []string{"authorization", "cookie", "token", "secret", "passw", "passwd", "apikey", "credential", "session", "privatekey", "signature", "csrf", "bearer", "accesskey"} {
		if strings.Contains(normalized, part) {
			return true
		}
	}
	return normalized == "key" || normalized == "pwd" || normalized == "auth"
}

func cleanURL(raw string) string {
	if raw == "*" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return masked
	}
	u.User = nil
	u.Fragment = ""
	if u.RawQuery != "" {
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			u.RawQuery = "redacted"
		} else {
			for key := range q {
				q.Set(key, masked)
			}
			u.RawQuery = q.Encode()
		}
	}
	return u.String()
}

// Only recorded HTTP start lines and known non-secret headers cross the client
// boundary. Unknown/binary bodies are opaque; JSON and form fields can be masked.
func redactHTTP(raw string, response bool) (string, bool) {
	if raw == "" {
		return "", false
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	head, body, hasBody := strings.Cut(raw, "\n\n")
	lines := strings.Split(head, "\n")
	start := strings.Fields(lines[0])
	if strings.Contains(head, "\r") || len(start) < 2 {
		return "[unstructured HTTP data withheld]", true
	}
	if response {
		if !strings.HasPrefix(start[0], "HTTP/") {
			return "[response body withheld without HTTP headers]", true
		}
	} else {
		if len(start) != 3 || !strings.HasPrefix(start[2], "HTTP/") {
			return "[unstructured HTTP request withheld]", true
		}
		lines[0] = start[0] + " " + cleanURL(start[1]) + " " + start[2]
	}
	headerType := ""
	for i := 1; i < len(lines); i++ {
		name, value, ok := strings.Cut(lines[i], ":")
		if !ok || name != strings.TrimSpace(name) || name == "" {
			lines[i] = masked
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(name) {
		case "content-type":
			headerType, _, _ = mime.ParseMediaType(value)
			value = headerType // omit multipart boundaries or arbitrary parameters
		case "location", "referer", "origin":
			value = cleanURL(value)
		case "host", "content-length", "accept", "accept-encoding", "content-encoding", "transfer-encoding", "date", "connection", "cache-control", "vary", "server", "user-agent", "accept-language", "content-language":
		default:
			value = masked
		}
		lines[i] = name + ": " + value
	}
	value := strings.Join(lines, "\r\n")
	partial := false
	if hasBody {
		value += "\r\n\r\n"
		if body != "" {
			shown, hidden := redactBody(headerType, body)
			value += shown
			partial = hidden
		}
	}
	if len(value) > packetLimit {
		cut := packetLimit
		for cut > 0 && !utf8.RuneStart(value[cut]) {
			cut--
		}
		return value[:cut], true
	}
	return value, partial
}

func redactBody(contentType, body string) (string, bool) {
	if len(body) > 128<<10 {
		return "[oversized body withheld]", true
	}
	if contentType == "application/json" || strings.HasSuffix(contentType, "+json") {
		var value any
		decoder := json.NewDecoder(strings.NewReader(body))
		decoder.UseNumber()
		if !json.Valid([]byte(body)) || decoder.Decode(&value) != nil {
			return "[incomplete JSON body withheld]", true
		}
		switch value.(type) {
		case map[string]any, []any:
			maskJSON(value)
		default:
			return "[scalar JSON body withheld]", true
		}
		output, err := json.Marshal(value)
		if err == nil {
			return string(output), false
		}
	}
	if contentType == "application/x-www-form-urlencoded" {
		query, err := url.ParseQuery(body)
		if err == nil {
			for key, values := range query {
				if sensitive(key) {
					query.Set(key, masked)
				} else {
					for i, v := range values {
						values[i] = maskBearer(v)
					}
				}
			}
			return query.Encode(), false
		}
	}
	return "[unstructured body withheld]", true
}

func maskBearer(value string) string {
	fields := strings.Fields(value)
	if len(fields) > 1 && slices.Contains([]string{"basic", "bearer"}, strings.ToLower(fields[0])) {
		return masked
	}
	return value
}
func maskJSON(value any) {
	switch node := value.(type) {
	case map[string]any:
		for key, val := range node {
			if sensitive(key) {
				node[key] = masked
			} else if str, ok := val.(string); ok {
				node[key] = maskBearer(str)
			} else {
				maskJSON(val)
			}
		}
	case []any:
		for i, val := range node {
			if str, ok := val.(string); ok {
				node[i] = maskBearer(str)
			} else {
				maskJSON(val)
			}
		}
	}
}
