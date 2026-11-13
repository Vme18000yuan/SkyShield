import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const now=new Date().toISOString()
const task={id:'30',name:'aiTasks/30',displayName:'会话测试',description:'',goal:'验证会话',remark:'',categoryId:null,categoryName:'',pinned:false,pinnedAt:null,sourceTaskIds:[],coverageEnabled:true,targetId:null,status:'paused',queued:false,workerCount:1,maxTurns:0,timeoutSeconds:0,planHeartbeatSeconds:600,firstRunAt:null,deadlineAt:null,errorMessage:'',createdAt:now,updatedAt:now,completedAt:null,llmProfileIds:[],activeLlmProfileId:null,llmFailoverState:'default',llmFailoverReason:''}
let sessions=[{seq:0,createdAt:null}], chats={0:[{id:'1',kind:'human_message',summary:'原始会话内容',createdAt:now}]},sent=[],running=false,timer=null,stopped=false,sideSent=[],sideRows={0:[],1:[]}
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
  if(path==='/v1/aiTasks/30/graph')data={nodes:[],edges:[],assets:[],truncated:false}
  if(path==='/v1/aiTasks/30/activities')data={activities:[]}
  if(path==='/v1/aiTasks/30/goals')data={goals:[]}
  if(path==='/v1/aiTasks/30/constraints')data={constraints:[]}
  if(path==='/v1/aiTasks/30/scope')data={scope:[]}
  if(path==='/v1/aiTasks/30/coverage')data={enabled:true,scope_rows:0,denominator:0,tested:0,pct:null,by_type:[]}
  if(path==='/v1/aiTasks/30/assetInterceptRules')data={rules:[]}
  if(path==='/v1/aiTasks/30/mainSessions'){
   if(method==='POST'){const seq=sessions[0].seq+1;sessions=[{seq,createdAt:now},...sessions];chats[seq]=[];data=sessions[0]}
   else data={sessions,current:sessions[0].seq}
  }
  if(/^\/v1\/aiTasks\/30\/mainSessions\/\d+\/messages$/.test(path)){const seq=Number(path.split('/').at(-2));data={messages:chats[seq],nextBefore:''}}
  if(/^\/v1\/aiTasks\/30\/mainSessions\/\d+\/activities$/.test(path)){const seg=Number(path.split('/').at(-2));data={activities:chats[seg].map(item=>({seq:Number(item.id),worker:'mainagent',kind:item.kind==='human_message'?'user':item.kind==='assistant_message'?'result':item.kind,tool:item.tool,tool_use_id:item.tool_use_id,summary:item.summary,ts:item.createdAt,main_seg:seg})),nextBefore:''}}
  if(/^\/v1\/aiTasks\/30\/activities\/\d+$/.test(path)){const id=path.split('/').at(-1),item=Object.values(chats).flat().find(row=>row.id===id);data={detail:item?.detail||item?.summary||''}}
  if(/^\/v1\/aiTasks\/30\/mainSessions\/\d+\/side-questions$/.test(path)){
   const seg=Number(path.split('/').at(-2)),model={model:'fixture',name:'fixture',format:'openai',profile_id:1}
   if(method==='POST'){const input=request.postDataJSON();sideSent.push({seg,question:input.question});const row={id:'side-'+seg,ordinal:1,client_request_id:input.client_request_id,question:input.question,answer:'旁路回答 '+seg,status:'completed',model,snapshot_at:now,created_at:now,sequence:1};sideRows[seg].push(row);data=row}
   else data={items:sideRows[seg],current:null,next_cursor:0,snapshot:{captured_at:now,model,available:true,reason:''}}
  }
  if(path==='/v1/aiChatAttachments'&&method==='POST')data={attachments:[{name:'evidence.txt',path:'uploads/fixture-evidence.txt',size:4}]}
  if(path==='/v1/aiTasks/30/mainChat'&&method==='GET')data={running}
  if(path==='/v1/aiTasks/30/mainChat'&&method==='POST'){const input=request.postDataJSON();sent.push(input);running=true;chats[input.segment].push({id:String(10+sent.length),kind:'human_message',summary:input.message,detail:JSON.stringify({text:input.message,attachments:input.attachments||[]}),createdAt:now});timer=setTimeout(()=>{running=false;if(sent.length===1)chats[input.segment].push({id:'31',kind:'tool_use',tool:'list_assets',tool_use_id:'call-1',summary:'list_assets {}',detail:'{}',createdAt:now},{id:'32',kind:'tool_result',tool:'list_assets',tool_use_id:'call-1',summary:'fixture-output',detail:'fixture-output',createdAt:now});chats[input.segment].push({id:String(20+sent.length),kind:'assistant_message',summary:'已收到 '+input.message,createdAt:now})},sent.length===1?500:10000);data={status:'accepted'}}
  if(path==='/v1/aiTasks/30/mainChat/stop'&&method==='POST'){clearTimeout(timer);running=false;stopped=true;data={stopping:true}}
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/ai/tasks/30/`,{waitUntil:'domcontentloaded',timeout:120000})
 await page.getByRole('tab',{name:'MainAgent 对话'}).click()
 await expect(page.getByText('原始会话内容')).toBeVisible()
 await page.getByRole('button',{name:'新建会话'}).click()
 await expect(page.getByLabel('主 Agent 会话')).toHaveValue('1')
 await expect(page.getByText('原始会话内容')).toHaveCount(0)
 await page.locator('input[type=file]').setInputFiles({name:'evidence.txt',mimeType:'text/plain',buffer:Buffer.from('test')})
 await expect(page.getByRole('button',{name:'移除附件 evidence.txt'})).toBeVisible()
 await page.getByLabel('发送给 MainAgent 的消息').fill('新会话消息')
 await page.getByRole('button',{name:'发送',exact:true}).click()
 await expect.poll(()=>sent[0]?.segment).toBe(1)
 await expect.poll(()=>sent[0]?.attachments?.[0]?.name).toBe('evidence.txt')
 await expect(page.getByText('新会话消息',{exact:true})).toBeVisible()
 await expect.poll(()=>running).toBe(false)
 await expect(page.getByRole('button',{name:'发送',exact:true})).toBeVisible()
 await page.getByRole('button',{name:/list_assets/}).click()
 await expect(page.getByText('fixture-output',{exact:false})).toBeVisible()
 await page.getByRole('button',{name:'旁路提问',exact:true}).click()
 await page.getByRole('textbox',{name:'旁路问题',exact:true}).fill('旁路测试')
 await page.getByRole('button',{name:'发送旁路问题'}).click()
 await expect.poll(()=>sideSent[0]?.seg).toBe(1)
 await expect(page.getByText('旁路回答 1')).toBeVisible()
 await page.getByLabel('主 Agent 会话').selectOption('0')
 await expect(page.getByText('旁路回答 1')).toHaveCount(0)
 await page.getByRole('button',{name:'关闭旁路面板'}).click()
 await expect(page.getByText('原始会话内容')).toBeVisible()
 await expect(page.getByText('新会话消息',{exact:true})).toHaveCount(0)
 await page.getByLabel('发送给 MainAgent 的消息').fill('继续原始会话')
 await page.getByRole('button',{name:'发送',exact:true}).click()
 await expect.poll(()=>sent[1]?.segment).toBe(0)
 await expect(page.getByText('继续原始会话',{exact:true})).toBeVisible()
 await page.getByRole('button',{name:'停止回复'}).click()
 await expect.poll(()=>stopped).toBe(true)
 await expect(page.getByRole('button',{name:'发送',exact:true})).toBeVisible()
 if(errors.length)throw new Error(errors.join('\n'))
 console.log(JSON.stringify({passed:true,create:true,historySwitch:true,sendToSelectedSession:true,pausedChat:true,stop:true,attachment:true,toolTrace:true,sideSession:true}))
}finally{await browser.close()}
