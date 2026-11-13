import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const ids=['11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222'],mcpId='33333333-3333-4333-8333-333333333333'
const models=ids.map((id,i)=>({id,displayName:`模型 ${i?'乙':'甲'}`,provider:'openai',model:`fixture-${i}`,isDefault:i===0,baseUrl:'https://fixture.invalid',hasApiKey:true,options:{streaming:true}}))
const initial={traffic_capture:false,credential_agent_enabled:false,credential_traffic_binding:false,task_intelligence_enabled:false,agent_traffic_binding:false,llm_record:false,web_search_enabled:false,web_search_backend:'ddgs',brave_key_set:false,tavily_key_set:false,web_search_proxy:'',global_proxy:'',python_interpreter:'/usr/bin/python3',workers:3,worker_pools:[],task_concurrency_enabled:false,task_concurrency_limit:5,llm_pool_enabled:false,llm_pool_bind_fallback:false,constraints_inject_planner:true,constraints_inject_worker:true,noa_compaction:false}
let settings={...initial},settingsPatches=[],taskInput,modelPatch,mcpPatch,mcpSaved,searchTest,task,retestNotes,retests=[]
let mcp={id:mcpId,displayName:'browser',endpoint:'',enabled:false,insecure:false,transport:'stdio',command:'npx',args:['@playwright/mcp','--headless'],hasEnv:true,tools:[],agents:[]}
const agent={key:'auto',name:'自动编排',builtin:true,description:'',role:'assistant',enabled:true,maxTurns:0,runSeconds:1200,options:{interactive_shell:true}}
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try{
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{
  const request=route.request(),url=new URL(request.url()),path=url.pathname,method=request.method();let data={results:[],totalSize:0,notifications:[],unreadCount:0}
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/aiLlmProfiles')data={profiles:models}
  if(path==='/v1/aiModels')data={models}
  if(path==='/v1/aiAgents')data={agents:[agent]}
  if(path==='/v1/aiRuntimeSettings'){if(method==='PATCH'){let input=request.postDataJSON();settingsPatches.push(input);if('brave_search_api_key' in input){settings.brave_key_set=!!input.brave_search_api_key;delete input.brave_search_api_key};settings={...settings,...input}};data=settings}
  if(path==='/v1/aiRuntimeSettings/pythonDetection')data={python_interpreter:'/usr/bin/python3'}
  if(path==='/v1/aiWebSearchTests'){searchTest=request.postDataJSON();data={ok:true,backend:searchTest.web_search_backend,count:1}}
  if(path==='/v1/aiMcpServers')data={servers:[mcp]}
  if(path===`/v1/aiMcpServers/${mcpId}/details`)data={env:{TOKEN:'fixture-token'}}
  if(path===`/v1/aiMcpServers/${mcpId}/visibility`)data={agents:[]}
  if(path===`/v1/aiMcpServers/${mcpId}/tools`)data={tools:[]}
  if(path===`/v1/aiMcpServers/${mcpId}`&&method==='PATCH'){mcpPatch=request.postDataJSON();mcp={...mcp,...mcpPatch};data={ok:true}}
  if(path===`/v1/aiMcpServers/${mcpId}`&&method==='PUT'){mcpSaved=request.postDataJSON();mcp={...mcp,displayName:mcpSaved.displayName,enabled:mcpSaved.enabled,command:mcpSaved.command,args:mcpSaved.args};data=mcp}
  if(path==='/v1/targets')data={targets:[],nextPageToken:''}
  if(path==='/v1/aiTasks'&&method==='GET')data={tasks:task?[task]:[],nextBefore:''}
  if(path==='/v1/aiTasks'&&method==='POST'){taskInput=request.postDataJSON();task={id:'555',name:'aiTasks/555',...taskInput,targetId:null,status:'created',queued:true,firstRunAt:null,deadlineAt:null,errorMessage:'',createdAt:'2026-11-01T00:00:00Z',updatedAt:'2026-11-01T00:00:00Z',completedAt:null,activeLlmProfileId:taskInput.llmProfileIds[0],llmFailoverState:'ready',llmFailoverReason:''};data=task}
  if(path==='/v1/aiTasks/555')data=task
  if(path==='/v1/aiTasks/555/graph')data={nodes:[{id:'29',kind:'finding',state:'confirmed',priority:9,findingId:'19',payload:{summary:'复测任务发现'},createdAt:new Date().toISOString()}],edges:[],assets:[],truncated:false}
  if(path==='/v1/aiTasks/555/activities')data={activities:[]}
  if(path==='/v1/aiTasks/555/assetInterceptRules')data={rules:[]}
  if(path==='/v1/aiFindings/19/retests'){
   if(method==='POST'){retestNotes=request.postDataJSON().notes;retests=[{id:'99',finding_id:'19',conversation_id:'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',status:'pending',verdict:'',notes:retestNotes,summary:'',evidence:'',error:'',created_at:new Date().toISOString()}];data={retest:retests[0],created:true}}
   else data={retests}
  }

  if(path==='/v1/aiTasks/555/models'&&method==='PUT'){modelPatch=request.postDataJSON();task={...task,llmProfileIds:modelPatch.profileIds,activeLlmProfileId:modelPatch.activeProfileId};data={profiles:[],activeProfileId:modelPatch.activeProfileId,revision:2,state:'ready',reason:''}}
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/settings/ai/runtime/`,{waitUntil:'domcontentloaded',timeout:120000})
 await page.getByRole('switch',{name:'录制 HTTP 流量'}).click();await expect.poll(()=>settingsPatches.some(p=>p.traffic_capture===true)).toBe(true)
 await page.getByRole('switch',{name:'Agent 自动关联漏洞流量'}).click();await expect.poll(()=>settingsPatches.some(p=>p.agent_traffic_binding===true)).toBe(true)
 const workerCard=page.locator('[data-slot="card"]').filter({hasText:'工作并发 · Work Agent 数'})
 await workerCard.getByRole('button',{name:'添加模型池'}).click();await workerCard.getByRole('combobox').click();await page.getByRole('option',{name:'模型 甲 · fixture-0'}).click();await workerCard.getByLabel('模型池 1 Worker 数').fill('2');await workerCard.getByRole('button',{name:'保存',exact:true}).first().click();await expect.poll(()=>settingsPatches.some(p=>p.worker_pools?.[0]?.profile_id===ids[0]&&p.worker_pools[0].workers===2)).toBe(true)
 await workerCard.getByRole('switch',{name:'限制同时运行的任务数'}).click();await workerCard.getByLabel('任务并发上限').fill('4');await workerCard.getByRole('button',{name:'保存',exact:true}).last().click();await expect.poll(()=>settingsPatches.some(p=>p.task_concurrency_enabled===true&&p.task_concurrency_limit===4)).toBe(true)
 await page.getByRole('switch',{name:'注入规划者（planner）'}).click();await expect.poll(()=>settingsPatches.some(p=>p.constraints_inject_planner===false)).toBe(true)
 await page.getByRole('switch',{name:'Agent 自动记录'}).click();await expect.poll(()=>settingsPatches.some(p=>p.task_intelligence_enabled===true)).toBe(true)
 await page.getByRole('switch',{name:'凭据整理 Agent'}).click();await expect.poll(()=>settingsPatches.some(p=>p.credential_agent_enabled===true)).toBe(true);await page.getByRole('switch',{name:'凭据关联已录制流量'}).click();await expect.poll(()=>settingsPatches.some(p=>p.credential_traffic_binding===true)).toBe(true)
 const searchCard=page.locator('[data-slot="card"]').filter({hasText:'网络搜索'});await searchCard.getByRole('switch',{name:'网络搜索总开关'}).click();await searchCard.getByRole('combobox',{name:'搜索来源'}).click();await page.getByRole('option',{name:'Brave（免费版）'}).click();await page.locator('#runtime-brave-key').fill('fixture-key');await page.locator('#runtime-brave-key').locator('..').getByRole('button',{name:'保存'}).click();await expect.poll(()=>settings.brave_key_set).toBe(true);await expect(page.locator('#runtime-brave-key')).toHaveValue('');await searchCard.getByRole('button',{name:'测试搜索'}).click();await expect.poll(()=>searchTest?.web_search_backend).toBe('brave-free')
 await page.goto(`${origin}/settings/ai/mcp/`,{waitUntil:'domcontentloaded',timeout:120000});await page.getByText('browser',{exact:true}).first().waitFor();await page.getByText('browser',{exact:true}).first().click();await expect(page.getByRole('dialog')).toBeVisible();await expect(page.getByLabel('命令',{exact:true})).toHaveValue('npx');await expect(page.locator('textarea')).toHaveValue('TOKEN=fixture-token');await page.getByRole('button',{name:'保存',exact:true}).click();await expect.poll(()=>mcpSaved?.env?.TOKEN).toBe('fixture-token')
 await page.getByRole('button',{name:'关闭'}).click();await page.locator('[data-slot="card"]').filter({hasText:'browser'}).getByRole('switch').click();await expect.poll(()=>mcpPatch?.enabled).toBe(true)
 await page.goto(`${origin}/ai/tasks/`,{waitUntil:'domcontentloaded',timeout:120000});await page.getByRole('button',{name:'新建任务'}).click();await page.locator('#task-goal').fill('任务模型链验证');await expect(page.locator('#task-workers')).toHaveValue('0');await expect(page.locator('#task-timeout')).toHaveValue('0');await page.getByRole('button',{name:'添加一条'}).click();await page.getByLabel('规则动作').selectOption('allow');await page.getByLabel('规则匹配内容 1').fill('task-runtime.example');await page.getByRole('switch',{name:'目标拆解后优先执行种子意图'}).click()
 const modelInput=page.locator('#task-model-chain');await modelInput.click();await page.getByRole('option',{name:/模型 甲/}).click();await modelInput.click();await page.getByRole('option',{name:/模型 乙/}).click();await page.getByRole('button',{name:'下移配置'}).first().click();await page.getByRole('dialog').locator('button[type=submit]').click();await expect.poll(()=>taskInput?.llmProfileIds?.join(',')).toBe(`${ids[1]},${ids[0]}`);if(taskInput.timeoutSeconds!==0||taskInput.workerCount!==0||taskInput.planHeartbeatSeconds!==600||taskInput.interceptRules?.[0]?.action!=='allow'||taskInput.interceptRules[0].pattern!=='task-runtime.example'||taskInput.seedFirstIntent!==true)throw new Error('Native task defaults, seed intent, or asset rules were not sent')
 await page.getByRole('button',{name:'模型配置链'}).click();await page.getByRole('dialog').getByRole('combobox').last().click();await page.getByRole('option',{name:'模型 甲',exact:true}).click();await page.getByRole('button',{name:'保存配置',exact:true}).click();await expect.poll(()=>modelPatch?.activeProfileId).toBe(ids[0]);await page.getByRole('button',{name:'复测',exact:true}).click();await page.getByRole('button',{name:'发起复测',exact:true}).click();await page.getByLabel('补充说明（可选）').fill('验证任务图发现');await page.getByRole('button',{name:'开始复测',exact:true}).click();await expect.poll(()=>retestNotes).toBe('验证任务图发现');if(errors.length)throw new Error(errors.join('\n'));await page.screenshot({path:'/tmp/artex-task-model-ui.png',fullPage:true});console.log(JSON.stringify({passed:true,workerPools:true,concurrency:true,webSearch:true,mcpCredentials:true,modelChain:true,taskDefaults:true,taskRetest:true,constraints:true,trafficCapture:true,automaticEvidence:true,credentialTraffic:true}))
}finally{await browser.close()}
