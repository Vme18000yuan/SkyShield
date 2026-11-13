import { api } from "@/lib/api-client"
import type { AITaskFindingCase } from "@/types/ai-task-finding-case.types"

// A review may not modify a group containing unselected findings. Expand an
// owned group before selection so the submitted scope is complete.
export async function loadReviewFindingGroup(taskId: string, caseId: string): Promise<AITaskFindingCase> {
  const group = (await api.get<AITaskFindingCase>(`/aiTasks/${encodeURIComponent(taskId)}/findingCases/${encodeURIComponent(caseId)}`)).data
  if (group.id !== caseId || group.task_id !== taskId || group.inherited || !group.members || group.members.length < 2 || group.members.length !== group.member_count || group.members.length > 200) {
    throw new Error("finding group changed or is not writable")
  }
  return group
}
