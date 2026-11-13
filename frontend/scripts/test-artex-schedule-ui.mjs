import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const bigId='9007199254740901'
const firstPage=Array.from({length:50},(_,i)=>({id:String(9007199254742000n-BigInt(i)),displayName:`普通任务 ${i}`,description:'测试',goal:'核验资产',status:'created',queued:false,workerCount:3,createdAt:'2026-11-01T00:00:00Z'}))
const older=[{id:bigId,displayName:'大编号测试任务',description:'fixture',goal:'核验资产',status:'paused',queued:false,workerCount:2,createdAt:'2026-11-01T00:00:00Z'}]
let rows=[],created,patch,action,taskRequests=0
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try{
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{
  const request=route.request(),url=new URL(request.url()),path=url.pathname,method=request.method();let data={results:[],totalSize:0,notifications:[],unreadCount:0}
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/aiTasks'){taskRequests++;data=url.searchParams.has('before')?{tasks:older,nextBefore:''}:{tasks:firstPage,nextBefore:firstPage.at(-1).id}}
  if(path==='/v1/aiSchedules'){
   if(method==='POST'){created=request.postDataJSON();rows=[{id:'101',displayName:created.displayName,remark:'',enabled:created.enabled??true,timezoneMode:created.timezoneMode,timezone:'Asia/Shanghai',scheduleType:created.scheduleType,runDate:created.runDate,endDate:created.endDate,weekdays:created.weekdays,startTime:created.startTime,endTime:created.endTime,startImmediately:created.startImmediately,status:'waiting',lastError:'',lastTransitionAt:null,createdAt:'2026-11-01T00:00:00Z',updatedAt:'2026-11-01T00:00:00Z',taskIds:created.taskIds,runCount:0}] ;data=rows[0]}
   else data={schedules:rows}
  }
  if(path==='/v1/aiSchedules/101'){
   if(method==='PATCH'){patch=request.postDataJSON();rows[0]={...rows[0],...patch};data=rows[0]}
   else data={...rows[0],history:[{id:'91',taskId:null,action:'running',success:true,message:'计划窗口',createdAt:'2026-11-01T00:00:00Z'}]}
  }
  if(path==='/v1/aiSchedules/101/actions'){action=request.postDataJSON();data=action.action==='run'?{started:1,errors:[]}:{enabled:action.action==='enable'}}
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/ai/schedules/`,{waitUntil:'domcontentloaded',timeout:120000})
 await page.getByRole('button',{name:'新建计划'}).click()
 await page.getByPlaceholder('例如：周末持续扫描').fill('工作日资产复核')
 await expect(page.getByText('按周',{exact:true}).first()).toBeVisible()
 await page.getByPlaceholder('检索任务名称、描述或 ID').fill('大编号测试任务')
 await page.getByText('大编号测试任务').click()
 await page.getByRole('button',{name:'保存计划'}).click()
 await expect.poll(()=>created?.taskIds?.[0]).toBe(bigId)
 await expect(page.getByRole('button',{name:'计划 #101名称，点击编辑'})).toBeVisible()
 await page.getByRole('button',{name:'计划 #101名称，点击编辑'}).click()
 const rename=page.getByRole('textbox',{name:'计划 #101名称'});await rename.fill('更新后的计划');await rename.press('Enter')
 await expect.poll(()=>patch?.displayName).toBe('更新后的计划')
 await page.getByRole('button',{name:'查看',exact:true}).click();await expect(page.getByText('计划窗口',{exact:true})).toBeVisible()
 await page.getByRole('button',{name:'立即运行',exact:true}).click();await expect.poll(()=>action?.action).toBe('run')
 if(taskRequests<2)throw new Error('Task list was truncated at its first page')
 if(errors.length)throw new Error(errors.join('\n'))
 await page.screenshot({path:'/tmp/artex-schedule-ui.png',fullPage:true})
 console.log(JSON.stringify({passed:true,nativeCalendar:true,taskPaging:true,exactTaskID:true,metadata:true,timeline:true,runNow:true}))
}finally{await browser.close()}
