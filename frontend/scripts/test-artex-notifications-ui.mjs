// Browser fixture uses only mocked local API routes and never contacts a real channel.
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
const temporary = await mkdtemp(path.join(os.tmpdir(), "skyshield-notification-browser-"))
const port = 33327, origin = `http://127.0.0.1:${port}`
await symlink(path.join(frontend, "node_modules"), path.join(temporary, "node_modules"))
await writeFile(path.join(temporary, "index.html"), '<!doctype html><html><head><meta charset="utf-8"><style>[data-base-ui-inert]{pointer-events:none!important}[data-slot="sheet-overlay"]{position:fixed;inset:0;background:#0004;z-index:50}[data-slot="sheet-content"]{position:fixed;z-index:51;top:0;right:0;width:min(90vw,650px);height:100vh;overflow:auto;background:white;padding:20px}button,input,textarea,select{font:inherit}label{display:block}</style></head><body><div id="root"></div><script type="module" src="/entry.tsx"></script></body></html>')
await writeFile(path.join(temporary, "entry.tsx"), `import React from "react";import {createRoot} from "react-dom/client";import NotificationSettings from ${JSON.stringify(path.join(frontend, "components/settings/ai/artex-notification/notification-settings.tsx"))};createRoot(document.getElementById("root")!).render(<NotificationSettings/>);`)
await writeFile(path.join(temporary, "auth.ts"), `export const useAuth=()=>({data:{user:{id:7}}});`)
await writeFile(path.join(temporary, "i18n.ts"), `export function useTranslations(){return (key:string)=>key}`)
await writeFile(path.join(temporary, "icons.tsx"), `import React from 'react';const Icon=()=> <span aria-hidden="true">×</span>;export const Loader2Icon=Icon,CheckIcon=Icon,ChevronDownIcon=Icon,ChevronUpIcon=Icon;export const semanticIcons={action:{cancel:Icon}};`)
await writeFile(path.join(temporary, "toast.ts"), `export const toast={success:()=>{},error:()=>{}};`)
await writeFile(path.join(temporary, "api.ts"), `async function request(method:string,url:string,body?:unknown,options?:any){const params=new URLSearchParams();for(const [key,value] of Object.entries(options?.params||{}))if(value!==undefined)params.set(key,String(value));const response=await fetch('/v1'+url+(params.size?'?'+params:''),{method,headers:{'content-type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});if(!response.ok)throw new Error('HTTP '+response.status);return {data:await response.json()}};export const getErrorMessage=(error:any)=>String(error);export const api={get:(u:string,o?:any)=>request('GET',u,undefined,o),post:(u:string,b?:unknown)=>request('POST',u,b),put:(u:string,b?:unknown)=>request('PUT',u,b),patch:(u:string,b?:unknown)=>request('PATCH',u,b),delete:(u:string)=>request('DELETE',u)};`)
const aliases = [["next-intl", "i18n.ts"], ["sonner", "toast.ts"], ["@/hooks/use-auth", "auth.ts"], ["@/lib/api-client", "api.ts"], ["@/components/icons", "icons.tsx"]].map(([find, replacement]) => ({ find, replacement: path.join(temporary, replacement) }))
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
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1440, height: 1000 } })
  const requests = [], channels = []
  const meta = { settings: { enabled: true, public_base_url: "", digest_interval_min: 30 }, kinds: [{ kind: "dingtalk", default_rate_per_min: 20, secret_keys: ["webhook", "secret"] }], stats: { channels: 0, channels_on: 0, pending: 0, failed: 0, sent_today: 0, backlog_age_ms: 0 } }
  await context.route("**/v1/**", async route => {
    const request = route.request(), url = new URL(request.url()), pathname = url.pathname, method = request.method(), body = request.postDataJSON?.()
    requests.push({ pathname, method, body })
    let data = { updated: true }
    if (method === "GET" && pathname.endsWith("/meta")) data = meta
    else if (method === "GET" && pathname.endsWith("/channels")) data = { channels }
    else if (method === "POST" && pathname.endsWith("/channels")) { channels.push({ ...body, id: "10", version: 2, config: { webhook: "__masked__:…123456", secret: "__masked__" }, secret_keys: ["webhook", "secret"] }); data = channels[0] }
    else if (method === "GET" && pathname.endsWith("/deliveries")) data = { items: [{ id: "20", task_id: "30", finding_id: "40", channel_id: "10", channel_name: "测试群", channel_kind: "dingtalk", event_kind: "finding_created", state: "failed", attempts: 3, last_error: "temporary delivery failure", created_at: new Date().toISOString(), next_attempt_at: new Date().toISOString(), title: "测试发现", severity: "high" }], total: 1 }
    await route.fulfill({ json: data })
  })
  const page = await context.newPage(), errors = []
  page.setDefaultTimeout(10000);page.on("pageerror", error => errors.push(String(error)))
  await page.goto(origin, { waitUntil: "domcontentloaded", timeout: 60000 })
  await page.getByRole("button", { name: "添加渠道" }).click()
  let dialog = page.getByRole("dialog")
  await dialog.getByLabel("渠道名称").fill("测试群")
  await dialog.getByLabel("Webhook 地址").fill("https://example.com/hook")
  await dialog.getByLabel("加签密钥").fill("fixture-secret")
  await dialog.getByRole("button", { name: "添加", exact: true }).click()
  await expect.poll(() => requests.some(item => item.method === "POST" && item.pathname.endsWith("/channels") && item.body?.config?.secret === "fixture-secret")).toBe(true)
  await page.getByText("测试群", { exact: true }).click()
  dialog = page.getByRole("dialog")
  await expect(dialog.getByLabel("Webhook 地址")).toHaveValue("")
  await dialog.getByRole("button", { name: "发送测试消息" }).click()
  await expect.poll(() => requests.some(item => item.pathname.endsWith("/channels/10/test"))).toBe(true)
  await dialog.getByRole("button", { name: "保存", exact: true }).click()
  await expect.poll(() => requests.some(item => item.method === "PATCH" && item.pathname.endsWith("/channels/10") && item.body?.version === 2 && item.body?.config?.secret === "__masked__")).toBe(true)
  await page.getByRole("switch", { name: "本账号推送总开关" }).click()
  await expect.poll(() => requests.some(item => item.method === "PATCH" && item.pathname.endsWith("/settings") && item.body?.enabled === false)).toBe(true)
  await page.getByRole("tab", { name: "投递记录" }).click()
  await expect(page.getByText("测试发现", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "重发" }).click()
  await expect.poll(() => requests.some(item => item.pathname.endsWith("/deliveries/20/retry"))).toBe(true)
  expect(errors).toEqual([])
  console.log(JSON.stringify({ passed: true, create: true, maskedEdit: true, testAction: true, accountSwitch: true, historyRetry: true }))
} finally {
  if (browser) await browser.close()
  if (server.exitCode === null) { try { process.kill(-server.pid, "SIGTERM") } catch {}; await new Promise(resolve => { server.once("exit", resolve); setTimeout(resolve, 3000).unref() }); if (server.exitCode === null) { try { process.kill(-server.pid, "SIGKILL") } catch {} } }
  await log.close()
}
