import React from "react"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ARTEXRuntimeSettings } from "../artex-runtime-settings"

const calls = vi.hoisted(() => ({ settings: vi.fn(), llmProfiles: vi.fn(), setSettings: vi.fn() }))
vi.mock("@/services/artex-ai.service", () => ({ api: calls }))
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

describe("Independent chat budget controls", () => {
  beforeEach(() => {
    calls.settings.mockResolvedValue({ workers: 3, worker_pools: [] })
    calls.llmProfiles.mockResolvedValue([])
    calls.setSettings.mockReset()
    calls.setSettings.mockImplementation(async patch => ({ workers: 3, worker_pools: [], ...patch }))
  })
  it("defaults both cumulative limits to unlimited and allows saving zero", async () => {
    render(<ARTEXRuntimeSettings />)
    const tokens = screen.getByLabelText("累计 Token 上限")
    const requests = screen.getByLabelText("累计模型请求次数上限")
    await waitFor(() => expect(tokens).toBeEnabled())
    expect(tokens).toHaveValue(0)
    expect(requests).toHaveValue(0)
    expect(screen.getByText(/工具调用轮数由 Agent 的最大轮数设置控制/)).toBeVisible()
    fireEvent.click(screen.getByRole("button", { name: "保存对话预算" }))
    await waitFor(() => expect(calls.setSettings).toHaveBeenCalledWith({ chat_run_max_tokens: 0, chat_run_max_requests: 0 }))
  })
  it("keeps an explicitly configured limit and rejects negative input", async () => {
    calls.settings.mockResolvedValue({ workers: 3, worker_pools: [], chat_run_max_tokens: 2000000, chat_run_max_requests: 512 })
    render(<ARTEXRuntimeSettings />)
    const tokens = screen.getByLabelText("累计 Token 上限")
    await waitFor(() => expect(tokens).toHaveValue(2000000))
    expect(screen.getByLabelText("累计模型请求次数上限")).toHaveValue(512)
    fireEvent.change(tokens, { target: { value: "-1" } })
    fireEvent.click(screen.getByRole("button", { name: "保存对话预算" }))
    expect(calls.setSettings).not.toHaveBeenCalled()
  })
  it("defaults task and task-chat budgets to unlimited and saves them separately", async () => {
    calls.settings.mockResolvedValue({ workers: 3, worker_pools: [], chat_run_max_tokens: 2000000, chat_run_max_requests: 512 })
    render(<ARTEXRuntimeSettings />)
    const tokens = screen.getByLabelText("任务累计 Token 上限")
    await waitFor(() => expect(tokens).toBeEnabled())
    expect(tokens).toHaveValue(0)
    expect(screen.getByLabelText("任务累计模型请求次数上限")).toHaveValue(0)
    expect(screen.getByLabelText("累计 Token 上限")).toHaveValue(2000000)
    fireEvent.click(screen.getByRole("button", { name: "保存任务预算" }))
    await waitFor(() => expect(calls.setSettings).toHaveBeenCalledWith({ task_run_max_tokens: 0, task_run_max_requests: 0 }))
  })
})
