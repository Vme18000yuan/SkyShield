// Isolated UI fixture: all API responses below are local mock data.
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
const temporary = await mkdtemp(path.join(os.tmpdir(), "skyshield-token-browser-"))
const port = 33332, origin = `http://127.0.0.1:${port}`
await symlink(path.join(frontend, "node_modules"), path.join(temporary, "node_modules"))
await writeFile(path.join(temporary, "index.html"), '<!doctype html><html data-theme="lunafox-light"><head><meta charset="utf-8" /></head><body><div id="root"></div><script type="module" src="/entry.tsx"></script></body></html>')
await writeFile(path.join(temporary, "entry.tsx"), `import React from "react";import {createRoot} from "react-dom/client";import {QueryClient,QueryClientProvider} from "@tanstack/react-query";import ${JSON.stringify(path.join(frontend, "app/globals.css"))};import {OverviewTokenUsage} from ${JSON.stringify(path.join(frontend, "components/overview/overview-token-usage.tsx"))};const client=new QueryClient({defaultOptions:{queries:{retry:false}}});createRoot(document.getElementById("root")!).render(<QueryClientProvider client={client}><main style={{padding:16,maxWidth:600}}><OverviewTokenUsage/></main></QueryClientProvider>);`)
const messages = JSON.parse(await readFile(path.join(frontend, "messages/zh.json"), "utf8"))
await writeFile(path.join(temporary, "i18n.ts"), `const messages=${JSON.stringify({overview:messages.overview})};export function useLocale(){return 'zh'};export function useTranslations(namespace:string){return (key:string,vars:Record<string,unknown>={})=>{let v:any=messages;for(const part of (namespace+'.'+key).split('.'))v=v?.[part];return typeof v==='string'?v.replace(/\\{(\\w+)\\}/g,(_,name)=>String(vars[name]??'')):key}}`)
await writeFile(path.join(temporary, "auth.ts"), `export const useAuth=()=>({data:{user:{id:7}},isLoading:false})`)
await writeFile(path.join(temporary, "link.tsx"), `import React from 'react';export default function Link({href,children,...props}:any){return <a href={href} {...props}>{children}</a>}`)
await writeFile(path.join(temporary, "api.ts"), `export const api={async get(path:string,config?:{params?:Record<string,string>}){const q=new URLSearchParams(config?.params??{});const r=await fetch('/v1'+path+(q.size?'?'+q:''));if(!r.ok)throw new Error('HTTP '+r.status);return {data:await r.json()}}}`)
const aliases = [["next-intl", "i18n.ts"], ["next/link", "link.tsx"], ["@/hooks/use-auth", "auth.ts"], ["@/lib/api-client", "api.ts"]].map(([find, replacement]) => ({ find, replacement: path.join(temporary, replacement) }))
aliases.push({ find: "@", replacement: frontend })
await writeFile(path.join(temporary, "vite.config.mjs"), `import tailwind from '@tailwindcss/postcss';export default {root:${JSON.stringify(temporary)},cacheDir:${JSON.stringify(path.join(temporary,"cache"))},resolve:{alias:${JSON.stringify(aliases)},dedupe:['react','react-dom']},esbuild:{jsx:'automatic'},css:{postcss:{plugins:[tailwind()]}},server:{host:'127.0.0.1',port:${port},strictPort:true,fs:{allow:${JSON.stringify([temporary,frontend])}}}};`)
const log = await open(path.join(temporary, "vite.log"), "w")
const server = spawn(process.execPath, [viteBin, "--config", path.join(temporary, "vite.config.mjs")], { cwd: frontend, env: { ...process.env, NODE_OPTIONS: "--max-old-space-size=1024" }, detached: true, stdio: ["ignore", log.fd, log.fd] })
let browser
try {
  let ready = false
  for (let i = 0; i < 100; i++) { if (server.exitCode !== null) throw new Error(`fixture server exited: ${server.exitCode}`); try { if ((await fetch(origin)).ok) { ready = true; break } } catch {}; await new Promise(resolve => setTimeout(resolve, 200)) }
  if (!ready) throw new Error("fixture server did not start")
  browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1000, height: 900 } })
  const ids = ["11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"], daysSeen = []
  const today = new Date().toISOString().slice(0, 10)
  const a = { profile_id: ids[0], profile_name: "测试模型 A", calls: 2, tasks: 1, input_tokens: 1000, output_tokens: 200, cache_read_tokens: 400, cache_write_tokens: 50 }
  const b = { profile_id: ids[1], profile_name: "测试模型 B", calls: 1, tasks: 0, input_tokens: 500, output_tokens: 100, cache_read_tokens: 0, cache_write_tokens: 0 }
  await context.route("**/v1/aiModelUsage*", async route => { const days = Number(new URL(route.request().url()).searchParams.get("days") || 30); daysSeen.push(days); await route.fulfill({ json: { totals: { calls: 3, tasks: 1, input_tokens: 1500, output_tokens: 300, cache_read_tokens: 400, cache_write_tokens: 50 }, profiles: [a, b], daily: [{ ...a, date: today }, { ...b, date: today }], days } }) })
  await context.route("**/v1/aiModels*", route => route.fulfill({ json: { models: [{ id: ids[0], displayName: "测试模型 A", model: "fixture-a", isDefault: true }, { id: ids[1], displayName: "测试模型 B", model: "fixture-b", isDefault: false }] } }))
  const page = await context.newPage(), errors = []; page.setDefaultTimeout(15000); page.on("pageerror", error => errors.push(String(error)))
  await page.goto(origin, { waitUntil: "domcontentloaded", timeout: 60000 })
  await expect(page.getByText("LLM Token 消耗", { exact: true })).toBeVisible()
  await expect(page.locator('[title="1,800"]')).toBeVisible()
  await page.getByRole("button", { name: "测试模型 A · 默认" }).click()
  await expect(page.locator('[title="1,200"]')).toBeVisible()
  await expect(page.getByText("40%", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "7天", exact: true }).click()
  await expect.poll(() => daysSeen.includes(7)).toBe(true)
  await expect(page.locator(".recharts-surface")).toBeVisible()
  await page.locator("main").screenshot({ path: "/tmp/skyshield-token-fixture-desktop.png" })
  await page.setViewportSize({ width: 390, height: 844 })
  const geometry = await page.locator("main").evaluate(node => ({ width: node.getBoundingClientRect().width, viewport: document.documentElement.clientWidth, scroll: node.scrollWidth }))
  if (geometry.width > geometry.viewport + 1 || geometry.scroll > geometry.width + 2) throw new Error("token panel overflows mobile: " + JSON.stringify(geometry))
  await page.locator("main").screenshot({ path: "/tmp/skyshield-token-fixture-mobile.png" })
  expect(errors).toEqual([])
  console.log(JSON.stringify({ passed: true, profileFilter: true, periodFilter: true, cacheRate: true, chart: true, mobile: true }))
} finally {
  if (browser) await browser.close()
  if (server.exitCode === null) { try { process.kill(-server.pid, "SIGTERM") } catch {}; await new Promise(resolve => { server.once("exit", resolve); setTimeout(resolve, 3000).unref() }); if (server.exitCode === null) { try { process.kill(-server.pid, "SIGKILL") } catch {} } }
  await log.close()
}
