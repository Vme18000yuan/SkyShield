import type { AITaskFindingCaseMember } from "./ai-task-finding-case.types"

export interface AIFindingReviewDecision {
  finding_id: string
  verdict: "independent" | "merged" | "suggested"
  reason: string
  root_cause_evidence: string
}

export interface AIFindingReview {
  id: string
  task_id: string
  conversation_id?: string
  state: "queued" | "running" | "completed" | "failed" | "stopped"
  error: string
  finding_count: number
  finding_ids?: string[]
  findings?: AITaskFindingCaseMember[]
  conclusion?: { summary?: string; decisions?: AIFindingReviewDecision[] }
  created_at: string
  started_at?: string
  finished_at?: string
}

export interface AIFindingReviewPage {
  items: AIFindingReview[]
  total: number
  page: number
  size: number
}
