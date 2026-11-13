package infogather

import (
	"encoding/json"
	"errors"
	"strings"

	contractresults "github.com/yyhuni/lunafox/contracts/results"
)

type riskCandidate struct {
	Category    string
	Address     string
	Title       string
	Description string
	Evidence    []string
	DNS         bool
}

// A clue is never promoted to a confirmed vulnerability: supported security
// observations become informational findings with explicit review status.
func decodeRisk(kind string, payload []byte) (riskCandidate, error) {
	var category, address string
	var evidence []string
	switch kind {
	case "vulnerability":
		var value Vulnerability
		if err := json.Unmarshal(payload, &value); err != nil {
			return riskCandidate{}, err
		}
		category, address, evidence = value.Type, value.URL, value.Evidence
	case "observation":
		var value Observation
		if err := json.Unmarshal(payload, &value); err != nil {
			return riskCandidate{}, err
		}
		category, address, evidence = value.Category, value.Value, value.Evidence
	default:
		return riskCandidate{}, errors.New("不是风险记录")
	}
	result := riskCandidate{Category: category, Address: address, Evidence: evidence}
	switch category {
	case "ANONYMOUS_DATA":
		result.Title, result.Description = "匿名数据响应", "匿名请求返回结构化数据，须核实接口是否应受保护；尚未确认漏洞。"
	case "REVIEW_TOKEN_DIFFERENCE":
		result.Title, result.Description = "令牌响应差异", "无效令牌的响应存在差异，缺少授权基线，尚未确认漏洞。"
	case "PUBLIC_LISTING":
		result.Title, result.Description = "云存储公开列举", "观察到存储内容可列举，需核实资产归属、预期权限及数据敏感性；尚未确认漏洞。"
	case "DNS_ZONE_TRANSFER":
		result.Title, result.Description, result.DNS = "DNS 区域传送开放", "观察到 DNS 区域传送成功，需核实开放范围和授权策略。", true
		name := strings.TrimPrefix(address, "dns://")
		if normalized, ok := contractresults.NormalizeSubdomainDNSName(name); ok {
			result.Address = normalized
		} else {
			return riskCandidate{}, errors.New("DNS 风险地址无效")
		}
	default:
		return riskCandidate{}, errors.New("该观察类型不支持同步为风险")
	}
	if !result.DNS && !validURL(result.Address) {
		return riskCandidate{}, errors.New("风险 URL 无效")
	}
	return result, nil
}

func sameRiskObservation(observation Observation, vulnerability Vulnerability) bool {
	if observation.ID != vulnerability.ID || observation.Category != vulnerability.Type {
		return false
	}
	if observation.Category == "DNS_ZONE_TRANSFER" {
		return strings.TrimPrefix(vulnerability.URL, "dns://") == observation.Value
	}
	return observation.Value == vulnerability.URL
}
