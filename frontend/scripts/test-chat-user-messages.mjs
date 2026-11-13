// All APIs use local fixtures. This test never sends a real prompt to a model.
import { chromium, expect } from "@playwright/test"

const origin = process.env.SKYSHIELD_TEST_ORIGIN || "http://127.0.0.1:33327"
const conversationId = "22222222-2222-4222-8222-222222222222"
const token = "fixture." + Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString("base64url") + ".fixture"
const timestamp = "2026-11-12T12:00:00Z"
const firstPrompt = "请检查这些本地测试记录。\n" + "保留完整的用户输入。".repeat(60) + "\n用户消息末尾标记"
const followup = "请继续处理，并保留已有结果。"
let messages = [
  { seq: 1, kind: "user", summary: firstPrompt.slice(0, 200), detail: firstPrompt },
  { seq: 2, kind: "text", summary: "已收到测试请求。" },
  { seq: 3, kind: "tool_use", tool: "Bash", tool_use_id: "fixture-call", summary: '{"command":"echo ' + "long-fixture-path/".repeat(150) + '"}' },
  { seq: 4, kind: "tool_result", tool: "Bash", tool_use_id: "fixture-call", summary: "fixture-result/".repeat(500) },
  { seq: 5, kind: "user", summary: followup, detail: followup },
  { seq: 6, kind: "text", summary: "已保留已有结果。" },
].map(item => ({ ...item, worker: "auto", ts: timestamp }))

const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
try {
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1440, height: 1000 } })
  await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: origin }])
  await context.addInitScript(value => { localStorage.setItem("accessToken", value); localStorage.setItem("refreshToken", value) }, token)
  await context.route("**/v1/**", async route => {
    const request = route.request(), url = new URL(request.url()), path = url.pathname
    if (path === "/v1/upgradeOperations:active") return route.fulfill({ status: 404, json: {} })
    let data = { results: [], totalSize: 0 }
    if (path === "/v1/users/current") data = { id: 7, username: "fixture", isSuperuser: true }
    if (path === "/v1/aiAgents") data = { agents: [{ key: "auto", name: "测试 Agent", role: "assistant", enabled: true }] }
    if (path === "/v1/aiModels") data = { models: [] }
    if (path === "/v1/aiConversations") data = { conversations: [{ id: conversationId, title: "用户消息测试", agentKey: "auto", pinned: false, running: false, createdAt: timestamp, updatedAt: timestamp }] }
    if (path === `/v1/aiConversations/${conversationId}/side-questions`) data = { items: [], current: null, next_cursor: 0, snapshot: { available: false, reason: "fixture" } }
    if (path === `/v1/aiConversations/${conversationId}/messages`) {
      if (request.method() === "POST") {
        const { message } = request.postDataJSON()
        const seq = messages.at(-1).seq + 1
        messages.push({ seq, worker: "auto", ts: timestamp, kind: "user", summary: message, detail: message }, { seq: seq + 1, worker: "auto", ts: timestamp, kind: "text", summary: "测试回复" })
        data = { runId: "fixture-run" }
      } else {
        const since = Number(url.searchParams.get("since") || "0")
        data = { items: messages.filter(item => item.seq > since).map(item => ({ ...item, detail: undefined })), cursor: messages.at(-1).seq, running: false, hasMore: false }
      }
    }
    const match = path.match(new RegExp(`^/v1/aiConversations/${conversationId}/messages/(\\d+)$`))
    if (match) {
      const item = messages.find(item => item.seq === Number(match[1]))
      data = { detail: item?.detail || item?.summary || "" }
    }
    return route.fulfill({ json: data })
  })
  const page = await context.newPage(), errors = []
  page.on("pageerror", error => errors.push(String(error)))
  const checkWithinChat = async bubble => {
    await expect(bubble).toBeVisible()
    const geometry = await bubble.evaluate(node => {
      const viewport = node.closest('[data-slot="scroll-area-viewport"]')
      if (!viewport) return null
      const box = node.getBoundingClientRect(), area = viewport.getBoundingClientRect()
      return { left: box.left, right: box.right, top: box.top, bottom: box.bottom, viewportLeft: area.left, viewportRight: area.right, viewportTop: area.top, viewportBottom: area.bottom, scrollWidth: viewport.scrollWidth, clientWidth: viewport.clientWidth, screenWidth: window.innerWidth }
    })
    expect(geometry).not.toBeNull()
    expect(geometry.left).toBeGreaterThanOrEqual(geometry.viewportLeft)
    expect(geometry.right).toBeLessThanOrEqual(geometry.viewportRight + 1)
    expect(geometry.right).toBeGreaterThan(geometry.viewportLeft + (geometry.viewportRight - geometry.viewportLeft) / 2)
    expect(geometry.bottom).toBeGreaterThan(geometry.viewportTop)
    expect(geometry.top).toBeLessThan(geometry.viewportBottom)
    expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.clientWidth + 1)
    expect(geometry.viewportRight).toBeLessThanOrEqual(geometry.screenWidth + 1)
  }
  await page.goto(`${origin}/ai/chat/?c=${conversationId}`, { waitUntil: "domcontentloaded" })
  const bubbles = page.getByRole("group", { name: "用户消息", exact: true })
  await expect(bubbles).toHaveCount(2)
  const first = bubbles.filter({ hasText: "请检查这些本地测试记录。" })
  await first.scrollIntoViewIfNeeded()
  await expect(first).toContainText("用户消息末尾标记")
  await checkWithinChat(first)
  const latest = bubbles.filter({ hasText: followup })
  await latest.scrollIntoViewIfNeeded()
  await checkWithinChat(latest)

  const message = "新的用户输入，应在右侧显示。"
  await page.getByRole("textbox", { name: "消息，输入 @ 引用记录" }).fill(message)
  await page.getByRole("button", { name: "发送消息", exact: true }).click()
  const sent = bubbles.filter({ hasText: message })
  await expect(sent).toHaveCount(1)
  await checkWithinChat(sent)
  await page.reload({ waitUntil: "domcontentloaded" })
  await expect(bubbles).toHaveCount(3)
  await checkWithinChat(bubbles.filter({ hasText: message }))
  await page.setViewportSize({ width: 390, height: 844 })
  await bubbles.filter({ hasText: message }).scrollIntoViewIfNeeded()
  await checkWithinChat(bubbles.filter({ hasText: message }))
  expect(errors).toEqual([])
  console.log("chat user messages passed: right-aligned, bounded, full history, new send, reload, and mobile")
} finally {
  await browser.close()
}
