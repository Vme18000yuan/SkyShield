import type { AssetInterceptRuleInput } from "@/types/artex-ai.types"

export type AITaskStatus = "created" | "running" | "paused" | "done" | "failed" | "timeout" | "stopped"
export type AITaskAction = "start" | "pause" | "resume" | "stop"

export interface AITask {
  id: string
  name: string
  displayName: string
  description: string
  goal: string
  remark: string
  categoryId: string | null
  categoryName: string
  pinned: boolean
  pinnedAt: string | null
  sourceTaskIds: string[]
  coverageEnabled: boolean
  targetId: number | null
  status: AITaskStatus
  queued: boolean
  workerCount: number
  maxTurns: number
  timeoutSeconds: number
  planHeartbeatSeconds: number
  firstRunAt: string | null
  deadlineAt: string | null
  errorMessage: string
  createdAt: string
  updatedAt: string
  completedAt: string | null
  llmProfileIds: string[]
  activeLlmProfileId: string | null
  llmFailoverState: "default" | "ready" | "chain_exhausted"
  llmFailoverReason: string
}

export interface AITaskInput {
  displayName: string
  description: string
  goal: string
  remark?: string
  categoryId?: string
  sourceTaskIds?: string[]
  targetId?: number
  assetIds: string[]
  llmProfileIds?: string[]
  workerCount: number
  maxTurns: number
  timeoutSeconds: number
  planHeartbeatSeconds?: number
  startImmediately: boolean
  seedFirstIntent?: boolean
  coverageEnabled?: boolean
  interceptRules?: AssetInterceptRuleInput[]
}

export interface AITaskModelState {
  profiles: Array<{profileId:string;position:number;status:"ready"|"quota_exhausted";lastError?:string;exhaustedAt?:string}>
  activeProfileId: string | null
  revision: number
  state: "default" | "ready" | "chain_exhausted"
  reason: string
}

export interface AITaskPage { tasks: AITask[]; nextBefore: string }
export interface AITaskGraph {
  nodes: Array<{ id: string; kind: string; state: string; priority: number; payload: Record<string, unknown>; createdAt: string; findingId?: string; trafficCount?: number; inherited?: boolean; sourceTaskId?: string; deleteReason?: string; deletionState?: "pending" | "conflict" }>
  edges: Array<{ source: string; target: string; relation: string }>
  assets: Array<{ nodeId: string; assetId: string; assetType: string; label: string }>
  truncated: boolean
}
export interface AITaskActivity {
  id: string
  nodeId: string | null
  worker: string
  kind: string
  tool: string
  toolUseId: string
  isError: boolean
  summary: string
  detail: string
	mainSeg?: number | null
  createdAt: string
}

export interface AITaskAsset {
  id: string
  assetType: string
  label: string
  source: string
  factCount: number
  inherited?: boolean
  sourceTaskId?: string
}

export interface AITaskAssetPage {
  assets: AITaskAsset[]
  nextAfter: string
}
