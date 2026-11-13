// Isolated UI fixture; API mutations are intercepted.
import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const now=new Date().toISOString()
const task={id:'30',name:'aiTasks/30',displayName:'漏洞任务',description:'',goal:'验证漏洞',remark:'',categoryId:null,categoryName:'',pinned:false,pinnedAt:null,sourceTaskIds:['10'],coverageEnabled:true,targetId:null,status:'paused',queued:false,workerCount:1,maxTurns:0,timeoutSeconds:0,planHeartbeatSeconds:600,firstRunAt:null,deadlineAt:null,errorMessage:'',createdAt:now,updatedAt:now,completedAt:null,llmProfileIds:[],activeLlmProfileId:null,llmFailoverState:'default',llmFailoverReason:''}
let items=[{id:'1',task_id:'30',node_id:'11',task_name:'漏洞任务',name:'本任务漏洞',vulnclass:'fixture',severity:'high',status:'pending',summary:'当前漏洞摘要',evidence_version:2,report_evidence_version:1,has_report:true,traffic_count:1,assets:[{id:'42',type:'service',label:'https://fixture.example/'}],inherited:false,created_at:now},{id:'2',task_id:'10',node_id:'12',task_name:'来源任务',name:'继承漏洞',vulnclass:'fixture',severity:'medium',status:'confirmed',summary:'来源漏洞摘要',evidence_version:0,report_evidence_version:0,has_report:false,traffic_count:0,assets:[],inherited:true,created_at:now}],updates=[],queries=[]
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try{
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{
  const req=route.request(),url=new URL(req.url()),path=url.pathname;let data={results:[],totalSize:0,notifications:[],unreadCount:0}
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
  if(path==='/v1/aiTasks/30/findingCases')data={items:[],total:0,page:0,size:25}
  if(path==='/v1/aiTasks/30/findingCaseSuggestions')data={items:[],total:0,page:0,size:25}
  if(path==='/v1/aiTasks/30/findingCaseReviews')data={items:[],total:0,page:0,size:25}
  if(path==='/v1/aiModelUsage/tasks/30')data={models:[]}
  if(path==='/v1/aiTasks/30/findings'){
   const severity=url.searchParams.get('severity'),q=url.searchParams.get('q'),page=Number(url.searchParams.get('page')||0);queries.push({severity,q,page})
   const filtered=items.filter(item=>(!severity||item.severity===severity)&&(!q||(item.name+item.summary).includes(q)))
   data={items:!severity&&!q&&page>0?[{...items[0],id:'3',name:'较早漏洞'}]:filtered,total:!severity&&!q?26:filtered.length,page,size:25}
  }
  if(path==='/v1/aiTasks/30/findings/1'&&req.method()==='GET')data={...items[0],evidence:'fixture PoC',report:'# fixture report'}
  if(path==='/v1/aiTasks/30/findings/2'&&req.method()==='GET')data={...items[1],evidence:'inherited evidence',report:''}
  if(path==='/v1/aiTasks/30/findings/1/status'&&req.method()==='PATCH'){const input=req.postDataJSON();updates.push(input);items=items.map(item=>item.id==='1'?{...item,status:input.status}:item);data={updated:true}}
  if(path==='/v1/aiTasks/30/findings/1/traffic')data={finding_id:'1',version:2,report_version:1,bindings:[]}
  if(path==='/v1/aiFindings/1/retests')data={retests:[]}
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/ai/tasks/30/`,{waitUntil:'domcontentloaded',timeout:120000})
 await page.getByRole('tab',{name:'漏洞发现'}).click()
 await expect(page.getByText('本任务漏洞',{exact:true})).toBeVisible();await expect(page.getByText('继承 · 来源任务')).toBeVisible()
 await expect(page.getByLabel('继承漏洞 的状态')).toHaveCount(0)
 await page.getByLabel('本任务漏洞 的状态').selectOption('confirmed');await expect.poll(()=>updates[0]?.status).toBe('confirmed')
 await page.locator('article').filter({hasText:'本任务漏洞'}).getByRole('button',{name:'查看详情'}).click()
 const dialog=page.getByRole('dialog');await expect(dialog.getByText('fixture PoC')).toBeVisible();await expect(dialog.getByText('fixture report')).toBeVisible();await expect(dialog.getByText('关联证据已更新，报告可能需要重新生成。')).toBeVisible()
 await dialog.getByText('流量证据',{exact:true}).click();await expect(dialog.getByText('尚未关联流量证据')).toBeVisible();await dialog.getByRole('button',{name:'关闭'}).click()
 await page.locator('article').filter({hasText:'继承漏洞'}).getByRole('button',{name:'查看详情'}).click();await expect(page.getByRole('dialog').getByText('inherited evidence')).toBeVisible();await expect(page.getByRole('dialog').getByRole('link',{name:'来源任务'})).toHaveAttribute('href','/ai/tasks/10/');await page.getByRole('dialog').getByRole('button',{name:'关闭'}).click()
 await page.getByRole('button',{name:'下一页'}).click();await expect(page.getByText('较早漏洞')).toBeVisible();await page.getByRole('button',{name:'上一页'}).click();await expect(page.getByText('本任务漏洞',{exact:true})).toBeVisible()
 await page.getByLabel('严重性').selectOption('high');await expect(page.getByText('继承漏洞',{exact:true})).toHaveCount(0);await page.getByLabel('搜索漏洞名称或摘要').fill('本任务');await expect.poll(()=>queries.some(item=>item.severity==='high'&&item.q==='本任务')).toBe(true)
 if(errors.length)throw new Error(errors.join('\n'))
 console.log(JSON.stringify({passed:true,status:true,inheritedReadOnly:true,evidence:true,report:true,traffic:true,filters:true,pagination:true}))
}finally{await browser.close()}
