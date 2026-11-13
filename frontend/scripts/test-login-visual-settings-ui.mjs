// Isolated login-visual settings test. No real settings or uploads are changed.
import { readFile } from "node:fs/promises"
import { chromium, expect } from "@playwright/test"

const origin = process.env.SKYSHIELD_TEST_ORIGIN || "http://127.0.0.1:33327"
const token = "fixture." + Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString("base64url") + ".fixture"
const image = await readFile(new URL("../public/images/brand/tianqiong-nebula.webp", import.meta.url))
const media = { kind: "image", contentType: "image/webp", sizeBytes: image.length }
const previewUrl = "data:image/webp;base64," + image.toString("base64")
let uploadCount = 0, publishCount = 0

const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
try {
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1440, height: 1000 } })
  await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: origin }])
  await context.addInitScript(({ value, appOrigin }) => {
    if (location.origin === appOrigin) { localStorage.setItem("accessToken", value); localStorage.setItem("refreshToken", value) }
  }, { value: token, appOrigin: origin })
  await context.route("**/api/github/repo", route => route.fulfill({ json: { stars: 0 } }))
  await context.route("**/v1/**", route => {
    const path = new URL(route.request().url()).pathname
    if (path === "/v1/upgradeOperations:active") return route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND" } } })
    if (path === "/v1/users/current") return route.fulfill({ json: { id: 7, username: "fixture", isSuperuser: true } })
    if (path === "/v1/settings/loginVisual:checkDiscoverability") return route.fulfill({ json: { unlocked: true } })
    if (path === "/v1/settings/loginVisual:upload") {
      uploadCount++
      return route.fulfill({ json: { draft: media, previewUrl } })
    }
    if (path === "/v1/settings/loginVisual:publish") {
      publishCount++
      return route.fulfill({ json: { published: media, previewUrl } })
    }
    if (path === "/v1/settings/loginVisual") return route.fulfill({ json: {} })
    if (path === "/v1/loginVisual/current") return route.fulfill({ json: { kind: "builtIn" } })
    return route.fulfill({ json: { results: [], totalSize: 0, notifications: [], unreadCount: 0 } })
  })
  const page = await context.newPage(), errors = []
  page.on("pageerror", error => errors.push(String(error)))
  await page.goto(`${origin}/settings/login-visual/`, { waitUntil: "domcontentloaded", timeout: 120000 })
  await expect(page.locator('[data-loading-owner="login-visual-settings-page-route"]')).toHaveAttribute("data-loading-phase", "content", { timeout: 60000 })
  const preview = page.locator('[data-login-preview="true"]').filter({ visible: true })
  await expect(preview).toHaveCount(1, { timeout: 60000 })
  await expect(preview.getByRole("heading", { level: 1, name: "天穹 企业级攻击面管理平台" })).toBeVisible({ timeout: 60000 })
  await expect(preview.locator("#login-visual-preview-email")).toBeDisabled()
  const compositionGeometry = () => preview.evaluate(root => {
    const scene = root.querySelector(".product-login__scene")
    const artwork = root.querySelector(".product-login__sky-art")
    const form = root.querySelector(".product-login__form")
    return { width: form.clientWidth,
      artworkWidth: getComputedStyle(artwork).width, sceneTop: getComputedStyle(scene).top,
      formOffset: form.offsetLeft }
  })
  const desktopGeometry = await compositionGeometry()
  expect(desktopGeometry.width).toBe(384)
  await page.setViewportSize({ width: 768, height: 1024 })
  await expect.poll(compositionGeometry).toEqual(desktopGeometry)
  await page.setViewportSize({ width: 1440, height: 1000 })
  const control = preview.getByRole("button", { name: "替换登录页视觉" })
  await expect(control).toBeEnabled()
  const [chooser] = await Promise.all([page.waitForEvent("filechooser"), control.click()])
  await chooser.setFiles({ name: "fixture.webp", mimeType: "image/webp", buffer: image })
  await expect.poll(() => uploadCount).toBe(1)
  await expect(preview.locator('img[data-login-visual-kind="image"]')).toBeVisible()
  await expect(page.getByText("未发布草稿")).toBeVisible()
  await page.getByRole("button", { name: "保存并发布" }).click()
  await expect.poll(() => publishCount).toBe(1)
  await expect(page.getByText("已发布", { exact: true })).toBeVisible()
  expect(errors).toEqual([])
  console.log(JSON.stringify({ passed: true, preview: true, consistentScaledComposition: true, filePicker: true, upload: true, publish: true }))
} finally {
  await browser.close()
}
