package transcript

import "github.com/Autumn-27/norma/llm"

// RecordToolIntent is a write-ahead record. A tool must not start until Err is
// nil. Outcomes are separate so a crash between an effect and its acknowledgement
// is represented as uncertain rather than silently deleting the call.
func (w *Writer) RecordToolIntent(use llm.ContentBlock) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.operations == nil {
		w.operations = map[string]string{}
	}
	op := newID()
	w.operations[use.ID] = op
	m := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{use}}
	w.writeLocked(Record{Type: "tool_intent", OperationID: op, Message: &m})
}

func (w *Writer) RecordToolOutcome(result llm.ContentBlock) {
	w.mu.Lock()
	defer w.mu.Unlock()
	m := llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{result}}
	w.writeLocked(Record{Type: "tool_outcome", OperationID: w.operations[result.ToolUseID], Message: &m})
}

const UncertainToolOutcome = "执行中断，工具结果尚未确认。操作可能已产生外部效果。请先核对已有文件、执行记录或目标状态，不要自动重放有副作用的操作。"

func recoverToolJournal(records []Record) []llm.Message {
	type invocation struct {
		use                llm.ContentBlock
		outcome            llm.ContentBlock
		confirmed, covered bool
	}
	type normalCall struct {
		use     llm.ContentBlock
		journal *invocation
		paired  bool
	}
	journalAt := map[int][]*invocation{}
	latestJournal := map[string]*invocation{}
	operations := map[string]*invocation{}
	normalAt := map[int][]*normalCall{}
	latestNormal := map[string]*normalCall{}
	for index, record := range records {
		if record.Message == nil {
			continue
		}
		for _, block := range record.Message.Content {
			switch record.Type {
			case "tool_intent":
				call := &invocation{use: block}
				journalAt[index] = append(journalAt[index], call)
				latestJournal[block.ID] = call
				if record.OperationID != "" {
					operations[record.OperationID] = call
				}
			case "tool_outcome":
				call := latestJournal[block.ToolUseID]
				if record.OperationID != "" {
					call = operations[record.OperationID]
				}
				if call != nil {
					call.outcome = block
					call.confirmed = true
				}
			case "message":
				if block.Type == llm.BlockToolUse {
					call := &normalCall{use: block}
					if journal := latestJournal[block.ID]; journal != nil && !journal.covered {
						journal.covered = true
						call.journal = journal
					}
					normalAt[index] = append(normalAt[index], call)
					latestNormal[block.ID] = call
				}
				if block.Type == llm.BlockToolResult {
					if call := latestNormal[block.ToolUseID]; call != nil {
						call.paired = true
					}
				}
			}
		}
	}
	resultFor := func(use llm.ContentBlock, journal *invocation) llm.ContentBlock {
		if journal != nil && journal.confirmed {
			return journal.outcome
		}
		return llm.ToolResultText(use.ID, UncertainToolOutcome, true)
	}
	var restored []llm.Message
	for index, record := range records {
		if record.Type == "checkpoint" {
			restored = append(restored, record.Checkpoint...)
			continue
		}
		if record.Message == nil {
			continue
		}
		if record.Type == "tool_intent" {
			for _, call := range journalAt[index] {
				if call.covered {
					continue
				}
				restored = append(restored, llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{call.use}}, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{resultFor(call.use, call)}})
			}
			continue
		}
		if record.Type == "boundary" || record.Type == "tool_outcome" {
			continue
		}
		restored = append(restored, *record.Message)
		var missing []llm.ContentBlock
		for _, call := range normalAt[index] {
			if !call.paired {
				missing = append(missing, resultFor(call.use, call.journal))
			}
		}
		if len(missing) > 0 {
			restored = append(restored, llm.Message{Role: llm.RoleUser, Content: missing})
		}
	}
	return restored
}
