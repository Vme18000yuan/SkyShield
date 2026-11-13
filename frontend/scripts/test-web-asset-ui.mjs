// Isolated browser fixture; no production asset data is modified.
import { chromium } from "@playwright/test"
import assert from "node:assert/strict"
import fs from "node:fs/promises"

const origin = process.env.WEB_ASSET_TEST_ORIGIN || "http://127.0.0.1:33221"
const output = process.env.ASSET_INVENTORY_TEST_OUTPUT || "/tmp/skyshield-inventory-ui"
await fs.mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined })
const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" })
const token = `fixture.${Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 7200 })).toString("base64url")}.fixture`
await context.addInitScript(({ token }) => { try { localStorage.setItem("accessToken", token); localStorage.setItem("refreshToken", "fixture-refresh") } catch {} }, { token })
await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: origin }])
const now = new Date().toISOString()
const orgs = [{ id: 1, name: "organizations/1", displayName: "组织 A", description: "", createdAt: now }, { id: 2, name: "organizations/2", displayName: "组织 B", description: "", createdAt: now }]
const entities = [
  { id: "10", assetType: "root_domain", label: "example.com", derived: true, org: 1 },
  { id: "11", assetType: "service", displayType: "web", label: "HTTP://site.example.com/", org: 1 },
  { id: "12", assetType: "endpoint", displayType: "web", label: "https://site.example.com/api", org: 1 },
  { id: "13", assetType: "service", displayType: "service", label: "ssh.example.com:22", org: 1 },
  { id: "20", assetType: "app", label: "示例业务平台", applicationSources: 2, org: 1 },
  ...Array.from({ length: 55 }, (_, i) => ({ id: String(100+i), assetType: "subdomain", label: `host${i}.example.com`, org: i === 54 ? 2 : 1 })),
].map(item => ({ ...item, name: `assetEntities/${item.id}`, method: null, sourceCount: 1, firstSeenAt: now, lastSeenAt: now, organizations: [{ id: item.org, name: item.org === 1 ? "组织 A" : "组织 B" }] }))
const portFixtures = [
  { label: "ssh.example.com:22", port: 22, service: "SSH", serviceSource: "port" },
  { label: "mqtt.example.com:1883", port: 1883, service: "MQTT", serviceSource: "observed" },
  { label: "192.0.2.10:3306", port: 3306, service: "MYSQL", serviceSource: "port" },
  ...Array.from({ length: 120 }, (_, i) => ({ label: `node${i}.example.com:443`, port: 443, service: "HTTPS", serviceSource: "port" })),
].map(item => ({ ...item, firstSeenAt: now, lastSeenAt: now, organizations: [{ id: 1, name: "组织 A" }] }))
let registration = null
const queries = []
await context.route("**/api/github/repo", route => route.fulfill({ json: { stars: 0, forks: 0 } }))
await context.route("**/v1/**", async route => {
  const request = route.request(), url = new URL(request.url())
  if (url.pathname === "/v1/users/current") return route.fulfill({ json: { id: 1, username: "fixture", isSuperuser: true } })
  if (url.pathname === "/v1/upgradeOperations:active") return route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND" } } })
  if (url.pathname === "/v1/organizations") return route.fulfill({ json: { results: orgs, totalSize: 2, nextPageToken: "" } })
  if (/^\/v1\/organizations\/\d+$/.test(url.pathname)) return route.fulfill({ json: orgs.find(item => String(item.id) === url.pathname.split("/").pop()) })
  if (url.pathname === "/v1/assetEntities:batchCreate") {
    registration = request.postDataJSON()
    return route.fulfill({ status: 201, json: { requestId: registration.requestId, results: [{ index: 0, id: "9", type: registration.assets[0].type, projection: "extended" }], errors: [] } })
  }
  if (url.pathname === "/v1/assetEntities/10/rootDomainOverview") return route.fulfill({ json: { id: "10", domain: "example.com", organizations: [{ id: 1, name: "组织 A" }], firstSeenAt: now, lastSeenAt: now, counts: { subdomains: 54, ips: 1, apps: 1, ports: 123, web: 2, urls: 1 } } })
  if (url.pathname === "/v1/assetEntities/20/applicationOverview") return route.fulfill({ json: { application: entities.find(item => item.id === "20"), websites: [{ id: 1, url: "https://site.example.com/", host: "site.example.com", title: "示例业务平台", statusCode: 200, technologies: [], evidenceKind: "title", evidenceValue: "示例业务平台", firstSeenAt: now }], totalSize: 1, page: 1 } })
  if (url.pathname === "/v1/assetInventory") {
    const org = url.searchParams.get("organizationId"), root = url.searchParams.get("rootDomainId"), page = Number(url.searchParams.get("page") || "1"), size = Number(url.searchParams.get("pageSize") || "50")
    queries.push({ inventory: true, org, root, page })
    const assets = entities.filter(item => (!org || String(item.org) === org) && (!root || item.assetType !== "root_domain") && (item.assetType !== "service" || item.displayType === "web")).map(asset => ({ kind: "asset", asset }))
    const ports = (org === "2" ? [] : portFixtures).map(port => ({ kind: "port", port }))
    const items = [...assets, ...ports]
    return route.fulfill({ json: { items: items.slice((page-1)*size,page*size), totalSize: items.length, page } })
  }
  if (url.pathname === "/v1/assetEntities") {
    const category = url.searchParams.get("assetCategory"), type = url.searchParams.get("assetType"), org = url.searchParams.get("organizationId"), root = url.searchParams.get("rootDomainId"), page = Number(url.searchParams.get("page") || "1"), size = Number(url.searchParams.get("pageSize") || "50")
    queries.push({ category, type, org, root, page })
    const items = entities.filter(item => (!org || String(item.org) === org) && (!root || item.assetType !== "root_domain") && (category === "web" ? item.displayType === "web" : type ? item.assetType === type : true))
    return route.fulfill({ json: { assetEntities: items.slice((page-1)*size,page*size), totalSize: items.length, page, nextPageToken: "" } })
  }
  if (url.pathname === "/v1/assetPorts") {
    const org = url.searchParams.get("organizationId"), root = url.searchParams.get("rootDomainId"), page = Number(url.searchParams.get("page") || "1"), size = Number(url.searchParams.get("pageSize") || "50")
    queries.push({ ports: true, org, root, page })
    const items = org === "2" ? [] : portFixtures
    return route.fulfill({ json: { ports: items.slice((page-1)*size,page*size), totalSize: items.length, page } })
  }
  return route.fulfill({ json: { results: [], targets: [], notifications: [], totalSize: 0, unreadCount: 0, nextPageToken: "" } })
})
const page = await context.newPage()
const errors = []
page.on("pageerror", error => errors.push(String(error)))
try {
  await page.goto(`${origin}/assets/`, { waitUntil: "domcontentloaded", timeout: 120000 })
  const site = page.getByRole("row").filter({ hasText: "HTTP://site.example.com/" })
  await site.waitFor({ timeout: 120000 })
  assert.equal((await site.getByRole("cell").nth(1).innerText()).trim(), "Web")
  assert.equal(await page.getByText("推导", { exact: true }).count(), 0)
  assert.equal(await page.getByRole("columnheader", { name: "方法", exact: true }).count(), 0)
  assert.equal(await page.getByRole("columnheader", { name: "来源数", exact: true }).count(), 0)
  await page.screenshot({ path: `${output}/inventory.png` })
  await page.getByRole("button", { name: "按组织筛选" }).click()
  await page.getByRole("button", { name: "组织 A", exact: true }).click()
  await page.waitForFunction(() => document.body.innerText.includes("组织 A"))
  assert.ok(queries.some(query => query.org === "1"))
  await page.getByRole("tab", { name: "Web", exact: true }).click()
  await page.getByText("https://site.example.com/api").waitFor()
  await page.getByRole("button", { name: "添加资产" }).click()
  await page.getByLabel("URL", { exact: true }).fill("https://manual.example.com/path")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.waitForFunction(() => !document.querySelector('[role="dialog"]'))
  assert.equal(registration.assets[0].organizationId, 1)
  assert.equal(registration.assets[0].type, "service")
  await page.getByRole("tab", { name: "端口", exact: true }).click()
  await page.getByText("ssh.example.com:22").waitFor()
  assert.equal(await page.getByText("SSH", { exact: true }).getAttribute("title"), "根据常见端口推测，实际服务可能不同")
  assert.ok(await page.getByText("MQTT", { exact: true }).count())
  assert.ok(await page.getByText("MYSQL", { exact: true }).count())
  await page.screenshot({ path: `${output}/ports.png` })
  await page.getByText("共 123 条 · 第 1 / 3 页").waitFor()
  await page.getByRole("spinbutton", { name: "跳转页码" }).fill("3")
  await page.getByRole("button", { name: "跳转", exact: true }).click()
  await page.getByText("共 123 条 · 第 3 / 3 页").waitFor()
  assert.ok(queries.some(query => query.ports && query.page === 3 && query.org === "1"))
  await page.getByRole("tab", { name: "根域名", exact: true }).click()
  const root = page.getByRole("link", { name: "example.com", exact: true })
  await root.waitFor()
  assert.match(await root.getAttribute("href"), /organizationId=1/)
  await root.click()
  await page.getByRole("heading", { name: "example.com", exact: true }).waitFor()
  await page.getByRole("heading", { name: "关联资产", exact: true }).waitFor()
  await page.screenshot({ path: `${output}/root.png` })
  await page.getByRole("tab", { name: "端口", exact: true }).click()
  await page.getByText("mqtt.example.com:1883").waitFor()
  assert.ok(queries.some(query => query.ports && query.root === "10" && query.org === "1"))
  await page.getByRole("link", { name: "返回资产", exact: true }).click()
  await page.waitForURL("**/assets/?organizationId=1")
  await page.waitForFunction(() => document.querySelector('[aria-label="按组织筛选"]')?.textContent?.includes("组织 A"))
  await page.getByRole("tab", { name: "应用", exact: true }).click()
  const app = page.getByRole("link", { name: "示例业务平台", exact: true })
  await app.waitFor()
  assert.ok(await page.getByText("站点识别", { exact: false }).count())
  await page.getByRole("button", { name: "添加资产" }).click()
  await page.getByLabel("应用名称", { exact: true }).fill("手动应用")
  await page.getByLabel("关联域名（每行一个）", { exact: true }).fill("site.example.com")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.waitForFunction(() => !document.querySelector('[role="dialog"]'))
  assert.equal(registration.assets[0].type, "app")
  assert.deepEqual(registration.assets[0].boundDomains, ["site.example.com"])
  await app.click()
  await page.getByRole("heading", { name: "示例业务平台", exact: true }).waitFor()
  await page.getByRole("heading", { name: "关联站点", exact: true }).waitFor()
  await page.getByRole("link", { name: "https://site.example.com/" }).waitFor()
  assert.ok(await page.getByText("站点标题", { exact: true }).count())
  await page.screenshot({ path: `${output}/application.png` })
  await page.setViewportSize({ width: 390, height: 844 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "mobile asset inventory should not overflow the page")
  assert.equal(errors.length, 0, errors.join("\n"))
  console.log(JSON.stringify({ passed: true, queryCount: queries.length }))
} finally { await browser.close() }
