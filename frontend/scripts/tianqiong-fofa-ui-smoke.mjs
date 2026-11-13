// Production-frontend browser smoke with local HTTP fixtures. No real FOFA
// account, discovered host, or business database is contacted.
import { chromium, expect } from "@playwright/test"
import { strict as assert } from "node:assert"
import { fileURLToPath } from "node:url"

const base = process.env.TIANQIONG_UI_SMOKE_URL || "http://127.0.0.1:39018"
const token = `e30.${Buffer.from(JSON.stringify({ exp: 4102444800 })).toString("base64url")}.fixture`
const stamp = "2026-10-30T00:00:00Z"
let configured = false
let saved = null
let imported = null
let searches = 0
let accountTests = 0
let releaseSettings
const settingsGate = new Promise(resolve => { releaseSettings = resolve })
let initialSettings = true
const settings = () => ({ name: "fofaSettings/current", email: configured ? "ops@fixture.example" : "", configured, canManage: true, canSearch: true, updatedAt: configured ? stamp : null })
const records = [
  { rowId: 0, host: "https://api.fixture.example/Case%2FPath", url: "https://api.fixture.example/Case%2FPath", ip: "192.0.2.80", port: 443, protocol: "https", hostname: "api.fixture.example", target: "api.fixture.example", domain: "fixture.example", title: "FOFA 浏览器验收站点", server: "nginx", country: "CN", countryName: "中国", region: "上海", city: "上海", icp: "", baseProtocol: "tcp", importable: true, importError: "" },
  { rowId: 1, host: "192.0.2.81:22", url: "", ip: "192.0.2.81", port: 22, protocol: "ssh", hostname: "192.0.2.81", target: "192.0.2.81", domain: "", title: "", server: "OpenSSH", country: "", countryName: "", region: "", city: "", icp: "", baseProtocol: "tcp", importable: true, importError: "" },
]

