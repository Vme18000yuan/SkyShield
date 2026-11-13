package application

import (
	"context"
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

//go:embed builtin_rules.json
var builtinRulesJSON []byte

func BuiltinRules() ([]Rule, error) {
	var rules []Rule
	err := json.Unmarshal(builtinRulesJSON, &rules)
	return rules, err
}
func (s *Service) Seed(ctx context.Context) error {
	rules, err := BuiltinRules()
	if err != nil {
		return err
	}
	if err = s.store.Seed(ctx, rules); err != nil {
		return err
	}
	store, err := s.assetRules()
	if err != nil {
		return err
	}
	return store.SeedAssetRules(ctx)
}
func validRule(rule Rule) bool {
	if rule.Priority < -2147483648 || rule.Priority > 2147483647 || strings.TrimSpace(rule.Name) == "" || utf8.RuneCountInString(rule.Name) > 200 || len(rule.Pattern) == 0 || len(rule.Pattern) > 8192 || len(rule.Message) > 16000 || !utf8.ValidString(rule.Name+rule.Pattern+rule.Message) || strings.ContainsRune(rule.Name+rule.Pattern+rule.Message, '\x00') || (rule.MatchTarget != "tool_name" && rule.MatchTarget != "tool_input") || (rule.MatchType != "string" && rule.MatchType != "regex") || (rule.Action != "allow" && rule.Action != "deny" && rule.Action != "ask") || (rule.TimeoutAction != "allow" && rule.TimeoutAction != "deny") || rule.TimeoutSeconds < 1 || rule.TimeoutSeconds > 2147483647 {
		return false
	}
	if rule.MatchType == "regex" {
		_, err := regexp.Compile(rule.Pattern)
		return err == nil
	}
	return true
}
func MatchRules(rules []Rule, tool string, input []byte) *Rule {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		subject := tool
		if rule.MatchTarget == "tool_input" {
			subject = string(input)
		} else if rule.MatchTarget != "tool_name" {
			continue
		}
		matched := false
		if rule.MatchType == "string" {
			matched = strings.Contains(subject, rule.Pattern)
		} else if rule.MatchType == "regex" {
			if re, err := regexp.Compile(rule.Pattern); err == nil {
				matched = re.MatchString(subject)
			}
		}
		if matched {
			copy := rule
			return &copy
		}
	}
	return nil
}
