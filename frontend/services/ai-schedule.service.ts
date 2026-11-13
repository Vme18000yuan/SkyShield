import { api } from "@/lib/api-client"
import type { AISchedule, AIScheduleAction, AIScheduleActionResult, AIScheduleDetail, AIScheduleInput } from "@/types/ai-schedule.types"
export class AIScheduleService {
  static async list(): Promise<AISchedule[]> {
    return (await api.get<{ schedules: AISchedule[] }>("/aiSchedules")).data.schedules
  }
  static async get(id: string): Promise<AIScheduleDetail> {
    return (await api.get<AIScheduleDetail>(`/aiSchedules/${encodeURIComponent(id)}`)).data
  }
  static async save(id: string | null, input: AIScheduleInput): Promise<AISchedule> {
    return id ? (await api.put<AISchedule>(`/aiSchedules/${encodeURIComponent(id)}`, input)).data : (await api.post<AISchedule>("/aiSchedules", input)).data
  }
  static async delete(id: string): Promise<void> { await api.delete(`/aiSchedules/${encodeURIComponent(id)}`) }
  static async action(id: string, action: AIScheduleAction): Promise<AIScheduleActionResult> { return (await api.post<AIScheduleActionResult>(`/aiSchedules/${encodeURIComponent(id)}/actions`, { action })).data }
}
