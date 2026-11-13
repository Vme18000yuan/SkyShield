// Verify actual rendered motion and the static accessibility fallback in a full Chromium browser.
import { createRequire } from "node:module"
import path from "node:path"
import { chromium, expect } from "@playwright/test"

const origin = process.env.SKYSHIELD_TEST_ORIGIN || "http://127.0.0.1:33327"
const require = createRequire(import.meta.url)
const nextDir = path.dirname(require.resolve("next/package.json"))
const sharp = require(require.resolve("sharp", { paths: [nextDir] }))
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })

async function changedPixels(first, second) {
  const a = await sharp(first).removeAlpha().raw().toBuffer()
  const b = await sharp(second).removeAlpha().raw().toBuffer()
  let count = 0
  for (let i = 0; i < a.length; i += 3) {
    if (a[i] !== b[i] || a[i + 1] !== b[i + 1] || a[i + 2] !== b[i + 2]) count++
  }
  return count
}

try {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "zh-CN", reducedMotion: "no-preference" })
  await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: origin }, { name: "theme-mode", value: "light", url: origin }])
  await context.route("**/v1/**", route => route.fulfill({ json: { kind: "builtIn" } }))
  const page = await context.newPage()
  await page.goto(`${origin}/login/`, { waitUntil: "networkidle" })
  await expect(page.locator("#lunafox-boot-layer")).toBeHidden({ timeout: 10000 })
  await expect(page.locator("#email")).toBeEnabled()
  await page.evaluate(async () => {
    for (const name of ["tianqiong-solar-system-v2.svg", "tianqiong-solar-system-v2-static.svg"]) {
      const image = new Image()
      image.src = "/images/brand/" + name
      await image.decode()
    }
  })
  const background = page.locator('[data-login-visual-kind="builtIn"]')
  await expect.poll(() => background.evaluate(element => getComputedStyle(element, "::before").backgroundImage)).toContain("tianqiong-solar-system-v2.svg")
  await page.screenshot({ path: "/tmp/tianqiong-solar-desktop.png", fullPage: true })
  const clip = { x: 0, y: 0, width: 800, height: 900 }
  const first = await page.screenshot({ clip })
  await page.waitForTimeout(1500)
  const moving = await changedPixels(first, await page.screenshot({ clip }))
  expect(moving).toBeGreaterThan(100)

  await page.emulateMedia({ reducedMotion: "reduce" })
  await expect.poll(() => background.evaluate(element => getComputedStyle(element, "::before").backgroundImage)).toContain("solar-system-v2-static.svg")
  const stillFirst = await page.screenshot({ clip })
  await page.waitForTimeout(1500)
  const staticDifference = await changedPixels(stillFirst, await page.screenshot({ clip }))
  expect(staticDifference).toBe(0)

  await page.emulateMedia({ reducedMotion: "no-preference" })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: "/tmp/tianqiong-solar-mobile.png", fullPage: true })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await context.addCookies([{ name: "theme-mode", value: "dark", url: origin }])
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.reload({ waitUntil: "networkidle" })
  await expect(page.locator("#lunafox-boot-layer")).toBeHidden({ timeout: 10000 })
  await page.screenshot({ path: "/tmp/tianqiong-solar-dark.png", fullPage: true })
  console.log(JSON.stringify({ passed: true, animatedPixels: moving, reducedMotionDifference: staticDifference, mobile: true, dark: true }))
} finally {
  await browser.close()
}
