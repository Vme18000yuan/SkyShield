import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const profileId='11111111-1111-4111-8111-111111111111'
let rules=[{id:1,name:'原版默认规则',enabled:true,priority:100,match_target:'tool_input',match_type:'regex',pattern:'rm',action:'deny',message:'阻止',timeout_enabled:false,timeout_seconds:60,timeout_action:'deny',created_at:new Date().toISOString(),updated_at:new Date().toISOString()}],enabledTools=['Bash'],judge={enabled:false,profile_id:null,prompt:'原版审查模板',timeout_seconds:15,retry_count:3,retry_interval_ms:1000,fail_action:'ask',ask_timeout_seconds:300,ask_timeout_action:'deny',incomplete_script_action:'model'},createdRule,savedJudge,savedTools
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try {
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{const request=route.request(),path=new URL(request.url()).pathname,method=request.method();let data={results:[],totalSize:0,notifications:[],unreadCount:0}
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  if(path==='/v1/aiInterceptRules'){if(method==='POST'){createdRule=request.postDataJSON();rules=[...rules,{...createdRule,id:2,created_at:new Date().toISOString(),updated_at:new Date().toISOString()}];data=rules.at(-1)}else data={rules}}
  if(path==='/v1/aiInterceptTools'){if(method==='PUT'){savedTools=request.postDataJSON().enabled_tools;enabledTools=savedTools};data={enabled_tools:enabledTools}}
  if(path==='/v1/aiInterceptJudge'){if(method==='PUT'){savedJudge=request.postDataJSON();judge=savedJudge};data=judge}
  if(path==='/v1/aiInterceptJudge/usage')data={calls:0,input_tokens:0,output_tokens:0,cache_read_tokens:0,cache_write_tokens:0,daily:[]}
  if(path==='/v1/aiLlmProfiles')data={profiles:[{id:profileId,displayName:'审批测试模型',provider:'openai',model:'judge-fixture',isDefault:true,hasApiKey:true,baseUrl:'https://fixture.invalid'}]}
  if(path==='/v1/aiTools')data={tools:[]}
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(String(e)))
 await page.goto(`${origin}/settings/ai/intercept/`,{waitUntil:'domcontentloaded',timeout:120000});await expect(page.getByText('原版默认规则',{exact:true})).toBeVisible()
 await page.getByRole('button',{name:'新建规则',exact:true}).click();await page.getByPlaceholder('给规则起个名字').fill('新增审批规则');await page.getByPlaceholder('rm -rf').fill('fixture-command');await page.getByRole('dialog').getByRole('button',{name:'保存',exact:true}).click();await expect.poll(()=>createdRule?.name).toBe('新增审批规则');await expect(page.getByText('新增审批规则',{exact:true})).toBeVisible()
 await page.getByRole('button',{name:'调整范围'}).click();await expect(page.getByRole('dialog')).toBeVisible();await page.locator('#scope-Bash').click();await page.getByRole('dialog').getByRole('button',{name:'保存',exact:true}).click();await expect.poll(()=>savedTools?.includes('Bash')).toBe(false)
 await page.getByRole('tab',{name:'模型配置'}).click();const judgeSwitch=page.locator('div').filter({hasText:/^模型兜底审批/}).filter({has:page.getByRole('switch')}).last().getByRole('switch').first();await judgeSwitch.click();const modelField=page.getByText('审批模型',{exact:true}).locator('..');await modelField.getByRole('combobox').click();await page.getByRole('option',{name:'审批测试模型（judge-fixture）'}).click();await expect(modelField.getByRole('combobox')).toContainText('审批测试模型');await page.getByRole('button',{name:'保存配置',exact:true}).click();await expect.poll(()=>savedJudge?.profile_id).toBe(profileId)
 if(errors.length)throw new Error(errors.join('\n'));await page.screenshot({path:'/tmp/artex-intercept-settings-ui.png',fullPage:true});console.log(JSON.stringify({passed:true,rules:true,toolScope:true,judgeProfile:true}))
}finally{await browser.close()}
