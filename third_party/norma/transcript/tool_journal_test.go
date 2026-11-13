package transcript

import (
	"github.com/Autumn-27/norma/llm"
	"strings"
	"sync"
	"testing"
)

func TestInterruptedToolKeepsUncertaintyAcrossRepeatedResume(t *testing.T) {
	store := NewStore(t.TempDir())
	writer := store.NewWriter("session", "")
	writer.RecordMessage(llm.UserText("first"), llm.Usage{})
	writer.RecordToolIntent(llm.ContentBlock{Type: llm.BlockToolUse, ID: "external-write", Name: "Bash", Input: []byte(`{"command":"fixture"}`)})
	writer.RecordMessage(llm.UserText("later continuation"), llm.Usage{})
	records, err := store.Load(store.MainPath("session"))
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := Messages(records)
	if len(messages) != 4 || messages[1].ToolUses()[0].ID != "external-write" || !messages[2].Content[0].IsError || !strings.Contains(messages[2].Content[0].Content[0].Text, "不要自动重放") || messages[3].Text() != "later continuation" {
		t.Fatalf("unsafe recovery: %+v", messages)
	}
}

func TestSeparateStoresAppendCompleteJSONLines(t *testing.T) {
	dir := t.TempDir()
	stores := []*Store{NewStore(dir), NewStore(dir)}
	var wg sync.WaitGroup
	for _, store := range stores {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			writer := store.NewWriter("shared", "")
			for i := 0; i < 30; i++ {
				writer.RecordMessage(llm.UserText(strings.Repeat("x", 16000)), llm.Usage{})
			}
			if err := writer.Err(); err != nil {
				t.Error(err)
			}
		}(store)
	}
	wg.Wait()
	records, err := stores[0].Load(stores[0].MainPath("shared"))
	if err != nil || len(records) != 60 {
		t.Fatalf("partial/interleaved transcript: records=%d err=%v", len(records), err)
	}
}

func TestJournalDoesNotDuplicateCommittedToolTurn(t *testing.T) {
	store := NewStore(t.TempDir())
	writer := store.NewWriter("session", "")
	use := llm.ContentBlock{Type: llm.BlockToolUse, ID: "one", Name: "fixture", Input: []byte(`{}`)}
	result := llm.ToolResultText("one", "confirmed result", false)
	writer.RecordToolIntent(use)
	writer.RecordToolOutcome(result)
	writer.RecordMessage(llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{use}}, llm.Usage{})
	writer.RecordMessage(llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{result}}, llm.Usage{})
	records, err := store.Load(store.MainPath("session"))
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := Messages(records)
	if len(messages) != 2 || messages[1].Content[0].IsError {
		t.Fatalf("duplicated committed result: %+v", messages)
	}
}

func TestCompactionCheckpointRestoresAsOneRecord(t *testing.T) {
	store := NewStore(t.TempDir())
	writer := store.NewWriter("session", "")
	writer.RecordMessage(llm.UserText("old history"), llm.Usage{})
	meta := llm.BoundaryMeta{Trigger: "auto", PreTokens: 5000}
	writer.RecordCheckpoint(meta, []llm.Message{llm.BoundaryMessage(meta), llm.UserText("summary"), llm.UserText("retained tail")})
	records, err := store.Load(store.MainPath("session"))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1].Type != "checkpoint" {
		t.Fatalf("checkpoint was split: %+v", records)
	}
	messages, _ := Messages(records)
	view := llm.MessagesForAPI(messages)
	if len(view) != 2 || view[0].Text() != "summary" || view[1].Text() != "retained tail" {
		t.Fatalf("incomplete compacted view: %+v", view)
	}
}

func TestReusedToolIDDoesNotHideLaterUnconfirmedExecution(t *testing.T) {
	store := NewStore(t.TempDir())
	writer := store.NewWriter("session", "")
	use := llm.ContentBlock{Type: llm.BlockToolUse, ID: "reused", Name: "Bash", Input: []byte(`{}`)}
	writer.RecordToolIntent(use)
	writer.RecordToolOutcome(llm.ToolResultText("reused", "old confirmed", false))
	writer.RecordMessage(llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{use}}, llm.Usage{})
	writer.RecordMessage(llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText("reused", "old confirmed", false)}}, llm.Usage{})
	writer.RecordToolIntent(use)
	records, err := store.Load(store.MainPath("session"))
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := Messages(records)
	if len(messages) != 4 || !messages[3].Content[0].IsError {
		t.Fatalf("old result masked a new uncertain effect: %+v", messages)
	}
}
