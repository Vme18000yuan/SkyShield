# Tool failure boundaries

Agent entrypoints create sessions through `newToolSafeSession`. Host-provided
tools, including deferred tools and the Compress tool, keep their schemas,
permissions, raw-input capability and deferred visibility.

- Ordinary returned errors remain error tool results for the next model turn.
- A tool callback panic becomes an explicit error result. The panic value is
  not included in model-visible text because it may contain credentials.
- Permission/scheduling callback panics fail closed. They never grant a tool
  permission or convert a blocked action into an allowed one.
- These guards do not replay calls. After a panic or transport loss, effects may
  be uncertain, so the error tells the Agent to inspect existing results first.
- User cancellation, run budgets, permission boundaries, provider failures and
  unavailable persistence retain their own stop/settlement behavior. This is
  isolation of recoverable tool failures, not a promise of uninterrupted runs.

`tool_guard_test.go` exercises an error followed by another tool and a final
answer, plus permission denial, malformed input, raw input and cancellation.
