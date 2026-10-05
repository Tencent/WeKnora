import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'

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
  await expect(page.locator('.mobile-app-header')).toBeVisible()
  await expect(page.locator('[data-guide="chat-send"]')).toBeInViewport()
  await fitsViewport(page)
})


test('desktop composer keeps tools visible without a mobile toggle', async ({ page }) => {
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
  await expect(page.locator('[data-guide="chat-send"]')).toBeInViewport()
})
