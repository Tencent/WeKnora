import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
test.use({ hasTouch: true })

for (const width of [360, 390, 430]) {
  test(`document heading and secondary controls collapse at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 700 })
    await mockApp(page)
    await page.route('**/api/v1/knowledge-bases/mobile-kb', route => route.fulfill({ json: { success: true, data: {
      id: 'mobile-kb', name: '庄子知识库：逍遥游、齐物论与历代注疏研究', type: 'document', tenant_id: 1,
      description: '以《庄子》原典、注译、哲学研究及庄子学史为核心。支持篇章与寓言解读、概念辨析、注家比较、版本校勘及接受史研究。'.repeat(5),
      summary_model_id: 'chat-model', embedding_model_id: 'embedding', storage_backend_id: 'test-storage',
      indexing_strategy: { wiki_enabled: true }, chunking_config: { chunk_size: 512, chunk_overlap: 50 },
    } } }))
    await page.route('**/api/v1/knowledge-bases/mobile-kb/knowledge?**', route => route.fulfill({ json: {
      success: true, total: 30, data: Array.from({ length: 30 }, (_, index) => ({
        id: `book-${index}`, file_name: `庄子哲学及其演变与历代注释研究第${index}册.epub`, file_type: 'epub',
        type: 'file', parse_status: 'completed', summary_status: 'completed', file_size: 3000000,
        updated_at: '2026-10-05T00:00:00Z', tags: [],
      })),
    } }))
    await page.goto('/platform/knowledge-bases/mobile-kb')
    const list = page.locator('.doc-scroll-container')
    await expect(page.locator('.doc-folder-path__count')).toContainText('30')
    await expect(page.locator('.mobile-description-toggle')).toBeVisible()
    await expect(page.locator('#kb-description')).toBeHidden()
    const compactHeight = (await list.boundingBox())!.height
    expect(compactHeight).toBeGreaterThan(300)
    await page.locator('.mobile-description-toggle').click()
    await expect(page.locator('#kb-description')).toBeVisible()
    expect((await list.boundingBox())!.height).toBeLessThan(compactHeight)
    await page.locator('.mobile-description-toggle').click()
    await expect(page.locator('#kb-description')).toBeHidden()
    const more = page.locator('.responsive-toolbar-toggle')
    await more.click()
    await expect(page.locator('.doc-view-toggle')).toBeVisible()
    await page.locator('.doc-view-toggle-btn').last().click()
    await more.click()
    await expect(page.locator('.doc-view-toggle')).toBeHidden()
    await more.click()
    await expect(page.locator('.doc-view-toggle-btn').last()).toHaveAttribute('aria-pressed', 'true')
    await more.click()
    await list.evaluate(node => { node.scrollTop = 150 })
    await page.locator('.mobile-description-toggle').click()
    await page.locator('.mobile-description-toggle').click()
    expect(await list.evaluate(node => node.scrollTop)).toBeGreaterThan(0)
    expect((await list.boundingBox())!.height).toBeGreaterThanOrEqual(compactHeight - 2)
    await expect(page.getByRole('button', { name: '添加文档', exact: true })).toBeInViewport()
    await fitsViewport(page)
  })
}

test('desktop retains the description and full toolbar', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApp(page)
  await page.goto('/platform/knowledge-bases/mobile-kb')
  await expect(page.locator('#kb-description')).toBeVisible()
  await expect(page.locator('.responsive-toolbar-toggle')).toBeHidden()
  await expect(page.locator('.doc-view-toggle')).toBeVisible()
  await expect(page.locator('.doc-sort-trigger')).toBeVisible()
})

test('FAQ secondary actions expand and collapse on phones', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 700 })
  await mockApp(page)
  await page.route('**/api/v1/knowledge-bases/mobile-kb', route => route.fulfill({ json: { success: true, data: {
    id: 'mobile-kb', name: '庄子问答知识库', type: 'faq', tenant_id: 1,
    summary_model_id: 'chat-model', embedding_model_id: 'embedding', chunking_config: {},
  } } }))
  await page.goto('/platform/knowledge-bases/mobile-kb')
  await expect(page.locator('.faq-scroll-container')).toBeVisible()
  await expect(page.getByRole('button', { name: '检索测试', exact: true })).toBeHidden()
  await page.locator('.responsive-toolbar-toggle').click()
  await expect(page.locator('.responsive-toolbar-panel')).toBeVisible()
  await page.locator('.responsive-toolbar-toggle').click()
  await expect(page.locator('.responsive-toolbar-panel')).toBeHidden()
  await fitsViewport(page)
})

for (const width of [360, 430, 1440]) {
 test(`knowledge home cards remain visible and fit ${width}px`, async ({ page }) => {
  await mockApp(page); await page.setViewportSize({ width, height: 844 })
  await page.goto('/platform/knowledge-bases')
  await expect(page.locator('.kb-card').first()).toBeVisible()
  await fitsViewport(page)
 })
}
test('file selection, confirmation and upload panel survive navigation', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.goto('/platform/knowledge-bases/mobile-kb')
  await expect(page.locator('.kb-upload-source-dropdown').first()).toBeVisible()
  await page.locator('.kb-upload-source-dropdown input[type=file]:not([webkitdirectory])').first().setInputFiles({
    name: '庄子研究测试文档.txt', mimeType: 'text/plain', buffer: Buffer.from('北冥有鱼，其名为鲲。'),
  })
  const dialog = page.locator('.upload-confirm-modal')
  await expect(dialog).toBeVisible()
  await expect(dialog.locator('.modal-footer button').last()).toBeInViewport()
  await dialog.locator('.modal-footer button').last().click()
  await expect(page.locator('.upload-tasks-panel')).toBeVisible()
  await fitsViewport(page)
  await page.evaluate(() => (document.querySelector('#app') as any).__vue_app__.config.globalProperties.$router.push('/platform/creatChat'))
  await expect(page.locator('.upload-tasks-panel')).toBeVisible()
})
