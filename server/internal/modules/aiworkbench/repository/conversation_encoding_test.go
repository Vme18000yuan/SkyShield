package repository

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
)

func TestSideSnapshotPreservesNULAndToolArguments(t *testing.T) {
	input := json.RawMessage(`{"text":"before\u0000after","literal":"\\u0000"}`)
	snapshot := sidequestion.Snapshot{RunID: 123, Version: 4, Request: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "reason\x00ing"}, {Type: llm.BlockToolUse, ID: "fixture", Name: "probe", Input: input}}}, {Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: "fixture", Content: []llm.ContentBlock{llm.TextBlock("output\x00end")}}}}}}}
	encoded, err := marshalSideDocument(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err = json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	if root["run_id"] != float64(123) || root["version"] != float64(4) {
		t.Fatal("snapshot ordering metadata was lost")
	}
	// The outer JSON decoder produces no actual NUL, as required by JSONB.
	for _, value := range root {
		if text, ok := value.(string); ok && strings.ContainsRune(text, 0) {
			t.Fatal("NUL reached the JSONB document")
		}
	}
	var restored sidequestion.Snapshot
	if err = unmarshalSideDocument(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(snapshot)
	after, _ := json.Marshal(restored)
	if !bytes.Equal(before, after) {
		t.Fatal("structured snapshot was modified")
	}
	if string(restored.Request.Messages[0].Content[1].Input) != string(input) {
		t.Fatal("tool arguments were changed")
	}
}

func TestSideDocumentsRemainCompatibleWithPlainRowsAndStatusOverrides(t *testing.T) {
	plain := sidequestion.Exchange{Answer: "plain answer", Status: "running"}
	encoded, err := marshalSideDocument(plain)
	if err != nil || bytes.Contains(encoded, []byte("_tianqiong_codec")) {
		t.Fatal("ordinary row layout changed")
	}
	var item sidequestion.Exchange
	if err = unmarshalSideDocument(encoded, &item); err != nil || item.Answer != plain.Answer {
		t.Fatal("legacy row unreadable")
	}
	plain.Answer = "first\x00second"
	encoded, _ = marshalSideDocument(plain)
	var root map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &root)
	root["status"] = json.RawMessage(`"interrupted"`)
	root["error"] = json.RawMessage(`"lease expired"`)
	encoded, _ = json.Marshal(root)
	if err = unmarshalSideDocument(encoded, &item); err != nil || item.Status != "interrupted" || item.Error != "lease expired" || item.Answer != plain.Answer {
		t.Fatal("authoritative status override was lost")
	}
}

func TestActivityDisplayEscapesBinaryWithoutLosingText(t *testing.T) {
	got := printableActivityText("前缀\x00后缀\xff")
	if !utf8.ValidString(got) || strings.ContainsRune(got, 0) || got != "前缀\\x00后缀\uFFFD" {
		t.Fatalf("unsafe activity text: %q", got)
	}
}
