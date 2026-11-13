export type AIScheduleStatus = "waiting" | "running" | "outside" | "completed" | "error" | "paused"
export interface AIScheduleInput {
  displayName: string
  remark: string
  enabled: boolean
  timezoneMode: "beijing" | "system"
  scheduleType: "once" | "weekly"
  runDate: string
  endDate: string
  weekdays: number[]
  startTime: string
  endTime: string
  startImmediately: boolean
  taskIds: string[]
}
export interface AISchedule extends AIScheduleInput {
  id: string
  name: string
  status: AIScheduleStatus
  lastError: string
  lastTransitionAt: string | null
  createdAt: string
  updatedAt: string
  serverTime: string
  nextStart: string | null
  nextEnd: string | null
}
export interface AIScheduleHistory {
  id: string
  taskId: string | null
  action: string
  success: boolean
  message: string
  createdAt: string
}
export interface AIScheduleDetail extends AISchedule { history: AIScheduleHistory[] }
export type AIScheduleAction = "enable" | "disable" | "run"
export interface AIScheduleActionResult { started?: number; errors?: string[] }
