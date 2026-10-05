import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'

for (const tab of ['wiki', 'graph']) {
  test(`mobile ${tab} remains usable with real content`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockApp(page)
    await page.goto(`/platform/knowledge-bases/mobile-kb?tab=${tab}`)
    await expect(page.locator('.wiki-browser')).toBeVisible()
    if (tab === 'wiki') {
      await page.locator('.mobile-wiki-directory').click()
      await expect(page.locator('.wiki-sidebar')).toBeVisible()
      await page.locator('.wiki-sidebar').getByText('逍遥游', { exact: true }).first().click()
      await expect(page.locator('.wiki-sidebar')).toBeHidden()
      await expect(page.locator('.wiki-reader-title')).toContainText('逍遥游')
      const reader = page.locator('.wiki-reader')
      await reader.evaluate(node => { node.scrollTop = 200 })
      await page.locator('.mobile-wiki-directory').click()
      await page.locator('.mobile-wiki-directory').click()
      expect(await reader.evaluate(node => node.scrollTop)).toBeGreaterThan(0)
    } else {
      const svg = page.locator('.wiki-graph-canvas svg')
      await expect(svg).toBeVisible()
      const before = await svg.locator(':scope > g').first().getAttribute('transform')
      await page.getByRole('button', { name: '放大', exact: true }).click()
      await expect(svg.locator(':scope > g').first()).not.toHaveAttribute('transform', before || '')
    }
    await fitsViewport(page)
    await page.screenshot({ path: `test-results/${tab}-390.png` })
  })
}

test('touch graph supports pan and pinch without opening a node', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })
  const page = await context.newPage()
  await mockApp(page)
  await page.goto('/platform/knowledge-bases/mobile-kb?tab=graph')
  const graph = page.locator('.wiki-graph-canvas svg')
  await expect(graph).toBeVisible()
  const bounds = await graph.boundingBox()
  if (!bounds) throw new Error('graph has no viewport')
  const cdp = await context.newCDPSession(page)
  const x = bounds.x + bounds.width / 2, y = bounds.y + bounds.height / 2
  const before = await graph.locator('.graph-root').getAttribute('transform')
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: x - 30, y }, { x: x + 30, y }] })
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: x - 60, y: y + 20 }, { x: x + 60, y: y + 20 }] })
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  await expect(graph.locator('.graph-root')).not.toHaveAttribute('transform', before || '')
  await expect(page.locator('.wiki-graph-drawer .t-drawer__content-wrapper')).toBeHidden()
  await context.close()
})

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
  await page.locator('.mobile-app-header button').first().click()
  await page.locator('[data-guide="nav-creatChat"]').click()
  await expect(page.locator('.upload-tasks-panel')).toBeVisible()
  await fitsViewport(page)
})
