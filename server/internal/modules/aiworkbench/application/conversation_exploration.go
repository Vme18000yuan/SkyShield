package application

import (
	"context"
	"time"
)

// This is a read-only projection of persisted execution records, not a second
// planner or a claim that a successful tool call proves a vulnerability.
type ConversationExplorationRun struct {
	TerminalReason string     `json:"terminalReason,omitempty"`
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"createdAt"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	Running        bool       `json:"running"`
}
type ConversationExplorationNode struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	Title           string    `json:"title"`
	State           string    `json:"state"`
	Tool            string    `json:"tool,omitempty"`
	ActivityID      string    `json:"activityId,omitempty"`
	VulnerabilityID int       `json:"vulnerabilityId,omitempty"`
	Severity        string    `json:"severity,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
}
type ConversationExplorationEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}
type ConversationExploration struct {
	Run        *ConversationExplorationRun   `json:"run"`
	Runs       []ConversationExplorationRun  `json:"runs"`
	RunsBefore string                        `json:"runsBefore,omitempty"`
	Nodes      []ConversationExplorationNode `json:"nodes"`
	Edges      []ConversationExplorationEdge `json:"edges"`
	HasMore    bool                          `json:"hasMore"`
	Before     string                        `json:"before,omitempty"`
}
type ConversationExplorationStore interface {
	ReadConversationExploration(context.Context, int, string, string, string, int64) (*ConversationExploration, error)
}

func (f *ConversationFacade) Exploration(ctx context.Context, user int, id, run, beforeRun string, before int64) (*ConversationExploration, error) {
	if user <= 0 || !validConversationID(id) || (run != "" && !validConversationID(run)) || (beforeRun != "" && !validConversationID(beforeRun)) || before < 0 {
		return nil, ErrInvalidInput
	}
	store, ok := f.store.(ConversationExplorationStore)
	if !ok {
		return nil, ErrConversationNotFound
	}
	return store.ReadConversationExploration(ctx, user, id, run, beforeRun, before)
}
