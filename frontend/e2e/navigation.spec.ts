import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
test.use({ hasTouch: true })

test('a delayed chat navigation shows the sent question in the conversation and retries the existing session', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 844 })
  await mockApp(page)
  await page.route('**/api/v1/agents**', route => route.fulfill({ json: { success: true, data: [{ id: 'builtin-quick-answer', name: '快速问答', is_builtin: true, config: { agent_mode: 'quick-answer', model_id: 'chat-model', kb_selection_mode: 'none' } }] } }))
  let creations = 0
  await page.route('**/api/v1/sessions', route => { creations++; return route.fulfill({ json: { success: true, data: { id: 'delayed-session' } } }) })
  await page.route('**/api/v1/sessions/delayed-session', route => route.fulfill({ json: { success: true, data: { id: 'delayed-session', title: '导航恢复', tenant_id: 1 } } }))
  await page.route('**/api/v1/*-chat/delayed-session', route => route.fulfill({ contentType: 'text/event-stream', body: 'data: '+JSON.stringify({ response_type: 'answer', content: '恢复后的回答', id: 'delayed-answer', done: true })+'\n\ndata: '+JSON.stringify({ response_type: 'complete', id: 'delayed-answer', done: true })+'\n\n' }))
  await page.goto('/platform/creatChat')
  await expect(page.locator('[data-guide="chat-input"] textarea')).toBeVisible()
  await page.clock.install()
  await page.evaluate(() => {
    const router = (document.querySelector('#app') as any).__vue_app__.config.globalProperties.$router
    const push = router.push.bind(router)
    let blocked = false
    router.push = (target: string) => {
      if (target.includes('/chat/') && !blocked) { blocked = true; (window as any).__navigationBlocked = true; return new Promise(() => {}) }
      return push(target)
    }
  })
  const input = page.locator('[data-guide="chat-input"] textarea')
  await input.fill('已发送的问题应该显示在对话区。')
  await page.locator('[data-guide="chat-send"]').tap()
  const bubble = page.locator('.new-chat-question')
  await expect(bubble).toHaveText('已发送的问题应该显示在对话区。')
  await expect(bubble).toBeInViewport()
  await expect(page.locator('[data-guide="chat-send"]')).toBeDisabled()
  await page.waitForFunction(() => (window as any).__navigationBlocked === true)
  await page.clock.fastForward(30_001)
  await expect(page.locator('.create-chat-composer [role="alert"]')).toBeVisible()
  await expect(input).toHaveValue('已发送的问题应该显示在对话区。')
  await page.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page).toHaveURL(/chat\/delayed-session/)
  await expect(page.locator('.msg_list')).toContainText('已发送的问题应该显示在对话区。')
  await expect(page.locator('.bot_msg')).toContainText('恢复后的回答')
  expect(creations).toBe(1)
})


test('creation failure restores the question and unlocks sending', async ({ page }) => {
  await mockApp(page)
  await page.route('**/api/v1/agents**', route => route.fulfill({ json: { success: true, data: [{ id: 'builtin-quick-answer', name: '快速问答', is_builtin: true, config: { agent_mode: 'quick-answer', model_id: 'chat-model', kb_selection_mode: 'none' } }] } }))
  await page.route('**/api/v1/sessions', route => route.fulfill({ status: 500, json: { message: 'creation failure' } }))
  await page.goto('/platform/creatChat')
  const input = page.locator('[data-guide="chat-input"] textarea')
  await input.fill('失败后保留这条问题')
  await page.locator('[data-guide="chat-send"]').click()
  await expect(page.locator('.create-chat-composer [role="alert"]')).toBeVisible()
  await expect(input).toHaveValue('失败后保留这条问题')
  await expect(page.locator('[data-guide="chat-send"]')).toBeEnabled()
})
