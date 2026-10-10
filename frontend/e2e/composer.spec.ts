import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
// Phone projects use isMobile:true; desktop cases run in a separate project.

test('mobile composer expands tools and keeps draft after viewport change', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.goto('/platform/creatChat')
  await expect(page.locator('.control-bar')).toBeVisible()
  await page.locator('.mobile-composer-more').click()
  await expect(page.locator('.control-left')).toBeVisible()
  await page.locator('.mobile-composer-more').click()
  await expect(page.locator('.control-left')).toBeHidden()
  const input = page.locator('[data-guide="chat-input"] textarea')
  await input.fill('庄子的逍遥是什么意思？')
  await page.setViewportSize({ width: 390, height: 420 })
  await expect(input).toHaveValue('庄子的逍遥是什么意思？')
  await expect(page.locator('[data-guide="chat-send"]')).toBeInViewport()
  await fitsViewport(page)
})

test('landscape and enlarged text keep navigation and composer reachable', async ({ page }) => {
  await mockApp(page)
  await page.setViewportSize({ width: 740, height: 390 })
  await page.goto('/platform/creatChat')
  await page.evaluate(() => { document.documentElement.style.zoom = '1.2'; window.dispatchEvent(new Event('resize')) })
  await expect(page.locator('[data-guide="chat-send"]')).toBeInViewport()
  await fitsViewport(page)
})


test('@desktop desktop composer keeps tools visible without a mobile toggle', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApp(page)
  await page.goto('/platform/creatChat')
  await expect(page.locator('.control-left')).toBeVisible()
  await expect(page.locator('.mobile-composer-more')).toBeHidden()
  await fitsViewport(page)
})

test('history renders long code and references without widening the phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.route('**/api/v1/sessions/mobile-history', route => route.fulfill({ json: { success: true, data: { id: 'mobile-history', title: '庄子问答', tenant_id: 1, knowledge_base_ids: [], chat_model_id: 'chat-model' } } }))
  await page.route('**/api/v1/messages/mobile-history/load?*', route => route.fulfill({ json: { success: true, data: [
    { id: 'question', role: 'user', content: '解释逍遥游', is_completed: true, created_at: '2026-01-01T00:00:00Z' },
    { id: 'answer', role: 'assistant', content: '逍遥游测试回答\n\n```text\n' + 'a'.repeat(180) + '\n```', is_completed: true, created_at: '2026-01-01T00:00:01Z', knowledge_references: [{ id: 'ref-1', chunk_id: 'chunk-1', knowledge_id: 'book-1', knowledge_base_id: 'mobile-kb', knowledge_title: '庄子集释', content: '北冥有鱼，其名为鲲。' }] },
  ] } }))
  await page.goto('/platform/chat/mobile-history')
  await expect(page.getByText('逍遥游测试回答', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '检索完成', exact: true }).click()
  await page.getByRole('button', { name: /检索知识库 找到/ }).click()
  await expect(page.locator('.chat-references-panel')).toBeVisible()
  await expect(page.locator('.chat-references-panel')).toContainText('庄子集释')
  await page.locator('.chat-references-panel__close').click()
  await fitsViewport(page)
  const input = page.locator('[data-guide="chat-input"] textarea')
  await input.fill('继续解释郭象')
  await page.setViewportSize({ width: 390, height: 420 })
  await expect(input).toHaveValue('继续解释郭象')
  await expect(page.locator('[data-guide="chat-send"]')).toBeInViewport()
})

for (const width of [360, 390, 430]) {
  test(`composer grows for typing and contracts when empty at ${width}px${width === 1440 ? ' @desktop' : ''}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await mockApp(page)
    await page.goto('/platform/creatChat')
    const composer = page.locator('[data-guide="chat-input"]')
    const input = composer.locator('textarea')
    const idle = (await composer.boundingBox())!.height
    expect(idle).toBeLessThan(130)
    const bounds = (await composer.boundingBox())!
    expect(bounds.x).toBeGreaterThanOrEqual(10)
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(width - 10)
    await input.focus()
    await expect(composer).toHaveClass(/composer-focused/)
    await page.waitForTimeout(220)
    expect((await composer.boundingBox())!.height).toBeGreaterThan(idle)
    await input.fill('北冥有鱼，其名为鲲。\n鲲之大，不知其几千里也。\n庄子如何理解逍遥？\n请比较几位注家的解释。\n并引用原文。')
    await page.waitForTimeout(220)
    const expanded = (await composer.boundingBox())!.height
    expect(expanded).toBeGreaterThan(idle + 35)
    await page.locator('.mobile-composer-more').click()
    await expect(page.locator('.control-left')).toBeVisible()
    expect((await composer.boundingBox())!.height).toBeLessThan(400)
    await expect(page.locator('[data-guide="chat-send"]')).toBeInViewport()
    await input.focus()
    await expect(page.locator('.control-left')).toBeHidden()
    await input.fill('')
    await input.blur()
    await page.waitForTimeout(220)
    expect((await composer.boundingBox())!.height).toBeLessThanOrEqual(idle + 2)
    await fitsViewport(page)
  })
}

test('mobile attachment picker is reachable without expanding settings', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.goto('/platform/creatChat')
  await expect(page.locator('.control-left')).toBeHidden()
  const picker = page.waitForEvent('filechooser')
  await page.locator('.mobile-composer-quick').first().click()
  const chooser = await picker
  expect(chooser.isMultiple()).toBe(true)
  await expect(page.locator('.control-left')).toBeHidden()
})
