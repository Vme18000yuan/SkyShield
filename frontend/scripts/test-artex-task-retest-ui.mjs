import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const task={id:'555',name:'aiTasks/555',displayName:'任务复测测试',description:'',goal:'验证任务发现',targetId:null,status:'running',queued:false,workerCount:0,maxTurns:0,timeoutSeconds:0,planHeartbeatSeconds:600,firstRunAt:null,deadlineAt:null,errorMessage:'',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString(),completedAt:null,llmProfileIds:[],activeLlmProfileId:null,llmFailoverState:'default',llmFailoverReason:''}
let retests=[],notes,constraints=[],goals=[],intents=[{id:'61',kind:'intent',state:'blocked',priority:3,payload:{summary:'重跑单个意图'},createdAt:new Date().toISOString()},{id:'62',kind:'intent',state:'blocked',priority:3,payload:{summary:'重跑其他意图'},createdAt:new Date().toISOString()},{id:'63',kind:'intent',state:'running',priority:3,payload:{summary:'暂停并继续'},createdAt:new Date().toISOString()},{id:'64',kind:'intent',state:'paused',priority:3,payload:{summary:'人工消息意图'},createdAt:new Date().toISOString()}],reruns=[],controls=[],workerMessage
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try {
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{const request=route.request(),path=new URL(request.url()).pathname;let data={results:[],totalSize:0,notifications:[],unreadCount:0}
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  if(path==='/v1/aiTasks/555')data=task
  if(path==='/v1/aiTasks/555/graph')data={nodes:[{id:'29',kind:'finding',state:'confirmed',priority:9,findingId:'19',payload:{summary:'任务发现'},createdAt:new Date().toISOString()},...intents],edges:[],assets:[],truncated:false}
  if(path==='/v1/aiTasks/555/intents/61/rerun'){reruns.push('61');intents=intents.map(item=>item.id==='61'?{...item,state:'open'}:item);data={reopened:1}}
  if(path==='/v1/aiTasks/555/intents/rerun-blocked'){reruns.push('blocked');const count=intents.filter(item=>item.state==='blocked').length;intents=intents.map(item=>item.state==='blocked'?{...item,state:'open'}:item);data={reopened:count}}
  if(path==='/v1/aiTasks/555/intents/63/control'){const action=request.postDataJSON().action;controls.push(action);const state=action==='pause'?'paused':'open';intents=intents.map(item=>item.id==='63'?{...item,state}:item);data={state}}
  if(path==='/v1/aiTasks/555/intents/64/messages'){workerMessage=request.postDataJSON();intents=intents.map(item=>item.id==='64'?{...item,state:'running'}:item);data={accepted:true,request_id:workerMessage.request_id}}
  if(path==='/v1/aiTasks/555/activities')data={activities:[]}
  if(path==='/v1/aiTasks/555/assetInterceptRules')data={rules:[]}
  if(path==='/v1/aiTasks/555/goals'){
   if(request.method()==='POST'){const value=request.postDataJSON();goals=[{id:'51',...value,state:'open',origin:'human',ts:new Date().toISOString()}];data=goals[0]}
   else data={goals}
  }
  if(path==='/v1/aiTasks/555/goals/51'){
   if(request.method()==='PUT'){goals=[{...goals[0],...request.postDataJSON()}];data=goals[0]}
   else if(request.method()==='DELETE'){goals=[];data={ok:true}}
  }
  if(path==='/v1/aiTasks/555/constraints'){
   if(request.method()==='POST'){const value=request.postDataJSON();constraints=[{id:'41',...value,origin:'human',ts:new Date().toISOString()}];data=constraints[0]}
   else data={constraints}
  }
  if(path==='/v1/aiTasks/555/constraints/41'){
   if(request.method()==='PUT'){constraints=[{...constraints[0],...request.postDataJSON()}];data=constraints[0]}
   else if(request.method()==='DELETE'){constraints=[];data={ok:true}}
  }

  if(path==='/v1/aiChatMentions')data={items:[{kind:'finding',id:'123',label:'引用漏洞',description:'高危'}]}
  if(path==='/v1/aiFindings/19/retests'){
   if(request.method()==='POST'){notes=request.postDataJSON().notes;retests=[{id:'99',finding_id:'19',conversation_id:'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',status:'pending',verdict:'',notes,summary:'',evidence:'',error:'',created_at:new Date().toISOString()}];data={retest:retests[0],created:true}}
   else data={retests}
  }
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/ai/tasks/555/`,{waitUntil:'domcontentloaded',timeout:120000})
 await page.getByRole('button',{name:'向 Worker 发送消息'}).click();const workerInput=page.getByRole('textbox',{name:'发送给 Worker 的消息'});await workerInput.fill('@漏洞');await page.getByRole('option',{name:/引用漏洞/}).click();await page.getByRole('dialog').getByRole('button',{name:'发送',exact:true}).click();await expect.poll(()=>workerMessage?.message).toMatch(/@\[漏洞#123 引用漏洞\]/);if(!workerMessage.request_id)throw new Error('Worker message request id missing')
 const controlled=page.locator('article').filter({hasText:'暂停并继续'});await controlled.getByRole('button',{name:'暂停意图'}).click();await expect.poll(()=>controls.join(',')).toBe('pause');await controlled.getByRole('button',{name:'继续意图'}).click();await expect.poll(()=>controls.join(',')).toBe('pause,resume');await expect(controlled.getByRole('button',{name:'继续意图'})).toHaveCount(0)
 await page.getByRole('button',{name:'重跑意图'}).first().click();await expect.poll(()=>reruns.join(',')).toBe('61');await expect(page.getByRole('button',{name:'重跑意图'})).toHaveCount(1);await page.getByRole('button',{name:'重跑全部失败意图'}).click();await expect.poll(()=>reruns.join(',')).toBe('61,blocked');await expect(page.getByRole('button',{name:'重跑意图'})).toHaveCount(0)
 await page.getByLabel('目标内容',{exact:true}).fill('验证用户访问权限');await page.getByRole('button',{name:'添加目标',exact:true}).click();await expect(page.getByText('验证用户访问权限',{exact:true})).toBeVisible();await page.getByRole('button',{name:'编辑目标'}).click();await page.getByLabel('编辑目标内容').fill('验证管理员访问权限');await page.getByRole('button',{name:'保存目标'}).click();await expect(page.getByText('验证管理员访问权限',{exact:true})).toBeVisible();await page.getByRole('button',{name:'删除目标'}).click();await expect(page.getByText('验证管理员访问权限',{exact:true})).toHaveCount(0)
 await page.getByLabel('约束内容',{exact:true}).fill('仅测试指定端口');await page.getByLabel('约束内容',{exact:true}).press('Enter');await expect(page.getByText('仅测试指定端口',{exact:true})).toBeVisible();await page.getByRole('button',{name:'编辑约束'}).click();await page.getByLabel('编辑约束内容').fill('禁止写入数据库');await page.getByRole('button',{name:'保存约束'}).click();await expect(page.getByText('禁止写入数据库',{exact:true})).toBeVisible();await page.getByRole('button',{name:'删除约束'}).click();await expect(page.getByText('禁止写入数据库',{exact:true})).toHaveCount(0)
 await page.getByRole('button',{name:'复测',exact:true}).click();await page.getByRole('button',{name:'发起复测',exact:true}).click();await page.getByLabel('补充说明（可选）').fill('任务发现复测');await page.getByRole('button',{name:'开始复测',exact:true}).click();await expect.poll(()=>notes).toBe('任务发现复测');await expect(page.getByRole('link',{name:'查看复测会话'})).toBeVisible()
 await page.getByRole('dialog').getByRole('button',{name:'关闭'}).click();await page.getByRole('tab',{name:/MainAgent/}).click();const input=page.getByRole('textbox',{name:/消息/});await input.fill('@漏洞');await expect(page.getByRole('option',{name:/引用漏洞/})).toBeVisible();await page.getByRole('option',{name:/引用漏洞/}).click();await expect(input).toHaveValue(/@\[漏洞#123 引用漏洞\]/)
 if(errors.length)throw new Error(errors.join('\n'));await page.screenshot({path:'/tmp/artex-task-retest-ui.png',fullPage:true});console.log(JSON.stringify({passed:true,taskRetest:true,taskMentions:true,taskConstraints:true,taskGoals:true,intentRerun:true,intentPauseResume:true,workerMessage:true}))
}finally{await browser.close()}
