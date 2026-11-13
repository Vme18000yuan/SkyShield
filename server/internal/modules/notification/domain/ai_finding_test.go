package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAIFindingNotificationEnvelope(t *testing.T) {
	payload := AIFindingPayload{TenantID: "00000000-0000-0000-0000-000000000001", OwnerUserID: 7, TaskID: 12, TaskName: "任务", FindingID: 34, Name: "测试发现", Severity: "high"}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	event := Occurrence{EventID: "ai-finding:34:created", Kind: KindAIFindingCreated, PayloadVersion: 1, Subject: "aiTasks/12/findings/34", OccurredAt: time.Now().UTC(), Priority: PriorityHigh, Payload: raw}
	decoded, err := DecodeOccurrence(event)
	if err != nil || decoded.Category != CategoryVulnerability || IsExternallyDeliverableKind(event.Kind) {
		t.Fatalf("AI finding envelope: %+v %v", decoded, err)
	}
	event.Priority = PriorityNormal
	if _, err = DecodeOccurrence(event); err == nil {
		t.Fatal("incorrect AI severity priority accepted")
	}
	event.Priority = PriorityHigh
	event.Subject = "aiTasks/99/findings/34"
	if _, err = DecodeOccurrence(event); err == nil {
		t.Fatal("cross-task AI notification subject accepted")
	}
	payload.FromStatus, payload.ToStatus = "pending", "fixed"
	raw, _ = json.Marshal(payload)
	event.Kind, event.Subject, event.Payload = KindAIFindingStatusChanged, "aiTasks/12/findings/34", raw
	if _, err = DecodeOccurrence(event); err != nil || IsExternallyDeliverableKind(event.Kind) {
		t.Fatalf("AI status notification: %v", err)
	}
	payload.ToStatus = "pending"
	raw, _ = json.Marshal(payload)
	event.Payload = raw
	if _, err = DecodeOccurrence(event); err == nil {
		t.Fatal("unchanged AI finding status accepted")
	}
}
