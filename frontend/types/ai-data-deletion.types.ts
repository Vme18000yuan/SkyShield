export type AIDataDeletionRequest =
  | { kind: "task"; id: string }
  | { kind: "executions"; source: "task" | "conversation"; ids: string[] }
  | { kind: "traffic"; source: "task" | "conversation"; id: string; ids?: string[]; all?: boolean }

export interface AIDataDeletionResult {
  deleted: number
  cleanupPending: boolean
}
