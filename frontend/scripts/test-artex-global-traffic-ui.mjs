// Isolated browser fixture; all API calls are intercepted.
import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const conversation='22222222-2222-4222-8222-222222222222'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const now=new Date().toISOString(),requests=[]
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try{
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{
  const req=route.request(),url=new URL(req.url()),path=url.pathname;requests.push({path,method:req.method(),params:Object.fromEntries(url.searchParams)})
  let data={results:[],totalSize:0,notifications:[],unreadCount:0}
  if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
  if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
  if(path==='/v1/aiTraffic'){
   const page=Number(url.searchParams.get('page')||0)
   const base={id:'same-id',host:'fixture.example',method:'GET',status:200,content_type:'text/html',response_length:10,captured_at:now}
   const items=page===0?[{...base,url:'https://fixture.example/task',source_type:'task',source_id:'30',source_name:'测试任务'},{...base,url:'https://fixture.example/chat',source_type:'conversation',source_id:conversation,source_name:'测试对话'}]:[{...base,url:'https://fixture.example/older',source_type:'task',source_id:'30',source_name:'测试任务'}]
   data={items,total:26,page,size:25}
  }
  if(path==='/v1/aiTraffic/hosts')data={hosts:[{host:'fixture.example',count:26}]}
  if(path==='/v1/aiTasks/30/traffic/exchange')data={request:'task request',response:'task response'}
  if(path===`/v1/aiConversations/${conversation}/traffic/exchange`)data={request:'conversation request',response:'conversation response'}
  await route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(String(error)))
 await page.goto(`${origin}/ai/traffic/`,{waitUntil:'domcontentloaded',timeout:120000})
 await expect(page.getByRole('heading',{name:'流量记录'})).toBeVisible({timeout:120000})
 await expect(page.getByRole('link',{name:'测试任务'})).toHaveAttribute('href','/ai/tasks/30/')
 await expect(page.getByRole('link',{name:'测试对话'})).toHaveAttribute('href',`/ai/chat/?c=${conversation}`)
 await page.getByRole('row').filter({hasText:'https://fixture.example/task'}).getByRole('button',{name:'查看'}).click()
 await expect(page.getByRole('dialog').getByText('task response')).toBeVisible();await page.getByRole('dialog').getByRole('button',{name:'关闭'}).click()
 await page.getByRole('row').filter({hasText:'https://fixture.example/chat'}).getByRole('button',{name:'查看'}).click()
 await expect(page.getByRole('dialog').getByText('conversation response')).toBeVisible();await page.getByRole('dialog').getByRole('button',{name:'关闭'}).click()
 await page.getByRole('button',{name:'选择主机'}).click();await page.getByLabel('搜索已录制主机').fill('fixture');await page.getByRole('button',{name:'fixture.example · 26'}).click();await expect.poll(()=>requests.some(r=>r.path==='/v1/aiTraffic'&&r.params.host==='fixture.example')).toBe(true)
 await page.getByText('高级筛选',{exact:true}).click();await page.getByLabel('状态码',{exact:true}).selectOption('4xx');await page.getByRole('button',{name:'筛选',exact:true}).click()
 await expect.poll(()=>requests.some(r=>r.path==='/v1/aiTraffic'&&r.params.status==='4xx')).toBe(true)
 await page.getByRole('button',{name:'下一页'}).click();await expect(page.getByText('https://fixture.example/older')).toBeVisible()
 expect(requests.filter(r=>r.path.startsWith('/v1/aiTraffic')).every(r=>r.method==='GET')).toBe(true)
 expect(errors).toEqual([])
 console.log(JSON.stringify({passed:true,scopedDetails:true,sourceNames:true,filters:true,pagination:true}))
}finally{await browser.close()}
