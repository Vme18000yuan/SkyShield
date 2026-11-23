package infogather

import (
	"encoding/json"
	"fmt"
	"strings"
)

// coverageSummary uses only the sanitized, allow-listed coverage projection.
// It helps the conversation report specific limitations without exposing raw
// tool commands, URLs, output files, or credentials to the model.
func coverageSummary(raw json.RawMessage) string {
	var value struct {
		Steps   map[string]string `json:"steps"`
		Tools   map[string]bool   `json:"tools"`
		Details struct {
			Passive struct {
				FailedSources []struct {
					Source     string `json:"source"`
					Status     string `json:"status"`
					HTTPStatus int    `json:"http_status"`
					Attempts   int    `json:"attempts"`
				} `json:"failed_sources"`
			} `json:"passive"`
			Ports struct {
				Total       int `json:"total"`
				Completed   int `json:"completed"`
				TimedOut    int `json:"timed_out"`
				Unprocessed int `json:"unprocessed"`
			} `json:"ports"`
			Web struct {
				Selected        int `json:"selected"`
				Completed       int `json:"completed"`
				Omitted         int `json:"omitted"`
				CrawlerTimedOut int `json:"crawler_timed_out"`
				CrawlerFailed   int `json:"crawler_failed"`
				FailedRequests  int `json:"failed_requests"`
			} `json:"web"`
			Organization struct {
				FailedSources []struct {
					Source     string `json:"source"`
					HTTPStatus int    `json:"http_status"`
					Attempts   int    `json:"attempts"`
				} `json:"failed_sources"`
			} `json:"organization"`
			Agentic struct {
				Unexecuted []string `json:"unexecuted"`
			} `json:"agentic"`
		} `json:"details"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	labels := []struct{ key, name string }{
		{"passive", "被动采集"}, {"organization", "组织线索"}, {"dns_brute", "子域枚举"},
		{"dns", "DNS"}, {"axfr", "区域传送"}, {"cdn", "CDN/ASN"}, {"http", "HTTP 探测"},
		{"ports", "端口扫描"}, {"web", "Web 分析"}, {"cloud", "云桶"},
	}
	statusNames := map[string]string{"partial": "部分完成", "failed": "失败", "timeout": "超时", "interrupted": "中断"}
	var stages []string
	for _, label := range labels {
		status, ok := statusNames[value.Steps[label.key]]
		if !ok {
			continue
		}
		switch label.key {
		case "ports":
			ports := value.Details.Ports
			if ports.Total > 0 && ports.Completed <= ports.Total {
				status += fmt.Sprintf("，完成 %d/%d", ports.Completed, ports.Total)
				if ports.TimedOut > 0 {
					status += fmt.Sprintf("，超时 %d", ports.TimedOut)
				}
				if ports.Unprocessed > 0 {
					status += fmt.Sprintf("，未完成 %d", ports.Unprocessed)
				}
			}
		case "web":
			web := value.Details.Web
			if web.Selected > 0 && web.Completed <= web.Selected {
				status += fmt.Sprintf("，完成 %d/%d 个选中入口", web.Completed, web.Selected)
			}
		}
		stages = append(stages, label.name+"（"+status+"）")
	}
	var parts []string
	if len(stages) > 0 {
		parts = append(parts, "未完成阶段："+strings.Join(stages, "、"))
	}
	var unexecuted []string
	for _, phase := range value.Details.Agentic.Unexecuted {
		if label, ok := phaseLabels[phase]; ok && phase != "finish" {
			unexecuted = append(unexecuted, label)
		}
	}
	if len(unexecuted) > 0 {
		parts = append(parts, "未执行阶段："+strings.Join(unexecuted, "、"))
	}
	if present, known := value.Tools["dnsx"]; known && !present && value.Tools["dig"] && value.Steps["dns"] == "success" {
		parts = append(parts, "dnsx 未安装，DNS 已使用 dig 完成")
	}
	if present, known := value.Tools["whois"]; known && !present && value.Steps["cdn"] != "success" {
		parts = append(parts, "whois 未安装，ASN 线索受限")
	}
	if present, known := value.Tools["katana"]; known && !present {
		parts = append(parts, "katana 未安装，Web 爬取覆盖受限")
	}
	if count := value.Details.Web.CrawlerTimedOut; count > 0 {
		parts = append(parts, fmt.Sprintf("Katana 爬取超时 %d 个入口，已保留完成的 HTTP/JS 分析", count))
	}
	if count := value.Details.Web.CrawlerFailed; count > 0 {
		parts = append(parts, fmt.Sprintf("Katana 爬取失败 %d 个入口", count))
	}
	if count := value.Details.Web.FailedRequests; count > 0 {
		parts = append(parts, fmt.Sprintf("Web 请求失败 %d 次", count))
	}
	for _, item := range value.Details.Passive.FailedSources {
		if item.Source == "certificate_transparency" && item.HTTPStatus >= 100 && item.HTTPStatus <= 599 {
			message := fmt.Sprintf("被动证书查询返回 HTTP %d", item.HTTPStatus)
			if item.Attempts > 1 && item.Attempts <= 3 {
				message += fmt.Sprintf("（已尝试 %d 次）", item.Attempts)
			}
			parts = append(parts, message)
		} else if item.Source == "subfinder" && (item.Status == "failed" || item.Status == "timeout") {
			parts = append(parts, "subfinder 来源"+statusNames[item.Status])
		}
	}
	for _, item := range value.Details.Organization.FailedSources {
		if item.Source == "certificate_transparency" && item.HTTPStatus >= 100 && item.HTTPStatus <= 599 {
			message := fmt.Sprintf("证书透明日志查询返回 HTTP %d", item.HTTPStatus)
			if item.Attempts > 1 && item.Attempts <= 3 {
				message += fmt.Sprintf("（已尝试 %d 次）", item.Attempts)
			}
			parts = append(parts, message)
		}
	}
	if value.Details.Web.Omitted > 0 {
		parts = append(parts, fmt.Sprintf("另有 %d 个 Web 入口未纳入本次深入分析", value.Details.Web.Omitted))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "；") + "。"
}
