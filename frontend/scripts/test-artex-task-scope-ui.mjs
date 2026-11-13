import { chromium, expect } from '@playwright/test'

const origin = process.env.SKYSHIELD_TEST_ORIGIN || 'http://127.0.0.1:33325'
const token = 'fixture.' + Buffer.from(JSON.stringify({ exp: Math.floor(Date.now()/1000)+3600 })).toString('base64url') + '.fixture'
const now = new Date().toISOString()
const makeTask = (id, name, sources = []) => ({ id, name:`aiTasks/${id}`, displayName:name, description:'', goal:'验证资产', remark:'', categoryId:null, categoryName:'', pinned:false, pinnedAt:null, sourceTaskIds:sources, coverageEnabled:true, targetId:null, status:'paused', queued:false, workerCount:1, maxTurns:0, timeoutSeconds:0, planHeartbeatSeconds:600, firstRunAt:null, deadlineAt:null, errorMessage:'', createdAt:now, updatedAt:now, completedAt:null, llmProfileIds:[], activeLlmProfileId:null, llmFailoverState:'default', llmFailoverReason:'' })
const tasks = [makeTask('10', '来源任务甲'), makeTask('30', '范围测试', ['10'])]
let scopes = [{ id:'8', task_id:'10', kind:'root_domain', domain:'inherited.example', source:'manual', inherited:true, source_task_id:'10' }]
let added = [], removed = []
const browser = await chromium.launch({ headless:true, executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
try {
  const context = await browser.newContext({ locale:'zh-CN', viewport:{ width:1440, height:1000 } })
  await context.addCookies([{ name:'NEXT_LOCALE', value:'zh', url:origin }])
  await context.addInitScript(t => { localStorage.setItem('accessToken',t); localStorage.setItem('refreshToken',t) }, token)
  await context.route('**/api/github/repo', route => route.fulfill({ json:{stars:0} }))
  await context.route('**/v1/**', async route => {
    const request=route.request(), url=new URL(request.url()), path=url.pathname, method=request.method()
    let data={results:[],totalSize:0,notifications:[],unreadCount:0}
    if(path==='/v1/users/current') data={id:7,username:'fixture',isSuperuser:true}
    if(path==='/v1/upgradeOperations:active') return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
    if(path==='/v1/organizations') data={results:[{id:7,name:'organizations/7',displayName:'示例组织',description:'',createdAt:now}],totalSize:1}
    if(path==='/v1/aiTasks/10') data=tasks[0]
    if(path==='/v1/aiTasks/30') data=tasks[1]
    if(path==='/v1/aiTasks/30/graph') data={nodes:[],edges:[],assets:[],truncated:false}
    if(path==='/v1/aiTasks/30/activities') data={activities:[]}
    if(path==='/v1/aiTasks/30/goals') data={goals:[]}
    if(path==='/v1/aiTasks/30/constraints') data={constraints:[]}
    if(path==='/v1/aiTasks/30/assetInterceptRules') data={rules:[]}
    if(path==='/v1/aiTasks/30/assets') data={assets:[{id:'35',assetType:'service',label:'https://app.example/',source:'scope',factCount:0}],nextAfter:''}
    if(path==='/v1/aiTasks/30/coverage') data={enabled:true,scope_rows:scopes.length,denominator:4,tested:1,pct:.25,by_type:[{type:'service',total:4,tested:1}]}
    if(path==='/v1/aiTasks/30/scope') {
      if(method==='POST') { const input=request.postDataJSON(); added.push(input); const row={id:String(100+added.length),task_id:'30',kind:input.kind,source:'manual',reason:input.reason,...(input.kind==='company'?{company_name:'示例组织'}:{domain:input.value})}; scopes=[...scopes,row]; data=row }
      else data={scope:scopes}
    }
    if(path.startsWith('/v1/aiTasks/30/scope/')&&method==='DELETE') { const id=path.split('/').at(-1); removed.push(id); scopes=scopes.filter(row=>row.id!==id); data={ok:true} }
    await route.fulfill({json:data})
  })
  const page=await context.newPage(), errors=[]
  page.on('pageerror', error => errors.push(String(error)))
  await page.goto(`${origin}/ai/tasks/30/`,{waitUntil:'domcontentloaded',timeout:120000})
  const card=page.locator('[data-slot="card"]').filter({hasText:'任务范围'})
  await expect(card.getByText('25%')).toBeVisible()
  await expect(card.getByText('有确认事实 1 / 范围内 4')).toBeVisible()
  await expect(card.getByText('继承 · 来源任务甲')).toBeVisible()
  await expect(card.getByRole('button',{name:'删除范围 inherited.example'})).toHaveCount(0)
  await card.getByLabel('范围内容').fill('new.example')
  await card.getByLabel('范围依据').fill('用户授权')
  await card.getByRole('button',{name:'添加',exact:true}).click()
  await expect.poll(()=>added[0]?.value).toBe('new.example')
  await expect(card.getByText('new.example',{exact:true})).toBeVisible()
  await card.getByRole('button',{name:'删除范围 new.example'}).click()
  await expect.poll(()=>removed.includes('101')).toBe(true)
  await card.getByLabel('范围类型').selectOption('company')
  await expect(card.getByLabel('选择组织').getByRole('option',{name:'示例组织'})).toHaveCount(1)
  await card.getByLabel('选择组织').selectOption('7')
  await card.getByRole('button',{name:'添加',exact:true}).click()
  await expect.poll(()=>added[1]?.value).toBe('7')
  await expect(card.locator('span').getByText('示例组织',{exact:true})).toBeVisible()
  await page.getByRole('tab',{name:'关联资产',exact:true}).click()
  await expect(page.getByText('任务范围',{exact:true})).toBeVisible()
  if(errors.length) throw new Error(errors.join('\n'))
  console.log(JSON.stringify({passed:true,coverage:true,scopeCRUD:true,organizationNames:true,inheritedReadOnly:true}))
} finally { await browser.close() }
