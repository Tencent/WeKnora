import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
test.use({ hasTouch: true })

test('starter questions fill a draft without creating a session and preserve their source on send', async ({ page }) => {
  await mockApp(page)
  let creations = 0
  await page.route('**/api/v1/agents**', route => route.fulfill({ json: { success: true, data: route.request().url().includes('suggested-questions') ? { questions: [{ question: '解释逍遥游', source: 'knowledge', knowledge_base_id: 'mobile-kb' }] } : [{ id: 'builtin-quick-answer', name: '快速问答', is_builtin: true, config: { agent_mode: 'quick-answer', model_id: 'chat-model', kb_selection_mode: 'none' } }] } }))
  await page.route('**/api/v1/sessions', route => { creations++; return route.fulfill({ json: { success: true, data: { id: 'draft-origin' } } }) })
  await page.route('**/api/v1/sessions/draft-origin', route => route.fulfill({ json: { success: true, data: { id: 'draft-origin', title: '测试', tenant_id: 1 } } }))
  await page.route('**/api/v1/*-chat/draft-origin', route => route.fulfill({ contentType: 'text/event-stream', body: 'data: '+JSON.stringify({ response_type: 'answer', content: '测试回答', id: 'answer', done: true })+'\n\ndata: '+JSON.stringify({ response_type: 'complete', id: 'answer', done: true })+'\n\n' }))
  await page.goto('/platform/creatChat')
  await page.getByRole('button', { name: '解释逍遥游', exact: true }).click()
  const input = page.locator('[data-guide="chat-input"] textarea')
  await expect(input).toHaveValue('解释逍遥游')
  await expect(input).toBeFocused()
  expect(creations).toBe(0)
  await input.fill('解释逍遥游，并比较注家。')
  const sent = page.waitForRequest(request => /\/(?:agent|knowledge|qa|chat).*chat\/draft-origin/.test(new URL(request.url()).pathname) || request.url().includes('-chat/draft-origin'))
  await page.locator('[data-guide="chat-send"]').click()
  const payload = (await sent).postDataJSON()
  expect(payload.query).toBe('解释逍遥游，并比较注家。')
  expect(payload.question_origin.knowledge_base_id).toBe('mobile-kb')
  expect(creations).toBe(1)
})
