import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
let row={id:7,rule_id:3,conversation_id:'22222222-2222-4222-8222-222222222222',agent_name:'auto',tool_name:'Bash',tool_input:{command:'printf fixture'},status:'pending',decision_source:'rule',reason:'需要确认',created_at:new Date().toISOString(),conv_title:'审批测试对话',conv_agent_key:'auto',rule_name:'测试规则',audit:{correlation:'exact',input_digest:'fixture',user_message:'',context:null,captured_at:new Date().toISOString(),initial_action:'ask',initial_reason:'需要确认',execution_status:'not_started'}}
let decision
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try {
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{const request=route.request(),path=new URL(request.url()).pathname;let data={results:[],totalSize:0,notifications:[],unreadCount:0}
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  if(path==='/v1/aiApprovals')data={items:[row],total:1}
  if(path==='/v1/aiApprovals/pending')data={pending:row.status==='pending'?[row]:[]}
  if(path==='/v1/aiApprovals/7'||path==='/v1/aiApprovals/7/detail')data=row
  if(path==='/v1/aiApprovals/7/decision'){decision=request.postDataJSON().decision;row={...row,status:decision,audit:{...row.audit,effective_action:decision==='allowed'?'allow':'deny'}};data={ok:true}}
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/ai/approvals/`,{waitUntil:'domcontentloaded',timeout:120000})
 await expect(page.getByText('待处理（1）')).toBeVisible();await page.getByRole('button',{name:'展开审批 #7'}).first().click();await expect(page.getByText('printf fixture',{exact:false}).first()).toBeVisible();await page.getByRole('button',{name:'允许',exact:true}).first().click();await expect.poll(()=>decision).toBe('allowed');await expect(page.getByText('已允许',{exact:true}).first()).toBeVisible()
 if(errors.length)throw new Error(errors.join('\n'));await page.screenshot({path:'/tmp/artex-approval-ui.png',fullPage:true});console.log(JSON.stringify({passed:true,pending:true,decision:true}))
}finally{await browser.close()}
