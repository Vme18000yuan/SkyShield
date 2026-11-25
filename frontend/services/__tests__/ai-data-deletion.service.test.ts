import { beforeEach, describe, expect, it, vi } from "vitest"
import { AxiosHeaders } from "axios"
import { api } from "@/lib/api-client"
import { deleteAIData } from "@/services/ai-data-deletion.service"

vi.mock("@/lib/api-client", () => ({ api: { delete: vi.fn() } }))
const response = (data: unknown) => ({ data, status: 200, statusText: "OK", headers: {}, config: { headers: new AxiosHeaders() } })

describe("AI data deletion transport", () => {
  beforeEach(() => { vi.clearAllMocks(); vi.mocked(api.delete).mockResolvedValue(response({ deleted: 1, cleanupPending: false })) })
  it("deletes a task without an option to delete registered assets", async () => {
    await deleteAIData({ kind: "task", id: "7" })
    expect(api.delete).toHaveBeenCalledWith("/aiTasks/7", undefined)
  })
  it("keeps task and conversation execution IDs in separate endpoints", async () => {
    await deleteAIData({ kind: "executions", source: "task", ids: ["1", "2"] })
    expect(api.delete).toHaveBeenLastCalledWith("/aiTaskToolExecutions", { data: { ids: ["1", "2"] } })
    await deleteAIData({ kind: "executions", source: "conversation", ids: ["1"] })
    expect(api.delete).toHaveBeenLastCalledWith("/aiToolExecutions", { data: { ids: ["1"] } })
  })
  it("distinguishes exact traffic deletion from an explicit whole-source clear", async () => {
    const id = "11111111-1111-4111-8111-111111111111"
    await deleteAIData({ kind: "traffic", source: "conversation", id, ids: ["capture-1"] })
    expect(api.delete).toHaveBeenLastCalledWith(`/aiConversations/${id}/traffic`, { data: { ids: ["capture-1"] } })
    await deleteAIData({ kind: "traffic", source: "task", id: "7", all: true })
    expect(api.delete).toHaveBeenLastCalledWith("/aiTasks/7/traffic", { data: { all: true } })
  })
  it("rejects invalid paths and ambiguous selections without issuing requests", async () => {
    await expect(deleteAIData({ kind: "task", id: "../7" })).rejects.toThrow()
    await expect(deleteAIData({ kind: "executions", source: "task", ids: [] })).rejects.toThrow()
    await expect(deleteAIData({ kind: "executions", source: "task", ids: ["1", "1"] })).rejects.toThrow()
    await expect(deleteAIData({ kind: "traffic", source: "task", id: "7", all: true, ids: ["one"] })).rejects.toThrow()
    expect(api.delete).not.toHaveBeenCalled()
  })
  it("preserves pending cleanup rather than treating it as completed", async () => {
    vi.mocked(api.delete).mockResolvedValue(response({ deleted: 1, cleanupPending: true }))
    expect(await deleteAIData({ kind: "task", id: "7" })).toEqual({ deleted: 1, cleanupPending: true })
    vi.mocked(api.delete).mockResolvedValue(response({ deleted: 1 }))
    await expect(deleteAIData({ kind: "task", id: "7" })).rejects.toThrow("Invalid deletion response")
  })
})
