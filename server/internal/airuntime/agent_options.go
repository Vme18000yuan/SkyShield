package airuntime

type AgentOptions struct {
	WebSearch          bool   `json:"web_search"`
	InteractiveShell   bool   `json:"interactive_shell"`
	TriggerRunMode     string `json:"trigger_run_mode"`
	TriggerMergeMode   string `json:"trigger_merge_mode"`
	TriggerMaxParallel int    `json:"trigger_max_parallel"`
}

func DefaultAgentOptions(key string) AgentOptions {
	out := AgentOptions{TriggerRunMode: "serial", TriggerMergeMode: "all", TriggerMaxParallel: 5}
	switch key {
	case "planner", "worker", "mainagent", "assistant", "auto", "pentest":
		out.InteractiveShell = true
	}
	if key == "reporter" {
		out.TriggerRunMode = "parallel"
		out.TriggerMergeMode = "none"
	}
	return out
}
