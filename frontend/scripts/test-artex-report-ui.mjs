import { chromium, expect } from '@playwright/test'
const origin=process.env.SKYSHIELD_TEST_ORIGIN||'http://127.0.0.1:33325'
const token='fixture.'+Buffer.from(JSON.stringify({exp:Math.floor(Date.now()/1000)+3600})).toString('base64url')+'.fixture'
const finding={id:1,target:2,url:'https://report.example/',vulnType:'报告测试漏洞',severity:'high',source:'ai',description:'测试发现',isReviewed:false,createdAt:new Date().toISOString(),rawOutput:{scanner:'keep',aiReports:{'19':{report:'## 复现步骤\n\n已保存的 **AI 报告**。',evidenceVersion:0}}}}
let retests=[], submittedNotes, aiRequests=[]
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
try {
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}})
 await context.addCookies([{name:'NEXT_LOCALE',value:'zh',url:origin}]);await context.addInitScript(t=>{localStorage.setItem('accessToken',t);localStorage.setItem('refreshToken',t)},token)
 await context.route('**/api/github/repo',route=>route.fulfill({json:{stars:0}}))
 await context.route('**/v1/**',async route=>{const path=new URL(route.request().url()).pathname;if(path.includes('/aiVulnerability')||path.includes('/aiFindings'))aiRequests.push(path);let data={results:[],totalSize:0,notifications:[],unreadCount:0}
   if(path==='/v1/users/current')data={id:7,username:'fixture',isSuperuser:true}
   if(path==='/v1/vulnerabilities/'||path==='/v1/vulnerabilities')data={results:[finding],totalSize:1,total:1,page:1,pageSize:10,nextPageToken:''}
   if(path==='/v1/vulnerabilityStatistics')data={total:1,pendingCount:1,reviewedCount:0,highCount:1}
   if(path==='/v1/aiVulnerabilityFindings/1')data={findings:[{id:'19',node_id:'29',task_id:'39',name:'报告测试漏洞',status:'pending'}]}
   if(path==='/v1/aiFindings/19/retests'){
     if(route.request().method()==='POST'){submittedNotes=route.request().postDataJSON().notes;retests=[{id:'11',finding_id:'19',conversation_id:'22222222-2222-4222-8222-222222222222',status:'pending',verdict:'',notes:submittedNotes,summary:'',evidence:'',error:'',created_at:new Date().toISOString()}];data={retest:retests[0],created:true}}
     else data={retests}
   }

   if(path==='/v1/upgradeOperations:active')return route.fulfill({status:404,json:{error:{code:'NOT_FOUND'}}})
   return route.fulfill({json:data})
 })
 const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(String(e)))
 await page.goto(`${origin}/vulnerabilities/`,{waitUntil:'domcontentloaded',timeout:120000})
 await page.getByText('报告测试漏洞',{exact:true}).first().click();await page.getByRole('tab',{name:'AI 报告'}).click()
 await expect(page.getByText('复现步骤',{exact:true})).toBeVisible();await expect(page.getByText('已保存的',{exact:false})).toBeVisible()
 await expect.poll(()=>aiRequests.length).toBeGreaterThan(0);await page.getByRole('tab',{name:'复测',exact:true}).click();await page.getByRole('button',{name:'发起复测',exact:true}).click();await page.getByLabel('补充说明（可选）').fill('使用原测试账号');await page.getByRole('button',{name:'开始复测',exact:true}).click();await expect.poll(()=>submittedNotes).toBe('使用原测试账号');await expect(page.getByRole('link',{name:'查看复测会话'})).toBeVisible()
 await page.screenshot({path:'/tmp/artex-report-ui.png',fullPage:true});if(errors.length)throw new Error(errors.join('\n'))
 console.log(JSON.stringify({passed:true,reportTab:true,markdown:true,retest:true}))
} finally {await browser.close()}
