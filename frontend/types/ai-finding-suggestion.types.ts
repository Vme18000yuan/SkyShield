import type { AITaskFindingCaseMember } from "./ai-task-finding-case.types"

export interface AIFindingSuggestion {
  id: string
  task_id: string
  task_name: string
  title: string
  reason: string
  inherited: boolean
  created_at: string
  members: AITaskFindingCaseMember[]
}

export interface AIFindingSuggestionPage {
  items: AIFindingSuggestion[]
  total: number
  page: number
  size: number
}
