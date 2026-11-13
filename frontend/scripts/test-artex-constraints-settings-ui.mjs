import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
let settings={constraints_inject_planner:true,constraints_inject_worker:true,workers:3,worker_pools:[],task_concurrency_enabled:false,task_concurrency_limit:5,web_search_enabled:false,web_search_backend:'ddgs',python_interpreter:'',noa_compaction:false},patches=[]
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try {
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{const request=route.request(),path=new URL(request.url()).pathname;let data={results:[],totalSize:0,notifications:[],unreadCount:0}
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  if(path==='/v1/aiLlmProfiles')data={profiles:[]}
  if(path==='/v1/aiRuntimeSettings'){if(request.method()==='PATCH'){const patch=request.postDataJSON();patches.push(patch);settings={...settings,...patch}};data=settings}
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/settings/ai/runtime/`,{waitUntil:'domcontentloaded',timeout:120000})
 const planner=page.getByRole('switch',{name:'注入规划者（planner）'}),worker=page.getByRole('switch',{name:'注入执行者（worker）'})
 await expect(planner).toBeChecked();await planner.click();await expect.poll(()=>patches.some(p=>p.constraints_inject_planner===false)).toBe(true)
 await expect(worker).toBeEnabled();await worker.click();await expect.poll(()=>patches.some(p=>p.constraints_inject_worker===false)).toBe(true)
 if(errors.length)throw new Error(errors.join('\n'));console.log(JSON.stringify({passed:true,plannerSwitch:true,workerSwitch:true}))
}finally{await browser.close()}
