package application

import "testing"

func TestReporterUnifiedReportRequiresCompleteSections(t *testing.T) {
	complete := "## 概述\n描述\n## 影响\n影响内容\n## 前置条件\n条件\n## 复现步骤\n步骤\n## 证据\n证据内容\n## PoC\n```sh\necho verified\n```\n## 根因分析\n原因\n## 修复建议\n建议"
	if err := validateUnifiedFindingCaseReport(complete); err != nil {
		t.Fatal(err)
	}
	if err := validateUnifiedFindingCaseReport("## 概述\n仅有概述"); err == nil {
		t.Fatal("incomplete report accepted")
	}
	if err := validateUnifiedFindingCaseReport(complete + "\n```sh\nunfinished"); err == nil {
		t.Fatal("unclosed code fence accepted")
	}
}
