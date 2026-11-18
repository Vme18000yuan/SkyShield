package repository

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/lib/pq"
	ai "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

const timelinePageSize = 24
const findingPageSize = 16

func (s *ConversationStore) ReadConversationTimeline(ctx context.Context, user int, conversation, run, beforeRun string, from int, afterFinding int64) (*ai.ConversationTimeline, error) {
	out := &ai.ConversationTimeline{Sections: []ai.TimelineSection{}, Steps: []ai.TimelineStep{}, Checks: []ai.TimelineCheck{}, Findings: []ai.TimelineFinding{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var title string
		err := tx.Raw("SELECT c.title"+ownedConversation, assets.DefaultTenantID, user, conversation).Row().Scan(&title)
		if errors.Is(err, sql.ErrNoRows) {
			return ai.ErrConversationNotFound
		}
		if err != nil {
			return err
		}
		window := ai.ConversationExploration{Runs: []ai.ConversationExplorationRun{}}
		if err = explorationRunWindow(tx, user, conversation, run, beforeRun, &window); err != nil {
			return err
		}
		out.Run, out.Runs, out.RunsBefore = window.Run, window.Runs, window.RunsBefore
		if out.Run == nil {
			return nil
		}
		scope := []any{assets.DefaultTenantID, user, conversation, out.Run.ID}
		if err = readTimelineSteps(tx, scope, from, afterFinding, out); err != nil {
			return err
		}

		const textFrom = ` FROM ai_conversation_activity WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND run_id=? `
		var request struct{ ActivityID, Text, Summary string }
		if err = tx.Raw(`SELECT id::text AS activity_id,left(COALESCE(NULLIF(detail,''),summary),131072) AS text,left(summary,900) AS summary`+textFrom+" AND kind='user' ORDER BY id LIMIT 1", scope...).Scan(&request).Error; err != nil {
			return err
		}
		out.Request = ai.TimelineText{ActivityID: request.ActivityID, Text: userRequestText(request.Text, request.Summary)}
		addUserChecks(out)
		out.Request.Text = previewText(out.Request.Text, 900)
		if !out.Run.Running {
			err = tx.Raw(`SELECT id::text AS activity_id,left(COALESCE(NULLIF(detail,''),summary),3000) AS text`+textFrom+`
              AND kind IN ('assistant_message','result','text') AND COALESCE(NULLIF(detail,''),summary)<>''
              ORDER BY CASE kind WHEN 'assistant_message' THEN 0 WHEN 'result' THEN 1 ELSE 2 END,id DESC LIMIT 1`, scope...).Scan(&out.Conclusion).Error
			out.Conclusion.Text = previewText(out.Conclusion.Text, 900)
		}
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return out, err
}

// Project bounded task cues while streaming events. Retain only the labels,
// evidence pointers and timings needed to group calls and resolve receipts.
type timelineMeta struct {
	purpose                      purposeSignal
	id, result                   int64
	tool, providerID, occurrence string
	recorded                     time.Time
	round, batch                 int
	started, finished            *time.Time
	failed, interrupted          bool
	overlapStep                  int
	finding                      *ai.TimelineFinding
	resultPreview                string
}
type outstandingCalls struct{ count, first int }

func readStepMetadata(tx *gorm.DB, scope []any) ([]timelineMeta, error) {
	rows, err := tx.Raw(`SELECT id,kind,tool,tool_use_id,created_at,COALESCE(source_activity_id,0),COALESCE(trace_call_id,''),
      COALESCE(model_round,0),COALESCE(tool_batch,0),tool_started_at,tool_finished_at,is_error,tool_interrupted,
      CASE WHEN tool='Compress' THEN ''
           WHEN kind='tool_use' AND (tool='TodoWrite' OR (octet_length(detail)>3072 AND detail ~ '"(objective|task|description)"[[:space:]]*:')) THEN detail
           WHEN kind='tool_use' THEN left(COALESCE(NULLIF(detail,''),summary),3072)
           WHEN kind='text' THEN left(COALESCE(NULLIF(detail,''),summary),400) ELSE left(summary,240) END
      FROM ai_conversation_activity WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND run_id=?
      AND kind IN ('tool_use','tool_result','text') ORDER BY id`, scope...).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	calls := []timelineMeta{}
	byID := map[int64]int{}
	pending := map[string]outstandingCalls{}
	var active *ai.TimelinePurpose
	var lastMarker int64
	for rows.Next() {
		var m timelineMeta
		var kind, cue string
		var source int64
		if err := rows.Scan(&m.id, &kind, &m.tool, &m.providerID, &m.recorded, &source, &m.occurrence, &m.round, &m.batch, &m.started, &m.finished, &m.failed, &m.interrupted, &cue); err != nil {
			return nil, err
		}
		if kind == "text" {
			if text := declaredTask(cue); text != "" {
				active = taskStatement("statement", text, m.id, 0)
				lastMarker = m.id
			}
			continue
		}
		if kind == "tool_use" {
			i := len(calls)
			m.purpose = taskSignal(m.tool, cue, m.id, i+1)
			m.purpose.active = active
			calls = append(calls, m)
			byID[m.id] = i
			if m.occurrence == "" && m.providerID != "" {
				p := pending[m.providerID]
				if p.count == 0 {
					p.first = i
				} else {
					p.first = -1
				}
				p.count++
				pending[m.providerID] = p
			}
			continue
		}
		var i int
		if m.occurrence != "" {
			index, exists := byID[source]
			if !exists || calls[index].occurrence != m.occurrence || calls[index].providerID != m.providerID {
				continue
			}
			i = index
		} else {
			p, exists := pending[m.providerID]
			if !exists {
				continue
			}
			p.count--
			if p.count > 0 {
				pending[m.providerID] = p
				continue
			}
			delete(pending, m.providerID)
			if p.first < 0 {
				continue
			} // old events contain no way to disambiguate these results
			i = p.first
		}
		if calls[i].result != 0 {
			continue
		}
		calls[i].result, calls[i].failed, calls[i].interrupted = m.id, m.failed, m.interrupted
		calls[i].resultPreview, _ = stepOutcome(cue, cue)
		if !m.failed && !m.interrupted {
			if calls[i].tool == "TodoWrite" && calls[i].id > lastMarker {
				active = calls[i].purpose.nextTodo
				lastMarker = calls[i].id
				if i == len(calls)-1 && active != nil {
					calls[i].purpose.active = active
				}
			}
			if text := resultPurposeText(calls[i].resultPreview); text != "" {
				scoreTopics(&calls[i].purpose.resultScores, text, 2)
			}
		}
		if m.started != nil {
			calls[i].started = m.started
		}
		calls[i].finished = m.finished
	}
	return calls, rows.Err()
}

func markMeasuredOverlap(calls []timelineMeta) {
	measured := []int{}
	for i, s := range calls {
		if s.started != nil && s.finished != nil && s.finished.After(*s.started) {
			measured = append(measured, i)
		}
	}
	slices.SortStableFunc(measured, func(a, b int) int { return calls[a].started.Compare(*calls[b].started) })
	longest := -1
	for _, i := range measured {
		if longest >= 0 && calls[i].started.Before(*calls[longest].finished) {
			calls[i].overlapStep = longest + 1
			if calls[longest].overlapStep == 0 {
				calls[longest].overlapStep = i + 1
			}
		}
		if longest < 0 || calls[i].finished.After(*calls[longest].finished) {
			longest = i
		}
	}
}

func readTimelineReceipts(tx *gorm.DB, scope []any, after int64, calls []timelineMeta, out *ai.ConversationTimeline) error {
	byID, byResult := map[int64]int{}, map[int64]int{}
	byProvider := map[string]int{}
	for i, step := range calls {
		if step.result != 0 {
			byResult[step.result] = i
		}
		if step.tool != "report_vulnerability" {
			continue
		}
		byID[step.id] = i
		if _, seen := byProvider[step.providerID]; seen {
			byProvider[step.providerID] = -1
		} else {
			byProvider[step.providerID] = i
		}
	}
	rows, err := tx.Raw(`SELECT p.id,p.tool_use_id,COALESCE(p.source_activity_id,0),p.created_at,p.vulnerability_id,p.evidence_activity_ids,
      left(COALESCE(NULLIF(v.display_name,''),NULLIF(p.report->>'name',''),v.vuln_type),200),v.severity,v.reviewed
      FROM ai_conversation_vulnerability_report p JOIN vulnerability v ON v.id=p.vulnerability_id
      WHERE p.tenant_id=? AND p.owner_user_id=? AND p.conversation_id=? AND p.run_id=? ORDER BY p.id`, scope...).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var f ai.TimelineFinding
		var id, source int64
		var provider string
		var at time.Time
		var refs pq.Int64Array
		if err := rows.Scan(&id, &provider, &source, &at, &f.VulnerabilityID, &refs, &f.Title, &f.Severity, &f.Reviewed); err != nil {
			return err
		}
		f.ReceiptID = positiveActivityID(id)
		if title := previewText(f.Title, 200); title != "" {
			f.Title = title
		} else {
			f.Title = "#V" + strconv.Itoa(f.VulnerabilityID)
		}
		i, found := byID[source]
		if source == 0 {
			i, found = byProvider[provider]
			found = found && i >= 0 && calls[i].occurrence == "" && !at.Before(calls[i].recorded)
		}
		if found && calls[i].providerID == provider {
			f.Step = i + 1
			f.SourceActivityID = positiveActivityID(calls[i].id)
			for _, ref := range refs {
				if n, ok := byResult[ref]; ok && n < i && calls[n].tool == "traffic_get" && !calls[n].failed && !calls[n].interrupted {
					f.EvidenceSources = append(f.EvidenceSources, ai.TimelineSource{ActivityID: positiveActivityID(ref), Step: n + 1})
				}
			}
			calls[i].finding = &f
			scoreTopics(&calls[i].purpose.scores, f.Title, 6)
		}
		out.FindingCount++
		if id > after {
			if len(out.Findings) < findingPageSize {
				out.Findings = append(out.Findings, f)
			} else {
				out.NextFinding = out.Findings[findingPageSize-1].ReceiptID
			}
		}
	}
	return rows.Err()
}
func (m timelineMeta) state(live bool) string {
	switch {
	case m.interrupted:
		return "interrupted"
	case m.failed:
		return "failed"
	case m.result != 0:
		return "succeeded"
	case !live:
		return "unknown"
	case m.started != nil:
		return "running"
	default:
		return "awaiting"
	}
}
func countStep(c *ai.TimelineCounts, state string, m timelineMeta) {
	c.Steps++
	if m.started != nil && m.finished != nil {
		c.Measured++
	}
	switch state {
	case "succeeded":
		c.Returned++
	case "failed":
		c.Failed++
	case "interrupted":
		c.Interrupted++
	case "unknown":
		c.Missing++
	default:
		c.Pending++
	}
	if m.overlapStep > 0 {
		c.Overlap++
	}
}
func sectionWidth(total int) int {
	width := 16
	for total > width*10 {
		width *= 2
	}
	return width
}

func readTimelineSteps(tx *gorm.DB, scope []any, from int, afterFinding int64, out *ai.ConversationTimeline) error {
	calls, err := readStepMetadata(tx, scope)
	if err != nil {
		return err
	}
	markMeasuredOverlap(calls)
	if err = readTimelineReceipts(tx, scope, afterFinding, calls, out); err != nil {
		return err
	}
	var todoID int64
	textIDs := []int64{}
	total := len(calls)
	out.Sections = timelineSections(calls)
	sectionIndex := 0
	for i, m := range calls {
		number := i + 1
		if number > out.Sections[sectionIndex].Last {
			sectionIndex++
		}
		section := &out.Sections[sectionIndex]
		if number == section.First {
			section.Title, section.Subject, section.ActionKind = m.purpose.action, m.purpose.actionSubject, m.purpose.actionKind
		}
		if number == section.Last && m.result > 0 {
			section.Outcome = m.resultPreview
		}
		state := m.state(out.Run.Running)
		countStep(&out.Counts, state, m)
		countStep(&section.TimelineCounts, state, m)
		if m.finding != nil {
			section.Findings++
			if section.FirstFinding == nil {
				section.FirstFinding = m.finding
			}
		}
		if m.tool == "TodoWrite" && state == "succeeded" {
			todoID = m.id
		}
		if number < from || number-from >= timelinePageSize || from < section.First {
			continue
		}
		textIDs = append(textIDs, m.id)
		if m.result > 0 {
			textIDs = append(textIDs, m.result)
		}
		out.Steps = append(out.Steps, ai.TimelineStep{ActivityID: positiveActivityID(m.id), ResultID: positiveActivityID(m.result), Number: number, Tool: m.tool, State: state,
			RecordedAt: m.recorded, StartedAt: m.started, FinishedAt: m.finished, Round: m.round, Batch: m.batch, Overlap: m.overlapStep != 0, OverlapStep: m.overlapStep, Finding: m.finding})
		if number == from && from > 1 {
			if from == section.First {
				previousFirst := out.Sections[sectionIndex-1].First
				out.Previous = previousFirst + (section.First-1-previousFirst)/timelinePageSize*timelinePageSize
			} else {
				out.Previous = max(section.First, from-timelinePageSize)
			}
		}
		if number < total {
			out.Next = number + 1
		} else {
			out.Next = 0
		}
	}
	if len(textIDs) == 0 {
		return nil
	}

	if todoID > 0 {
		textIDs = append(textIDs, todoID)
	}
	var data []struct {
		ID                          int64
		Tool, Kind, Summary, Detail string
	}
	err = tx.Raw(`SELECT id,tool,kind,left(summary,600) AS summary,
       left(detail,CASE WHEN tool='TodoWrite' AND kind='tool_use' THEN 32768 ELSE 8000 END) AS detail
       FROM ai_conversation_activity WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND run_id=? AND id=ANY(?::bigint[])`,
		append(scope, pq.Array(textIDs))...).Scan(&data).Error
	if err != nil {
		return err
	}
	byID := make(map[string]int, len(data))
	for i, item := range data {
		byID[positiveActivityID(item.ID)] = i
		if todoID == item.ID {
			addTodoChecks(out, item.ID, item.Detail)
		}
	}
	for i := range out.Steps {
		step := &out.Steps[i]
		if idx, ok := byID[step.ActivityID]; ok {
			step.Action, step.Subject, step.ActionKind = stepAction(step.Tool, data[idx].Detail)
		}
		if idx, ok := byID[step.ResultID]; ok {
			step.Outcome, step.OutcomeCount = stepOutcome(data[idx].Summary, data[idx].Detail)
		}
	}
	return nil
}
