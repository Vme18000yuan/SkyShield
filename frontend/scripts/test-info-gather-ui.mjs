// UI fixture: all application APIs are intercepted, and no target is scanned.
import { chromium, expect } from "@playwright/test"

const origin = process.env.SKYSHIELD_TEST_ORIGIN || "http://127.0.0.1:33327"
const runId = "11111111-1111-4111-8111-111111111111"
const assetId = "a".repeat(64), observationId = "b".repeat(64), otherId = "c".repeat(64)
const token = "fixture." + Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString("base64url") + ".fixture"
const now = new Date().toISOString()
const syncBodies = []
let ignoredBody
let records = [
  { kind: "asset", id: assetId, status: "pending", reason: "", payload: { type: "domain", value: "app.fixture.example", scope: "in_scope", evidence: ["01-subdomains.txt"] } },
  { kind: "observation", id: observationId, status: "pending", reason: "", payload: { category: "PUBLIC_LISTING", value: "https://bucket.vendor.test/", scope: "unverified", evidence: ["recon.json"] } },
  { kind: "observation", id: otherId, status: "pending", reason: "", payload: { category: "OTHER", value: "fixture clue", scope: "unverified", evidence: ["recon.json"] } },
]
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
try {
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1440, height: 1000 } })
  await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: origin }])
  await context.addInitScript(({ value, appOrigin }) => {
    if (location.origin === appOrigin) { localStorage.setItem("accessToken", value); localStorage.setItem("refreshToken", value) }
  }, { value: token, appOrigin: origin })
  await context.route("**/api/github/repo", route => route.fulfill({ json: { stars: 0 } }))
  await context.route("https://all-in-ai.site/**", route => route.fulfill({
    status: 200,
    contentType: "text/html; charset=utf-8",
    body: "Token purchase fixture",
  }))
  await context.route("**/v1/**", async route => {
    const request = route.request(), path = new URL(request.url()).pathname
    if (path === "/v1/upgradeOperations:active") return route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND" } } })
    let data = { results: [], totalSize: 0, notifications: [], unreadCount: 0 }
    if (path === "/v1/users/current") data = { id: 7, username: "fixture", isSuperuser: true }
    if (path === "/v1/aiAgents") data = { agents: [{ key: "auto", name: "Auto", builtin: true, role: "assistant", enabled: true }, { key: "info_gatherer", name: "信息收集", builtin: false, role: "assistant", enabled: true }, { key: "credential_processor", name: "凭据整理", builtin: false, role: "assistant", enabled: true }] }
    if (path === "/v1/aiConversations") data = { conversations: [] }
    if (path === "/v1/aiLlmProfiles") data = { profiles: [] }
    if (path === "/v1/aiInfoGatherRuns") data = { runs: [{ id: runId, target: "fixture.example", targetKind: "HOST", mode: "normal", status: "completed", error: "", coverage: { steps: { dns: "success", cloud: "skipped" } }, createdAt: now, updatedAt: now, imported: syncBodies.length, pending: records.filter(item => item.kind === "asset" || item.id === observationId).length, observations: records.some(item => item.id === otherId) ? 1 : 0 }] }
    if (path === `/v1/aiInfoGatherRuns/${runId}/records`) data = { records, total: records.length, offset: 0, pageSize: 100 }
    if (path === `/v1/aiInfoGatherRuns/${runId}/sync`) {
      const body = request.postDataJSON()
      syncBodies.push(body)
      if (body.targetId === 23) {
        records = records.filter(item => item.id !== assetId).map(item => item.id === observationId ? { ...item, reason: "风险地址不属于所选目标" } : item)
        data = { imported: 1, failed: 1, failures: [{ record: { kind: "observation", id: observationId }, reason: "风险地址不属于所选目标" }] }
      } else {
        records = records.filter(item => item.id !== observationId)
        data = { imported: 1, failed: 0, failures: [] }
      }
    }
    if (path === `/v1/aiInfoGatherRuns/${runId}/ignore`) { ignoredBody = request.postDataJSON(); records = records.filter(item => item.id !== otherId); data = { processed: 1 } }
    if (path === "/v1/targets") data = { results: [{ id: 23, name: "targets/23", displayName: "fixture.example", type: "domain", createdAt: now, organizations: [{ id: 11, name: "organizations/11", displayName: "测试组织" }] }, { id: 24, name: "targets/24", displayName: "bucket.vendor.test", type: "domain", createdAt: now, organizations: [] }], totalSize: 2, pageSize: 20 }
    return route.fulfill({ json: data })
  })
  const page = await context.newPage(), errors = []
  page.on("pageerror", error => errors.push(String(error)))
  await page.goto(`${origin}/ai/info-gather/`, { waitUntil: "domcontentloaded", timeout: 120000 })
  await expect(page.getByRole("heading", { name: "信息收集", exact: true })).toBeVisible({ timeout: 120000 })
  const tokenPurchase = page.getByRole("link", { name: "Token购买", exact: true })
  await expect(tokenPurchase).toHaveAttribute("href", "https://all-in-ai.site")
  await expect(tokenPurchase).toHaveAttribute("target", "_blank")
  const [purchaseTab] = await Promise.all([page.waitForEvent("popup"), tokenPurchase.click()])
  await purchaseTab.waitForLoadState("domcontentloaded")
  expect(purchaseTab.url()).toBe("https://all-in-ai.site/")
  await purchaseTab.close()
  await page.getByRole("button", { name: "关于系统" }).click()
  const about = page.getByRole("dialog")
  const aboutLink = about.getByRole("link", { name: "Token购买" })
  await expect(aboutLink).toHaveAttribute("href", "https://all-in-ai.site")
  await expect(aboutLink).toHaveAttribute("target", "_blank")
  const [aboutTab] = await Promise.all([page.waitForEvent("popup"), aboutLink.click()])
  await aboutTab.waitForLoadState("domcontentloaded")
  expect(aboutTab.url()).toBe("https://all-in-ai.site/")
  await aboutTab.close()
  await page.keyboard.press("Escape")
  const legacyPage = await context.newPage()
  await legacyPage.goto(`${origin}/token-purchase/`, { waitUntil: "domcontentloaded" })
  await expect(legacyPage).toHaveURL("https://all-in-ai.site/")
  await legacyPage.close()
  await expect(page.getByRole("checkbox", { name: /app.fixture.example/ })).toBeVisible()
  await expect(page.getByRole("button", { name: "查看 fixture.example 的待处理记录" })).toBeVisible()
  await expect(page.getByText("归属待核实").first()).toBeVisible()
  await expect(page.getByRole("checkbox", { name: /bucket.vendor.test/ })).toBeEnabled()
  await page.getByRole("checkbox", { name: "选择本页全部记录" }).click()
  await page.getByRole("checkbox", { name: /fixture clue/ }).click()
  await page.getByLabel("选择归属目标").click()
  await page.getByRole("option", { name: "fixture.example (测试组织)" }).click()
  await expect(page.getByLabel("选择归属目标")).toContainText("fixture.example")
  await page.getByRole("button", { name: "同步所选记录" }).click()
  await expect.poll(() => syncBodies[0]?.targetId).toBe(23)
  expect(syncBodies[0].records).toEqual([{ kind: "asset", id: assetId }, { kind: "observation", id: observationId }])
  expect(syncBodies[0].allowCdn).toBe(false)
  await expect(page.getByRole("checkbox", { name: /app.fixture.example/ })).toHaveCount(0)
  await expect(page.getByText("风险地址不属于所选目标")).toBeVisible()
  await page.getByRole("checkbox", { name: /bucket.vendor.test/ }).click()
  await page.getByLabel("选择归属目标").click()
  await page.getByRole("option", { name: "bucket.vendor.test" }).click()
  await page.getByRole("button", { name: "同步所选记录" }).click()
  await expect.poll(() => syncBodies[1]?.targetId).toBe(24)
  expect(syncBodies[1].records).toEqual([{ kind: "observation", id: observationId }])
  await expect(page.getByRole("checkbox", { name: /bucket.vendor.test/ })).toHaveCount(0)
  await page.getByRole("checkbox", { name: /fixture clue/ }).click()
  await page.getByRole("button", { name: "标记所选为已处理" }).click()
  await expect.poll(() => ignoredBody?.records?.[0]?.id).toBe(otherId)
  await expect(page.getByRole("checkbox", { name: /fixture clue/ })).toHaveCount(0)
  await page.getByRole("link", { name: "前往 AI 对话发起采集" }).click()
  const agentPicker = page.getByRole("combobox").filter({ hasText: "信息收集" })
  await expect(agentPicker).toBeVisible({ timeout: 120000 })
  await agentPicker.click()
  await expect(page.getByRole("option", { name: /信息收集/ })).toBeVisible()
  await expect(page.getByRole("option", { name: /凭据整理/ })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await page.getByRole("combobox", { name: "按 Agent 筛选对话" }).click()
  await expect(page.getByRole("option", { name: /凭据整理/ })).toHaveCount(0)
  await page.keyboard.press("Escape")
  expect(errors).toEqual([])
  console.log(JSON.stringify({ passed: true, tokenPurchaseNewTabs: true, legacyRedirect: true, targetNames: true, pendingReview: true, manualSync: true, backgroundAgentHidden: true }))
} finally { await browser.close() }
