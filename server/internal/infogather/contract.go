// Package infogather runs the bundled information-collection skill and imports
// its results through the same authorization and asset writers as the product.
package infogather

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const maxReportBytes = 32 << 20

var recordIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Envelope struct {
	SchemaVersion   string          `json:"schema_version"`
	Run             ReportRun       `json:"run"`
	Assets          []Asset         `json:"assets"`
	Vulnerabilities []Vulnerability `json:"vulnerabilities"`
	Observations    []Observation   `json:"observations"`
}

type ReportRun struct {
	ID     string `json:"id"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Mode   string `json:"mode"`
}

type Asset struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Value        string   `json:"value"`
	Scope        string   `json:"scope"`
	Evidence     []string `json:"evidence"`
	BoundDomains []string `json:"bound_domains,omitempty"`
	CDN          bool     `json:"cdn,omitempty"`
	Port         int      `json:"port,omitempty"`
	Protocol     string   `json:"protocol,omitempty"`
	ServiceName  string   `json:"service_name,omitempty"`
	StatusCode   *int64   `json:"status_code,omitempty"`
	Confidence   string   `json:"confidence,omitempty"`
	PageTitle    string   `json:"page_title,omitempty"`
	Technologies []string `json:"technologies,omitempty"`
}

type Vulnerability struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Severity    string   `json:"severity"`
	Confidence  string   `json:"confidence"`
	Status      string   `json:"status"`
	Scope       string   `json:"scope"`
	Description string   `json:"description"`
	Evidence    []string `json:"evidence"`
}

type Observation struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Value    string   `json:"value"`
	Scope    string   `json:"scope"`
	Status   string   `json:"status"`
	Evidence []string `json:"evidence"`
}

func decodeEnvelope(raw []byte, id string, target Scope, mode string) (*Envelope, error) {
	if len(raw) == 0 || len(raw) > maxReportBytes {
		return nil, errors.New("采集结果为空或超过大小限制")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var report Envelope
	if err := dec.Decode(&report); err != nil {
		return nil, fmt.Errorf("采集结果格式无效: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF || report.SchemaVersion != "1.0" || report.Run.ID != id || report.Run.Target != target.Name || report.Run.Kind != target.Kind || report.Run.Mode != mode || report.Assets == nil || report.Vulnerabilities == nil || report.Observations == nil {
		return nil, errors.New("采集结果与本次运行不匹配")
	}
	if report.Run.Status != "completed" && report.Run.Status != "partial" && report.Run.Status != "interrupted" && report.Run.Status != "failed" {
		return nil, errors.New("采集状态无效")
	}
	if len(report.Assets)+len(report.Vulnerabilities)+len(report.Observations) > 50000 {
		return nil, errors.New("采集结果超过记录数量限制")
	}
	seen := map[string]bool{}
	check := func(kind, id string, evidence []string) error {
		if !recordIDPattern.MatchString(id) || seen[kind+id] || len(evidence) > 20 {
			return errors.New("采集结果标识无效或重复")
		}
		seen[kind+id] = true
		for _, path := range evidence {
			if !validEvidence(path) {
				return errors.New("采集证据路径无效")
			}
		}
		return nil
	}
	for _, a := range report.Assets {
		if err := check("asset", a.ID, a.Evidence); err != nil {
			return nil, err
		}
		if len(a.Value) == 0 || len(a.Value) > 4096 || len(a.BoundDomains) > 100 || len(a.ServiceName) > 64 || len(a.PageTitle) > 2048 || len(a.Technologies) > 100 || !validHint(a.Scope) {
			return nil, errors.New("资产字段无效")
		}
		for _, name := range a.Technologies {
			if len(name) > 120 {
				return nil, errors.New("技术指纹字段过长")
			}
		}
	}
	for _, v := range report.Vulnerabilities {
		if err := check("vulnerability", v.ID, v.Evidence); err != nil {
			return nil, err
		}
		if v.Status != "needs_review" || v.Confidence != "candidate" || v.Severity != "info" || len(v.URL) > 4096 || len(v.Description) > 2000 || len(v.Title) > 200 || !validHint(v.Scope) {
			return nil, errors.New("漏洞候选字段无效")
		}
		raw, _ := json.Marshal(v)
		if _, err := decodeRisk("vulnerability", raw); err != nil {
			return nil, errors.New("漏洞候选类型或地址无效")
		}
	}
	for _, o := range report.Observations {
		if err := check("observation", o.ID, o.Evidence); err != nil {
			return nil, err
		}
		if o.Status != "needs_review" || len(o.Value) == 0 || len(o.Value) > 4096 || !validHint(o.Scope) {
			return nil, errors.New("观察记录字段无效")
		}
	}
	// Some Skill versions emit the same candidate in both arrays. Keep one
	// actionable risk record; the vulnerability carries the same evidence.
	byID := make(map[string]Vulnerability, len(report.Vulnerabilities))
	for _, v := range report.Vulnerabilities {
		byID[v.ID] = v
	}
	unique := make([]Observation, 0, len(report.Observations))
	for _, o := range report.Observations {
		if v, ok := byID[o.ID]; ok && sameRiskObservation(o, v) {
			continue
		}
		unique = append(unique, o)
	}
	report.Observations = unique
	return &report, nil
}

func validHint(s string) bool {
	return s == "in_scope" || s == "associated" || s == "unverified" || s == "out_of_scope"
}

func validEvidence(path string) bool {
	if path == "" || len(path) > 512 || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
