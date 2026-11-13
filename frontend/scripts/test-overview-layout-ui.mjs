// Isolated overview fixture; all product APIs use local synthetic responses.
import { chromium, expect } from "@playwright/test"

const origin = process.env.SKYSHIELD_TEST_ORIGIN || "http://127.0.0.1:33327"
const token = "fixture." + Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString("base64url") + ".fixture"
const now = new Date().toISOString()
const statistics = {
  totalTargets: 4, totalSubdomains: 15, totalIps: 6, totalServices: 9, totalEndpoints: 0, totalWebsites: 0,
  totalVulns: 3, totalAssets: 30, runningScans: 1, updatedAt: now,
  changeTargets: 1, changeSubdomains: 1, changeIps: 0, changeServices: 0, changeEndpoints: 0,
  changeWebsites: 0, changeVulns: 1, changeAssets: 1,
  vulnBySeverity: { critical: 1, high: 1, medium: 1, low: 0, info: 0 },
}
const history = Array.from({ length: 7 }, (_, index) => ({
  date: `2026-11-0${index + 1}`, totalTargets: 4, totalSubdomains: 9 + index,
  totalIps: 6, totalServices: 9, totalEndpoints: 0, totalWebsites: 0, totalVulns: 3, totalAssets: 24 + index,
}))
const database = { status: "online", observedAt: now, role: "primary", version: "fixture", readOnly: false, uptimeSeconds: 60,
  databaseSizeBytes: 1, coreSignals: { probeLatencyMs: 4, connectionsUsed: 3, connectionsMax: 50, connectionUsagePercent: 6,
    lockWaitCount: 0, deadlocks1h: 0, longTransactionCount: 0, oldestPendingTaskAgeSec: 0 },
  optionalSignals: { qps: null, walGeneratedMb24h: null, cacheHitRate: null }, unavailableSignals: [], findings: [], alerts: [] }
const runtime = { scope: "host", latest: { cpu: 20, memory: 40, disk: 30, updatedAt: now }, series: [],
  capacity: { cpuCores: 4, memoryTotalGb: 8, diskTotalGb: 100 }, source: {}, sampleIntervalSeconds: 2, retentionSeconds: 60 }
const scanStatistics = { total: 1, pending: 0, running: 1, succeeded: 0, failed: 0, cancelled: 0, totalVulns: 0,
  totalSubdomains: 0, totalEndpoints: 0, totalWebsites: 0, totalAssets: 0,
  retentionPolicy: { minimumRetentionSeconds: 0, automaticCleanupEnabled: false } }
const usage = { calls: 0, tasks: 0, input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 }

const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
try {
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1600, height: 1100 } })
  await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: origin }])
  await context.addInitScript(({ value, appOrigin }) => {
    if (location.origin === appOrigin) { localStorage.setItem("accessToken", value); localStorage.setItem("refreshToken", value) }
  }, { value: token, appOrigin: origin })
  await context.route("**/api/github/repo", route => route.fulfill({ json: { stars: 0 } }))
  await context.route("**/v1/**", route => {
    const path = new URL(route.request().url()).pathname
    if (path === "/v1/upgradeOperations:active") return route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND" } } })
    let data = { results: [], totalSize: 0, notifications: [], unreadCount: 0 }
    if (path === "/v1/users/current") data = { id: 7, username: "fixture", isSuperuser: true }
    if (path === "/v1/assetStatistics") data = statistics
    if (path === "/v1/assetStatistics/history") data = history
    if (path === "/v1/databaseHealthReports/current") data = database
    if (path === "/v1/admin/system/runtimeMetrics/current") data = runtime
    if (path === "/v1/vulnerabilityStatistics") data = { total: 3, pendingCount: 2, reviewedCount: 1 }
    if (path === "/v1/scanStatistics") data = scanStatistics
    if (path === "/v1/scans") data = { results: [], totalSize: 0, page: 1, pageSize: 6 }
    if (path === "/v1/aiModelUsage") data = { totals: usage, profiles: [], daily: [], days: 30 }
    if (path === "/v1/aiModels") data = { models: [] }
    return route.fulfill({ json: data })
  })
  const page = await context.newPage(), errors = []
  page.on("pageerror", error => errors.push(String(error)))
  await page.goto(`${origin}/overview/`, { waitUntil: "domcontentloaded", timeout: 120000 })
  const risk = page.getByText("风险摘要", { exact: true }).filter({ visible: true })
  const assets = page.getByText("资产总量", { exact: true }).filter({ visible: true })
  const targets = page.getByText("监控目标", { exact: true }).filter({ visible: true })
  const tokens = page.getByText("LLM Token 消耗", { exact: true }).filter({ visible: true })
  const scans = page.getByRole("heading", { name: "最近扫描" })
  const db = page.locator('[data-runtime-card="database"]')
  const resources = page.locator('[data-runtime-card="resources"]')
  const trend = page.getByRole("heading", { name: "近 7 天资产变化" })
  for (const item of [risk, assets, targets, tokens, scans, db, resources, trend]) await expect(item).toBeVisible({ timeout: 30000 })
  const rects = async () => {
    const result = {}
    for (const [name, item] of Object.entries({ risk, assets, targets, tokens, scans, db, resources, trend })) {
      result[name] = await item.evaluate(element => { const r = element.getBoundingClientRect(); return { x: r.x, y: r.y, right: r.right, bottom: r.bottom } })
    }
    return result
  }
  const desktop = await rects()
  expect(desktop.assets.x).toBeGreaterThan(desktop.risk.x)
  expect(Math.abs(desktop.assets.y - desktop.risk.y)).toBeLessThan(90)
  expect(desktop.targets.x).toBeGreaterThan(desktop.assets.x)
  expect(desktop.db.y).toBeGreaterThan(desktop.assets.y)
  expect(desktop.db.x).toBeLessThan(desktop.assets.x)
  expect(desktop.resources.x).toBeGreaterThan(desktop.db.x)
  expect(desktop.resources.x).toBeLessThan(desktop.trend.x)
  expect(desktop.tokens.y).toBeGreaterThan(desktop.risk.y)
  expect(desktop.db.y).toBeGreaterThan(desktop.tokens.y)
  await page.screenshot({ path: "/tmp/skyshield-overview-rearranged-desktop.png", fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  const mobile = await rects()
  for (const [first, second] of [["risk", "assets"], ["assets", "tokens"], ["tokens", "scans"], ["scans", "db"], ["db", "resources"], ["resources", "trend"]]) {
    expect(mobile[second].y).toBeGreaterThan(mobile[first].y)
  }
  for (const slot of ["overview-runtime-region", "overview-operational-region", "overview-asset-region"]) {
    const widths = await page.locator(`[data-loading-slot="${slot}"]`).evaluate(element => ({ visible: element.clientWidth, content: element.scrollWidth }))
    expect(widths.content).toBeLessThanOrEqual(widths.visible + 1)
  }
  await page.screenshot({ path: "/tmp/skyshield-overview-rearranged-mobile.png", fullPage: true })
  expect(errors).toEqual([])
  console.log(JSON.stringify({ passed: true, desktopPositions: true, mobileOrder: true }))
} finally { await browser.close() }
