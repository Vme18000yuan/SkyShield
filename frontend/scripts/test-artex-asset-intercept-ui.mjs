import { chromium, expect } from '@playwright/test'

const origin = process.env.SKYSHIELD_TEST_ORIGIN || 'http://127.0.0.1:33325'
const token = 'fixture.' + Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString('base64url') + '.fixture'
const now = new Date().toISOString()
const task = { id:'555', name:'aiTasks/555', displayName:'规则测试', description:'', goal:'验证规则', targetId:null, status:'paused', queued:false, workerCount:0, maxTurns:0, timeoutSeconds:0, planHeartbeatSeconds:600, firstRunAt:null, deadlineAt:null, errorMessage:'', createdAt:now, updatedAt:now, completedAt:null, llmProfileIds:[], activeLlmProfileId:null, llmFailoverState:'default', llmFailoverReason:'' }
let globalRules = [{ id:'101', enabled:true, action:'block', kind:'fuzzy_domain', pattern:'.gov', note:'内置', builtin:true, created_at:now, updated_at:now }]
let taskRules = []
const browser = await chromium.launch({ headless:true, executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
try {
  const context = await browser.newContext({ locale:'zh-CN', viewport:{ width:1440, height:1000 } })
  await context.addCookies([{ name:'NEXT_LOCALE', value:'zh', url:origin }])
  await context.addInitScript(t => { localStorage.setItem('accessToken',t); localStorage.setItem('refreshToken',t) }, token)
  await context.route('**/api/github/repo', route => route.fulfill({json:{stars:0}}))
  await context.route('**/v1/**', async route => {
    const req = route.request(), path = new URL(req.url()).pathname, method = req.method()
    let data = { results:[], totalSize:0, notifications:[], unreadCount:0 }
    if (path === '/v1/users/current') data = {id:7,username:'fixture',isSuperuser:true}
    if (path === '/v1/upgradeOperations:active') return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
    if (path === '/v1/aiAssetInterceptRules') {
      if (method === 'POST') { globalRules.push({id:'102', ...req.postDataJSON(), action:'block', builtin:false, created_at:now, updated_at:now}); data=globalRules.at(-1) }
      else data={rules:globalRules}
    }
    if (path === '/v1/aiAssetInterceptRules/102') {
      if (method === 'PUT') { globalRules=globalRules.map(r=>r.id==='102'?{...r,...req.postDataJSON()}:r); data=globalRules.find(r=>r.id==='102') }
      else if (method === 'DELETE') { globalRules=globalRules.filter(r=>r.id!=='102'); data={ok:true} }
    }
    if (path === '/v1/aiAssetInterceptRules/101/toggle') { globalRules=globalRules.map(r=>r.id==='101'?{...r,...req.postDataJSON()}:r); data={ok:true} }
    if (path === '/v1/aiTasks/555') data=task
    if (path === '/v1/aiTasks/555/graph') data={nodes:[],edges:[],assets:[],truncated:false}
    if (path === '/v1/aiTasks/555/activities') data={activities:[]}
    if (path === '/v1/aiTasks/555/goals') data={goals:[]}
    if (path === '/v1/aiTasks/555/constraints') data={constraints:[]}
    if (path === '/v1/aiTasks/555/assetInterceptRules') {
      if (method === 'POST') { taskRules.push({id:'201',...req.postDataJSON(),builtin:false,created_at:now,updated_at:now});data=taskRules.at(-1) }
      else data={rules:taskRules}
    }
    if (path === '/v1/aiTasks/555/assetInterceptRules/201') {
      if (method === 'PUT') { taskRules=taskRules.map(r=>r.id==='201'?{...r,...req.postDataJSON()}:r);data=taskRules[0] }
      else if (method === 'DELETE') { taskRules=[];data={ok:true} }
    }
    if (path === '/v1/aiTasks/555/assetInterceptRules/201/toggle') { taskRules=taskRules.map(r=>({...r,...req.postDataJSON()}));data={ok:true} }
    await route.fulfill({json:data})
  })
  const page=await context.newPage(), errors=[]
  page.on('pageerror', error=>errors.push(String(error)))
  page.on('dialog', dialog=>dialog.accept())
  await page.goto(`${origin}/settings/ai/asset-intercept/`,{waitUntil:'domcontentloaded',timeout:120000})
  await expect(page.getByText('.gov',{exact:true})).toBeVisible()
  await page.getByRole('switch',{name:'启用规则 .gov'}).click()
  await expect.poll(()=>globalRules[0].enabled).toBe(false)
  await page.getByRole('button',{name:'新建规则'}).click()
  await page.getByLabel('匹配内容').fill('blocked.example')
  await page.getByRole('dialog').getByRole('button',{name:'保存',exact:true}).click()
  await expect(page.getByText('blocked.example',{exact:true})).toBeVisible()
  await page.screenshot({path:'/tmp/artex-global-asset-intercept-ui.png',fullPage:true})
  await page.getByRole('button',{name:'编辑规则 blocked.example'}).click()
  await page.getByLabel('匹配内容').fill('changed.example')
  await page.getByRole('dialog').getByRole('button',{name:'保存',exact:true}).click()
  await expect(page.getByText('changed.example',{exact:true})).toBeVisible()
  await page.getByRole('button',{name:'删除规则 changed.example'}).click()
  await expect(page.getByText('changed.example',{exact:true})).toHaveCount(0)
  await page.goto(`${origin}/ai/tasks/555/`,{waitUntil:'domcontentloaded',timeout:120000})
  await page.getByLabel('任务规则动作').selectOption('allow')
  await page.getByLabel('任务规则匹配内容').fill('allowed.example')
  await page.getByLabel('任务规则匹配内容').press('Enter')
  await expect(page.getByText('allowed.example',{exact:true})).toBeVisible()
  if(taskRules[0]?.action!=='allow')throw new Error('Task allow rule was not submitted')
  await page.getByRole('button',{name:'编辑任务规则 allowed.example'}).click()
  await page.getByLabel('编辑任务规则匹配内容').fill('changed-allow.example')
  await page.getByRole('button',{name:'保存任务规则'}).click()
  await expect(page.getByText('changed-allow.example',{exact:true})).toBeVisible()
  await page.getByRole('switch',{name:'启用任务规则 changed-allow.example'}).click()
  await expect.poll(()=>taskRules[0]?.enabled).toBe(false)
  await page.getByRole('button',{name:'删除任务规则 changed-allow.example'}).click()
  await expect(page.getByText('changed-allow.example',{exact:true})).toHaveCount(0)
  if(errors.length)throw new Error(errors.join('\n'))
  await page.screenshot({path:'/tmp/artex-asset-intercept-ui.png',fullPage:true})
  console.log(JSON.stringify({passed:true,globalRules:true,taskRules:true}))
} finally { await browser.close() }
