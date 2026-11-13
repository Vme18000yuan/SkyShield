import { describe, expect, it } from "vitest"

import { buildTokenDays, buildTokenProfiles, tokenProfileKey } from "@/components/overview/overview-token-usage-data"
import type { AIUsageDay, AIUsageProfile } from "@/types/ai-usage.types"

function usage(id: string, name: string, input: number, output: number, cache: number): AIUsageProfile {
  return { profile_id: id, profile_name: name, calls: 1, tasks: 1, input_tokens: input, output_tokens: output, cache_read_tokens: cache, cache_write_tokens: 0 }
}

describe("overview token usage", () => {
  it("groups renamed profiles by their stable ID and retains historical and empty configurations", () => {
    const profiles = buildTokenProfiles(
      [{ id: "a", displayName: "Current name", model: "model-a", isDefault: true }, { id: "b", displayName: "Unused", model: "model-b", isDefault: false }],
      [usage("a", "Old name", 100, 20, 30), usage("a", "Current name", 200, 40, 50), usage("deleted", "Former model", 10, 2, 0), usage("", "", 5, 1, 0)],
      "Environment",
    )
    expect(profiles).toHaveLength(4)
    expect(profiles[0]).toMatchObject({ key: "id:a", label: "Current name", isDefault: true, metrics: { input_tokens: 300, output_tokens: 60, cache_read_tokens: 80, calls: 2 } })
    expect(profiles[1]).toMatchObject({ key: "id:b", metrics: { calls: 0 } })
    expect(profiles[2]).toMatchObject({ key: "id:deleted", historical: true })
    expect(profiles[3]).toMatchObject({ key: "name:", label: "Environment", historical: false })
  })

  it("fills UTC date gaps and does not count cached input twice", () => {
    const rows: AIUsageDay[] = [
      { ...usage("a", "Old name", 100, 20, 30), date: "2026-11-04" },
      { ...usage("a", "Current name", 200, 40, 50), date: "2026-11-04" },
      { ...usage("b", "Other", 500, 10, 100), date: "2026-11-05" },
    ]
    const days = buildTokenDays(rows, tokenProfileKey("a", "Current name"), 3, new Date("2026-11-05T18:00:00Z"))
    expect(days).toEqual([
      { date: "2026-11-03", input: 0, cacheRead: 0, output: 0 },
      { date: "2026-11-04", input: 220, cacheRead: 80, output: 60 },
      { date: "2026-11-05", input: 0, cacheRead: 0, output: 0 },
    ])
    expect(days[1].input + days[1].cacheRead + days[1].output).toBe(360)
  })

  it("does not render negative input when a provider reports inconsistent cache counts", () => {
    const rows: AIUsageDay[] = [{ ...usage("a", "Model", 10, 2, 30), date: "2026-11-05" }]
    expect(buildTokenDays(rows, "all", 1, new Date("2026-11-05T00:00:00Z"))).toEqual([{ date: "2026-11-05", input: 0, cacheRead: 10, output: 2 }])
  })
})
