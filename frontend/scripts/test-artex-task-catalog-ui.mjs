import { chromium, expect } from '@playwright/test'

const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const now=new Date().toISOString()
const makeTask=(id,name)=>({id,name:`aiTasks/${id}`,displayName:name,description:'',goal:'验证资产',remark:'',categoryId:null,categoryName:'',pinned:false,pinnedAt:null,targetId:null,status:'paused',queued:false,workerCount:0,maxTurns:0,timeoutSeconds:0,planHeartbeatSeconds:600,firstRunAt:null,deadlineAt:null,errorMessage:'',createdAt:now,updatedAt:now,completedAt:null,llmProfileIds:[],activeLlmProfileId:null,llmFailoverState:'default',llmFailoverReason:''})
let categories=[],templates=[],tasks=[makeTask('10','任务甲'),makeTask('20','任务乙')],taskInput,metadata=[]
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try {
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{
  const request=route.request(),url=new URL(request.url()),path=url.pathname,method=request.method();let data={results:[],totalSize:0,notifications:[],unreadCount:0}
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  if(path==='/v1/targets')data={targets:[],nextPageToken:''}
  if(path==='/v1/aiModels')data={models:[]}
  if(path==='/v1/aiTaskCategories'){
   if(method==='POST'){const item={id:String(101+categories.length),name:request.postDataJSON().name,task_count:0,created_at:now,updated_at:now};categories.push(item);data=item}
   else data={categories}
  }
  if(path==='/v1/aiTaskCategories/101'&&method==='PUT'){categories=categories.map(c=>({...c,...request.postDataJSON()}));tasks=tasks.map(t=>t.categoryId==='101'?{...t,categoryName:categories[0].name}:t);data=categories[0]}
  if(path==='/v1/aiTaskCategories/101'&&method==='DELETE'){categories=[];tasks=tasks.map(t=>({...t,categoryId:null,categoryName:''}));templates=templates.map(t=>({...t,category_id:null}));data={ok:true}}
  if(path==='/v1/aiTaskTemplates'){
   if(method==='POST'){const item={id:'201',...request.postDataJSON(),created_at:now,updated_at:now};templates.push(item);data=item}
   else data={templates}
  }
  if(path==='/v1/aiTaskTemplates/201'&&method==='DELETE'){templates=[];data={ok:true}}
  if(path==='/v1/aiTaskTemplates/201'&&method==='PUT'){templates=templates.map(t=>({...t,...request.postDataJSON()}));data=templates[0]}
  if(path==='/v1/aiTasks'&&method==='GET'){
   let items=[...tasks];const q=url.searchParams.get('q'),category=url.searchParams.get('category'),status=url.searchParams.get('status')
   if(q)items=items.filter(t=>(t.displayName+' '+t.goal+' '+t.remark+' '+t.categoryName).includes(q))
   if(category)items=items.filter(t=>category==='none'?t.categoryId===null:t.categoryId===category)
   if(status)items=items.filter(t=>status==='queued'?t.queued:t.status===status&&!t.queued)
   items.sort((a,b)=>Number(b.pinned)-Number(a.pinned)||Number(b.id)-Number(a.id));data={tasks:items,nextBefore:''}
  }
  if(path==='/v1/aiTasks'&&method==='POST'){taskInput=request.postDataJSON();const item={...makeTask('30',taskInput.displayName||'新 AI 任务'),...taskInput,displayName:taskInput.displayName||'新 AI 任务',categoryId:taskInput.categoryId||null,categoryName:categories.find(c=>c.id===taskInput.categoryId)?.name||'',targetId:null,status:'created',queued:false};tasks.push(item);data=item}
  if(/^\/v1\/aiTasks\/(10|20)$/.test(path)&&method==='PATCH'){
   const id=path.split('/').at(-1),patch=request.postDataJSON();metadata.push({id,...patch});tasks=tasks.map(t=>t.id===id?{...t,...patch,categoryName:categories.find(c=>c.id===patch.categoryId)?.name??(patch.categoryId===null?'':t.categoryName)}:t);data=tasks.find(t=>t.id===id)
  }
	  if(path==='/v1/aiTasks/10'&&method==='GET')data=tasks.find(t=>t.id==='10')
  if(path==='/v1/aiTasks/30')data=tasks.find(t=>t.id==='30')
  if(path==='/v1/aiTasks/30/graph')data={nodes:[{id:'70',kind:'fact',state:'confirmed',priority:0,payload:{summary:'来源已确认结论'},createdAt:now,inherited:true,sourceTaskId:'10'},{id:'71',kind:'intent',state:'blocked',priority:0,payload:{summary:'来源已结束意图'},createdAt:now,inherited:true,sourceTaskId:'10'},{id:'72',kind:'finding',state:'confirmed',priority:0,payload:{summary:'来源发现'},findingId:'73',createdAt:now,inherited:true,sourceTaskId:'10'}],edges:[],assets:[],truncated:false}
  if(path==='/v1/aiTasks/30/activities')data={activities:[]}
  if(path==='/v1/aiTasks/30/goals')data={goals:[]}
  if(path==='/v1/aiTasks/30/constraints')data={constraints:[]}
  if(path==='/v1/aiTasks/30/assetInterceptRules')data={rules:[]}
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)));page.on('dialog',dialog=>dialog.accept())
 await page.goto(`${origin}/ai/tasks/`,{waitUntil:'domcontentloaded',timeout:120000})
 await page.getByRole('button',{name:'管理分类'}).click();await page.getByLabel('新建分类名称').fill('外部资产');await page.getByRole('button',{name:'新建分类',exact:true}).click();await expect.poll(()=>categories.length).toBe(1);await page.getByRole('button',{name:'编辑分类 外部资产'}).click();await page.getByRole('textbox',{name:'编辑分类 外部资产'}).fill('外部资产调整');await page.getByRole('dialog').getByRole('button',{name:'保存',exact:true}).click();await expect.poll(()=>categories[0]?.name).toBe('外部资产调整');await page.getByRole('dialog').getByRole('button',{name:'关闭'}).click()
 await page.getByLabel('任务 任务甲 分类').selectOption('101');await expect.poll(()=>metadata.some(m=>m.id==='10'&&m.categoryId==='101')).toBe(true)
 await page.getByRole('button',{name:'置顶 任务甲'}).click();await expect.poll(()=>metadata.some(m=>m.id==='10'&&m.pinned===true)).toBe(true)
 await page.getByRole('button',{name:'任务 任务甲备注，点击编辑'}).click();await page.getByRole('textbox',{name:'任务 任务甲备注'}).fill('窗口内测试');await page.getByRole('textbox',{name:'任务 任务甲备注'}).press('Enter');await expect.poll(()=>metadata.some(m=>m.remark==='窗口内测试')).toBe(true)
 await page.getByLabel('任务分类筛选').selectOption('101');await expect(page.getByText('任务甲',{exact:true})).toBeVisible();await expect(page.getByText('任务乙',{exact:true})).toHaveCount(0);await page.getByLabel('任务分类筛选').selectOption('')
 await page.getByRole('button',{name:'新建任务'}).click();await page.getByRole('button',{name:'管理模板'}).click();await page.locator('#task-template-name').fill('外部 Web 模板');await page.locator('#task-template-description').fill('测试对象');await page.locator('#task-template-goal').fill('核验接口');await page.locator('#task-template-remark').fill('只读');await page.locator('#task-template-worker-count').fill('2');await page.locator('#task-template-category').selectOption('101');await page.getByRole('dialog').last().getByRole('button',{name:'添加一条'}).click();await page.getByRole('dialog').last().getByLabel('规则动作').selectOption('allow');await page.getByRole('dialog').last().getByLabel('规则匹配内容 1').fill('allowed.example');await page.getByRole('button',{name:'创建模板'}).click();await expect.poll(()=>templates.length).toBe(1);await expect(page.getByRole('button',{name:'保存修改'})).toBeEnabled();await expect(page.locator('#task-template-description')).toHaveValue('测试对象');await page.locator('#task-template-description').fill('测试对象（更新）');await page.getByRole('button',{name:'保存修改'}).click();await expect.poll(()=>templates[0]?.description).toBe('测试对象（更新）');await page.getByRole('dialog').last().getByText('关闭',{exact:true}).click()
 await page.locator('#task-template-picker').click();await page.getByRole('option',{name:/外部 Web 模板/}).click();await expect(page.locator('#task-goal')).toHaveValue('核验接口');await expect(page.locator('#task-remark')).toHaveValue('只读');await expect(page.locator('#task-workers')).toHaveValue('2');await page.getByLabel('继承任务 任务甲').check();await page.getByRole('dialog').locator('button[type=submit]').click();await expect.poll(()=>taskInput?.goal).toBe('核验接口');if(taskInput.categoryId!=='101'||taskInput.interceptRules?.[0]?.pattern!=='allowed.example'||taskInput.sourceTaskIds?.join(',')!=='10')throw new Error('Template or source values were not applied');await page.waitForURL('**/ai/tasks/30/',{timeout:120000});await expect(page.getByRole('heading',{name:'新 AI 任务'})).toBeVisible();await expect(page.getByText('继承 · 任务甲')).toHaveCount(3);await expect(page.locator('article').filter({hasText:'来源已结束意图'}).getByRole('button',{name:'重跑意图'})).toHaveCount(0);await expect(page.locator('article').filter({hasText:'来源发现'}).getByRole('button',{name:'复测'})).toHaveCount(0)
 await page.goto(`${origin}/ai/tasks/`,{waitUntil:'domcontentloaded',timeout:120000});await page.getByRole('button',{name:'新建任务'}).click();await page.getByRole('button',{name:'管理模板'}).click();await page.getByRole('button',{name:'删除模板'}).click();await page.getByRole('alertdialog').getByRole('button',{name:'删除',exact:true}).click();await expect.poll(()=>templates.length).toBe(0);await page.getByRole('dialog').last().getByText('关闭',{exact:true}).click();await page.getByRole('dialog',{name:'新建任务'}).getByRole('button',{name:'关闭',exact:true}).click();await page.getByRole('button',{name:'管理分类'}).click();await page.getByRole('button',{name:'删除分类 外部资产调整'}).click();await expect.poll(()=>categories.length).toBe(0);await expect.poll(()=>tasks.find(t=>t.id==='30')?.categoryId).toBe(null);await expect(page.getByRole('dialog',{name:'任务分类管理'}).getByText('外部资产调整',{exact:true})).toHaveCount(0)
 if(errors.length)throw new Error(errors.join('\n'));await page.screenshot({path:'/tmp/artex-task-catalog-ui.png',fullPage:true});console.log(JSON.stringify({passed:true,categories:true,metadata:true,filters:true,templates:true,inheritance:true}))
}finally{await browser.close()}
