package airuntime

// Immutable result crossing from the single planner into the coordinator.
// Human events are acknowledged only after the associated plan completes.
type taskPlanResult struct {
	met      bool
	reason   string
	err      error
	human    []humanTaskEvent
	settling bool
}
