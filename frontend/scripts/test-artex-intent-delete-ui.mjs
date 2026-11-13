import { chromium, expect } from '@playwright/test'

const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const now=new Date().toISOString()
const task={id:'30',name:'aiTasks/30',displayName:'删除意图测试',description:'',goal:'验证',remark:'',categoryId:null,categoryName:'',pinned:false,pinnedAt:null,sourceTaskIds:['10'],coverageEnabled:true,targetId:null,status:'running',queued:false,workerCount:1,maxTurns:0,timeoutSeconds:0,planHeartbeatSeconds:600,firstRunAt:null,deadlineAt:null,errorMessage:'',createdAt:now,updatedAt:now,completedAt:null,llmProfileIds:[],activeLlmProfileId:null,llmFailoverState:'default',llmFailoverReason:''}
let nodes=[{id:'1',kind:'intent',state:'open',priority:1,payload:{summary:'待删除方向'},createdAt:now},{id:'2',kind:'intent',state:'running',priority:1,payload:{summary:'运行中方向'},createdAt:now},{id:'3',kind:'intent',state:'open',priority:1,payload:{summary:'来源方向'},createdAt:now,inherited:true,sourceTaskId:'10'}],calls=[]
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try {
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{
  const request=route.request(),path=new URL(request.url()).pathname,method=request.method();let data={results:[],totalSize:0,notifications:[],unreadCount:0}
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  if(path==='/v1/aiTasks/30')data=task
  if(path==='/v1/aiTasks/10')data={...task,id:'10',displayName:'来源任务'}
  if(path==='/v1/aiTasks/30/graph')data={nodes,edges:[],assets:[],truncated:false}
  if(path==='/v1/aiTasks/30/activities')data={activities:[]}
  if(path==='/v1/aiTasks/30/goals')data={goals:[]}
  if(path==='/v1/aiTasks/30/constraints')data={constraints:[]}
  if(path==='/v1/aiTasks/30/assetInterceptRules')data={rules:[]}
  if(path==='/v1/aiTasks/30/scope')data={scope:[]}
  if(path==='/v1/aiTasks/30/coverage')data={enabled:true,scope_rows:0,denominator:0,tested:0,pct:null,by_type:[]}
  if(/^\/v1\/aiTasks\/30\/intents\/(1|2)\/control$/.test(path)&&method==='POST'){
   const id=path.split('/').at(-2),input=request.postDataJSON();calls.push({id,...input})
   if(id==='1'){nodes=nodes.map(node=>node.id===id?{...node,state:'deleted',deleteReason:input.reason}:node);data={state:'deleted'}}else {nodes=nodes.map(node=>node.id===id?{...node,deletionState:'pending'}:node);data={state:'pending'}}
  }
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/ai/tasks/30/`,{waitUntil:'domcontentloaded',timeout:120000})
 const inherited=page.locator('article').filter({hasText:'来源方向'})
 await expect(inherited.getByRole('button',{name:'删除意图'})).toHaveCount(0)
 await page.locator('article').filter({hasText:'待删除方向'}).getByRole('button',{name:'删除意图'}).click()
 await expect(page.getByRole('dialog').getByRole('button',{name:'确认删除'})).toBeDisabled()
 await page.getByRole('dialog').getByLabel('删除原因').fill('方向不再适用')
 await page.getByRole('dialog').getByRole('button',{name:'确认删除'}).click()
 await expect.poll(()=>calls[0]?.reason).toBe('方向不再适用')
 await expect(page.locator('article').filter({hasText:'待删除方向'}).getByText('删除原因: 方向不再适用')).toBeVisible()
 await page.locator('article').filter({hasText:'运行中方向'}).getByRole('button',{name:'删除意图'}).click()
 await page.getByRole('radio',{name:/彻底删除/}).check()
 await page.getByRole('dialog').getByLabel('删除原因').fill('等待执行停止')
 await page.getByRole('dialog').getByRole('button',{name:'确认删除'}).click()
 await expect.poll(()=>calls[1]?.action).toBe('cancel')
 await expect.poll(()=>calls[1]?.mode).toBe('hard')
 await expect(page.locator('article').filter({hasText:'运行中方向'}).getByText('正在等待 Worker 停止，意图将自动删除。')).toBeVisible()
 await expect(page.locator('article').filter({hasText:'运行中方向'}).getByRole('button',{name:'删除意图'})).toBeDisabled()
 if(errors.length)throw new Error(errors.join('\n'))
 console.log(JSON.stringify({passed:true,reasonRequired:true,pending:true,inheritedReadOnly:true}))
}finally{await browser.close()}
