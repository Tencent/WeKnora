import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
test.use({ hasTouch: true })

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

for (const width of [360, 390, 430]) {
  test(`composer grows for typing and contracts when empty at ${width}px`, async ({ page }) => {
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

test('mobile recommendation fills an editable draft and send opens a visible conversation', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.route('**/api/v1/agents**', async route => {
    if (route.request().url().includes('suggested-questions')) return route.fulfill({ json: { success: true, data: { questions: [
      { question: '庄子的逍遥是什么意思？', source: 'knowledge', knowledge_base_id: 'mobile-kb' },
      { question: '比较不同注家的解释。', source: 'knowledge' },
      { question: '阅读齐物论。', source: 'knowledge' },
    ] } } })
    await route.fulfill({ json: { success: true, data: [{ id: 'builtin-smart-reasoning', name: '智能推理', is_builtin: true, config: { agent_mode: 'smart-reasoning', model_id: 'chat-model', kb_selection_mode: 'none', allowed_tools: ['thinking'] } }, { id: 'builtin-quick-answer', name: '快速问答', is_builtin: true, config: { agent_mode: 'quick-answer', model_id: 'chat-model', kb_selection_mode: 'none' } }] } })
  })
  await page.route('**/api/v1/sessions', route => route.fulfill({ json: { success: true, data: { id: 'mobile-new' } } }))
  await page.route('**/api/v1/sessions/mobile-new', route => route.fulfill({ json: { success: true, data: { id: 'mobile-new', title: '手机测试', tenant_id: 1 } } }))
  await page.route('**/api/v1/*-chat/mobile-new', route => route.fulfill({ contentType: 'text/event-stream', body: 'data: '+JSON.stringify({ response_type: 'answer', content: '逍遥是自由自在。', id: 'answer-test', done: true })+'\n\ndata: '+JSON.stringify({ response_type: 'complete', id: 'answer-test', done: true })+'\n\n' }))
  const renderErrors: string[] = []
  page.on('console', message => { if (message.type() === 'error' && message.text().includes('Unhandled Vue error')) renderErrors.push(message.text()) })
  await page.goto('/platform/creatChat')
  const first = page.getByRole('button', { name: '庄子的逍遥是什么意思？', exact: true })
  const third = page.getByRole('button', { name: '阅读齐物论。', exact: true })
  await expect(third).toBeVisible()
  expect((await third.boundingBox())!.y).toBeGreaterThan((await first.boundingBox())!.y)
  await page.getByRole('button', { name: '庄子的逍遥是什么意思？', exact: true }).click()
  const input = page.locator('[data-guide="chat-input"] textarea')
  await expect(input).toHaveValue('庄子的逍遥是什么意思？')
  await expect(page).toHaveURL(/creatChat/)
  await page.locator('[data-guide="chat-send"]').tap()
  await expect(page).toHaveURL(/chat\/mobile-new/)
  await expect(page.locator('.msg_list')).toContainText('庄子的逍遥是什么意思？')
  await expect(page.locator('.msg_list')).toContainText('逍遥是自由自在。')
  await expect(page.locator('.msg_list')).toBeInViewport()
  await input.fill('进一步解释自由。')
  await page.locator('[data-guide="chat-send"]').tap()
  await expect(page.locator('.msg_list')).toContainText('进一步解释自由。')
  expect(renderErrors).toEqual([])
  await page.goto('/platform/creatChat')
  await page.route('**/api/v1/sessions', route => route.fulfill({ status: 500, json: { message: 'Session creation failed' } }))
  const restored = page.locator('[data-guide="chat-input"] textarea')
  await restored.fill('失败时保留这条草稿。')
  await page.locator('[data-guide="chat-send"]').tap()
  await expect(page.locator('.create-chat-composer [role="alert"]')).toBeVisible()
  await expect(restored).toHaveValue('失败时保留这条草稿。')
})

test('a delayed chat navigation shows the sent question in the conversation and retries the existing session', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
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
  expect((await bubble.boundingBox())!.y).toBeLessThan(200)
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

test('chat sends and survives reload when the remote desktop decoder probe never resolves', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.route('**/api/v1/agents**', route => route.fulfill({ json: { success: true, data: [{ id: 'builtin-quick-answer', name: '快速问答', is_builtin: true, config: { agent_mode: 'quick-answer', model_id: 'chat-model', kb_selection_mode: 'none' } }] } }))
  let creations = 0
  await page.route('**/api/v1/sessions', route => { creations++; return route.fulfill({ json: { success: true, data: { id: 'delayed-session' } } }) })
  await page.route('**/api/v1/sessions/delayed-session', route => route.fulfill({ json: { success: true, data: { id: 'delayed-session', title: '导航恢复', tenant_id: 1 } } }))
  await page.route('**/api/v1/*-chat/delayed-session', route => route.fulfill({ contentType: 'text/event-stream', body: 'data: '+JSON.stringify({ response_type: 'answer', content: '恢复后的回答', id: 'delayed-answer', done: true })+'\n\ndata: '+JSON.stringify({ response_type: 'complete', id: 'delayed-answer', done: true })+'\n\n' }))
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
  await page.locator('[data-guide="chat-send"]').tap()
  await expect(page).toHaveURL(/chat\/delayed-session/)
  await expect(page.locator('.msg_list')).toContainText('已发送的问题应该显示在对话区。')
  await expect(page.locator('.bot_msg')).toContainText('恢复后的回答')
  await expect(page.locator('.answer-truncated-notice')).toHaveCount(0)
  expect(creations).toBe(1)
  expect(await page.evaluate(() => (window as any).__decoderChecks)).toBe(0)
  await page.reload()
  await expect(page).toHaveURL(/chat\/delayed-session/)
  await expect(page.locator('[data-guide="chat-input"] textarea')).toBeVisible()
  expect(await page.evaluate(() => (window as any).__decoderChecks)).toBe(0)
})


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
  await fitsViewport(page)
  await page.reload()
  await expect(page.locator('.answer-truncated-notice')).toBeVisible()
  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(page.locator('.answer-truncated-notice')).toBeVisible()
})
