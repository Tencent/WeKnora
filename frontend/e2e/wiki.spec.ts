import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
// Phone projects use isMobile:true; desktop cases run in a separate project.

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
      await expect(page.locator('.wiki-graph-legend')).toHaveCount(0)
      await page.locator('.mobile-graph-legend-toggle').click()
      await expect(page.locator('.wiki-graph-legend')).toBeVisible()
      await page.locator('.mobile-graph-legend-toggle').click()
      await expect(page.locator('.wiki-graph-legend')).toHaveCount(0)
      const svg = page.locator('.wiki-graph-canvas svg')
      await expect(svg).toBeVisible()
      const before = await svg.locator(':scope > g').first().getAttribute('transform')
      await page.getByRole('button', { name: '放大', exact: true }).click()
      await expect(svg.locator(':scope > g').first()).not.toHaveAttribute('transform', before || '')
    }
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


test('@desktop desktop graph keeps wheel zoom and hides mobile-only controls', async ({ page }) => {
 await mockApp(page); await page.setViewportSize({ width: 1440, height: 900 })
 await page.goto('/platform/knowledge-bases/mobile-kb?tab=graph')
 await expect(page.locator('.mobile-graph-zoom')).toHaveCount(0)
 await expect(page.locator('.mobile-wiki-directory')).toHaveCount(0)
 const svg = page.locator('.wiki-graph-canvas svg')
 await expect(svg).toBeVisible()
 await expect(svg.locator('g[tabindex=\"0\"]')).toHaveCount(0)
 const graph = svg.locator('.graph-root'); const before = await graph.getAttribute('transform')
 await svg.hover(); await page.mouse.wheel(0, 100)
 await expect(graph).not.toHaveAttribute('transform', before || '')
})

test('resizing a phone graph to desktop removes node tab stops', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.goto('/platform/knowledge-bases/mobile-kb?tab=graph')
  const nodes = page.locator('.wiki-graph-canvas g[role="button"]')
  await expect(nodes.first()).toHaveAttribute('tabindex', '0')
  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(nodes.first()).not.toHaveAttribute('tabindex')
  await expect(page.locator('.mobile-graph-legend-toggle')).toHaveCount(0)
})
