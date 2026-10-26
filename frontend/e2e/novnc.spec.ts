import { test, expect } from '@playwright/test'
import { mockApp } from './fixtures'

test('@desktop chat sends and survives reload when the remote desktop decoder probe never resolves', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 844 })
  await mockApp(page)
  await page.route('**/api/v1/agents**', route => route.fulfill({ json: { success: true, data: [{ id: 'builtin-quick-answer', name: '快速问答', is_builtin: true, config: { agent_mode: 'quick-answer', model_id: 'chat-model', kb_selection_mode: 'none' } }] } }))
  let creations = 0
  await page.route('**/api/v1/sessions', route => { creations++; return route.fulfill({ json: { success: true, data: { id: 'delayed-session' } } }) })
  await page.route('**/api/v1/sessions/delayed-session', route => route.fulfill({ json: { success: true, data: { id: 'delayed-session', title: '导航恢复', tenant_id: 1 } } }))
  await page.route('**/api/v1/*-chat/delayed-session', route => route.fulfill({ contentType: 'text/event-stream', body: 'data: '+JSON.stringify({ response_type: 'answer', content: '恢复后的回答', id: 'delayed-answer', done: true })+'\n\ndata: '+JSON.stringify({ response_type: 'complete', id: 'delayed-answer', done: true })+'\n\n' }))
  await page.route('**/api/v1/messages/delayed-session/load?*', route => route.fulfill({ json: { success: true, data: [
    { id: 'question', role: 'user', content: '已发送的问题应该显示在对话区。', is_completed: true, created_at: '2026-01-01T00:00:00Z' },
    { id: 'answer', role: 'assistant', content: '恢复后的回答', is_completed: true, created_at: '2026-01-01T00:00:01Z' },
  ] } }))
  await page.addInitScript(() => {
    ;(window as any).__decoderChecks = 0
    ;(window as any).VideoDecoder = class {
      static isConfigSupported() {
        ;(window as any).__decoderChecks++
        return new Promise(() => {})
      }
    }
  })
  await page.goto('/platform/creatChat')
  const input = page.locator('[data-guide="chat-input"] textarea')
  await input.fill('已发送的问题应该显示在对话区。')
  await page.locator('[data-guide="chat-send"]').click()
  await expect(page).toHaveURL(/chat\/delayed-session/)
  await expect(page.locator('.msg_list')).toContainText('已发送的问题应该显示在对话区。')
  await expect(page.locator('.bot_msg')).toContainText('恢复后的回答')
  expect(creations).toBe(1)
  expect(await page.evaluate(() => (window as any).__decoderChecks)).toBe(0)
  await page.reload()
  await expect(page).toHaveURL(/chat\/delayed-session/)
  await expect(page.locator('[data-guide="chat-input"] textarea')).toBeVisible()
  expect(await page.evaluate(() => (window as any).__decoderChecks)).toBe(0)
})
