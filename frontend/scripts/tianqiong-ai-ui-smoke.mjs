// Browser interaction smoke with isolated HTTP fixtures; it never contacts a real API.
import { chromium } from "@playwright/test"
import { strict as assert } from "node:assert"

const base = process.env.TIANQIONG_UI_SMOKE_URL || "http://127.0.0.1:39017"
const token = `e30.${Buffer.from(JSON.stringify({ exp: 4102444800 })).toString("base64url")}.fixture`
const stamp = "2026-10-29T00:00:00Z"
const task = { id: "123", name: "aiTasks/123", displayName: "测试任务", goal: "验证资产", description: "", targetId: null, status: "running", queued: false, workerCount: 1, maxTurns: 30, timeoutSeconds: 3600, firstRunAt: stamp, deadlineAt: null, errorMessage: "", createdAt: stamp, updatedAt: stamp, completedAt: null }
let scheduleBody = null
let actionBody = null
let agentBody = null
let toolBody = null
let skillBody = null
let mcpBody = null
let plannerSearch = true
const browser = await chromium.launch({ headless: true, ...(process.env.TIANQIONG_CHROMIUM ? { executablePath: process.env.TIANQIONG_CHROMIUM } : {}) })
let page
try {
  const context = await browser.newContext({ locale: "zh-CN" })
  await context.addCookies([{ name: "NEXT_LOCALE", value: "zh", url: base }])
  await context.addInitScript(({ access }) => { localStorage.setItem("accessToken", access); localStorage.setItem("refreshToken", "fixture") }, { access: token })
  await context.route("**/v1/**", async route => {
    const url = new URL(route.request().url())
    const path = url.pathname
    const method = route.request().method()
    let status = 200
    let body = {}
    if (path === "/v1/users/current") body = { id: 1, username: "fixture", email: "fixture@example.test" }
    else if (path === "/v1/upgradeOperations:active") { status = 404; body = { error: { code: "NOT_FOUND", message: "none" } } }
    else if (path === "/v1/users/current/notifications") body = { results: [], nextPageToken: "" }
    else if (path === "/v1/users/current/notifications:unreadCount") body = { unreadCount: 0 }
    else if (path === "/v1/aiTasks" && method === "GET") body = { tasks: [task], nextBefore: "" }
    else if (path === "/v1/aiTasks/123" && method === "GET") body = task
    else if (path === "/v1/aiTasks/123/graph") body = { nodes: [{ id: "8", kind: "intent", state: "running", priority: 5, payload: { summary: "检查资产" }, createdAt: stamp }], edges: [], assets: [{ nodeId: "8", assetId: "55", assetType: "service", label: "https://fixture.example/" }], truncated: false }
    else if (path === "/v1/aiTasks/123/activities") body = { activities: [] }
    else if (path === "/v1/aiTasks/123/actions") { actionBody = route.request().postDataJSON(); body = { ...task, status: "paused" } }
    else if (path === "/v1/aiSchedules" && method === "GET") body = { schedules: [] }
    else if (path === "/v1/aiSchedules" && method === "POST") { scheduleBody = route.request().postDataJSON(); status = 201; body = { ...scheduleBody, id: "99", name: "aiSchedules/99", status: "waiting", lastError: "", lastTransitionAt: null, createdAt: stamp, updatedAt: stamp, serverTime: stamp, nextStart: null, nextEnd: null } }
    else if (path === "/v1/aiTaskToolExecutions") body = { executions: [{ id: "17", taskId: "123", conversation: "测试任务", runId: "run", tool: "insert_assets", input: "{}", output: "完成", status: "succeeded", createdAt: stamp }], nextBefore: "" }
    else if (path === "/v1/aiToolExecutions") body = { executions: [], nextBefore: "" }
    else if (path === "/v1/aiAgentSettings" && method === "GET") body = { agents: [
      { name: "aiAgentSettings/assistant", key: "assistant", enabled: true, maxTurns: 30, runSeconds: 600, additionalPrompt: "" },
      { name: "aiAgentSettings/planner", key: "planner", enabled: true, maxTurns: 0, runSeconds: 0, additionalPrompt: "" },
      { name: "aiAgentSettings/worker", key: "worker", enabled: true, maxTurns: 0, runSeconds: 1200, additionalPrompt: "" },
    ] }
    else if (path === "/v1/aiAgentSettings/assistant" && method === "PUT") { agentBody = route.request().postDataJSON(); body = { name: "aiAgentSettings/assistant", key: "assistant", ...agentBody } }
    else if (path === "/v1/aiToolSettings" && method === "GET") body = { tools: ["assistant", "planner", "worker"].flatMap(agent => ["search_platform_assets", "insert_assets"].map(tool => ({ name: `aiToolSettings/${agent}/${tool}`, agentKey: agent, toolKey: tool, enabled: agent === "planner" && tool === "search_platform_assets" ? plannerSearch : true }))) }
    else if (path === "/v1/aiToolSettings/planner/search_platform_assets" && method === "PUT") { toolBody = route.request().postDataJSON(); plannerSearch = toolBody.enabled; body = { name: "aiToolSettings/planner/search_platform_assets", agentKey: "planner", toolKey: "search_platform_assets", ...toolBody } }
    else if (path === "/v1/aiSkills" && method === "GET") body = { skills: [] }
    else if (path === "/v1/aiSkills" && method === "POST") { skillBody = route.request().postDataJSON(); status = 201; body = { id: "fixture", name: "aiSkills/fixture", ...skillBody, createdAt: stamp, updatedAt: stamp } }
    else if (path === "/v1/aiMcpServers" && method === "GET") body = { servers: [] }
    else if (path === "/v1/aiMcpServers" && method === "POST") { mcpBody = route.request().postDataJSON(); status = 201; body = { id: "fixture", name: "aiMcpServers/fixture", displayName: mcpBody.displayName, endpoint: mcpBody.endpoint, enabled: mcpBody.enabled, insecure: mcpBody.insecure, agents: mcpBody.agents, hasHeaders: true, createdAt: stamp, updatedAt: stamp } }
    else if (path === "/v1/targets") body = { results: [], total: 0, totalSize: 0, nextPageToken: "" }
    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  })
  page = await context.newPage()
  page.setDefaultTimeout(10000)
  page.on("pageerror", error => console.error("页面异常:", error.message))
  page.on("console", message => { if (message.type() === "error") console.error("浏览器错误:", message.text()) })
  await page.goto(`${base}/ai/schedules/`)
  await page.getByRole("heading", { name: "AI 计划任务" }).waitFor()
  await page.getByRole("button", { name: "新建计划" }).click()
  await page.locator("#schedule-name").fill("测试计划")
  await page.getByText("测试任务 · #123").click()
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.waitForTimeout(400)
  assert.equal(scheduleBody?.displayName, "测试计划")
  assert.deepEqual(scheduleBody?.taskIds, ["123"])
  assert.deepEqual(scheduleBody?.weekdays, [1, 2, 3, 4, 5])
  await page.goto(`${base}/ai/tasks/123/`)
  await page.getByText("https://fixture.example/").waitFor()
  await page.getByRole("button", { name: "暂停", exact: true }).click()
  await page.waitForTimeout(200)
  assert.equal(actionBody?.action, "pause")
  await page.goto(`${base}/ai/tool-executions/`)
  await page.getByRole("tab", { name: "任务", exact: true }).click()
  await page.getByText("insert_assets", { exact: true }).waitFor()
  await page.goto(`${base}/settings/ai/agents/`)
  await page.getByText("对话助手", { exact: true }).waitFor()
  await page.getByRole("button", { name: "配置", exact: true }).first().click()
  await page.locator("#ai-agent-prompt").fill("模拟管理要求")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.waitForTimeout(200)
  assert.equal(agentBody?.additionalPrompt, "模拟管理要求")
  await page.goto(`${base}/settings/ai/tools/`)
  await page.getByRole("switch", { name: "任务规划者 - 查询平台资产" }).click()
  await page.waitForTimeout(200)
  assert.equal(toolBody?.enabled, false)
  await page.goto(`${base}/settings/ai/skills/`)
  await page.getByRole("button", { name: "新建 Skill" }).click()
  await page.locator("#skill-name").fill("示例技能")
  await page.locator("#skill-instructions").fill("按资产证据进行分析。")
  await page.getByText("对话助手", { exact: true }).click()
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.waitForTimeout(200)
  assert.deepEqual(skillBody?.agents, ["assistant"])
  await page.goto(`${base}/settings/ai/mcp/`)
  await page.getByRole("button", { name: "添加服务器" }).click()
  await page.locator("#mcp-name").fill("fixture")
  await page.locator("#mcp-endpoint").fill("https://mcp.example.test/api")
  await page.locator("#mcp-headers").fill('{"Authorization":"Bearer fixture"}')
  await page.getByText("对话助手", { exact: true }).click()
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.waitForTimeout(200)
  assert.deepEqual(mcpBody?.agents, ["assistant"])
  assert.equal(mcpBody?.headers?.Authorization, "Bearer fixture")
  console.log("AI 计划、任务、工具记录、Agent、工具、Skill 和 MCP 设置浏览器交互通过（隔离 HTTP fixture）。")
} catch (error) {
  if (page) console.error("页面地址:", page.url(), "页面文本:", (await page.locator("body").innerText()).slice(0, 2000))
  throw error
} finally { await browser.close() }
