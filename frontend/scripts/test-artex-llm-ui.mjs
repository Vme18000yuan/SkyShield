// Isolated browser HTTP fixtures; does not touch credentials or business data.
import { chromium, expect } from "@playwright/test"
import assert from "node:assert/strict"

const origin = process.env.ARTEX_UI_ORIGIN || "http://127.0.0.1:33325"
const token = `fixture.${Buffer.from(JSON.stringify({ exp: 4102444800 })).toString("base64url")}.fixture`
const zero = { attempts: 0, interval_ms: 0 }
let retry = { connect: zero, empty: zero, stream: zero, breaker: zero, intent: zero }
let enabled = false, fallback = false, saved, tested, catalogued, usagePeriods = [], recording = false, recordingDeleted = false
const profiles = [{ id: "11111111-1111-4111-8111-111111111111", name: "aiLlmProfiles/11111111-1111-4111-8111-111111111111", displayName: "原版模型", provider: "openai", model: "fixture-model", baseUrl: "https://model.fixture.invalid/v1", isDefault: true, hasApiKey: true, apiKeyHint: "…test", options: { rate_per_second: 0, rate_per_minute: 0, streaming: true } }]
const pool = () => ({ enabled, bind_fallback: fallback, chain: profiles.map(item => ({ profile_id: item.id, name: item.displayName, model: item.model, format: item.provider, active: item.isDefault, priority: 0, excluded: false, state: "ok", fails: 0, trips: 0, cooldown_secs: 0 })) })
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
try {
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1440, height: 1000 } })
  await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: origin }])
  await context.addInitScript(t => { localStorage.setItem("accessToken", t); localStorage.setItem("refreshToken", t) }, token)
  await context.route("**/api/github/repo", route => route.fulfill({ json: { stars: 0 } }))
  await context.route("**/v1/**", async route => {
    const url = new URL(route.request().url()), path = url.pathname, method = route.request().method()
    if (path === "/v1/upgradeOperations:active") return route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND" } } })
    let data = { results: [], totalSize: 0, notifications: [], unreadCount: 0 }
    if (path === "/v1/users/current") data = { id: 7, username: "fixture", isSuperuser: true }
    if (path === "/v1/aiLlmProfiles") data = { profiles }
    if (path === `/v1/aiLlmProfiles/${profiles[0].id}` && method === "PUT") {
      saved = route.request().postDataJSON()
      Object.assign(profiles[0], { displayName: saved.name, model: saved.model, options: saved.options })
      data = profiles[0]
    }
    if (path === "/v1/aiLlmProfileTests") { tested = route.request().postDataJSON(); data = { ok: true, latency_ms: 42, reply: "OK", model: tested.model } }
    if (path === "/v1/aiLlmModelCatalog") { catalogued = route.request().postDataJSON(); data = { ok: true, models: ["fixture-model", "fixture-other"] } }
    if (path === "/v1/aiLlmRuntime/pool") data = pool()
    if (path === "/v1/aiLlmRuntime" && method === "PATCH") { const input = route.request().postDataJSON(); enabled = input.llm_pool_enabled ?? enabled; fallback = input.llm_pool_bind_fallback ?? fallback; data = { llm_pool_enabled: enabled, llm_pool_bind_fallback: fallback } }
    if (path === "/v1/aiLlmRuntime/retryPolicy") { if (method === "PUT") retry = route.request().postDataJSON(); data = retry }
    if (path === "/v1/aiModelUsage") { const days = Number(url.searchParams.get("days")); usagePeriods.push(days); const row = { profile_id: profiles[0].id, profile_name: "原版模型", calls: 3, tasks: 1, input_tokens: 330, output_tokens: 120, cache_read_tokens: 40, cache_write_tokens: 10 }; data = { totals: row, profiles: [row], daily: [{ ...row, date: new Date().toISOString().slice(0,10) }], days } }
    if (path === "/v1/aiRuntimeSettings") { if(method === "PATCH") recording = route.request().postDataJSON().llm_record; data = { llm_record: recording } }
    if (path === "/v1/aiLlmRecordTasks") data = { items: recordingDeleted ? [] : [{id:"30",name:"资产测试任务",count:1}], has_more:false }
    if (path === "/v1/aiLlmRecords") { if(method === "DELETE") recordingDeleted = true; data = method === "DELETE" ? {deleted:1} : {items:recordingDeleted ? [] : [{id:"44444444-4444-4444-8444-444444444444",task_id:"30",task_name:"资产测试任务",session_id:"exp10-worker-i1",worker:"worker",model:"fixture-model",profile_name:"原版模型",duration_ms:100,input_tokens:11,output_tokens:7,cache_read_tokens:0,cache_write_tokens:0,status:"ok",truncated:false,created_at:new Date().toISOString()}],total:recordingDeleted?0:1,page:0,size:25} }
    if (path === "/v1/aiLlmRecords/44444444-4444-4444-8444-444444444444") data = {id:"44444444-4444-4444-8444-444444444444",model:"fixture-model",task_name:"资产测试任务",status:"ok",truncated:false,payload:{request_body:'{"wire":"raw-request"}',response_body:'data: raw-response',normalized_request:'{"messages":"parsed-request"}',normalized_response:'parsed-response',truncated:false}}
    await route.fulfill({ json: data })
  })
  const page = await context.newPage(), errors = []
  page.on("pageerror", error => errors.push(String(error)))
  await page.goto(`${origin}/settings/ai/llm/`, { waitUntil: "domcontentloaded", timeout: 120000 })
  await page.getByText("原版模型", { exact: true }).waitFor({ timeout: 120000 })
  await page.getByText("原版模型", { exact: true }).click()
  await expect(page.locator("#p-api-key")).toHaveValue("")
  await page.locator("#p-rps").fill("2")
  await page.locator("#p-rpm").fill("90")
  await page.locator("#p-cw").fill("64")
  await page.locator("#p-max-tokens").fill("321")
  await page.locator("#p-session-header").fill("X-Run-Session")
  await page.getByRole("switch", { name: "流式输出", exact: true }).click()
  const selects = page.getByRole("combobox")
  await selects.nth(1).click()
  await page.getByRole("option", { name: "max_completion_tokens", exact: true }).click()
  await selects.nth(2).click()
  await page.getByRole("option", { name: "开启", exact: true }).click()
  await selects.nth(3).click()
  await page.getByRole("option", { name: "high", exact: true }).click()
  await page.getByRole("button", { name: "测试连接", exact: true }).click()
  await expect.poll(() => tested?.options?.streaming).toBe(false)
  assert.equal(tested.profileId, profiles[0].id)
  await page.getByRole("button", { name: "从 API 加载可用模型", exact: true }).click()
  await page.getByRole("button", { name: "fixture-other", exact: true }).waitFor()
  assert.equal(catalogued.profileId, profiles[0].id)
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await expect(page.locator("#p-name")).not.toBeVisible()
  assert.equal(saved.preserveDefault, true)
  assert.equal(saved.apiKey, "")
  assert.deepEqual([saved.options.rate_per_second, saved.options.rate_per_minute, saved.options.context_window_k, saved.options.streaming, saved.options.max_tokens, saved.options.max_tokens_field, saved.options.thinking_type, saved.options.reasoning_effort], [2, 90, 64, false, 321, "max_completion_tokens", "enabled", "high"])
  await page.getByRole("button", { name: "轮询配置", exact: false }).click()
  await page.getByRole("switch", { name: "LLM 轮询开关", exact: true }).click()
  await expect.poll(() => enabled).toBe(true)
  await page.getByRole("switch", { name: "绑定配置失败兜底开关", exact: true }).click()
  await expect.poll(() => fallback).toBe(true)
  await page.keyboard.press("Escape")
  await page.getByRole("tab", { name: "重试与退避", exact: true }).click()
  await page.locator("#gl-connect-n").fill("4")
  await page.locator("#gl-intent-n").fill("-1")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await expect.poll(() => retry.connect.attempts).toBe(4)
  assert.equal(retry.intent.attempts, -1)
  await page.getByRole("tab", { name: "用量统计", exact: true }).click()
  await expect(page.getByText("330", { exact: true }).first()).toBeVisible()
  await expect(page.getByRole("row").filter({ hasText: "原版模型" })).toBeVisible()
  await page.getByLabel("统计周期").selectOption("7")
  await expect.poll(() => usagePeriods.includes(7)).toBe(true)
  await page.getByRole("tab", { name: "LLM 录制", exact: true }).click()
  await page.getByRole("switch", { name: "录制模型请求与响应" }).click()
  await expect.poll(() => recording).toBe(true)
  await expect(page.getByRole("row").filter({hasText:"资产测试任务"})).toBeVisible()
  await page.getByRole("button",{name:"查看录制"}).click()
  await expect(page.getByRole("dialog").getByText(/raw-request/)).toBeVisible()
  await page.waitForTimeout(400)
  await page.screenshot({path:"/tmp/skyshield-artex-llm-recording-fixture.png"})
  await page.getByRole("dialog").getByRole("button",{name:"解析结果"}).click()
  await expect(page.getByRole("dialog").getByText(/parsed-request/)).toBeVisible()
  await page.getByRole("dialog").getByRole("button",{name:"关闭"}).click()
  await page.getByLabel("任务",{exact:true}).selectOption("30")
  await page.getByRole("button",{name:"删除所选任务的录制"}).click()
  await page.getByRole("alertdialog").getByRole("button",{name:"确认删除"}).click()
  await expect.poll(() => recordingDeleted).toBe(true)
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ passed: true, nativeProfileDrawer: true, requestOptions: true, pool: true, retryPolicy: true, usage: true, recordings: true }))
} finally { await browser.close() }
