export interface AIFindingReviewCandidate {
  id: string
  task_id: string
  task_name: string
  target_name: string
  name: string
  vulnclass: string
  severity: string
  status: string
  summary: string
  in_group: boolean
  case_id?: string
  created_at: string
}

export interface AIFindingReviewCandidatePage {
  items: AIFindingReviewCandidate[]
  total: number
  page: number
  size: number
}
