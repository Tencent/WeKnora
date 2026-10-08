import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
test.use({ hasTouch: true })

test('a truncated historical answer explains the cutoff without hovering, including after reload', async ({ page }) => {
  await mockApp(page)
  await page.route('**/api/v1/sessions/cutoff-session', route => route.fulfill({ json: { success: true, data: { id: 'cutoff-session', title: '截断回答', tenant_id: 1 } } }))
  await page.route('**/api/v1/messages/cutoff-session**', route => route.fulfill({ json: { success: true, data: [
    { id: 'cutoff-user', role: 'user', content: '解释庄子', is_completed: true, created_at: '2026-01-01T00:00:00Z' },
    { id: 'cutoff-answer', role: 'assistant', content: '部分回答\n\n## 未完成的标题', is_completed: true, created_at: '2026-01-01T00:00:01Z', agent_steps: [{ iteration: 0, thought: '部分回答\n\n## 未完成的标题', truncated: true, tool_calls: [], timestamp: '2026-01-01T00:00:01Z' }] }
  ] } }))
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/platform/chat/cutoff-session')
  await expect(page.locator('.answer-truncated-notice')).toBeVisible()
  await expect(page.locator('.answer-truncated-notice')).toContainText('单次输出上限')
  await page.reload()
  await expect(page.locator('.answer-truncated-notice')).toBeVisible()
  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(page.locator('.answer-truncated-notice')).toBeVisible()
})
