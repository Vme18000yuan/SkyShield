// Isolated fixture: all API requests are intercepted and no product data is changed.
import { chromium, expect } from '@playwright/test'

const origin = process.env.SKYSHIELD_TEST_ORIGIN || 'http://127.0.0.1:33325'
const token = 'fixture.' + Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString('base64url') + '.fixture'
const now = new Date().toISOString()
const asset = { name: 'assetEntities/42', id: '42', assetType: 'service', displayType: 'web', label: 'https://fixture.example/', method: null, sourceCount: 1, firstSeenAt: now, lastSeenAt: now, organizations: [{ id: 1, name: '测试组织' }] }
const ref = { id: '1700000000-0001-abcdef', url: 'https://fixture.example/login', method: 'POST', status: 200, side: 'request', excerpt: 'fixture credential excerpt', accounts: [0] }
const items = [
  { id: '1', task_id: '30', task_name: '资产测试任务', kind: 'credential', title: '服务账号', content: 'fixture-user', evidence: 'fixture-evidence', confidence: 'observed', status: 'unverified', credentials: { state: 'ready', accounts: [{ username: 'fixture-user', password: 'fixture-password' }], traffic: [ref] } },
  { id: '2', task_id: '31', task_name: '另一任务', kind: 'lead', title: '线索记录', content: 'another clue', evidence: 'source', confidence: 'inferred', status: 'pending' },
]
const requests = []
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
try {
  const context = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1440, height: 1000 } })
  await context.addCookies([{ name: 'NEXT_LOCALE', value: 'zh', url: origin }])
  await context.addInitScript(t => { localStorage.setItem('accessToken', t); localStorage.setItem('refreshToken', t) }, token)
  await context.route('**/api/github/repo', route => route.fulfill({ json: { stars: 0 } }))
  await context.route('**/v1/**', async route => {
    const request = route.request(), url = new URL(request.url()), path = url.pathname
    requests.push({ path, method: request.method(), query: url.searchParams.toString() })
    let data = { results: [], totalSize: 0, notifications: [], unreadCount: 0 }
    if (path === '/v1/users/current') data = { id: 7, username: 'fixture', isSuperuser: true }
    if (path === '/v1/upgradeOperations:active') return route.fulfill({ status: 404, json: { error: { code: 'NOT_FOUND' } } })
    if (path === '/v1/assetInventory') data = { items: [{ kind: 'asset', asset }], totalSize: 1, page: 1 }
    if (path === '/v1/aiAssets/42/intelligence') {
      const kind = url.searchParams.get('kind'), q = url.searchParams.get('q')
      const filtered = items.filter(item => (!kind || item.kind === kind) && (!q || (item.title + item.content + item.evidence).includes(q)))
      data = { items: filtered, total: filtered.length, enabled: true }
    }
    if (path === `/v1/aiAssets/42/intelligence/1/traffic/${ref.id}`) data = { reference: ref, req: 'POST /login', resp: 'HTTP 200', unavailable: false }
    return route.fulfill({ json: data })
  })
  const page = await context.newPage(), errors = []
  page.on('pageerror', error => errors.push(String(error)))
  await page.goto(`${origin}/assets/`, { waitUntil: 'domcontentloaded', timeout: 120000 })
  const row = page.getByRole('row').filter({ hasText: asset.label })
  await expect(row).toBeVisible({ timeout: 120000 })
  await row.getByRole('button', { name: '资产情报' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('服务账号')).toBeVisible()
  await expect(dialog.getByRole('link', { name: '资产测试任务' })).toHaveAttribute('href', '/ai/tasks/30/')
  await dialog.locator('article').filter({ hasText: '服务账号' }).getByText('查看内容与证据').click()
  await expect(dialog.getByText(/fixture-password/)).toBeVisible()
  await dialog.getByRole('button', { name: '查看关联流量' }).click()
  await expect(dialog.getByText('POST /login')).toBeVisible()
  await expect(dialog.getByText('HTTP 200')).toBeVisible()
  await dialog.getByRole('button', { name: '关闭流量详情' }).click()
  await dialog.getByLabel('情报类型').selectOption('lead')
  await expect(dialog.getByText('线索记录')).toBeVisible()
  await expect(dialog.getByText('服务账号')).toHaveCount(0)
  await dialog.getByLabel('搜索标题、内容或证据').fill('another')
  await expect.poll(() => requests.some(r => r.path === '/v1/aiAssets/42/intelligence' && r.query.includes('q=another'))).toBe(true)
  await expect(dialog.getByText('共 1 条')).toBeVisible()
  expect(requests.filter(r => r.path.startsWith('/v1/aiAssets/')).every(r => r.method === 'GET')).toBe(true)
  expect(errors).toEqual([])
  console.log(JSON.stringify({ passed: true, asset: true, sourceTask: true, credentialTraffic: true, filtering: true, readOnly: true }))
} finally { await browser.close() }
