// Browser smoke test: all API requests are mocked; no real account is used.
import { chromium, expect } from "@playwright/test"

const origin = process.env.SKYSHIELD_TEST_ORIGIN || "http://127.0.0.1:33327"
const token = "fixture." + Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString("base64url") + ".fixture"
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
try {
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1200, height: 900 } })
  await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: origin }])
  await context.route("**/v1/**", async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/v1/sessions") {
      const body = request.postDataJSON()
      if (body.password !== "fixture-secret") {
        return route.fulfill({ status: 401, json: { code: 401, message: "Invalid username or password" } })
      }
      return route.fulfill({ json: { accessToken: token, refreshToken: token, expiresIn: 3600, user: { id: 7, username: "fixture" } } })
    }
    if (path === "/v1/loginVisual/current") return route.fulfill({ json: { kind: "builtIn" } })
    if (path === "/v1/users/current") return route.fulfill({ json: { id: 7, username: "fixture" } })
    if (path === "/v1/users/me:changePassword") return route.fulfill({ json: { message: "ok" } })
    if (path === "/v1/upgradeOperations:active") return route.fulfill({ status: 404, json: {} })
    return route.fulfill({ json: { results: [], totalSize: 0 } })
  })
  const page = await context.newPage()
  await page.goto(`${origin}/login/?returnTo=%2Fassets%2F`, { waitUntil: "domcontentloaded", timeout: 120000 })
  const username = page.getByLabel("用户名或邮箱")
  const password = page.getByLabel("密码", { exact: true })
  const remember = page.getByRole("checkbox", { name: "记住密码" })
  await expect(remember).not.toBeChecked()
  await username.fill("fixture")
  await password.fill("invalid-secret")
  await remember.click()
  await expect(page.getByText("密码将明文保存在当前浏览器，请勿在公共设备上使用。")).toBeVisible()
  await Promise.all([
    page.waitForResponse(response => response.url().endsWith("/v1/sessions") && response.status() === 401),
    page.getByRole("button", { name: "登录", exact: true }).click(),
  ])
  await expect(page.getByRole("button", { name: "登录", exact: true })).toBeEnabled()
  expect(await page.evaluate(() => localStorage.getItem("tianqiong.remembered-login"))).toBeNull()

  await password.fill("fixture-secret")
  await page.getByRole("button", { name: "登录", exact: true }).click()
  await expect(page).toHaveURL(/\/assets\/$/, { timeout: 30000 })
  await page.getByRole("button", { name: /fixture/ }).click({ timeout: 30000 })
  await page.getByRole("menuitem", { name: "Change Password" }).click()
  const dialog = page.getByRole("dialog", { name: "修改密码" })
  await dialog.getByLabel("当前密码").fill("fixture-secret")
  await dialog.getByLabel("新密码", { exact: true }).fill("changed-secret")
  await dialog.getByLabel("确认新密码").fill("changed-secret")
  await dialog.getByRole("button", { name: "保存" }).click()
  await expect.poll(() => page.evaluate(() => JSON.parse(localStorage.getItem("tianqiong.remembered-login") || "null")?.password)).toBe("changed-secret")
  await page.evaluate(() => { localStorage.removeItem("accessToken"); localStorage.removeItem("refreshToken") })
  await page.goto(`${origin}/login/`, { waitUntil: "domcontentloaded" })
  await expect(page.getByLabel("用户名或邮箱")).toHaveValue("fixture")
  await expect(page.getByLabel("密码", { exact: true })).toHaveValue("changed-secret")
  await expect(page.getByRole("checkbox", { name: "记住密码" })).toBeChecked()
  await page.getByRole("checkbox", { name: "记住密码" }).click()
  await page.reload({ waitUntil: "domcontentloaded" })
  await expect(page.getByRole("checkbox", { name: "记住密码" })).not.toBeChecked()
  await expect(page.getByLabel("用户名或邮箱")).toHaveValue("")
  await expect(page.getByLabel("密码", { exact: true })).toHaveValue("")
  console.log("remember-password browser smoke passed")
} finally {
  await browser.close()
}
