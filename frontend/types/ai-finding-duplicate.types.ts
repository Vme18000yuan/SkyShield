import type { AITaskFinding } from "./ai-task-finding.types"

export interface AIFindingDuplicate {
  id: string
  name: string
  vulnclass: string
  severity: string
  summary: string
  case_id?: string
  score: number
  tier: "strong" | "weak"
  hit_reasons: Array<"same_type" | "asset_overlap" | "same_name">
  assets: AITaskFinding["assets"]
}

export interface AIFindingDuplicatePage {
  items: AIFindingDuplicate[]
  total: number
  strong_total: number
  page: number
  size: number
}
