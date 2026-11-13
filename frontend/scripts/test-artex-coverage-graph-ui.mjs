// This browser fixture mocks every API response and never contacts the running app.
import { chromium, expect } from "@playwright/test"
import { spawn } from "node:child_process"
import { mkdtemp, writeFile, symlink, open, realpath, readFile } from "node:fs/promises"
import os from "node:os"
import path from "node:path"
import { createRequire } from "node:module"
import { fileURLToPath } from "node:url"

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
const requireFromVitest = createRequire(await realpath(path.join(frontend, "node_modules/vitest/package.json")))
const viteBin = path.join(path.dirname(requireFromVitest.resolve("vite/package.json")), "bin/vite.js")
const temporary = await mkdtemp(path.join(os.tmpdir(), "skyshield-coverage-browser-"))
const port = 33329, origin = `http://127.0.0.1:${port}`
await symlink(path.join(frontend, "node_modules"), path.join(temporary, "node_modules"))
await writeFile(path.join(temporary, "index.html"), `<!doctype html><html><head><meta charset="utf-8"><style>
html,body,#root{margin:0;width:100%;height:100%;font:14px sans-serif}button{font:inherit;cursor:pointer}
#root>div>div:nth-of-type(2){height:650px;width:1100px;position:relative}
[data-slot="sheet-overlay"]{position:fixed;inset:0;background:#0004;z-index:40}
[data-slot="sheet-content"]{position:fixed;right:0;top:0;bottom:0;width:400px;background:white;z-index:50;overflow:auto;padding:16px}
[data-slot="sheet-header"]{padding:10px 0}pre{white-space:pre-wrap;overflow-wrap:anywhere}
</style></head><body><div id="root"></div><script type="module" src="/entry.tsx"></script></body></html>`)
await writeFile(path.join(temporary, "entry.tsx"), `import React from "react";import {createRoot} from "react-dom/client";import {QueryClient,QueryClientProvider} from "@tanstack/react-query";import {TaskCoverageGraph} from ${JSON.stringify(path.join(frontend, "components/ai/artex/task-coverage-graph.tsx"))};import {AssetCoverageGraph} from ${JSON.stringify(path.join(frontend, "components/shared/asset-coverage/asset-coverage-graph.tsx"))};const client=new QueryClient({defaultOptions:{queries:{retry:false}}});const params=new URLSearchParams(location.search);createRoot(document.getElementById("root")!).render(<QueryClientProvider client={client}>{params.has("root") ? <AssetCoverageGraph scope={{kind:"root",id:"17",organizationId:Number(params.get("organization")||3)}}/> : <TaskCoverageGraph taskId="30" coverageEnabled={!location.search.includes('off=1')} sourceNames={{"8":"来源任务"}}/>}</QueryClientProvider>);`)
const messages = JSON.parse(await readFile(path.join(frontend, "messages/zh.json"), "utf8"))
await writeFile(path.join(temporary, "i18n.ts"), `const messages=${JSON.stringify(messages)};export function useTranslations(namespace:string){return (key:string,vars:Record<string,unknown>={})=>{let v:any=messages;for(const part of (namespace+'.'+key).split('.'))v=v?.[part];return typeof v==='string'?v.replace(/\\{(\\w+)\\}/g,(_,name)=>String(vars[name]??'')):key}}`)
await writeFile(path.join(temporary, "api.ts"), `export const api={async get(path:string,config?:{params?:Record<string,string>}){const query=new URLSearchParams(config?.params??{});const r=await fetch('/v1'+path+(query.size?'?'+query:''));if(!r.ok)throw new Error('HTTP '+r.status);return {data:await r.json()}}}`)
await writeFile(path.join(temporary, "auth.ts"), `import * as React from 'react';let id=7;const listeners=new Set<()=>void>();(window as any).fixtureSwitchUser=(next:number)=>{id=next;listeners.forEach(fn=>fn())};export function useAuth(){const user=React.useSyncExternalStore(fn=>{listeners.add(fn);return()=>listeners.delete(fn)},()=>id);return {data:{user:{id:user}}}}`)
await writeFile(path.join(temporary, "link.tsx"), `export default function Link({href,children,...props}:any){return <a href={href} {...props}>{children}</a>}`)
const aliases = [["next/link", "link.tsx"], ["next-intl", "i18n.ts"], ["@/lib/api-client", "api.ts"], ["@/hooks/use-auth", "auth.ts"]].map(([find, replacement]) => ({ find, replacement: path.join(temporary, replacement) }))
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
  let denyGraph = false
  const assets = Array.from({ length: 25 }, (_, index) => ({ key: `a:${index + 1}`, kind: "subdomain", label: `sub-${String(index).padStart(2, "0")}.example.test`, tested: index === 24, in_scope: true, asset_id: String(index + 1) }))
  await context.route("**/v1/**", async route => {
    const url = new URL(route.request().url()); requests.push(url.pathname + url.search)
    if (url.pathname.endsWith("/coverageGraph")) {
      if (denyGraph) return route.fulfill({ status: 403, json: { error: "forbidden" } })
      return route.fulfill({ json: { nodes: [{ key: "r:example.test", kind: "root_domain", label: "example.test", tested: false, in_scope: false }, ...assets], edges: assets.map(item => ({ src: item.key, dst: "r:example.test" })), truncated: false } })
    }
    if (url.pathname.endsWith("/coverageRefs")) return route.fulfill({ json: { intents: [], facts: [{ id: "3", kind: "fact", state: "confirmed", summary: "根域名确认事实", source_task_id: "8", task_name: "来源任务" }], findings: [] } })
    if (url.pathname.endsWith("/assetRefs")) return route.fulfill({ json: { intents: [], facts: [{ id: "3", kind: "fact", state: "confirmed", summary: "已确认的关联事实", inherited: true, source_task_id: "8" }], findings: [] } })
    throw new Error(`unexpected fixture request: ${url}`)
  })
  const page = await context.newPage(), errors = []; page.setDefaultTimeout(10000); page.on("pageerror", error => { errors.push(String(error)); console.error("fixture page error:", String(error)) })
  await page.goto(origin, { waitUntil: "domcontentloaded", timeout: 60000 })
  await expect(page.getByText("范围内 25")).toBeVisible()
  await expect(page.locator(".react-flow__node")).toHaveCount(22)
  await page.getByText("还有 5 个子域名").click()
  await expect(page.getByText("未展示的子域名（5）")).toBeVisible()
  await page.getByRole("button", { name: "sub-19.example.test" }).click()
  await expect(page.getByText("已确认的关联事实")).toBeVisible()
  await expect(page.getByText("继承自 来源任务")).toBeVisible()
  await expect(page.getByText("原始数据")).toBeVisible()
  await expect.poll(() => requests.some(item => item.includes("assetRefs?asset_id=20"))).toBe(true)
  await page.getByRole("button", { name: "关闭" }).click()
  await page.getByText("还有 5 个子域名").click()
  await page.getByRole("button", { name: "展示更多（+20）" }).click()
  await expect(page.locator(".react-flow__node")).toHaveCount(26)
  await expect(page.getByText("还有 5 个子域名")).toHaveCount(0)
  denyGraph = true
  await page.getByRole("button", { name: "刷新" }).click()
  await expect(page.getByRole("alert")).toContainText("加载覆盖图失败")
  await expect(page.getByText("sub-24.example.test")).toHaveCount(0)
  await expect(page.getByText("范围内 25")).toHaveCount(0)
  denyGraph = false
  await page.getByRole("button", { name: "刷新" }).click()
  await expect(page.getByText("sub-24.example.test")).toBeVisible()
  await page.getByText("sub-24.example.test").click()
  await expect(page.getByText("已确认的关联事实")).toBeVisible()
  denyGraph = true
  await page.evaluate(() => window.fixtureSwitchUser(8))
  await expect(page.getByRole("alert")).toContainText("加载覆盖图失败")
  await expect(page.getByText("已确认的关联事实")).toHaveCount(0)
  await expect(page.getByText("sub-24.example.test")).toHaveCount(0)
  denyGraph = false
  await page.goto(`${origin}/?off=1`, { waitUntil: "domcontentloaded" })
  await expect(page.getByText("范围内 25")).toBeVisible()
  await expect(page.getByText("有确认事实 1")).toHaveCount(0)
  await page.getByText("sub-00.example.test").click()
  await expect(page.getByText("任务范围内")).toBeVisible()
  await page.goto(`${origin}/?root=1&organization=3`, { waitUntil: "domcontentloaded" })
  await expect(page.getByText("范围内 25")).toBeVisible()
  await page.getByText("sub-24.example.test").click()
  await expect(page.getByText("根域名确认事实")).toBeVisible()
  await expect(page.getByRole("link", {name:"来源任务"})).toHaveAttribute("href", "/ai/tasks/8/")
  expect(requests.some(item => item.includes("/assetEntities/17/coverageGraph?organizationId=3"))).toBe(true)
  expect(requests.some(item => item.includes("/assetEntities/17/coverageRefs?assetId=25&organizationId=3"))).toBe(true)
  await page.getByRole("button", { name: "关闭" }).click()
  await page.screenshot({path:path.join(temporary,"root-coverage.png"),fullPage:true})
  await page.goto(`${origin}/?root=1&organization=4`, { waitUntil: "domcontentloaded" })
  await expect(page.getByText("范围内 25")).toBeVisible()
  expect(requests.some(item => item.includes("/assetEntities/17/coverageGraph?organizationId=4"))).toBe(true)
  expect(errors).toEqual([])
  console.log(JSON.stringify({ passed: true, fold: true, hiddenAsset: true, references: true, expansion: true, accessLoss: true, accountSwitch: true, coverageDisabled: true, rootCoverage: true, organizationScope: true, screenshot: path.join(temporary,"root-coverage.png") }))
} finally {
  if (browser) await browser.close()
  if (server.exitCode === null) { try { process.kill(-server.pid, "SIGTERM") } catch {}; await new Promise(resolve => { server.once("exit", resolve); setTimeout(resolve, 3000).unref() }); if (server.exitCode === null) { try { process.kill(-server.pid, "SIGKILL") } catch {} } }
  await log.close()
}
