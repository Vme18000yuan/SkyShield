import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const profileId='11111111-1111-4111-8111-111111111111',convId='22222222-2222-4222-8222-222222222222'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const agents=[['mainagent','主 Agent',true],['planner','规划 Agent',true],['worker','执行 Agent',true],['goals','目标拆解',true],['auto','自动编排',true],['pentest','独立测试',true],['reporter','报告撰写',false],['retester','漏洞复测',false]].map(([key,name,builtin])=>({key,name,builtin,description:'ARTEX 原版默认能力',role:key,enabled:true,maxTurns:0,runSeconds:1200,options:{interactive_shell:true,web_search:false,trigger_run_mode:'serial',trigger_merge_mode:'all',trigger_max_parallel:5}}))
let savedConfig,savedPrompt,savedFile
const skills=[{id:'33333333-3333-4333-8333-333333333333',name:'api-recon',description:'接口识别',files:['SKILL.md','scripts/','scripts/check.py'],mcps:[],calls:3,tasks:1,usage_agents:['worker'],last_used:new Date().toISOString()}]
let conversations=[{id:convId,title:'原版历史会话',agentKey:'auto',llmProfileId:profileId,pinned:false,running:false,createdAt:new Date().toISOString(),updatedAt:new Date().toISOString()}]
let messages=[{seq:1,worker:'auto',kind:'user',summary:'检查附件',ts:new Date().toISOString()},{seq:2,worker:'auto',kind:'text',summary:'已完成 **原版 Markdown** 渲染',ts:new Date().toISOString()}]
const tools=[{key:'graph_overview',system:true,description:'探索图总览',schema:{type:'object',properties:{}},agents:['planner','mainagent'],enabled:true,kind:'builtin',exec:{},calls:3}]
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try{
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 const unknown=[]
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{
  const request=route.request(),url=new URL(request.url()),path=url.pathname,method=request.method()
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  let data={results:[],totalSize:0,notifications:[],unreadCount:0},handled=false
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/aiAgents'){data={agents};handled=true}
  if(path==='/v1/aiModels'||path==='/v1/aiLlmProfiles'){data={[path.endsWith('aiModels')?'models':'profiles']:[{id:profileId,displayName:'测试模型名称',provider:'openai',model:'fixture-model',isDefault:true,hasApiKey:true,baseUrl:'https://fixture.invalid'}]};handled=true}
  const agentMatch=path.match(/^\/v1\/aiAgents\/([^/]+)\/(details|config|prompt)$/)
  if(agentMatch){const source=agents.find(a=>a.key===agentMatch[1]);handled=true;if(method==='PUT'){if(agentMatch[2]==='config')savedConfig=request.postDataJSON();else savedPrompt=request.postDataJSON();data={version:2}}else data={agent:{...source,max_turns:0,run_seconds:1200,interactive_shell:true,llm_profile_id:profileId},prompt:savedPrompt?.template||'原版系统提示词 {{.Now}}',variables:[{name:'Now',description:'当前时间',source:'runtime',example:'2026-11-01'}],versions:[],visibility:{mcp:[],skill:['api-recon']},llm_profiles:[{id:profileId,name:'测试模型名称',model:'fixture-model',is_default:true}],wrapup_default:'原版收尾',wrapup_max_turns_default:10,task_timeout_wrapup_supported:true}}
  if(path.endsWith('/triggers')){data={triggers:[]};handled=true}
  if(path==='/v1/aiRuntimeSettings'){data={web_search_enabled:true,traffic_capture:false,workers:3};handled=true}
  if(path==='/v1/aiTools'){data={tools};handled=true}
  if(path==='/v1/aiMcpServers'){data={servers:[]};handled=true}
  if(path==='/v1/aiSkillPackages'){data={skills};handled=true}
  if(path==='/v1/aiMissingSkills'){data={missing:[]};handled=true}
  if(path==='/v1/aiSkillPackages/api-recon/visibility'){data={agents:['worker']};handled=true}
  if(path==='/v1/aiSkillPackages/api-recon/usage'){data={calls:[]};handled=true}
  if(path==='/v1/aiSkillPackages/api-recon/files/SKILL.md'){handled=true;if(method==='PUT'){savedFile=request.postDataJSON();data={ok:true}}else data={content:savedFile?.content||'---\nname: api-recon\ndescription: 接口识别\n---\n原版技能正文'}}
  if(path==='/v1/aiChatMentions'){data={items:[{kind:'finding',id:'9007199254740901',label:'测试引用漏洞',description:'高危'}]};handled=true}
  if(path==='/v1/aiConversations'){data={conversations};handled=true}
  if(path===`/v1/aiConversations/${convId}/messages`){handled=true;if(method==='POST'){const input=request.postDataJSON();messages=[...messages,{seq:messages.length+1,worker:'auto',kind:'user',summary:input.message,ts:new Date().toISOString()}];data={runId:'fixture-run'}}else data={items:url.searchParams.has('since')?messages.filter(a=>a.seq>Number(url.searchParams.get('since'))):messages,cursor:messages.at(-1)?.seq||0,hasMore:false,running:false}}
  if(path.match(new RegExp(`/v1/aiConversations/${convId}/messages/\\d+$`))){const seq=Number(path.split('/').at(-1));data={detail:seq===1?JSON.stringify({text:'检查附件',attachments:[{name:'fixture.txt',path:'uploads/fixture.txt',size:12}]}):messages.find(a=>a.seq===seq)?.summary||''};handled=true}
  if(path===`/v1/aiConversations/${convId}/side-questions`){data={items:[],current:null,next_cursor:0,snapshot:{available:true,reason:'',captured_at:new Date().toISOString(),model:{model:'fixture-model',name:'测试模型名称',profile_id:1,format:'openai'}}};handled=true}
  if(path===`/v1/aiConversations/${convId}/traffic`){data={items:[{id:'capture-1',host:'fixture.example',method:'GET',url:'https://fixture.example/chat',status:200,content_type:'text/html',response_length:12,captured_at:new Date().toISOString()}],total:1,page:0,size:25};handled=true}
  if(path===`/v1/aiConversations/${convId}/traffic/exchange`){data={request:'GET /chat HTTP/1.1',response:'HTTP/1.1 200 OK\nfixture response'};handled=true}
  if(path.startsWith('/v1/ai')&&!handled)unknown.push(`${method} ${path}`)
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/settings/ai/agents/`,{waitUntil:'domcontentloaded',timeout:120000})
 await page.getByRole('button').filter({hasText:'规划 Agent'}).click()
 await expect(page.locator('#max-turns')).toHaveValue('0');await expect(page.locator('#run-seconds')).toHaveValue('1200')
 await expect(page.locator('#llm-profile')).toContainText('测试模型名称');await page.locator('#max-turns').fill('9');await page.getByRole('button',{name:'保存配置',exact:true}).click();await expect.poll(()=>savedConfig?.max_turns).toBe(9);await expect(page.locator('#max-turns')).toHaveValue('0')
 await page.getByPlaceholder('留空则使用内置默认提示词').fill('迁移后的提示词 {{.Now}}');await page.getByRole('button',{name:'保存为新版本'}).click();await expect.poll(()=>savedPrompt?.template).toBe('迁移后的提示词 {{.Now}}')
 await page.getByRole('tab',{name:'Tools',exact:true}).click();await expect(page.getByText('graph_overview',{exact:true})).toBeVisible()
 await page.screenshot({path:'/tmp/artex-agent-ui.png',fullPage:true})
 await page.goto(`${origin}/settings/ai/skills/`,{waitUntil:'domcontentloaded',timeout:120000});await page.getByText('api-recon',{exact:true}).first().click();await page.getByText('SKILL.md',{exact:true}).first().click();const editor=page.locator('textarea').filter({visible:true}).first();await expect(editor).toContainText('原版技能正文');await editor.fill('---\nname: api-recon\ndescription: 已编辑\n---\n保留原版文件编辑');await page.getByRole('button',{name:/保存/}).first().click();await expect.poll(()=>savedFile?.content).toContain('保留原版文件编辑')
 await page.goto(`${origin}/ai/chat/?c=${convId}`,{waitUntil:'domcontentloaded',timeout:120000});await expect(page.getByText('原版历史会话',{exact:true}).first()).toBeVisible();await expect(page.getByText('原版 Markdown',{exact:true})).toBeVisible();await expect(page.getByText('fixture.txt',{exact:true})).toBeVisible();await page.getByRole('textbox',{name:'消息，输入 @ 引用记录'}).fill('继续检查');await page.getByRole('button',{name:'发送消息',exact:true}).click();await expect.poll(()=>messages.some(a=>a.summary==='继续检查')).toBe(true)
 await page.getByRole('button',{name:'流量记录'}).click();const traffic=page.getByRole('dialog',{name:'流量记录'});await expect(traffic.getByText('https://fixture.example/chat')).toBeVisible();await traffic.getByRole('button',{name:'查看'}).click();const exchange=page.getByRole('dialog',{name:'请求与响应'});await expect(exchange.getByText('GET /chat HTTP/1.1')).toBeVisible();await exchange.getByRole('button',{name:'关闭'}).click();await traffic.getByRole('button',{name:'关闭'}).click()
 const composer=page.getByRole('textbox',{name:'消息，输入 @ 引用记录'});await composer.fill('@漏洞');await expect(page.getByRole('option',{name:/测试引用漏洞/})).toBeVisible();await page.getByRole('option',{name:/测试引用漏洞/}).click();await expect(composer).toHaveValue(/@\[漏洞#9007199254740901 测试引用漏洞\]/);await page.getByRole('button',{name:'发送消息',exact:true}).click();await expect.poll(()=>messages.some(a=>a.summary.includes('@[漏洞#9007199254740901'))).toBe(true)
 const send=await page.getByRole('button',{name:'发送消息',exact:true}).boundingBox();if(!send||send.y+send.height>1000)throw new Error('Chat composer is outside the viewport')
 await page.screenshot({path:'/tmp/artex-chat-ui.png',fullPage:true});if(errors.length)throw new Error(errors.join('\n'));if(unknown.length)throw new Error('Unmapped native APIs: '+unknown.join(', '));console.log(JSON.stringify({passed:true,nativeAgents:true,profilesUseNames:true,prompts:true,skillFiles:true,chatHistory:true,attachmentCards:true,send:true,mentions:true,conversationTraffic:true}))
}finally{await browser.close()}
