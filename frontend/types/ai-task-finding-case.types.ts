export interface AITaskFindingCaseMember {
  id: string
  name: string
  vulnclass: string
  severity: string
  status: string
  summary: string
}

export interface AITaskFindingCase {
  id: string
  task_id: string
  task_name: string
  title: string
  reason: string
  severity: string
  severity_reason?: string
  report?: string
  version: number
  report_version: number
  has_report: boolean
  member_count: number
  inherited: boolean
  created_at: string
  members?: AITaskFindingCaseMember[]
  events?: Array<{ id: string; action: string; actor: string; reason: string; finding_ids: string[]; created_at: string }>
  events_truncated?: boolean
}

export interface AITaskFindingCasePage {
  items: AITaskFindingCase[]
  total: number
  page: number
  size: number
}
