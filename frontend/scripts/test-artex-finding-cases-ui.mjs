// A lightweight browser fixture: it serves only the case component and mocks
// all API calls, so it never connects to or mutates the running application.
import { chromium, expect } from "@playwright/test"
import { spawn } from "node:child_process"
import { mkdtemp, readFile, writeFile, symlink, open, realpath } from "node:fs/promises"
import os from "node:os"
import path from "node:path"
import { createRequire } from "node:module"
import { fileURLToPath } from "node:url"

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
const requireFromVitest = createRequire(await realpath(path.join(frontend, "node_modules/vitest/package.json")))
const viteBin = path.join(path.dirname(requireFromVitest.resolve("vite/package.json")), "bin/vite.js")
const temporary = await mkdtemp(path.join(os.tmpdir(), "skyshield-case-browser-"))
const port = Number(process.env.SKYSHIELD_CASE_TEST_PORT || 33326)
const origin = `http://127.0.0.1:${port}`
const zh = JSON.parse(await readFile(path.join(frontend, "messages/zh.json"), "utf8"))
await symlink(path.join(frontend, "node_modules"), path.join(temporary, "node_modules"))
await writeFile(path.join(temporary, "index.html"), '<!doctype html><html><head><meta charset="utf-8"><style>[data-base-ui-inert]{pointer-events:none!important}[data-slot="dialog-overlay"]{position:fixed;inset:0;background:#0004;z-index:50}[data-slot="dialog-content"]{position:fixed;z-index:51;top:5vh;left:50%;transform:translateX(-50%);width:min(90vw,900px);max-height:90vh;overflow:auto;background:white;padding:20px}button,input,textarea,select{font:inherit}label{display:block}</style></head><body><div id="root"></div><script type="module" src="/entry.tsx"></script></body></html>')
await writeFile(path.join(temporary, "entry.tsx"), `import React from "react";
import {createRoot} from "react-dom/client";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import {TaskFindings} from ${JSON.stringify(path.join(frontend, "components/ai/artex/task-findings.tsx"))};
createRoot(document.getElementById("root")!).render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><TaskFindings taskId="30"/></QueryClientProvider>);
`)
await writeFile(path.join(temporary, "i18n.ts"), `const messages=${JSON.stringify({ aiFindingCases: zh.aiFindingCases, aiFindingDuplicates: zh.aiFindingDuplicates, aiFindingSuggestions: zh.aiFindingSuggestions, aiFindings: zh.aiFindings, common: zh.common })};
export function useTranslations(namespace:string){return (key:string,params:Record<string,unknown>={})=>{let value:any=namespace.split('.').reduce((v,k)=>v?.[k],messages as any);for(const part of key.split('.'))value=value?.[part];return typeof value==='string'?value.replace(/\\{(\\w+)\\}/g,(_:string,k:string)=>String(params[k]??'')):key}};
`)
await writeFile(path.join(temporary, "auth.ts"), `export const useAuth=()=>({data:{user:{id:7}}});`)
await writeFile(path.join(temporary, "api.ts"), `async function request(method:string,url:string,body?:unknown,options?:any){const params=new URLSearchParams();for(const [key,value] of Object.entries(options?.params||{}))if(value!==undefined)params.set(key,String(value));const response=await fetch('/v1'+url+(params.size?'?'+params:''),{method,headers:{'content-type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});if(!response.ok)throw new Error('HTTP '+response.status);return {data:await response.json()}};export const api={get:(u:string,o?:any)=>request('GET',u,undefined,o),post:(u:string,b?:unknown)=>request('POST',u,b),put:(u:string,b?:unknown)=>request('PUT',u,b)};`)
await writeFile(path.join(temporary, "link.tsx"), `import React from 'react';export default function Link({href,children,...props}:any){return <a href={href} {...props}>{children}</a>}`)
await writeFile(path.join(temporary, "markdown.tsx"), `import React from 'react';export function Markdown({text}:{text:string}){return <div>{text}</div>}`)
await writeFile(path.join(temporary, "icons.tsx"), `import React from 'react';const Icon=()=> <span aria-hidden="true">×</span>;export const Loader2Icon=Icon;export const semanticIcons={action:{cancel:Icon}};`)
await writeFile(path.join(temporary, "toast.ts"), `export const toast={success:()=>{},error:()=>{}};`)
await writeFile(path.join(temporary, "retest.tsx"), `export function FindingRetestPanel(){return null}`)
await writeFile(path.join(temporary, "traffic.tsx"), `export function FindingTrafficPanel(){return null}`)
const aliases = [
  ["next-intl", "i18n.ts"], ["next/link", "link.tsx"], ["sonner", "toast.ts"],
  ["@/hooks/use-auth", "auth.ts"], ["@/lib/api-client", "api.ts"],
  ["@/components/settings/ai/artex-markdown", "markdown.tsx"], ["@/components/icons", "icons.tsx"],
  ["@/components/ai/finding-retest-panel", "retest.tsx"], ["@/components/ai/artex/task-traffic", "traffic.tsx"],
].map(([find, replacement]) => ({ find, replacement: path.join(temporary, replacement) }))
aliases.push({ find: "@", replacement: frontend })
await writeFile(path.join(temporary, "vite.config.mjs"), `export default ${JSON.stringify({
  root: temporary,
  cacheDir: path.join(temporary, "cache"),
  resolve: { alias: aliases, dedupe: ["react", "react-dom", "@tanstack/react-query"] },
  esbuild: { jsx: "automatic" },
  server: { host: "127.0.0.1", port, strictPort: true, fs: { allow: [temporary, frontend] } },
})};`)
const log = await open(path.join(temporary, "vite.log"), "w")
const server = spawn(process.execPath, ["--no-concurrent-recompilation", viteBin, "--config", path.join(temporary, "vite.config.mjs")], {
  cwd: frontend, env: { ...process.env, NODE_OPTIONS: "--max-old-space-size=768" }, detached: true, stdio: ["ignore", log.fd, log.fd],
})
let browser
try {
  let ready = false
  for (let i = 0; i < 100; i++) {
    if (server.exitCode !== null) throw new Error(`fixture server exited: ${server.exitCode}`)
    try { if ((await fetch(origin)).ok) { ready = true; break } } catch {}
    await new Promise(resolve => setTimeout(resolve, 200))
  }
  if (!ready) throw new Error("fixture server did not start")
  browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1440, height: 1000 } })
  const findings = [
    { id: "1", task_id: "30", name: "测试发现甲", vulnclass: "fixture", severity: "high", status: "pending", summary: "摘要甲", evidence: "证据甲", assets: [], inherited: false },
    { id: "2", task_id: "30", name: "测试发现乙", vulnclass: "fixture", severity: "medium", status: "pending", summary: "摘要乙", evidence: "证据乙", assets: [], inherited: false },
  ]
  const own = { id: "8", task_id: "30", task_name: "当前任务", title: "测试归并组", reason: "相同问题", severity: "", version: 2, report_version: 0, has_report: false, member_count: 2, inherited: false, created_at: new Date().toISOString(), members: findings, events: [{ id: "20", action: "merge", actor: "human", reason: "历史归并理由", finding_ids: ["1", "2"], created_at: new Date().toISOString() }] }
  const inherited = { ...own, id: "9", task_id: "10", task_name: "来源任务", title: "继承归并组", inherited: true, events: [] }
  const suggestion = { id: "20", task_id: "30", task_name: "当前任务", title: "待确认建议", reason: "同一端点但根因待确认", inherited: false, created_at: new Date().toISOString(), members: [...findings, { ...findings[1], id: "3", name: "测试发现丙" }] }
  let pendingSuggestions = [suggestion]
  const requests = []
  await context.route("**/v1/**", async route => {
    const request = route.request(), url = new URL(request.url()), pathname = url.pathname
    requests.push({ pathname, method: request.method(), body: request.postDataJSON?.() })
    let data = { updated: true }
    if (request.method() === "GET" && pathname.endsWith("/findingCaseSuggestions")) data = { items: pendingSuggestions, total: pendingSuggestions.length, page: 0, size: 25 }
    else if (request.method() === "POST" && pathname.endsWith("/findingCaseSuggestions/20/resolve")) pendingSuggestions = []
    else if (request.method() === "GET" && pathname.endsWith("/findingCases")) data = { items: [own, inherited], total: 2, page: 0, size: 25 }
    else if (request.method() === "GET" && pathname.endsWith("/findingCases/8")) data = own
    else if (request.method() === "GET" && pathname.endsWith("/findingCases/9")) data = inherited
    else if (request.method() === "GET" && pathname.endsWith("/findings")) data = { items: findings, total: 2, page: 0, size: 25 }
    else if (request.method() === "GET" && pathname.endsWith("/findings/1/duplicates")) data = { items: [{ ...findings[1], score: 7, tier: "strong", hit_reasons: ["same_type", "asset_overlap"] }], total: 1, strong_total: 1, page: 0, size: 20 }
    else if (request.method() === "GET" && pathname.endsWith("/findings/1")) data = findings[0]
    await route.fulfill({ json: data })
  })
  const page = await context.newPage(), errors = []
  page.setDefaultTimeout(10000)
  page.on("pageerror", error => errors.push(String(error)))
  await page.goto(origin, { waitUntil: "domcontentloaded", timeout: 60000 })
  await expect(page.getByText("测试归并组", { exact: true })).toBeVisible({ timeout: 60000 })
  await page.getByRole("button", { name: "归并发现" }).click()
  let dialog = page.getByRole("dialog")
  await dialog.getByLabel("归并组名称").fill("新归并组")
  await dialog.getByLabel("归并理由").fill("相同根因")
  await dialog.getByRole("checkbox", { name: /测试发现甲/ }).check()
  await dialog.getByRole("checkbox", { name: /测试发现乙/ }).check()
  await dialog.getByRole("button", { name: "确认归并" }).click()
  await expect.poll(() => requests.some(item => item.method === "POST" && item.pathname.endsWith("/findingCases") && item.body?.finding_ids?.length === 2)).toBe(true)
  await expect(dialog).toHaveCount(0)
  await page.locator("article").filter({ hasText: "测试归并组" }).getByRole("button", { name: "查看归并组" }).click()
  dialog = page.getByRole("dialog")
  await expect(dialog.getByText("测试发现甲", { exact: true })).toBeVisible()
  const row = dialog.getByText("测试发现甲", { exact: true }).locator("..").locator("..")
  await row.getByRole("button", { name: "查看成员证据" }).click()
  await expect(dialog.getByText("证据甲")).toBeVisible()
  await dialog.getByText("操作记录").click()
  await expect(dialog.getByText("历史归并理由")).toBeVisible()
  await dialog.getByText("编辑统一报告").click()
  await dialog.getByLabel("归并组名称").fill("统一报告标题")
  await dialog.getByLabel("评级依据").fill("已确认影响")
  await dialog.getByLabel("统一报告", { exact: true }).fill("报告内容")
  await dialog.getByRole("button", { name: "保存报告" }).click()
  await expect.poll(() => requests.some(item => item.method === "PUT" && item.body?.report === "报告内容" && item.body?.version === 2)).toBe(true)
  await expect(dialog.getByRole("button", { name: "AI 审查整组" })).toHaveCount(0)
  const reopenedRow = dialog.getByText("测试发现甲", { exact: true }).locator("..").locator("..")
  await reopenedRow.getByRole("button", { name: "移出", exact: true }).click()
  await dialog.getByRole("textbox", { name: "移出理由" }).fill("不是同一问题")
  await dialog.getByRole("button", { name: "确认移出" }).click()
  await expect.poll(() => requests.some(item => item.pathname.endsWith("/members/1/detach") && item.body?.reason === "不是同一问题")).toBe(true)
  await expect(dialog).toHaveCount(0)
  await page.locator("article").filter({ hasText: "继承归并组" }).getByRole("button", { name: "查看归并组" }).click()
  dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("link", { name: "来源任务" })).toHaveAttribute("href", "/ai/tasks/10/")
  await expect(dialog.getByRole("button", { name: "移出", exact: true })).toHaveCount(0)
  await expect(dialog.getByText("操作记录")).toHaveCount(0)
  await dialog.getByRole("button", { name: "关闭" }).click()
  await expect(dialog).toHaveCount(0)
  await page.getByRole("button", { name: "查找与 测试发现甲 相似的发现" }).click()
  dialog = page.getByRole("dialog")
  await expect(dialog.getByText("较强线索")).toBeVisible()
  await dialog.getByRole("checkbox", { name: /测试发现乙/ }).check()
  await dialog.getByRole("button", { name: "归并所选 2 条" }).click()
  await expect(page.getByRole("dialog").getByText("选择至少两条本任务发现", { exact: false })).toBeVisible()
  dialog = page.getByRole("dialog")
  await expect(dialog.getByLabel("归并组名称")).toHaveValue("测试发现甲")
  await dialog.getByLabel("归并理由").fill("相同根因已核验")
  await dialog.getByRole("button", { name: "确认归并" }).click()
  await expect.poll(() => requests.some(item => item.method === "POST" && item.pathname.endsWith("/findingCases") && item.body?.reason === "相同根因已核验" && item.body?.finding_ids?.length === 2)).toBe(true)
  await expect(dialog).toHaveCount(0)
  await page.locator("article").filter({ hasText: "待确认建议" }).getByRole("button", { name: "审查建议" }).click()
  dialog = page.getByRole("dialog")
  await dialog.getByRole("checkbox", { name: "保留 测试发现丙" }).uncheck()
  const suggestionRow = dialog.getByText("测试发现甲", { exact: true }).locator("..")
  await suggestionRow.getByRole("button", { name: "查看证据" }).click()
  await expect(dialog.getByText("证据甲")).toBeVisible()
  await dialog.getByRole("button", { name: "确认归并" }).click()
  await expect.poll(() => requests.some(item => item.pathname.endsWith("/findingCaseSuggestions/20/resolve") && item.body?.accept === true && item.body?.exclude?.includes("3"))).toBe(true)
  await expect(page.getByRole("button", { name: "发起 AI 审查" })).toHaveCount(0)
  expect(requests.some(item => item.pathname.includes("findingCaseReviews") || item.pathname === "/v1/aiFindingCaseReviews")).toBe(false)
  expect(errors).toEqual([])
  console.log(JSON.stringify({ passed: true, merge: true, evidence: true, report: true, detach: true, inheritedReadOnly: true, duplicateHints: true, suggestionReview: true, aiReviewRemoved: true }))
} finally {
  if (browser) await browser.close()
  if (server.exitCode === null) {
    try { process.kill(-server.pid, "SIGTERM") } catch {}
    await new Promise(resolve => { server.once("exit", resolve); setTimeout(resolve, 3000).unref() })
    if (server.exitCode === null) { try { process.kill(-server.pid, "SIGKILL") } catch {} }
  }
  await log.close()
}
