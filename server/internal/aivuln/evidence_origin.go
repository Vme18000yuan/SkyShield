package aivuln

import (
	"context"
	"time"

	"github.com/Autumn-27/norma/harness"
	"github.com/lib/pq"
	"github.com/yyhuni/lunafox/server/internal/pkg"
)

// Enrich a newly committed receipt, independently of the inventory write. A
// retry of the original business operation must never reassign its source.
func (s *Service) linkRecordedEvidence(ctx context.Context, user int, run, call string, trafficIDs []string) {
	occurrence := harness.ToolOccurrence(ctx)
	if occurrence == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	err := s.db.WithContext(ctx).Exec(`WITH origin AS (
      SELECT p.id AS receipt_id,a.id,a.run_id,a.tenant_id,a.owner_user_id,a.conversation_id
      FROM ai_conversation_vulnerability_report p JOIN ai_conversation_activity a
        ON a.run_id=p.run_id AND a.tenant_id=p.tenant_id AND a.owner_user_id=p.owner_user_id AND a.conversation_id=p.conversation_id
        AND a.tool_use_id=p.tool_use_id AND a.trace_call_id=? AND a.kind='tool_use' AND a.tool='report_vulnerability'
      WHERE p.run_id=? AND p.owner_user_id=? AND p.tool_use_id=? AND p.source_activity_id IS NULL
    ), evidence_reads AS (
      SELECT DISTINCT ON (a.evidence_ref) x.id FROM origin o JOIN ai_conversation_activity a
        ON a.run_id=o.run_id AND a.tenant_id=o.tenant_id AND a.owner_user_id=o.owner_user_id AND a.conversation_id=o.conversation_id
      JOIN ai_conversation_activity x ON x.run_id=a.run_id AND x.source_activity_id=a.id AND x.trace_call_id=a.trace_call_id
      WHERE a.kind='tool_use' AND a.tool='traffic_get' AND a.evidence_ref=ANY(?::text[]) AND x.kind='tool_result'
        AND NOT x.is_error AND x.id<o.id ORDER BY a.evidence_ref,x.id DESC
    )
    UPDATE ai_conversation_vulnerability_report p SET source_activity_id=o.id,
      evidence_activity_ids=ARRAY(SELECT id FROM evidence_reads ORDER BY id)
    FROM origin o WHERE p.id=o.receipt_id AND p.source_activity_id IS NULL`, occurrence, run, user, call, pq.Array(trafficIDs)).Error
	if err != nil {
		pkg.Warn("Conversation receipt source metadata could not be saved")
	}
}
