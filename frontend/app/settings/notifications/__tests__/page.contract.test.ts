import { readFileSync } from "node:fs"
import path from "node:path"
import { describe, expect, it } from "vitest"

describe("legacy notification settings bookmark", () => {
  it("redirects to the single outbound settings page", () => {
    const source = readFileSync(path.resolve(process.cwd(), "app/settings/notifications/page.tsx"), "utf8")
    expect(source).toContain('redirect("/settings/ai/notifications/")')
    const menu = readFileSync(path.resolve(process.cwd(), "components/app-sidebar.tsx"), "utf8")
    expect(menu).not.toContain('url: "/settings/notifications/"')
    expect(menu).toContain('url: "/settings/ai/notifications/"')
  })
})
