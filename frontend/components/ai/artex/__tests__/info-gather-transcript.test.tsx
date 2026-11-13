import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { Transcript } from "../transcript"
import type { Activity } from "@/types/artex-ai.types"

const base = { worker: "info_gatherer", ts: "2026-11-05T00:00:00Z" }

describe("information gathering execution", () => {
  it("shows the actual phase, live progress and completed result", () => {
    const activity: Activity[] = [
      { ...base, seq: 1, kind: "tool_use", tool: "run_info_gather_phase", tool_use_id: "step-1", summary: 'run_info_gather_phase {"phase":"dns","run_id":"r"}' },
      { ...base, seq: 2, kind: "progress", summary: "正在执行：DNS 解析。" },
      { ...base, seq: 3, kind: "tool_result", tool: "run_info_gather_phase", tool_use_id: "step-1", summary: '{"message":"DNS 解析：完成。已保存 2 条资产。","status":"success"}' },
    ]
    render(<Transcript activity={activity} chat fetchDetail={async () => ""} />)
    expect(screen.getByText("DNS 解析")).toBeVisible()
    expect(screen.getByText("正在执行：DNS 解析。")).toBeVisible()
    expect(screen.getByText("DNS 解析：完成。已保存 2 条资产。")).toBeVisible()
  })

  it("does not show a success message while a phase is still running", () => {
    const activity: Activity[] = [
      { ...base, seq: 1, kind: "tool_use", tool: "run_info_gather_phase", tool_use_id: "step-1", summary: 'run_info_gather_phase {"phase":"ports","run_id":"r"}' },
    ]
    render(<Transcript activity={activity} chat fetchDetail={async () => ""} />)
    expect(screen.getByText("端口扫描")).toBeVisible()
    expect(screen.getByText("执行中…")).toBeVisible()
    expect(screen.queryByText(/已保存/)).not.toBeInTheDocument()
  })

  it("does not display a completed tool call as a successful phase when it timed out", () => {
    const activity: Activity[] = [
      { ...base, seq: 1, kind: "tool_use", tool: "run_info_gather_phase", tool_use_id: "step-1", summary: 'run_info_gather_phase {"phase":"ports","run_id":"r"}' },
      { ...base, seq: 2, kind: "tool_result", tool: "run_info_gather_phase", tool_use_id: "step-1", summary: '{"message":"端口扫描：超时。已保存 2 条资产。","status":"timeout"}' },
    ]
    render(<Transcript activity={activity} chat fetchDetail={async () => ""} />)
    expect(screen.getByText("存在限制")).toBeVisible()
    expect(screen.queryByText("✓")).not.toBeInTheDocument()
  })
})
