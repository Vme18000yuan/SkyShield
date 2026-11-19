import { describe, expect, it, vi } from "vitest"
import { newRequestId } from "../request-id"

describe("request IDs on LAN HTTP origins", () => {
  it("uses secure random bytes when randomUUID is unavailable", () => {
    const getRandomValues = crypto.getRandomValues.bind(crypto)
    vi.stubGlobal("crypto", { getRandomValues })
    try {
      const first = newRequestId()
      expect(first).toMatch(/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/)
      expect(newRequestId()).not.toBe(first)
    } finally { vi.unstubAllGlobals() }
  })
})
