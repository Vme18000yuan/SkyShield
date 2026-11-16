// Browser acceptance test. All records below are isolated synthetic fixtures.
// Run against an isolated `next dev` preview: MONITOR_TEST_ORIGIN=http://127.0.0.1:33220 node scripts/test-asset-monitor-ui.mjs
import { chromium, expect } from "@playwright/test"
import assert from "node:assert/strict"
import fs from "node:fs/promises"

const origin = process.env.MONITOR_TEST_ORIGIN || "http://127.0.0.1:33220"
const output = process.env.MONITOR_TEST_OUTPUT || "/tmp/skyshield-asset-monitor-ui"
await fs.mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined })
const context = await browser.newContext({ viewport: { width: 1920, height: 1080 }, locale: "zh-CN" })
const token = `fixture.${Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 7200 })).toString("base64url")}.fixture`
await context.addInitScript(({ token }) => { try { localStorage.setItem("accessToken", token); localStorage.setItem("refreshToken", "monitor-fixture-refresh") } catch {} }, { token })
await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: origin }])
await context.route("**/api/github/repo", route => route.fulfill({ json: { stars: 0, forks: 0 } }))
const organizations = [{ id: 1, name: "organizations/1", displayName: "云海科技集团", description: "集团总部及全国业务", targetCount: 96 }, { id: 2, name: "organizations/2", displayName: "华东业务中心", description: "华东区域业务资产", targetCount: 48 }]
const rawLocations = [["北京", 39.9, 116.4, 2800, "CN"], ["上海", 31.23, 121.47, 2100, "CN"], ["深圳", 22.55, 114.06, 1750, "CN"], ["东京", 35.68, 139.7, 650, "JP"], ["新加坡", 1.35, 103.82, 540, "SG"], ["法兰克福", 50.11, 8.68, 380, "DE"], ["加利福尼亚", 37.4, -122.1, 1260, "US"], ["纽约", 40.71427, -74.00597, 500, "US"], ["伦敦", 51.5, -.1, 220, "GB"], ["悉尼", -33.86, 151.2, 250, "AU"], ["圣保罗", -23.55, -46.6, 198, "BR"]]
const providerCities = ["Beijing", "Shanghai", "Shenzhen", "Tokyo", "Singapore", "Frankfurt am Main", "Mountain View", "", "London", "Sydney", "Sao Paulo"]
const locations = rawLocations.map(([label, latitude, longitude, count, countryCode], index) => ({ key: `fixture-${index}`, label, latitude, longitude, count, countryCode, cityName: providerCities[index], regionName: index === 2 ? "Guangdong" : "", precision: index === 1 ? "dns" : "ip", expired: false }))
const mapped = locations.reduce((sum, point) => sum + point.count, 0)
const fixture = {
  generatedAt: new Date().toISOString(), organizations: organizations.map((item, index) => ({ id: item.id, displayName: item.displayName, targetCount: item.targetCount, assetCount: index ? 4832 : 10896 })),
  summary: { totalAssets: 12648, targets: 128, ipAssets: 2440, risks: 86, newAssets24h: 146, newRisks24h: 7, runningScans: 6, pendingScans: 2, locatedAssets: mapped, unlocatedAssets: 12648 - mapped },
  assetTypes: [30, 900, 2440, 1730, 7890, 38].map((count, i) => ({ key: ["root_domain", "subdomain", "ip", "service", "endpoint", "app"][i], count })),
  assetCategories: [30, 700, 2440, 9620, 0, 38].map((count, i) => ({ key: ["root_domain", "subdomain", "ip", "web", "service", "app"][i], count })),
  inventoryCategories: [520, 2440, 9688].map((count, i) => ({ key: ["subdomain", "ip", "service"][i], count })),
  severities: [3, 12, 28, 31, 10, 2].map((count, i) => ({ key: ["critical", "high", "medium", "low", "info", "unknown"][i], count })),
  ports: [443, 80, 8080, 22, 3306, 8443, ...Array.from({ length: 39 }, (_, i) => 10000 + i)].map((key, i) => ({ key: String(key), count: i < 6 ? [1420, 896, 327, 186, 100, 50][i] : 45 - i })),
  technologies: ["Nginx", "Vue.js", "Spring Boot", "Apache", "React", ...Array.from({ length: 40 }, (_, i) => `Technology-${String(i + 1).padStart(2, "0")}`)].map((key, i) => ({ key, count: i < 5 ? [628, 382, 264, 219, 178][i] : 45 - i })),
  trend: [42, 78, 66, 124, 97, 186, 146].map((assets, i) => ({ date: new Date(Date.now() + 8 * 3600000 - (6 - i) * 86400000).toISOString().slice(0, 10), assets, risks: [2, 4, 3, 8, 5, 9, 7][i] })),
  recentAssets: ["api.yunhai.example:443", "portal.yunhai.example", "203.0.113.18", "sso.yunhai.example:443", "app.yunhai.example:8080", "cdn.yunhai.example", "auth.yunhai.example:443", "dev.yunhai.example", "203.0.113.19", "files.yunhai.example:443", "docs.yunhai.example:8080", "mail.yunhai.example"].map((label, i) => ({ id: String(i + 1), label, assetType: ["service", "subdomain", "ip", "service", "service", "subdomain"][i % 6], firstSeenAt: new Date(Date.now() - i * 180000).toISOString() })),
  locations,
}
let snapshotCalls = 0
let dataMode = "normal"
const recent50 = Array.from({ length: 50 }, (_, index) => ({ id: `recent-${index}`, label: `asset-${index + 1}.yunhai.example`, assetType: "subdomain", firstSeenAt: new Date(Date.now() - index * 180000).toISOString() }))
await context.route("**/v1/**", async route => {
  const url = new URL(route.request().url())
  if (url.pathname === "/v1/users/current") return route.fulfill({ json: { id: 1, username: "monitor-fixture", isSuperuser: true } })
  if (url.pathname === "/v1/upgradeOperations:active") return route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND" } } })
  if (url.pathname === "/v1/organizations") return route.fulfill({ json: { results: organizations, totalSize: organizations.length, nextPageToken: "" } })
  if (url.pathname === "/v1/assetMonitors/current") {
    snapshotCalls++
    assert.equal(url.searchParams.get("organizationIds"), "1,2")
    if (dataMode === "error") return route.fulfill({ status: 503, json: { error: { message: "Fixture refresh failure" } } })
    if (dataMode === "short") return route.fulfill({ json: { ...fixture, recentAssets: fixture.recentAssets.slice(0, 3), ports: fixture.ports.slice(0, 3), technologies: fixture.technologies.slice(0, 2) } })
    if (dataMode === "recent50") return route.fulfill({ json: { ...fixture, recentAssets: recent50 } })
    if (dataMode === "empty") return route.fulfill({ json: { ...fixture, summary: Object.fromEntries(Object.keys(fixture.summary).map(key => [key, 0])), locations: [], assetTypes: [], assetCategories: [], inventoryCategories: [], severities: [], ports: [], technologies: [], recentAssets: [], trend: fixture.trend.map(item => ({ ...item, assets: 0, risks: 0 })) } })
    return route.fulfill({ json: fixture })
  }
  return route.fulfill({ json: { results: [], notifications: [], totalSize: 0, unreadCount: 0 } })
})
const page = await context.newPage()
const errors = []
page.on("pageerror", error => errors.push(String(error)))
try {
  await page.goto(`${origin}/asset-monitor/`, { waitUntil: "networkidle", timeout: 120000 })
  assert.equal(await page.getByText("ASSET MONITOR", { exact: true }).count(), 0, "launcher should not show the explanatory panel")
  assert.equal(await page.getByRole("button", { name: "确定", exact: true }).isDisabled(), true)
  await page.getByRole("checkbox", { name: /云海科技集团/ }).check()
  await page.getByRole("checkbox", { name: /华东业务中心/ }).check()
  const themePicker = page.getByRole("group", { name: "选择大屏配色" })
  const open = page.getByRole("link", { name: "确定", exact: true })
  assert.match(await open.getAttribute("href"), /organizations=1,2/)
  assert.equal(await open.getAttribute("target"), "_blank")
  const colors = new Set()
  let screen
  for (const [name, id] of [["鎏金", "gold"], ["深海蓝", "ocean"], ["暮光紫", "violet"], ["青黛", "teal"], ["暗夜黑", "black"]]) {
    await themePicker.getByRole("button", { name, exact: true }).click()
    assert.equal(await themePicker.getByRole("button", { name, exact: true }).getAttribute("aria-pressed"), "true")
    assert.equal(await page.evaluate(() => localStorage.getItem("skyshield.asset-monitor.theme")), id)
    assert.equal(new URL(await open.getAttribute("href"), origin).searchParams.get("theme"), id)
    const popupPromise = page.waitForEvent("popup")
    await open.click()
    const popup = await popupPromise
    popup.on("pageerror", error => errors.push(String(error)))
    await popup.waitForURL("**/asset-monitor/screen/**", { timeout: 120000 })
    await popup.getByText("资产总量", { exact: true }).waitFor({ timeout: 120000 })
    const themeRoot = popup.locator(".monitor-root")
    assert.equal(await themeRoot.getAttribute("data-monitor-theme"), id)
    assert.equal(await popup.getByRole("group", { name: "选择大屏配色" }).count(), 0, "palette picker belongs only on the launcher")
    colors.add(await themeRoot.evaluate(node => getComputedStyle(node).getPropertyValue("--monitor-cyan").trim()))
    await popup.screenshot({ path: `${output}/screen-${id}.png`, fullPage: true })
    if (id === "black") screen = popup
    else await popup.close()
  }
  assert.equal(colors.size, 5, "each palette must have a distinct accent")
  await page.screenshot({ path: `${output}/launcher.png`, fullPage: true })
  await page.reload({ waitUntil: "domcontentloaded" })
  await page.waitForFunction(() => document.querySelector('[role="group"][aria-label="选择大屏配色"] button[aria-pressed="true"]')?.textContent?.includes("暗夜黑"))
  await themePicker.getByRole("button", { name: "鎏金", exact: true }).click()
  assert.equal(await screen.locator(".monitor-root").getAttribute("data-monitor-theme"), "black", "an already opened screen should retain its selected palette")
  await screen.reload({ waitUntil: "domcontentloaded" })
  await screen.getByText("资产总量", { exact: true }).waitFor({ timeout: 120000 })
  assert.equal(await screen.locator(".monitor-root").getAttribute("data-monitor-theme"), "black", "the URL should restore the chosen palette")
  assert.equal(await screen.getByText("按地址去重", { exact: true }).count(), 0)
  assert.equal(await screen.getByText("子域名、IP 和主机端口 · 去重统计", { exact: true }).count(), 0)
  assert.equal(await screen.getByText("按主机端口计数", { exact: true }).count(), 0)
  assert.ok(await screen.getByText("9,688", { exact: true }).count(), "inventory should show host-port category")
  const domains = screen.locator(".monitor-kpi").filter({ has: screen.getByText("域名", { exact: true }) })
  await expect(domains.locator("strong")).toHaveText("520")
  assert.equal(await screen.getByText("地图覆盖率", { exact: true }).count(), 0)
  assert.equal(await screen.locator(".monitor-scope-strip,.monitor-footer").count(), 0)
  assert.equal(await screen.getByText("展示范围", { exact: true }).count(), 0)
  assert.equal(await screen.getByText("组织间资产可能共享", { exact: true }).count(), 0)
  for (const organization of organizations) assert.equal(await screen.getByText(organization.displayName, { exact: true }).count(), 0)
  assert.equal(await screen.getByText("每 30 秒自动刷新", { exact: true }).count(), 0)
  assert.equal(await screen.getByText(/^数据更新/).count(), 0)
  const ports = screen.locator("section").filter({ has: screen.getByRole("heading", { name: "暴露端口 TOP 40" }) })
  const technologies = screen.locator("section").filter({ has: screen.getByRole("heading", { name: "技术栈 TOP 40" }) })
  for (const [panel, expectedFirst, expectedLast, excluded] of [[ports, "443 端口", "10033 端口", "10034 端口"], [technologies, "Nginx", "Technology-35", "Technology-36"]]) {
    const track = panel.locator(".monitor-bars-track")
    const original = track.locator(":scope > div").first()
    await expect.poll(() => track.evaluate(node => getComputedStyle(node).animationName)).toBe("monitor-recent-scroll")
    await expect(original.locator(".monitor-bar")).toHaveCount(40)
    await expect(original.locator(".monitor-bar").first()).toContainText(expectedFirst)
    await expect(original.locator(".monitor-bar").last()).toContainText(expectedLast)
    assert.equal(await panel.getByText(excluded, { exact: true }).count(), 0)
    await panel.locator(".monitor-bars-viewport").hover()
    assert.equal(await track.evaluate(node => getComputedStyle(node).animationPlayState), "paused")
    await screen.locator(".monitor-header").hover()
    await panel.locator(".monitor-bars-viewport").focus()
    assert.equal(await track.evaluate(node => getComputedStyle(node).animationPlayState), "paused")
    await panel.locator(".monitor-bars-viewport").evaluate(node => node.blur())
    await track.evaluate(node => {
      const animation = node.getAnimations()[0]
      const rows = node.firstElementChild
      const last = rows.lastElementChild
      const progress = (rows.clientHeight - (node.parentElement.clientHeight + last.clientHeight) / 2) / rows.clientHeight
      animation.pause()
      animation.currentTime = Number(animation.effect.getTiming().duration) * progress
    })
    await expect(original.locator(".monitor-bar").last()).toBeInViewport()
    await track.evaluate(node => node.getAnimations().forEach(animation => animation.play()))
  }
  const recentTrack = screen.locator(".monitor-recent-track")
  await expect.poll(() => recentTrack.evaluate(node => getComputedStyle(node).animationName)).toBe("monitor-recent-scroll")
  assert.equal(await recentTrack.locator(".monitor-recent-row").count(), fixture.recentAssets.length * 2)
  const position = await recentTrack.evaluate(node => getComputedStyle(node).transform)
  await screen.waitForTimeout(500)
  assert.notEqual(await recentTrack.evaluate(node => getComputedStyle(node).transform), position, "recent assets should scroll")
  await screen.emulateMedia({ reducedMotion: "reduce" })
  assert.equal(await recentTrack.evaluate(node => getComputedStyle(node).animationName), "none")
  assert.equal(await screen.locator(".monitor-recent-list").evaluate(node => getComputedStyle(node).overflowY), "auto")
  for (const panel of [ports, technologies]) {
    assert.equal(await panel.locator(".monitor-bars-track").evaluate(node => getComputedStyle(node).animationName), "none")
    assert.equal(await panel.locator(".monitor-bars-viewport").evaluate(node => getComputedStyle(node).overflowY), "auto")
    assert.equal(await panel.locator('.monitor-bars[aria-hidden="true"]').isVisible(), false)
  }
  await screen.emulateMedia({ reducedMotion: "no-preference" })
  await screen.locator(".monitor-map-marker").first().waitFor()
  assert.equal(await screen.locator('[data-slot="sidebar"]').count(), 0, "display should not include normal navigation")
  assert.equal(await screen.evaluate(() => window.opener === null), true, "the display tab should not retain its opener")
  assert.equal(await screen.locator(".monitor-map-marker").count(), rawLocations.length)
  await screen.locator(".monitor-map-marker").nth(1).focus()
  assert.ok(await screen.locator(".monitor-map-readout").getByText("DNS 解析推断", { exact: true }).count(), "DNS-derived coordinates should be identified")
  const shenzhen = screen.locator('[data-location-key="fixture-2"]')
  await shenzhen.focus()
  await expect(screen.locator(".monitor-map-readout")).toContainText("深圳")
  assert.doesNotMatch(await shenzhen.getAttribute("aria-label"), /Shenzhen|Guangdong/)
  assert.doesNotMatch(await shenzhen.locator("title").textContent(), /Shenzhen|Guangdong/)
  const newYork = screen.locator('[data-location-key="fixture-7"]')
  await newYork.focus()
  await expect(screen.locator(".monitor-map-readout")).toContainText("纽约")
  await expect(screen.locator(".monitor-map-readout")).toContainText("坐标附近城市（推断）")
  await expect(newYork.locator("title")).toContainText("坐标附近城市（推断）")
  assert.equal(await screen.getByRole("link", { name: "GeoNames" }).count(), 0)
  await screen.getByLabel("地名数据来源").click()
  await expect(screen.getByRole("link", { name: "GeoNames" })).toHaveAttribute("href", "https://www.geonames.org/")
  await screen.getByLabel("地名数据来源").click()
  assert.ok(await screen.locator(".rsm-geography[d]").count() > 150, "world country paths should be rendered")
  const chinaCenter = await screen.locator('.rsm-geography[data-country="China"]').evaluate(node => { const box = node.getBBox(); return box.x + box.width / 2 })
  assert.ok(chinaCenter > 300 && chinaCenter < 470, "Asia should be visible to the left of the Pacific")
  for (const country of ["China", "United States of America", "Brazil", "Australia"]) {
    const box = await screen.locator(`.rsm-geography[data-country="${country}"]`).evaluate(node => { const b = node.getBBox(); return { x: b.x, y: b.y, right: b.x + b.width, bottom: b.y + b.height } })
    assert.ok(box.x >= 0 && box.y >= 0 && box.right <= 1000 && box.bottom <= 380, `${country} must fit within the map: ${JSON.stringify(box)}`)
  }
  const map = screen.locator(".monitor-map")
  const cityMarker = screen.locator('[data-location-key="fixture-0"]')
  const beforeSize = await cityMarker.locator("circle").nth(2).boundingBox()
  const markerBox = await cityMarker.boundingBox()
  await screen.mouse.move(markerBox.x + markerBox.width / 2, markerBox.y + markerBox.height / 2)
  await screen.mouse.wheel(0, -1100)
  await expect.poll(async () => Number(await map.getAttribute("data-map-zoom"))).toBeGreaterThan(3)
  await expect(cityMarker.locator('[data-map-city-label="true"]')).toHaveText("北京")
  const afterSize = await cityMarker.locator("circle").nth(2).boundingBox()
  assert.ok(Math.abs(afterSize.width - beforeSize.width) < 2, "markers should keep their screen size while zooming")
  await cityMarker.focus()
  assert.ok((await screen.locator(".monitor-map-readout").innerText()).includes("北京"))
  const mapBox = await map.boundingBox()
  const zoomGroup = screen.locator(".rsm-zoomable-group")
  const beforePan = await zoomGroup.getAttribute("transform")
  await screen.mouse.move(mapBox.x + mapBox.width * .6, mapBox.y + mapBox.height * .6)
  await screen.mouse.down()
  await screen.mouse.move(mapBox.x + mapBox.width * .65, mapBox.y + mapBox.height * .65, { steps: 6 })
  await screen.mouse.up()
  await expect.poll(() => zoomGroup.getAttribute("transform")).not.toBe(beforePan)
  await screen.screenshot({ path: `${output}/screen-city-zoom.png`, fullPage: true })
  await screen.getByRole("button", { name: "复位", exact: true }).click()
  await expect(map).toHaveAttribute("data-map-zoom", "1.00")
  if (await screen.evaluate(() => document.fullscreenEnabled)) {
    await screen.getByRole("button", { name: "全屏", exact: true }).click()
    await screen.getByRole("button", { name: "退出全屏", exact: true }).waitFor()
    await screen.getByRole("button", { name: "退出全屏", exact: true }).click()
    await screen.getByRole("button", { name: "全屏", exact: true }).waitFor()
  }
  const desktopLayout = await screen.evaluate(() => Object.fromEntries(["monitor-root", "monitor-header", "monitor-toolbar", "monitor-kpis", "monitor-main-grid", "monitor-map", "monitor-bottom-grid"].map(name => { const element = document.querySelector(`.${name}`); const box = element.getBoundingClientRect(); return [name, { top: Math.round(box.top), height: Math.round(box.height) }] })))
  await screen.screenshot({ path: `${output}/screen-desktop.png`, fullPage: true })
  const bottom = desktopLayout["monitor-bottom-grid"].top + desktopLayout["monitor-bottom-grid"].height
  assert.ok(bottom <= 1081 && bottom >= 1048, `the desktop display should fill 1920×1080: ${JSON.stringify(desktopLayout)}`)
  assert.ok(desktopLayout["monitor-map"].height >= 380, "the world map should be larger than the old 340px canvas")
  for (const viewport of [{ width: 2560, height: 1440 }, { width: 2560, height: 1600 }]) {
    await screen.setViewportSize(viewport)
    const metrics = await screen.evaluate(() => ({ end: document.querySelector(".monitor-bottom-grid").getBoundingClientRect().bottom,
      viewport: innerHeight, width: document.querySelector(".monitor-root").scrollWidth }))
    assert.ok(metrics.end <= metrics.viewport + 1 && metrics.end >= metrics.viewport - 32, "large displays should not leave a blank lower region")
    assert.ok(metrics.width <= viewport.width + 1)
    if (viewport.height === 1440) await screen.screenshot({ path: `${output}/screen-large.png`, fullPage: true })
  }
  await expect.poll(() => recentTrack.evaluate(node => getComputedStyle(node).animationName)).toBe("none")
  await expect(recentTrack.locator(".monitor-recent-row")).toHaveCount(fixture.recentAssets.length)
  await screen.setViewportSize({ width: 1920, height: 1080 })
  await expect.poll(() => recentTrack.evaluate(node => getComputedStyle(node).animationName)).toBe("monitor-recent-scroll")
  assert.equal(await screen.locator(".monitor-eyebrow,.monitor-kpi-index,.monitor-brand-mark").count(), 0, "decorative labels should not compete with data")
  assert.equal(await screen.locator(".monitor-trend-series").count(), 2, "assets and risks should have separate readable scales")
  assert.ok(await screen.locator(".monitor-risk-priority").getByText("15", { exact: true }).count(), "priority count must equal recorded high and critical findings")
  await screen.getByRole("button", { name: "暂停刷新", exact: true }).click()
  await screen.getByRole("button", { name: "恢复刷新", exact: true }).waitFor()
  await screen.locator(".monitor-map-marker").last().focus()
  await screen.keyboard.press("Enter")
  dataMode = "error"
  await screen.getByRole("button", { name: "立即刷新", exact: true }).click()
  await screen.getByText("刷新失败 · 显示上次数据", { exact: true }).waitFor({ timeout: 20000 })
  assert.ok(await screen.getByText("12,648", { exact: true }).count())
  dataMode = "empty"
  await screen.getByRole("button", { name: "立即刷新", exact: true }).click()
  await screen.getByText("所选组织暂无资产", { exact: true }).waitFor()
  await screen.getByText("暂未获得可展示的地区信息", { exact: true }).waitFor()
  await expect(domains.locator("strong")).toHaveText("0")
  dataMode = "short"
  await screen.getByRole("button", { name: "立即刷新", exact: true }).click()
  await screen.getByText("最近 3 条", { exact: true }).waitFor()
  await expect.poll(() => recentTrack.evaluate(node => getComputedStyle(node).animationName)).toBe("none")
  await expect(recentTrack.locator(".monitor-recent-row")).toHaveCount(3)
  for (const [panel, count] of [[ports, 3], [technologies, 2]]) {
    await expect.poll(() => panel.locator(".monitor-bars-track").evaluate(node => getComputedStyle(node).animationName)).toBe("none")
    await expect(panel.locator(".monitor-bar")).toHaveCount(count)
  }
  dataMode = "recent50"
  await screen.getByRole("button", { name: "立即刷新", exact: true }).click()
  await screen.getByText("最近 50 条", { exact: true }).waitFor()
  await expect.poll(() => recentTrack.evaluate(node => getComputedStyle(node).animationName)).toBe("monitor-recent-scroll")
  await expect(recentTrack.locator(".monitor-recent-row")).toHaveCount(100)
  const originalRows = recentTrack.locator(":scope > div").first().locator(".monitor-recent-row")
  await expect(originalRows).toHaveCount(50)
  await expect(originalRows.first().locator(".monitor-recent-label")).toHaveText("asset-1.yunhai.example")
  await expect(originalRows.last().locator(".monitor-recent-label")).toHaveText("asset-50.yunhai.example")
  // Advance the real CSS animation to prove the final record scrolls into view.
  await recentTrack.evaluate(track => {
    const animation = track.getAnimations()[0]
    const rows = track.firstElementChild
    const last = rows.lastElementChild
    const progress = (rows.clientHeight - (track.parentElement.clientHeight + last.clientHeight) / 2) / rows.clientHeight
    animation.pause()
    animation.currentTime = Number(animation.effect.getTiming().duration) * progress
  })
  await expect(originalRows.last()).toBeInViewport()
  await recentTrack.evaluate(track => track.getAnimations().forEach(animation => animation.play()))
  dataMode = "normal"
  await screen.getByRole("button", { name: "立即刷新", exact: true }).click()
  await screen.locator(".monitor-map-marker").first().waitFor()
  await screen.setViewportSize({ width: 390, height: 844 })
  const attributionBox = await screen.locator(".monitor-map-attribution").boundingBox()
  const controlsBox = await screen.locator(".monitor-map-controls").boundingBox()
  assert.ok(attributionBox.y >= controlsBox.y + controlsBox.height, "mobile map attribution must not overlap zoom controls")
  await screen.screenshot({ path: `${output}/screen-mobile.png`, fullPage: true })
  assert.ok(await screen.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "mobile should not overflow horizontally")
  for (const viewport of [{ width: 320, height: 568 }, { width: 768, height: 1024 }, { width: 1366, height: 768 }]) {
    await screen.setViewportSize(viewport)
    const monitor = screen.locator(".monitor-root")
    assert.ok(await monitor.evaluate(node => node.scrollWidth <= node.clientWidth + 1), `screen should fit ${viewport.width}px`)
    await monitor.focus()
    await screen.keyboard.press("Control+End")
    const lastPanel = screen.locator(".monitor-bottom-grid>.monitor-panel").last()
    await lastPanel.scrollIntoViewIfNeeded()
    const lastPanelBox = await lastPanel.boundingBox()
    assert.ok(lastPanelBox.y >= 0 && lastPanelBox.y + lastPanelBox.height <= viewport.height + 1, "lower panels must remain reachable in short and narrow windows")
  }
  await context.addCookies([{ name: "NEXT_LOCALE", value: "en", url: origin }])
  await screen.reload({ waitUntil: "domcontentloaded" })
  await screen.locator(".monitor-kpis").waitFor()
  await screen.setViewportSize({ width: 320, height: 568 })
  assert.ok(await screen.locator(".monitor-root").evaluate(node => node.scrollWidth <= node.clientWidth + 1), "English display should fit a narrow screen")
  await screen.locator(".monitor-root").evaluate(node => { node.scrollTop = node.scrollHeight })
  await screen.screenshot({ path: `${output}/screen-english-mobile-bottom.png`, fullPage: true })
  await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: origin }])
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: `${output}/launcher-mobile.png`, fullPage: true })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "mobile launcher should not overflow horizontally")
  assert.equal(await themePicker.getByRole("button").count(), 5)
  assert.equal(errors.length, 0, errors.join("\n"))
  console.log(JSON.stringify({ passed: true, snapshotCalls, screenshots: output, desktopLayout }))
} catch (error) {
  await page.screenshot({ path: `${output}/failure.png`, fullPage: true }).catch(() => {})
  console.error(JSON.stringify({ url: page.url(), text: (await page.locator("body").innerText()).slice(0, 1800), checkboxes: await page.locator('[role="checkbox"],input[type="checkbox"]').evaluateAll(nodes => nodes.map(node => node.outerHTML)), errors }))
  throw error
} finally { await browser.close() }
