import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { resetMockScenario, setMockScenario } from "@/mock/scenarios"

describe("AI deletion in mock mode", () => {
  beforeEach(() => setMockScenario("happy"))
  afterEach(() => resetMockScenario())
  it.each([
    "/v1/aiTasks/7",
    "/v1/aiToolExecutions",
    "/v1/aiTaskToolExecutions",
    "/v1/aiTasks/7/traffic",
    "/v1/aiConversations/11111111-1111-4111-8111-111111111111/traffic",
  ])("explicitly refuses unsupported deletion instead of forwarding %s", async path => {
    const result = await fetch(`http://localhost${path}`, { method: "DELETE", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ids: ["1"] }) })
    expect(result.status).toBe(501)
    expect(await result.json()).toMatchObject({ error: { code: "mock_route_not_implemented" } })
  })
})
