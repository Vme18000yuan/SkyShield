import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const now=new Date().toISOString(),hash='a'.repeat(64)
const task={id:'30',name:'aiTasks/30',displayName:'流量测试',description:'',goal:'验证流量',remark:'',categoryId:null,categoryName:'',pinned:false,pinnedAt:null,sourceTaskIds:[],coverageEnabled:true,targetId:null,status:'done',queued:false,workerCount:1,maxTurns:0,timeoutSeconds:0,planHeartbeatSeconds:600,firstRunAt:null,deadlineAt:null,errorMessage:'',createdAt:now,updatedAt:now,completedAt:now,llmProfileIds:[],activeLlmProfileId:null,llmFailoverState:'default',llmFailoverReason:''}
let pages=[],filters=[],advancedRequests=[],bound=[],bindingVersion=0
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
  if(path==='/v1/aiTasks/30/graph')data={nodes:[{id:'29',kind:'finding',state:'confirmed',priority:1,findingId:'19',payload:{summary:'演示漏洞'},createdAt:now}],edges:[],assets:[],truncated:false}
  if(path==='/v1/aiTasks/30/activities')data={activities:[]}
  if(path==='/v1/aiTasks/30/goals')data={goals:[]}
  if(path==='/v1/aiTasks/30/constraints')data={constraints:[]}
  if(path==='/v1/aiTasks/30/scope')data={scope:[]}
  if(path==='/v1/aiTasks/30/coverage')data={enabled:true,scope_rows:0,denominator:0,tested:0,pct:null,by_type:[]}
  if(path==='/v1/aiTasks/30/assetInterceptRules')data={rules:[]}
  if(path==='/v1/aiTasks/30/findings'){const q=url.searchParams.get('q')||'';data={items:q==='远端漏洞'?[{id:'20',name:'远端漏洞',vulnclass:'fixture',inherited:false}]:[{id:'19',name:'演示漏洞',vulnclass:'fixture',inherited:false}],total:1,page:0,size:100}}
  if(path==='/v1/aiModelUsage/tasks/30')data={models:[{model:'usage-fixture-model',calls:2,tasks:1,input_tokens:100,output_tokens:20,cache_read_tokens:3,cache_write_tokens:1}]}
  if(path==='/v1/aiTasks/30/traffic'){const page=Number(url.searchParams.get('page')||0);pages.push(page);filters.push(url.searchParams.get('host')||'');advancedRequests.push(Object.fromEntries(url.searchParams));data={items:[{id:'flow-'+page,host:'api.example.com',method:'GET',url:'https://api.example.com/path-'+page,status:200,content_type:'text/plain',response_length:42,captured_at:now}],total:26,page,size:25}}
  if(path==='/v1/aiTasks/30/traffic/exchange')data={request:'GET /path HTTP/1.1',response:`HTTP 200\n\npreview …[truncated] @blob sha256:${hash}`}
  if(path==='/v1/aiTasks/30/traffic/blob')data={data:Buffer.from('完整正文片段').toString('base64'),offset:0,total:16}
  if(path==='/v1/aiTasks/30/findings/19/traffic'){
   if(req.method()==='POST'){const input=req.postDataJSON();bound=input.traffic_refs.map((ref,index)=>({id:String(index+1),role:ref.role,note:ref.note,position:index,snapshot:{source_traffic_id:ref.traffic_id,url:'https://api.example.com/path-0',method:'GET',status:200}}));bindingVersion++}
   data={finding_id:'19',version:bindingVersion,report_version:0,bindings:bound}
  }
  if(path==='/v1/aiTasks/30/findings/19/traffic/1/body')data={head:'HTTP 200',data:Buffer.from('已保存证据').toString('base64'),offset:0,total:15}
  if(path==='/v1/aiTasks/30/findings/19/traffic/1'&&req.method()==='PATCH'){const input=req.postDataJSON();bound[0]={...bound[0],role:input.role,note:input.note};bindingVersion++;data={finding_id:'19',version:bindingVersion,report_version:0,bindings:bound}}
  if(path==='/v1/aiTasks/30/findings/19/traffic/1'&&req.method()==='DELETE'){bound=[];bindingVersion++;data={finding_id:'19',version:bindingVersion,report_version:0,bindings:bound}}

  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(String(e)))
 await page.goto(`${origin}/ai/tasks/30/`,{waitUntil:'domcontentloaded',timeout:120000})
 await expect(page.getByText('usage-fixture-model')).toBeVisible()
 await page.getByRole('tab',{name:'流量记录'}).click()
 await expect(page.getByText('https://api.example.com/path-0')).toBeVisible()
 await expect(page.getByText(/共 26 条/)).toBeVisible()
 await page.getByLabel('主机').fill('api.example.com');await page.getByRole('button',{name:'筛选',exact:true}).click();await expect.poll(()=>filters.includes('api.example.com')).toBe(true)
 await page.getByText('高级筛选',{exact:true}).click();await page.getByLabel('方法').selectOption('POST');await page.getByLabel('状态码').selectOption('4xx');await page.getByLabel('路径').fill('/login');await page.getByLabel('请求或响应内容').fill('fixture');await page.getByLabel('最小响应字节').fill('10');await page.getByLabel('最大响应字节').fill('100');await page.getByLabel('排序字段').selectOption('status');await page.getByLabel('排序方向').selectOption('asc');await page.getByRole('button',{name:'筛选',exact:true}).click();await expect.poll(()=>advancedRequests.some(q=>q.method==='POST'&&q.status==='4xx'&&q.path==='/login'&&q.body==='fixture'&&q.respMin==='10'&&q.respMax==='100'&&q.sort==='status'&&q.order==='asc')).toBe(true)
 await page.getByRole('button',{name:'查看',exact:true}).click();await expect(page.getByRole('dialog').getByText('GET /path HTTP/1.1')).toBeVisible();await expect(page.getByText('完整正文片段')).toBeVisible();await page.getByRole('button',{name:'关闭'}).click()
 await page.getByRole('checkbox',{name:'选择流量 https://api.example.com/path-0'}).click();await page.getByRole('button',{name:'关联漏洞'}).click();await page.getByLabel('搜索漏洞名称').fill('远端漏洞');await expect(page.getByLabel('漏洞',{exact:true}).locator('option',{hasText:'远端漏洞'})).toHaveCount(1);await page.getByLabel('搜索漏洞名称').fill('');await expect(page.getByLabel('漏洞',{exact:true}).locator('option',{hasText:'演示漏洞'})).toHaveCount(1);await page.getByLabel('说明').fill('支持该发现');await page.getByRole('button',{name:'确认关联'}).click();await expect.poll(()=>bound[0]?.note).toBe('支持该发现')
 await page.getByRole('tab',{name:'探索过程'}).click();await page.getByRole('button',{name:'流量证据'}).click();await expect(page.getByRole('dialog').getByText(/支持该发现/)).toBeVisible();await page.getByRole('dialog').getByRole('button',{name:'编辑'}).click();await page.getByRole('dialog').getByLabel('用途').selectOption('verification');await page.getByRole('dialog').getByLabel('说明').fill('补充核验');await page.getByRole('dialog').getByRole('button',{name:'保存'}).click();await expect.poll(()=>bound[0]?.role).toBe('verification');await page.getByRole('dialog').getByRole('button',{name:'查看'}).click();await expect(page.getByText('已保存证据')).toBeVisible();await page.getByRole('dialog').getByRole('button',{name:'解除关联'}).click();await expect.poll(()=>bound.length).toBe(0);await page.getByRole('button',{name:'关闭'}).click()
 await page.getByRole('tab',{name:'流量记录'}).click()
 await page.getByRole('button',{name:'下一页'}).click();await expect.poll(()=>pages.includes(1)).toBe(true);await expect(page.getByText('https://api.example.com/path-1')).toBeVisible()
 if(errors.length)throw new Error(errors.join('\n'))
 console.log(JSON.stringify({passed:true,filter:true,advancedFilters:true,pagination:true,exchange:true,blob:true,bind:true,evidence:true,edit:true}))
}finally{await browser.close()}
