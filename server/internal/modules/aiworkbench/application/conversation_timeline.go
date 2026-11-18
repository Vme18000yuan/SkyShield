package application

import (
	"context"
	"time"
)

// The timeline is a read model of one chat run. A source ID refers to a saved
// activity in that run. Ordinals express display order only.
type TimelineText struct {
	ActivityID string `json:"activityId,omitempty"`
	Text       string `json:"text"`
}
type TimelineCounts struct {
	Measured    int `json:"measured"`
	Steps       int `json:"steps"`
	Returned    int `json:"returned"`
	Failed      int `json:"failed"`
	Interrupted int `json:"interrupted"`
	Pending     int `json:"pending"`
	Missing     int `json:"missing"`
	Overlap     int `json:"overlap"`
}
type TimelineFinding struct {
	EvidenceSources  []TimelineSource `json:"evidenceSources,omitempty"`
	ReceiptID        string           `json:"receiptId"`
	VulnerabilityID  int              `json:"vulnerabilityId"`
	Title            string           `json:"title"`
	Severity         string           `json:"severity"`
	Reviewed         bool             `json:"reviewed"`
	SourceActivityID string           `json:"sourceActivityId,omitempty"`
	Step             int              `json:"step,omitempty"`
}
type TimelineSource struct {
	ActivityID string `json:"activityId"`
	Step       int    `json:"step"`
}
type TimelineStep struct {
	ActionKind   string           `json:"actionKind,omitempty"`
	OverlapStep  int              `json:"overlapStep,omitempty"`
	ActivityID   string           `json:"activityId"`
	ResultID     string           `json:"resultId,omitempty"`
	Number       int              `json:"number"`
	Tool         string           `json:"tool"`
	Action       string           `json:"action,omitempty"`
	Subject      string           `json:"subject,omitempty"`
	Outcome      string           `json:"outcome,omitempty"`
	OutcomeCount *int             `json:"outcomeCount,omitempty"`
	State        string           `json:"state"`
	RecordedAt   time.Time        `json:"recordedAt"`
	StartedAt    *time.Time       `json:"startedAt,omitempty"`
	FinishedAt   *time.Time       `json:"finishedAt,omitempty"`
	Round        int              `json:"round,omitempty"`
	Batch        int              `json:"batch,omitempty"`
	Overlap      bool             `json:"overlap"`
	Finding      *TimelineFinding `json:"finding,omitempty"`
}
type TimelineSection struct {
	Purpose      TimelinePurpose  `json:"purpose"`
	ActionKind   string           `json:"actionKind,omitempty"`
	Subject      string           `json:"subject,omitempty"`
	Outcome      string           `json:"outcome,omitempty"`
	FirstFinding *TimelineFinding `json:"firstFinding,omitempty"`
	First        int              `json:"first"`
	Last         int              `json:"last"`
	Title        string           `json:"title,omitempty"`
	Tool         string           `json:"tool"`
	Findings     int              `json:"findings"`
	TimelineCounts
}

// Purpose describes the work represented by a section. Sources point to the
// existing records behind the label, not prerequisites for executing a tool.
type TimelinePurpose struct {
	Source          string           `json:"source"`
	Text            string           `json:"text,omitempty"`
	Topics          []string         `json:"topics,omitempty"`
	Subject         string           `json:"subject,omitempty"`
	SupportingSteps int              `json:"supportingSteps,omitempty"`
	Basis           []TimelineSource `json:"basis,omitempty"`
}
type TimelineCheck struct {
	StatusActivityID string `json:"statusActivityId,omitempty"`
	Text             string `json:"text"`
	State            string `json:"state"`
	Source           string `json:"source"`
	ActivityID       string `json:"activityId"`
}
type ConversationTimeline struct {
	Run             *ConversationExplorationRun  `json:"run"`
	Runs            []ConversationExplorationRun `json:"runs"`
	RunsBefore      string                       `json:"runsBefore,omitempty"`
	Request         TimelineText                 `json:"request"`
	Conclusion      TimelineText                 `json:"conclusion"`
	Counts          TimelineCounts               `json:"counts"`
	Sections        []TimelineSection            `json:"sections"`
	Steps           []TimelineStep               `json:"steps"`
	Checks          []TimelineCheck              `json:"checks"`
	ChecksTruncated bool                         `json:"checksTruncated"`
	Findings        []TimelineFinding            `json:"findings"`
	FindingCount    int                          `json:"findingCount"`
	NextFinding     string                       `json:"nextFinding,omitempty"`
	Previous        int                          `json:"previous,omitempty"`
	Next            int                          `json:"next,omitempty"`
}

func (f *ConversationFacade) Timeline(ctx context.Context, user int, id, run, beforeRun string, from int, afterFinding int64) (*ConversationTimeline, error) {
	if user <= 0 || !validConversationID(id) || (run != "" && !validConversationID(run)) || (beforeRun != "" && !validConversationID(beforeRun)) || from < 1 || afterFinding < 0 || ((from > 1 || afterFinding > 0) && run == "") {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(interface {
		ReadConversationTimeline(context.Context, int, string, string, string, int, int64) (*ConversationTimeline, error)
	})
	if !ok {
		return nil, ErrConversationNotFound
	}
	return store.ReadConversationTimeline(ctx, user, id, run, beforeRun, from, afterFinding)
}
