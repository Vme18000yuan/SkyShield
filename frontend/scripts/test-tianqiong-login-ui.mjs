// Login regression test. Backend responses, media, and credentials are synthetic.
import { chromium, expect } from "@playwright/test"

const origin = process.env.SKYSHIELD_TEST_ORIGIN || "http://127.0.0.1:33327"
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
const whiteVisual = '<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="900"><rect width="1600" height="900" fill="white"/></svg>'

async function cardPalette(card) {
  return card.evaluate(element => {
    // Compare rendered colors; production CSS may serialize OKLCH lightness as a percentage.
    const canvas = document.createElement("canvas")
    canvas.width = canvas.height = 1
    const context = canvas.getContext("2d")
    const pixel = color => {
      context.clearRect(0, 0, 1, 1)
      context.fillStyle = color
      context.fillRect(0, 0, 1, 1)
      return Array.from(context.getImageData(0, 0, 1, 1).data)
    }
    const style = getComputedStyle(element)
    return { actual: pixel(style.backgroundColor), token: pixel(style.getPropertyValue("--card").trim()) }
  })
}

async function fixture({ visual = { kind: "builtIn" }, failMedia = false, locale = "zh", mode = "light", reducedMotion = "no-preference" } = {}) {
  const context = await browser.newContext({ locale: locale === "zh" ? "zh-CN" : "en-US", viewport: { width: 1440, height: 900 }, reducedMotion })
  await context.addCookies([{ name: "NEXT_LOCALE", value: locale, url: origin }, { name: "theme-mode", value: mode, url: origin }])
  let credentials, mediaRequested = false
  await context.route("**/v1/**", route => {
    const path = new URL(route.request().url()).pathname
    if (path === "/v1/loginVisual/current") return route.fulfill({ json: visual })
    if (path === "/v1/loginVisual/media/fixture") {
      mediaRequested = true
      return failMedia ? route.abort() : route.fulfill({ body: whiteVisual, contentType: "image/svg+xml" })
    }
    if (path === "/v1/sessions") {
      credentials = route.request().postDataJSON()
      return route.fulfill({ status: 401, json: { error: { code: "INVALID_CREDENTIALS", message: "fixture" } } })
    }
    return route.fulfill({ json: { results: [], totalSize: 0 } })
  })
  const page = await context.newPage()
  await page.goto(`${origin}/login/`, { waitUntil: "domcontentloaded", timeout: 120000 })
  const title = locale === "zh" ? "天穹 企业级攻击面管理平台" : "Tianqiong Enterprise Attack Surface Management"
  await expect(page.getByRole("heading", { level: 1, name: title })).toBeVisible({ timeout: 60000 })
  await expect(page.locator('[data-boot-handoff-pending="true"]')).toHaveCount(0, { timeout: 30000 })
  await expect(page.locator("#lunafox-boot-layer")).toBeHidden({ timeout: 10000 })
  return { context, page, getCredentials: () => credentials, mediaRequested: () => mediaRequested }
}

