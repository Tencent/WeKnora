import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
test.use({ hasTouch: true })

test('unconfigured agent explains why sending is blocked and retains the draft', async ({ page }) => {
  await mockApp(page)
  await page.route('**/api/v1/models', route => route.fulfill({ json: { success: true, data: [] } }))
  await page.route('**/api/v1/agents**', route => route.fulfill({ json: { success: true, data: [{ id: 'builtin-quick-answer', name: '快速问答', is_builtin: true, config: { agent_mode: 'quick-answer', model_id: '', kb_selection_mode: 'none' } }] } }))
  let creations = 0
  await page.route('**/api/v1/sessions', route => { creations++; return route.fulfill({ json: { success: true, data: {} } }) })
  await page.goto('/platform/creatChat')
  const input = page.locator('[data-guide="chat-input"] textarea')
  await input.fill('保留这条问题')
  await page.locator('[data-guide="chat-send"]').click()
  await expect(page.locator('.composer-submission-error')).toBeVisible()
  await expect(page.locator('.composer-submission-error')).toContainText('快速问答')
  await expect(input).toHaveValue('保留这条问题')
  expect(creations).toBe(0)
})
