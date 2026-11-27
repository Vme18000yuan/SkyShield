package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/llm"
)

func budgetHistory() []llm.Message {
	history := []llm.Message{llm.UserText("Keep the original authorized scope; preserve saved results.")}
	for i := range 64 {
		id := string(rune('A' + i))
		history = append(history,
			llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: llm.BlockThinking, Thinking: strings.Repeat("analysis ", 1500), Signature: "original-signature"},
				{Type: llm.BlockToolUse, ID: id, Name: "Bash", Input: json.RawMessage("{\"command\":\"fixture\"}")},
			}},
			llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText(id, strings.Repeat("result ", 480), false)}})
	}
	return history
}

func TestBudgetCheckpointPreservesInstructionsPairsAndFullArchive(t *testing.T) {
	dir := t.TempDir()
	c := &contextBudgetCompactor{Compactor: compaction.New(compaction.Config{ContextWindow: 256000}, nil),
		window: func() int { return 256000 }, directory: filepath.Join(dir, ".context")}
	c.SetRequestBudget(6000, 8192)
	history := budgetHistory()
	original, _ := json.Marshal(history)
	out := c.Pre(t.Context(), history, 0)
	if llm.LastBoundaryIndex(out) < 0 {
		t.Fatal("overflow did not create a checkpoint")
	}
	view := c.View(t.Context(), out)
	if c.size(view) >= 256000 {
		t.Fatalf("still oversized: %d", c.size(view))
	}
	raw, _ := json.Marshal(view)
	if !strings.Contains(string(raw), "original authorized scope") || !strings.Contains(string(raw), "original-signature") {
		t.Fatal("instructions or retained tool reasoning lost")
	}
	pairs, _ := json.Marshal(llm.PairToolBlocks(view))
	if string(pairs) != string(raw) {
		t.Fatal("checkpoint split a tool exchange")
	}
	after, _ := json.Marshal(history)
	if string(original) != string(after) {
		t.Fatal("raw history mutated")
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".context", "context-*.json"))
	if len(files) != 1 {
		t.Fatalf("archive count=%d", len(files))
	}
	saved, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var archive struct{ History []llm.Message }
	if err = json.Unmarshal(saved, &archive); err != nil {
		t.Fatal(err)
	}
	archived, _ := json.Marshal(archive.History)
	if string(archived) != string(original) {
		t.Fatal("original tool results not fully archived")
	}
	info, _ := os.Stat(files[0])
	if info.Mode().Perm() != 0600 {
		t.Fatal("archive permissions")
	}
	// A fresh manager reads the persisted working set, not the old raw prefix.
	resumed := &contextBudgetCompactor{Compactor: compaction.New(compaction.Config{ContextWindow: 256000}, nil),
		window: func() int { return 256000 }, directory: filepath.Join(dir, ".context")}
	resumed.SetRequestBudget(6000, 8192)
	out = append(out, llm.UserText("Continue the unfinished work."))
	out = resumed.Pre(t.Context(), out, 0)
	if resumed.size(resumed.View(t.Context(), out)) >= 256000 {
		t.Fatal("cold resume re-exposed archived context")
	}
}

func TestCheckpointNeverDropsHistoryWhenArchiveCannotBeSaved(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".context"), []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &contextBudgetCompactor{Compactor: compaction.New(compaction.Config{ContextWindow: 256000}, nil),
		window: func() int { return 256000 }, directory: filepath.Join(dir, ".context")}
	out := c.Pre(t.Context(), budgetHistory(), 0)
	if llm.LastBoundaryIndex(out) >= 0 {
		t.Fatal("checkpoint committed without a durable archive")
	}
}

func TestCheckpointRefusesOversizedUserInstructions(t *testing.T) {
	c := &contextBudgetCompactor{Compactor: compaction.New(compaction.Config{ContextWindow: 32000}, nil),
		window: func() int { return 32000 }, directory: filepath.Join(t.TempDir(), ".context")}
	history := []llm.Message{llm.UserText(strings.Repeat("user constraint ", 10000))}
	if out, ok := c.Reactive(context.Background(), history); ok || llm.LastBoundaryIndex(out) >= 0 {
		t.Fatal("discarded user instructions to make an oversized request fit")
	}
	if !c.IsOverflow(errors.New("context length exceeds configured model window (32000 tokens)")) {
		t.Fatal("local guard error not recognized")
	}
}

func TestCheckpointPreservesRepeatedUserRequestsInOrder(t *testing.T) {
	original := []llm.Message{llm.UserText("repeat"), llm.UserText("second constraint"), llm.UserText("repeat")}
	view := append([]llm.Message(nil), original...)
	kept := checkpointInstructions(original, view, 2, "")
	kept = append(kept, view[2:]...)
	a, _ := json.Marshal(original)
	b, _ := json.Marshal(kept)
	if string(a) != string(b) {
		t.Fatal("reordered or deduplicated user requests")
	}
}

func TestCheckpointRejectsArchiveDirectoryEscape(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".context")); err != nil {
		t.Fatal(err)
	}
	if err := saveContextArchive(t.Context(), filepath.Join(dir, ".context"), "context.json", []byte("{}")); err == nil {
		t.Fatal("archive followed a workspace symlink outside its scope")
	}
	files, _ := os.ReadDir(outside)
	if len(files) != 0 {
		t.Fatal("wrote outside the session")
	}
}

func TestCheckpointCutWithReusedCallID(t *testing.T) {
	call := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, Name: "Bash", ID: "reused", Input: json.RawMessage("{}")}}}
	result := llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText("reused", "saved", false)}}
	history := []llm.Message{call, result, call, result}
	if !completeExchangeCut(history, 2) || completeExchangeCut(history, 3) {
		t.Fatal("paired by global ID instead of the latest preceding call")
	}
}
