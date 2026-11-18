package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

const explorationCallPage = 30
const explorationRunPage = 20

type explorationCall struct {
	ID                              int64
	Tool, ToolUseID, Summary, Input string
	CreatedAt                       time.Time
	ResultID                        *int64
	ResultError                     bool
	Output                          string
	ResultAt                        *time.Time
	VulnerabilityID                 *int
	FindingName, Severity           string
	Reviewed                        bool
}

func (s *ConversationStore) ReadConversationExploration(ctx context.Context, user int, conversation, run, beforeRun string, before int64) (*aiapp.ConversationExploration, error) {
	out := &aiapp.ConversationExploration{Runs: []aiapp.ConversationExplorationRun{}, Nodes: []aiapp.ConversationExplorationNode{}, Edges: []aiapp.ConversationExplorationEdge{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var title string
		err := tx.Raw(`SELECT c.title`+ownedConversation+`FOR SHARE OF c,m,t,u`, assets.DefaultTenantID, user, conversation).Row().Scan(&title)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrConversationNotFound
		}
		if err != nil {
			return err
		}
		if err := explorationRunWindow(tx, user, conversation, run, beforeRun, out); err != nil {
			return err
		}
		if out.Run == nil {
			return nil
		}
		current := out.Run
		run = current.ID
		var goal struct {
			ID        int64
			Summary   string
			CreatedAt time.Time
		}
		if err := tx.Raw(`SELECT id,left(COALESCE(NULLIF(detail,''),summary),600) summary,created_at FROM ai_conversation_activity WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND run_id=? AND kind='user' ORDER BY id LIMIT 1`, assets.DefaultTenantID, user, conversation, run).Scan(&goal).Error; err != nil {
			return err
		}
		rootID, goalID := "begin:"+run, "goal:"+run
		out.Nodes = append(out.Nodes, aiapp.ConversationExplorationNode{ID: rootID, Kind: "begin", Title: title, State: current.Status, Timestamp: current.CreatedAt})
		goalState := "finished"
		if current.Running {
			goalState = "running"
		}
		out.Nodes = append(out.Nodes, aiapp.ConversationExplorationNode{ID: goalID, Kind: "goal", Title: goal.Summary, State: goalState, ActivityID: positiveActivityID(goal.ID), Timestamp: current.CreatedAt})
		out.Edges = append(out.Edges, aiapp.ConversationExplorationEdge{Source: rootID, Target: goalID, Kind: "spawns"})
		calls := []explorationCall{}
		// Pair by exact run/call ID AND occurrence bounds. A reused call ID cannot
		// acquire the result of a later invocation or a different conversation.
		statement := `SELECT u.id,u.tool,u.tool_use_id,left(u.summary,500) summary,left(u.detail,8000) input,u.created_at,
    result.id result_id,COALESCE(result.is_error,FALSE) result_error,left(COALESCE(NULLIF(result.summary,''),result.detail,''),500) output,result.created_at result_at,
    report.vulnerability_id,COALESCE(NULLIF(v.display_name,''),report.report->>'name',v.vuln_type,'') finding_name,COALESCE(v.severity,'') severity,COALESCE(v.reviewed,FALSE) reviewed
    FROM (SELECT * FROM ai_conversation_activity WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND run_id=? AND kind='tool_use' AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?) u
    LEFT JOIN LATERAL (SELECT x.id,x.summary,x.detail,x.is_error,x.created_at FROM ai_conversation_activity x
      WHERE x.tenant_id=u.tenant_id AND x.owner_user_id=u.owner_user_id AND x.conversation_id=u.conversation_id AND x.run_id=u.run_id
      AND u.tool_use_id<>'' AND x.tool_use_id=u.tool_use_id AND x.kind='tool_result' AND x.id>u.id
      AND NOT EXISTS(SELECT 1 FROM ai_conversation_activity later WHERE later.tenant_id=u.tenant_id AND later.owner_user_id=u.owner_user_id AND later.conversation_id=u.conversation_id AND later.run_id=u.run_id AND later.tool_use_id=u.tool_use_id AND later.kind='tool_use' AND later.id>u.id AND later.id<x.id)
      ORDER BY x.id LIMIT 1) result ON TRUE
    LEFT JOIN ai_conversation_vulnerability_report report ON u.tool='report_vulnerability' AND report.tenant_id=u.tenant_id AND report.owner_user_id=u.owner_user_id AND report.conversation_id=u.conversation_id AND report.run_id=u.run_id AND report.tool_use_id=u.tool_use_id
      AND report.created_at>=u.created_at AND NOT EXISTS(SELECT 1 FROM ai_conversation_activity later WHERE later.tenant_id=u.tenant_id AND later.owner_user_id=u.owner_user_id AND later.run_id=u.run_id AND later.conversation_id=u.conversation_id AND later.tool_use_id=u.tool_use_id AND later.kind='tool_use' AND later.id>u.id AND later.created_at<=report.created_at)
    LEFT JOIN vulnerability v ON v.id=report.vulnerability_id
    ORDER BY u.id DESC`
		if err := tx.Raw(statement, assets.DefaultTenantID, user, conversation, run, before, before, explorationCallPage+1).Scan(&calls).Error; err != nil {
			return err
		}
		if len(calls) > explorationCallPage {
			out.HasMore = true
			calls = calls[:explorationCallPage]
		}
		if len(calls) > 0 {
			out.Before = strconv.FormatInt(calls[len(calls)-1].ID, 10)
		}
		for left, right := 0, len(calls)-1; left < right; left, right = left+1, right-1 {
			calls[left], calls[right] = calls[right], calls[left]
		}
		var previousID string
		for _, call := range calls {
			id := "intent:" + strconv.FormatInt(call.ID, 10)
			state := "unknown"
			if current.Running {
				state = "running"
			}
			if call.ResultID != nil {
				state = "succeeded"
				if call.ResultError {
					state = "failed"
				}
			}
			out.Nodes = append(out.Nodes, aiapp.ConversationExplorationNode{ID: id, Kind: "intent", Title: explorationCallTitle(call), Tool: call.Tool, State: state, ActivityID: positiveActivityID(call.ID), Timestamp: call.CreatedAt})
			out.Edges = append(out.Edges, aiapp.ConversationExplorationEdge{Source: goalID, Target: id, Kind: "spawns"})
			if previousID != "" {
				out.Edges = append(out.Edges, aiapp.ConversationExplorationEdge{Source: previousID, Target: id, Kind: "next"})
			}
			previousID = id
			if call.ResultID != nil {
				resultID := "result:" + strconv.FormatInt(*call.ResultID, 10)
				timestamp := call.CreatedAt
				if call.ResultAt != nil {
					timestamp = *call.ResultAt
				}
				out.Nodes = append(out.Nodes, aiapp.ConversationExplorationNode{ID: resultID, Kind: "result", Title: call.Output, Tool: call.Tool, State: state, ActivityID: positiveActivityID(*call.ResultID), Timestamp: timestamp})
				out.Edges = append(out.Edges, aiapp.ConversationExplorationEdge{Source: id, Target: resultID, Kind: "yields"})
			}
			if call.VulnerabilityID != nil {
				findingID := "finding:" + strconv.Itoa(*call.VulnerabilityID) + ":" + strconv.FormatInt(call.ID, 10)
				findingState := "pending_review"
				if call.Reviewed {
					findingState = "reviewed"
				}
				out.Nodes = append(out.Nodes, aiapp.ConversationExplorationNode{ID: findingID, Kind: "finding", Title: call.FindingName, State: findingState, VulnerabilityID: *call.VulnerabilityID, Severity: call.Severity, Timestamp: call.CreatedAt})
				source := id
				if call.ResultID != nil {
					source = "result:" + strconv.FormatInt(*call.ResultID, 10)
				}
				out.Edges = append(out.Edges, aiapp.ConversationExplorationEdge{Source: source, Target: findingID, Kind: "registers"})
			}
		}
		// The most recent visible assistant summary is a hint, never an asserted fact.
		var summary struct {
			ID        int64
			Summary   string
			CreatedAt time.Time
		}
		if err := tx.Raw(`SELECT id,left(COALESCE(NULLIF(detail,''),summary),600) summary,created_at FROM ai_conversation_activity WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND run_id=? AND kind IN ('assistant_message','text','result') AND COALESCE(NULLIF(detail,''),summary)<>'' ORDER BY id DESC LIMIT 1`, assets.DefaultTenantID, user, conversation, run).Scan(&summary).Error; err != nil {
			return err
		}
		if summary.ID > 0 {
			hintID := "hint:" + strconv.FormatInt(summary.ID, 10)
			out.Nodes = append(out.Nodes, aiapp.ConversationExplorationNode{ID: hintID, Kind: "hint", Title: summary.Summary, State: "recorded", ActivityID: positiveActivityID(summary.ID), Timestamp: summary.CreatedAt})
			out.Edges = append(out.Edges, aiapp.ConversationExplorationEdge{Source: goalID, Target: hintID, Kind: "summary"})
		}
		return nil
	})
	return out, err
}