try {
  const main = await fixture()
  const land = main.page.locator('[data-globe-layer="land"]')
  const initialGlobe = await land.getAttribute("d")
  await expect.poll(() => land.getAttribute("d")).not.toBe(initialGlobe)
  const satellite = main.page.locator('[data-satellite-layer="near"][data-satellite="0"]')
  await expect(main.page.locator('[data-satellite-layer="near"]')).toHaveCount(18)
  const initialSatellite = await satellite.getAttribute("transform")
  await expect.poll(() => satellite.getAttribute("transform")).not.toBe(initialSatellite)
  const heading = main.page.getByRole("heading", { level: 1 })
  const card = main.page.locator(".product-login__card")
  const titleBox = await heading.boundingBox(), cardBox = await card.boundingBox()
  expect(titleBox.y + titleBox.height).toBeLessThan(cardBox.y)
  expect(Math.abs(cardBox.x + cardBox.width / 2 - 720)).toBeLessThan(2)
  expect(cardBox.x + cardBox.width).toBeLessThan(1440 - 24)
  // The form remains centered while the decorative globe is clipped behind it.
  for (const viewport of [{ width: 1024, height: 768 }, { width: 1440, height: 900 }, { width: 1920, height: 1080 }, { width: 2560, height: 1440 }]) {
    await main.page.setViewportSize(viewport)
    const layout = await main.page.evaluate(() => {
      const root = document.querySelector(".product-login")
      const form = document.querySelector(".product-login__form").getBoundingClientRect()
      const card = document.querySelector(".product-login__card").getBoundingClientRect()
      const scene = document.querySelector(".product-login__scene")
      const artwork = document.querySelector(".product-login__sky-art")
      const earth = document.querySelector('.product-login__sky-art circle[cx="450"][cy="390"][r="250"]').getBoundingClientRect()
      return { formWidth: form.width, center: form.x + form.width / 2,
        backgroundOwner: Boolean(scene.closest('[data-login-visual-kind="builtIn"]')),
        backgroundOpacity: Number(getComputedStyle(artwork).opacity),
        globeBehindCard: earth.x < card.x && earth.right > card.right,
        verticalOffset: Math.abs(form.y + form.height / 2 - earth.y - earth.height / 2),
        contentWidth: root.scrollWidth, viewportWidth: root.clientWidth,
        contentHeight: root.scrollHeight, viewportHeight: root.clientHeight }
    })
    expect(layout.formWidth).toBeLessThanOrEqual(384)
    expect(Math.abs(layout.center - viewport.width / 2)).toBeLessThan(2)
    expect(layout.backgroundOwner).toBe(true)
    expect(layout.globeBehindCard).toBe(true)
    expect(layout.backgroundOpacity).toBeGreaterThan(0)
    expect(layout.backgroundOpacity).toBeLessThanOrEqual(0.35)
    expect(layout.verticalOffset).toBeLessThan(2)
    expect(layout.contentWidth).toBeLessThanOrEqual(layout.viewportWidth + 1)
    expect(layout.contentHeight).toBeLessThanOrEqual(layout.viewportHeight + 1)
    if (viewport.width === 1920) await main.page.screenshot({ path: "/tmp/tianqiong-login-backdrop-wide.png", fullPage: true })
  }
  await main.page.setViewportSize({ width: 1440, height: 900 })
  await expect(main.page.locator('[data-login-visual-kind="builtIn"]')).toBeVisible()
  await expect(main.page.locator(".tianqiong-mark")).toHaveCount(1)
  expect(await main.page.locator(".tianqiong-mark").evaluate(element => getComputedStyle(element).maskImage)).toContain("tianqiong-mark.svg")
  const visibleCopy = await main.page.locator(".product-login").innerText()
  expect(visibleCopy).not.toMatch(/[a-zA-Z]/)
  expect(visibleCopy).not.toContain("洞察资产全貌，守护数字边界")
  expect(visibleCopy).not.toContain("从资产发现到风险洞察")
  expect(await main.page.locator("#email").getAttribute("placeholder")).not.toMatch(/[a-zA-Z]/)
  const palette = await cardPalette(card)
  expect(palette.actual).toEqual(palette.token)
  await main.page.waitForLoadState("networkidle")
  await main.page.screenshot({ path: "/tmp/tianqiong-login-native-desktop.png", fullPage: true })
  await main.page.setViewportSize({ width: 390, height: 844 })
  await expect(card).toBeVisible()
  const mobileCard = await card.boundingBox()
  expect(Math.abs(mobileCard.x + mobileCard.width / 2 - 195)).toBeLessThan(2)
  await main.page.screenshot({ path: "/tmp/tianqiong-login-native-mobile.png", fullPage: true })
  for (const width of [320, 390, 768, 900, 1024]) {
    await main.page.setViewportSize({ width, height: 844 })
    const metrics = await main.page.evaluate(() => ({ width: document.documentElement.clientWidth, content: document.documentElement.scrollWidth,
      cardWidth: document.querySelector(".product-login__card").getBoundingClientRect().width }))
    expect(metrics.content).toBeLessThanOrEqual(metrics.width + 1)
    expect(metrics.cardWidth).toBeLessThanOrEqual(width)
  }
  await main.page.getByRole("checkbox", { name: "记住密码" }).click()
  for (const viewport of [{ width: 320, height: 480 }, { width: 844, height: 390 }]) {
    await main.page.setViewportSize(viewport)
    const help = main.page.getByText("需要访问权限？请联系管理员。")
    await help.scrollIntoViewIfNeeded()
    const helpBox = await help.boundingBox()
    expect(helpBox.y).toBeGreaterThanOrEqual(0)
    expect(helpBox.y + helpBox.height).toBeLessThanOrEqual(viewport.height)
    await main.page.locator("#email").scrollIntoViewIfNeeded()
    const fieldBox = await main.page.locator("#email").boundingBox()
    expect(fieldBox.y).toBeGreaterThanOrEqual(0)
  }
  await main.page.getByRole("checkbox", { name: "记住密码" }).click()
  await main.page.setViewportSize({ width: 1440, height: 900 })
  await main.page.locator("#email").fill("fixture-user")
  await main.page.locator("#password").fill("fixture-password")
  await main.page.locator("#password").press("Tab")
  await expect(main.page.getByRole("button", { name: "显示密码" })).toBeFocused()
  await main.page.keyboard.press("Space")
  await expect(main.page.locator("#password")).toHaveAttribute("type", "text")
  await expect(main.page.locator("#password")).toHaveValue("fixture-password")
  expect(main.getCredentials()).toBeUndefined()
  await main.page.getByRole("button", { name: "隐藏密码" }).click()
  await expect(main.page.locator("#password")).toHaveAttribute("type", "password")
  await main.page.locator('button[type="submit"]').click()
  await expect.poll(main.getCredentials).toEqual({ username: "fixture-user", password: "fixture-password" })
  await main.context.close()

  const custom = await fixture({ visual: { kind: "image", mediaUrl: "/v1/loginVisual/media/fixture" } })
  const customImage = custom.page.locator('img[data-login-visual-kind="image"]')
  await expect(customImage).toBeVisible({ timeout: 30000 })
  await expect.poll(() => customImage.evaluate(image => Number(getComputedStyle(image).opacity))).toBe(1)
  await expect(custom.page.locator(".product-login__veil")).toBeVisible()
  await expect(custom.page.locator(".product-login__sky")).toBeHidden()
  const coveredLand = custom.page.locator('[data-globe-layer="land"]')
  const coveredFrame = await coveredLand.getAttribute("d")
  const coveredSatellite = custom.page.locator('[data-satellite-layer="near"][data-satellite="0"]')
  const coveredPosition = await coveredSatellite.getAttribute("transform")
  await custom.page.waitForTimeout(250)
  expect(await coveredLand.getAttribute("d")).toBe(coveredFrame)
  expect(await coveredSatellite.getAttribute("transform")).toBe(coveredPosition)
  await custom.context.close()

  const broken = await fixture({ visual: { kind: "image", mediaUrl: "/v1/loginVisual/media/fixture" }, failMedia: true })
  await expect.poll(broken.mediaRequested).toBe(true)
  await expect(broken.page.locator('img[data-login-visual-kind="image"]')).toHaveCount(0)
  await expect(broken.page.locator('[data-login-visual-kind="builtIn"]')).toBeVisible()
  await expect(broken.page.locator(".product-login__sky")).toBeVisible()
  const restoredLand = broken.page.locator('[data-globe-layer="land"]')
  const restoredFrame = await restoredLand.getAttribute("d")
  await expect.poll(() => restoredLand.getAttribute("d")).not.toBe(restoredFrame)
  await expect(broken.page.locator("#email")).toBeEnabled()
  await broken.context.close()

  const dark = await fixture({ mode: "dark" })
  await expect(dark.page.locator("html")).toHaveAttribute("data-theme", "lunafox-dark")
  const darkPalette = await cardPalette(dark.page.locator(".product-login__card"))
  expect(darkPalette.actual).toEqual(darkPalette.token)
  await dark.page.waitForLoadState("networkidle")
  await dark.page.screenshot({ path: "/tmp/tianqiong-login-native-dark.png", fullPage: true })
  await dark.context.close()

  const reduced = await fixture({ reducedMotion: "reduce" })
  const reducedLand = reduced.page.locator('[data-globe-layer="land"]')
  const stillGlobe = await reducedLand.getAttribute("d")
  const reducedSatellite = reduced.page.locator('[data-satellite-layer="near"][data-satellite="0"]')
  const stillSatellite = await reducedSatellite.getAttribute("transform")
  await reduced.page.waitForTimeout(250)
  expect(await reducedLand.getAttribute("d")).toBe(stillGlobe)
  expect(await reducedSatellite.getAttribute("transform")).toBe(stillSatellite)
  await reduced.page.emulateMedia({ reducedMotion: "no-preference" })
  await expect.poll(() => reducedLand.getAttribute("d")).not.toBe(stillGlobe)
  await expect.poll(() => reducedSatellite.getAttribute("transform")).not.toBe(stillSatellite)
  await reduced.context.close()

  const english = await fixture({ locale: "en" })
  await english.page.setViewportSize({ width: 320, height: 844 })
  const englishWidth = await english.page.evaluate(() => {
    const brand = document.querySelector("h1")
    const range = document.createRange()
    range.selectNodeContents(brand)
    return { width: document.documentElement.clientWidth, content: document.documentElement.scrollWidth,
      brandRight: range.getBoundingClientRect().right }
  })
  expect(englishWidth.content).toBeLessThanOrEqual(englishWidth.width + 1)
  expect(englishWidth.brandRight).toBeLessThanOrEqual(englishWidth.width)
  await english.context.close()
  console.log(JSON.stringify({ passed: true, chineseCopy: true, sharedTheme: true, desktopAndMobile: true, shortViewportScrolling: true, customVisual: true, fallback: true, loginSubmission: true, keyboardPasswordToggle: true, rotatingEarth: true, orbitingSatellites: 18, reducedMotion: true, english: true, centeredForm: true, ambientGlobe: true }))
} finally {
  await browser.close()
}
