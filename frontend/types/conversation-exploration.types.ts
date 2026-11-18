export type ConversationExploreKind = "begin" | "goal" | "intent" | "result" | "finding" | "hint"
export type ConversationExploreRelation = "spawns" | "next" | "yields" | "registers" | "summary"
export type ConversationExploreNode = {
  id: string
  kind: ConversationExploreKind
  title: string
  state: string
  tool?: string
  activityId?: string
  vulnerabilityId?: number
  severity?: string
  timestamp: string
}
export type ConversationExploreRun = { id: string; status: string; running: boolean; createdAt: string; finishedAt?: string }
export type ConversationExploration = {
  run: ConversationExploreRun | null
  runs: ConversationExploreRun[]
  runsBefore?: string
  nodes: ConversationExploreNode[]
  edges: { source: string; target: string; kind: ConversationExploreRelation }[]
  hasMore: boolean
  before?: string
}

export type TimelineText = { activityId?: string; text: string }
export type TimelineCounts = { steps: number; returned: number; failed: number; interrupted: number; pending: number; missing: number; measured: number; overlap: number }
export type TimelineFinding = {
  receiptId: string; vulnerabilityId: number; title: string; severity: string; reviewed: boolean
  sourceActivityId?: string; step?: number; evidenceSources?: { activityId: string; step: number }[]
}
export type TimelineStep = {
  activityId: string; resultId?: string; number: number; tool: string; action?: string; subject?: string; actionKind?: string
  outcome?: string; outcomeCount?: number; state: string; recordedAt: string; startedAt?: string; finishedAt?: string
  round?: number; batch?: number; overlap: boolean; overlapStep?: number; finding?: TimelineFinding
}
export type TimelinePurpose = {
  source: "todo" | "statement" | "step" | "observed" | "receipt" | "unknown"
  text?: string; topics?: string[]; subject?: string; supportingSteps?: number; basis?: { activityId: string; step: number }[]
}
export type TimelineSection = TimelineCounts & { first: number; last: number; purpose?: TimelinePurpose; title?: string; tool: string; subject?: string; actionKind?: string; outcome?: string; findings: number; firstFinding?: TimelineFinding }
export type TimelineCheck = { text: string; state: string; source: string; activityId: string; statusActivityId?: string }
export type ConversationTimeline = {
  run: (ConversationExploreRun & { terminalReason?: string }) | null; runs: ConversationExploreRun[]; runsBefore?: string
  request: TimelineText; conclusion: TimelineText; counts: TimelineCounts; sections: TimelineSection[]; steps: TimelineStep[]
  checks: TimelineCheck[]; checksTruncated: boolean; findings: TimelineFinding[]; findingCount: number
  nextFinding?: string; previous?: number; next?: number
}
