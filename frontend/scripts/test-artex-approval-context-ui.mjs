// This fixture mocks every API response and never contacts the running app.
import { chromium, expect } from "@playwright/test"
import { spawn } from "node:child_process"
import { mkdtemp, writeFile, symlink, open, realpath } from "node:fs/promises"
import os from "node:os"
import path from "node:path"
import { createRequire } from "node:module"
import { fileURLToPath } from "node:url"

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
const requireFromVitest = createRequire(await realpath(path.join(frontend, "node_modules/vitest/package.json")))
const viteBin = path.join(path.dirname(requireFromVitest.resolve("vite/package.json")), "bin/vite.js")
const temporary = await mkdtemp(path.join(os.tmpdir(), "skyshield-approval-browser-"))
const port = 33328, origin = `http://127.0.0.1:${port}`
await symlink(path.join(frontend, "node_modules"), path.join(temporary, "node_modules"))
await writeFile(path.join(temporary, "index.html"), '<!doctype html><html><head><meta charset="utf-8"><style>button{font:inherit}[data-slot="scroll-area-viewport"]{height:300px;overflow:auto;border:1px solid #888;padding:10px}pre{white-space:pre-wrap}</style></head><body><div id="root"></div><script type="module" src="/entry.tsx"></script></body></html>')
await writeFile(path.join(temporary, "entry.tsx"), `import React from "react";import {createRoot} from "react-dom/client";import {TaskApprovalExecution} from ${JSON.stringify(path.join(frontend, "components/ai/artex/task-approval-execution.tsx"))};createRoot(document.getElementById("root")!).render(<TaskApprovalExecution taskId="30"/>);`)
await writeFile(path.join(temporary, "auth.ts"), `export const useAuth=()=>({data:{user:{id:7}}});`)
await writeFile(path.join(temporary, "i18n.ts"), `export function useTranslations(){return (key:string)=>key}`)
await writeFile(path.join(temporary, "icons.tsx"), `import React from 'react';const Icon=()=> <span aria-hidden="true">×</span>;export const Loader2Icon=Icon;export const semanticIcons={action:{cancel:Icon}};`)
await writeFile(path.join(temporary, "toast.ts"), `export const toast={success:()=>{},error:()=>{}};`)
await writeFile(path.join(temporary, "markdown.tsx"), `import React from 'react';export function Markdown({text}:{text:string}){return <div>{text}</div>}`)
await writeFile(path.join(temporary, "approval.tsx"), `export function ApprovalDetail(){return null}`)
await writeFile(path.join(temporary, "service.ts"), `async function get(path:string){const response=await fetch('/v1'+path);if(!response.ok)throw new Error('HTTP '+response.status);return response.json()};export const api={interceptExecution:(id:number)=>get('/aiApprovals/'+id+'/execution'),interceptExecutionContext:(id:number,before?:number,after?:number)=>get('/aiApprovals/'+id+'/execution/context?'+new URLSearchParams(Object.entries({before,after}).filter(([,v])=>v!==undefined).map(([k,v])=>[k,String(v)]))),activityDetail:(seq:number,task?:string)=>get('/aiTasks/'+task+'/activities/'+seq),interceptGetOne:()=>Promise.reject(),interceptDecide:()=>Promise.reject()};`)
const aliases = [["next-intl", "i18n.ts"], ["sonner", "toast.ts"], ["@/hooks/use-auth", "auth.ts"], ["@/services/artex-ai.service", "service.ts"], ["@/components/settings/ai/artex-markdown", "markdown.tsx"], ["@/components/ai/artex/approval-records", "approval.tsx"], ["@/components/icons", "icons.tsx"]].map(([find, replacement]) => ({ find, replacement: path.join(temporary, replacement) }))
aliases.push({ find: "@", replacement: frontend })
await writeFile(path.join(temporary, "vite.config.mjs"), `export default ${JSON.stringify({ root: temporary, cacheDir: path.join(temporary, "cache"), resolve: { alias: aliases, dedupe: ["react", "react-dom"] }, esbuild: { jsx: "automatic" }, server: { host: "127.0.0.1", port, strictPort: true, fs: { allow: [temporary, frontend] } } })};`)
const log = await open(path.join(temporary, "vite.log"), "w")
const server = spawn(process.execPath, ["--no-concurrent-recompilation", viteBin, "--config", path.join(temporary, "vite.config.mjs")], { cwd: frontend, env: { ...process.env, NODE_OPTIONS: "--max-old-space-size=768" }, detached: true, stdio: ["ignore", log.fd, log.fd] })
let browser
try {
  let ready = false
  for (let i = 0; i < 100; i++) { if (server.exitCode !== null) throw new Error(`fixture server exited: ${server.exitCode}`); try { if ((await fetch(origin)).ok) { ready = true; break } } catch {}; await new Promise(resolve => setTimeout(resolve, 200)) }
  if (!ready) throw new Error("fixture server did not start")
  browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1200, height: 900 } }), requests = []
  const activity = (seq, kind, summary, extra = {}) => ({ seq, worker: "work#1", kind, ts: "2026-01-01T00:00:00Z", summary, ...extra })
  const use = activity(100, "tool_use", "fixture command", { tool: "Bash", tool_use_id: "call-1" }), result = activity(101, "tool_result", "fixture result", { tool: "Bash", tool_use_id: "call-1" })
  await context.route("**/v1/**", async route => {
    const url = new URL(route.request().url());requests.push(url.pathname+url.search)
    let data = { detail: url.pathname.endsWith("/100") ? "命令输入内容" : "工具输出内容" }
    if (url.pathname.endsWith("/execution")) data = { conversation_id: null, task_id: "30", session: "intent:9", seq: 100, items: [use, result] }
    else if (url.pathname.endsWith("/context")) data = url.searchParams.has("before") ? { items: [activity(90, "text", "更早的执行")], has_before: false, has_after: true } : { items: [activity(99, "text", "相邻执行"), use, result, activity(102, "text", "后续执行")], has_before: true, has_after: false }
    await route.fulfill({ json: data })
  })
  const page = await context.newPage(), errors = [];page.setDefaultTimeout(10000);page.on("pageerror", error => errors.push(String(error)))
  await page.goto(`${origin}/?tab=activity&approval=50`, { waitUntil: "domcontentloaded", timeout: 60000 })
  await expect(page.getByLabel("定位的工具调用 #100")).toBeVisible()
  await expect(page.getByText("命令输入内容")).toBeVisible()
  await page.getByRole("button", { name: "加载更早记录" }).click()
  await expect(page.getByText("更早的执行")).toBeVisible()
  await expect.poll(() => requests.some(item => item.includes("before=99"))).toBe(true)
  await page.getByRole("button", { name: "关闭定位" }).click()
  expect(new URL(page.url()).searchParams.has("approval")).toBe(false)
  expect(errors).toEqual([])
  console.log(JSON.stringify({ passed: true, focusedCall: true, fullDetails: true, previousContext: true, close: true }))
} finally {
  if (browser) await browser.close()
  if (server.exitCode === null) { try { process.kill(-server.pid, "SIGTERM") } catch {}; await new Promise(resolve => { server.once("exit", resolve); setTimeout(resolve, 3000).unref() }); if (server.exitCode === null) { try { process.kill(-server.pid, "SIGKILL") } catch {} } }
  await log.close()
}
