import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const now=new Date().toISOString()
const task={id:'30',name:'aiTasks/30',displayName:'情报测试',description:'',goal:'验证情报',remark:'',categoryId:null,categoryName:'',pinned:false,pinnedAt:null,sourceTaskIds:['10'],coverageEnabled:true,targetId:null,status:'paused',queued:false,workerCount:1,maxTurns:0,timeoutSeconds:0,planHeartbeatSeconds:600,firstRunAt:null,deadlineAt:null,errorMessage:'',createdAt:now,updatedAt:now,completedAt:null,llmProfileIds:[],activeLlmProfileId:null,llmFailoverState:'default',llmFailoverReason:''}
let items=[{id:'1',task_id:'30',kind:'credential',title:'服务账号',content:'fixture-user',evidence:'fixture-evidence',confidence:'observed',status:'unverified',inherited:false,source_session:'intent:7',created_at:now,credentials:{state:'ready',method:'rules',accounts:[{username:'fixture-user',password:'fixture-password'}],traffic:[{id:'1700000000-0001-abcdef',url:'https://fixture.example/login',method:'POST',status:200,side:'request',excerpt:'fixture credential excerpt',accounts:[0]}],traffic_state:'ready',traffic_note:'已核对凭据',input_tokens:0,output_tokens:0}},{id:'2',task_id:'10',kind:'lead',title:'来源线索',content:'inherited clue',evidence:'source',confidence:'inferred',status:'pending',inherited:true,created_at:now}],patches=[],retries=0
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try{
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{
  const request=route.request(),url=new URL(request.url()),path=url.pathname,method=request.method();let data={results:[],totalSize:0,notifications:[],unreadCount:0}
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  if(path==='/v1/aiTasks/30')data=task
  if(path==='/v1/aiTasks/10')data={...task,id:'10',displayName:'来源任务'}
  if(path==='/v1/aiTasks/30/graph')data={nodes:[],edges:[],assets:[],truncated:false}
  if(path==='/v1/aiTasks/30/activities')data={activities:[]}
  if(path==='/v1/aiTasks/30/goals')data={goals:[]}
  if(path==='/v1/aiTasks/30/constraints')data={constraints:[]}
  if(path==='/v1/aiTasks/30/scope')data={scope:[]}
  if(path==='/v1/aiTasks/30/coverage')data={enabled:true,scope_rows:0,denominator:0,tested:0,pct:null,by_type:[]}
  if(path==='/v1/aiTasks/30/assetInterceptRules')data={rules:[]}
  if(path==='/v1/aiTasks/30/intelligence'){const kind=url.searchParams.get('kind'),q=url.searchParams.get('q');let filtered=items.filter(item=>(!kind||item.kind===kind)&&(!q||(item.title+item.content+item.evidence).includes(q)));data={items:filtered,total:filtered.length,counts:{credential:1,lead:1,sensitive_data:0},enabled:true,credential_agent_enabled:true}}
  if(path==='/v1/aiTasks/30/intelligence/1'&&method==='PATCH'){const input=request.postDataJSON();patches.push(input);items=items.map(item=>item.id==='1'?{...item,status:input.status}:item);data={updated:true}}
  if(path==='/v1/aiTasks/30/intelligence/1/traffic/1700000000-0001-abcdef')data={reference:items[0].credentials.traffic[0],req:'POST /login',resp:'HTTP 200',unavailable:false}
  if(path==='/v1/aiTasks/30/intelligence/1/credentials/retry'&&method==='POST'){retries++;items=items.map(item=>item.id==='1'?{...item,credentials:{...item.credentials,state:'queued'}}:item);data={queued:true}}
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/ai/tasks/30/`,{waitUntil:'domcontentloaded',timeout:120000})
 await page.getByRole('tab',{name:'任务情报'}).click()
 await expect(page.getByText('服务账号')).toBeVisible();await expect(page.getByText('继承 · 来源任务')).toBeVisible()
 await expect(page.getByLabel('来源线索 的状态')).toHaveCount(0)
 await page.getByLabel('服务账号 的状态').selectOption('verified');await expect.poll(()=>patches[0]?.status).toBe('verified')
 await page.locator('article').filter({hasText:'服务账号'}).getByText('查看内容与证据').click();await expect(page.getByText('fixture-user',{exact:true})).toBeVisible()
 await expect(page.getByText(/fixture-password/)).toBeVisible();await page.getByRole('button',{name:'查看关联流量'}).click();await expect(page.getByRole('dialog').getByText('POST /login')).toBeVisible();await page.getByRole('button',{name:'关闭'}).click();await page.getByRole('button',{name:'重新整理'}).click();await expect.poll(()=>retries).toBe(1);await expect(page.getByText('凭据整理: 排队中')).toBeVisible()
 await page.getByLabel('情报类型').selectOption('lead');await expect(page.getByText('来源线索')).toBeVisible();await expect(page.getByText('服务账号')).toHaveCount(0)
 await page.getByLabel('搜索标题、内容或证据').fill('inherited');await expect(page.getByText('共 1 条')).toBeVisible()
 if(errors.length)throw new Error(errors.join('\n'))
 console.log(JSON.stringify({passed:true,filter:true,detail:true,status:true,inheritedReadOnly:true,credentialTraffic:true}))
}finally{await browser.close()}