const browser = await chromium.launch({ headless: true, ...(process.env.TIANQIONG_CHROMIUM ? { executablePath: process.env.TIANQIONG_CHROMIUM } : {}) })
let page
const pageErrors = []
try {
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1440, height: 1000 } })
  await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: base }])
  await context.addInitScript(access => { localStorage.setItem("accessToken", access); localStorage.setItem("refreshToken", "fixture") }, token)
  await context.route("**/api/github/repo", route => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ stargazersCount: 0, openIssuesCount: 0 }) }))
  await context.route("**/v1/**", async route => {
    const path = new URL(route.request().url()).pathname
    const method = route.request().method()
    let status = 200
    let body = {}
    if (path === "/v1/users/current") body = { id: 9, username: "fofa-fixture", email: "ops@fixture.example", isSuperuser: true }
    else if (path === "/v1/upgradeOperations:active") { status = 404; body = { error: { code: "NOT_FOUND", message: "none" } } }
    else if (path === "/v1/users/current/notifications") body = { results: [], nextPageToken: "" }
    else if (path === "/v1/users/current/notifications:unreadCount") body = { unreadCount: 0 }
    else if (path === "/v1/fofaSettings/current" && method === "GET") {
      if (initialSettings) { initialSettings = false; await settingsGate }
      body = settings()
    }
    else if (path === "/v1/fofaSettings/current" && method === "PUT") { saved = route.request().postDataJSON(); configured = true; body = settings() }
    else if (path === "/v1/fofaSettings/current/test") { accountTests++; body = { valid: true } }
    else if (path === "/v1/fofaSearches" && method === "POST") {
      searches++
      const input = route.request().postDataJSON()
      assert.equal(input.query, 'domain="fixture.example"')
      status = 201
      body = { ...input, id: "browser-snapshot", name: "fofaSearches/browser-snapshot", total: 2, results: records, hasNext: false, createdAt: stamp, expiresAt: "2030-01-01T00:00:00Z" }
    }
    else if (path === "/v1/fofaSearches/browser-snapshot/import") {
      imported = route.request().postDataJSON()
      body = { importedRows: 2, failedRows: 0, assetCount: 5, results: [{ rowId: 0, targetId: 11, assetIds: ["1", "2", "3"], warnings: [], reused: false }, { rowId: 1, targetId: 12, assetIds: ["4", "5"], warnings: [], reused: false }], errors: [] }
    }
    else if (path === "/v1/organizations") body = { results: [{ id: 7, name: "organizations/7", displayName: "浏览器测试组织", description: "fixture", createdAt: stamp }], totalSize: 1, nextPageToken: "" }
    else if (path === "/v1/targets") body = { results: [{ id: 11, name: "targets/11", displayName: "fixture.example", type: "domain", createdAt: stamp }], totalSize: 1, nextPageToken: "" }
    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  })
  page = await context.newPage()
  page.setDefaultTimeout(15000)
  page.on("pageerror", error => pageErrors.push(error.message))
  await page.goto(`${base}/tools/space-search/`)
  await page.getByRole("heading", { name: "网络空间搜索", exact: true }).waitFor()
  const initialQuery = page.locator("#fofa-query").first()
  await initialQuery.waitFor({ state: "visible" })
  const loadingRect = await initialQuery.evaluate(element => {
    const r = element.closest("form").getBoundingClientRect()
    return { top: r.top, left: r.left, width: r.width, height: r.height }
  })
  releaseSettings()
  await page.getByText("FOFA 未配置", { exact: true }).waitFor()
  await expect(page.locator("#fofa-query")).toHaveCount(1)
  const query = page.getByRole("textbox", { name: "FOFA 查询语句", exact: true })
  const resolvedRect = await query.evaluate(element => {
    const r = element.closest("form").getBoundingClientRect()
    return { top: r.top, left: r.left, width: r.width, height: r.height }
  })
  for (const key of ["top", "left", "width", "height"]) assert.ok(Math.abs(loadingRect[key] - resolvedRect[key]) <= 2, `FOFA initial ${key} shifted`)
  assert.equal(searches, 0)
  await page.getByRole("button", { name: "FOFA 配置", exact: true }).click()
  await page.locator("#fofa-email").fill("ops@fixture.example")
  await page.locator("#fofa-api-key").fill("browser-fixture-key")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.getByText("FOFA 已配置", { exact: true }).waitFor()
  assert.equal(saved?.email, "ops@fixture.example")
  assert.equal(saved?.apiKey, "browser-fixture-key")
  await page.getByRole("button", { name: "FOFA 配置", exact: true }).click()
  await expect(page.locator("#fofa-api-key")).toHaveValue("")
  await page.getByRole("button", { name: "测试已保存配置", exact: true }).click()
  await expect.poll(() => accountTests).toBe(1)
  await page.keyboard.press("Escape")
  await expect(page.locator("#fofa-api-key")).not.toBeVisible()
  await query.fill('domain="fixture.example"')
  assert.equal(searches, 0)
  await page.getByRole("button", { name: "搜索", exact: true }).click()
  await page.getByText("FOFA 浏览器验收站点", { exact: true }).waitFor()
  await page.screenshot({ path: fileURLToPath(new URL("../../.local/logs/fofa-desktop.png", import.meta.url)), fullPage: true, animations: "disabled", style: "[data-sonner-toaster] { visibility: hidden !important; }" })
  await page.getByRole("checkbox", { name: "全选当前页可同步的结果", exact: true }).click()
  await page.getByRole("button", { name: "同步选中（2）", exact: true }).click()
  await expect(page.getByRole("button", { name: "确认同步", exact: true })).toBeDisabled()
  await page.locator("#fofa-organization").click()
  await page.getByRole("option", { name: "浏览器测试组织", exact: true }).click()
  await expect(page.locator("#fofa-organization")).toHaveText("浏览器测试组织")
  await expect(page.getByRole("button", { name: "确认同步", exact: true })).toBeEnabled()
  await page.locator("#fofa-target").click()
  await page.getByRole("option", { name: "fixture.example", exact: true }).click()
  await expect(page.locator("#fofa-target")).toHaveText("fixture.example")
  await page.locator("#fofa-target").click()
  await page.getByRole("option", { name: "按根域名自动匹配或新建目标", exact: true }).click()
  await expect(page.locator("#fofa-target")).toHaveText("按根域名自动匹配或新建目标")
  await page.getByRole("button", { name: "确认同步", exact: true }).click()
  await page.getByText("同步成功 2 条，登记 5 项资产，失败 0 条。", { exact: true }).waitFor()
  await expect(page.locator('[data-slot="dialog-content"]')).toHaveCount(0)
  assert.deepEqual(imported?.rows, [0, 1])
  assert.equal(imported?.organizationId, 7)
  assert.equal(imported?.targetId, undefined)
  assert.equal(searches, 1)
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole("heading", { name: "网络空间搜索", exact: true })).toBeVisible()
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, "FOFA mobile page overflowed")
  await page.screenshot({ path: fileURLToPath(new URL("../../.local/logs/fofa-mobile.png", import.meta.url)), fullPage: true, animations: "disabled", style: "[data-sonner-toaster] { visibility: hidden !important; }" })
  assert.deepEqual(pageErrors, [])
  console.log("FOFA 配置、连接测试、查询、组织选择、同步与移动布局浏览器验收通过（HTTP fixture）。")
} catch (error) {
  if (page) console.error("FOFA 页面验收失败:", page.url(), (await page.locator("body").innerText()).slice(0, 2400))
  throw error
} finally { releaseSettings(); await browser.close() }