func positiveActivityID(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
func explorationCallTitle(call explorationCall) string {
	var fields map[string]json.RawMessage
	input := call.Input
	if offset := strings.Index(input, "{"); offset >= 0 {
		input = input[offset:]
	}
	if json.Unmarshal([]byte(input), &fields) == nil {
		for _, key := range []string{"description", "name", "summary", "command", "url", "pattern", "query", "prompt"} {
			var text string
			if json.Unmarshal(fields[key], &text) == nil && strings.TrimSpace(text) != "" {
				runes := []rune(strings.TrimSpace(text))
				if len(runes) > 300 {
					runes = runes[:300]
				}
				return string(runes)
			}
		}
	}
	if strings.TrimSpace(call.Summary) != "" {
		return call.Summary
	}
	return call.Tool
}

func explorationRunWindow(tx *gorm.DB, user int, conversation, run, beforeRun string, out *aiapp.ConversationExploration) error {
	runScope := ` FROM ai_run r WHERE r.tenant_id=? AND r.owner_user_id=? AND r.conversation_id=? `
	runColumns := `SELECT r.id::text,CASE WHEN r.status IN ('running','stopping') AND r.lease_until<=clock_timestamp() THEN 'interrupted' ELSE r.status END status,r.created_at,r.finished_at,r.terminal_reason,(r.status IN ('running','stopping') AND r.lease_until>clock_timestamp()) running`
	runArgs := []any{assets.DefaultTenantID, user, conversation}
	runFilter := ""
	if beforeRun != "" {
		var cursor struct {
			ID        string
			CreatedAt time.Time
		}
		if err := tx.Raw(`SELECT r.id::text,r.created_at`+runScope+`AND r.id=?`, append(runArgs, beforeRun)...).Scan(&cursor).Error; err != nil {
			return err
		}
		if cursor.ID == "" {
			return aiapp.ErrConversationNotFound
		}
		runFilter = `AND (r.created_at,r.id) < (?,?::uuid) `
		runArgs = append(runArgs, cursor.CreatedAt, cursor.ID)
	}
	if err := tx.Raw(runColumns+runScope+runFilter+`ORDER BY r.created_at DESC,r.id DESC LIMIT ?`, append(runArgs, explorationRunPage+1)...).Scan(&out.Runs).Error; err != nil {
		return err
	}
	if len(out.Runs) > explorationRunPage {
		out.Runs = out.Runs[:explorationRunPage]
		out.RunsBefore = out.Runs[len(out.Runs)-1].ID
	}
	if run == "" {
		if len(out.Runs) == 0 {
			return nil
		}
		run = out.Runs[0].ID
	}
	current := aiapp.ConversationExplorationRun{}
	if err := tx.Raw(runColumns+runScope+`AND r.id=?`, assets.DefaultTenantID, user, conversation, run).Scan(&current).Error; err != nil {
		return err
	}
	if current.ID == "" {
		return aiapp.ErrConversationNotFound
	}
	out.Run = &current
	return nil
}
