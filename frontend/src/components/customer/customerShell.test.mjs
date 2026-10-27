import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

/**
 * 客户外壳与对话链路（AC-5 / AC-17 / AC-20 / AC-21 / AC-22 / AC-29 / C6）。
 * 组件逻辑依赖运行时 i18n / Pinia / 真实发送链路，这里做源码级接线断言；
 * 真机效果由 05-tests.md 的浏览器证据覆盖。
 */

const welcome = readFileSync(new URL('./CustomerWelcome.vue', import.meta.url), 'utf8')
const topBar = readFileSync(new URL('./CustomerTopBar.vue', import.meta.url), 'utf8')
const lineSelector = readFileSync(new URL('./LineSelector.vue', import.meta.url), 'utf8')
const creatChat = readFileSync(new URL('../../views/creatChat/creatChat.vue', import.meta.url), 'utf8')
const inputField = readFileSync(new URL('../Input-field.vue', import.meta.url), 'utf8')
const chatView = readFileSync(new URL('../../views/chat/index.vue', import.meta.url), 'utf8')
const menuVue = readFileSync(new URL('../menu.vue', import.meta.url), 'utf8')
const loginVue = readFileSync(new URL('../../views/auth/Login.vue', import.meta.url), 'utf8')

test('customer welcome renders lines, samples, disclaimer and the fallback notice', () => {
  assert.match(welcome, /brandConfig\.productName/)
  assert.match(welcome, /v-for="line in lines"/)
  assert.match(welcome, /v-for="question in samples"/)
  assert.match(welcome, /brandConfig\.chatDisclaimer/)
  assert.match(welcome, /v-if="productLinesFallback"[\s\S]*?role="status"/)
  assert.match(welcome, /samples-swap/)
  assert.doesNotMatch(welcome, /WeKnora/)
  // 示例问题只抛事件，不自己发请求：与手动输入共用发送链路（AC-20）。
  assert.match(welcome, /emit\('send', question\)/)
  assert.doesNotMatch(welcome, /api\/|fetch\(|axios|knowledge-chat/)
})

test('sample questions and manual input both go through Input-field.triggerSend', () => {
  assert.match(creatChat, /CustomerWelcome v-if="isCustomer" @send="handleSampleQuestion"/)
  assert.match(creatChat, /const handleSampleQuestion = \(question: string\) => \{[\s\S]*?inputFieldRef\.value\?\.triggerSend\(question\)/)
  assert.match(creatChat, /<InputField ref="inputFieldRef" :customer-mode="isCustomer"/)
  // 推荐问题块只在非客户分支渲染，客户首页不显示空消息列表/旧推荐块。
  assert.match(creatChat, /<CustomerWelcome v-if="isCustomer"/)
  assert.match(creatChat, /<div v-else ref="sqContainerRef" class="suggested-questions-container">/)
})

test('customer composer keeps only the line selector and send button', () => {
  assert.match(inputField, /<LineSelector\s+v-if="customerMode"/)
  assert.match(inputField, /<template v-else>\s*<!-- Agent 模式切换按钮 -->/)
  assert.match(inputField, /<AttachmentUpload v-if="!customerMode"/)
  assert.match(inputField, /<MorphIcon v-if="customerMode" class="send-btn__morph"/)
  // 客户模式不创建/读取管理员侧的智能体与知识库控件。
  assert.doesNotMatch(inputField, /customerMode[\s\S]{0,200}AgentSelector\s+v-if/)
})

test('customer line selector shows only configured lines, with no admin entry points', () => {
  assert.match(lineSelector, /v-for="\(line, index\) in lines"/)
  assert.match(lineSelector, /brandConfig\.productLines/)
  for (const forbidden of ['管理智能体', '内置智能体', '共享给我', '内置', '自定义']) {
    assert.equal(lineSelector.includes(forbidden), false, `line selector must not contain ${forbidden}`)
  }
  assert.match(lineSelector, /role="listbox"/)
  assert.match(lineSelector, /aria-expanded/)
  assert.match(lineSelector, /ArrowDown/)
  assert.match(lineSelector, /Escape/)
})

test('customer shell swaps in CustomerTopBar on both new-chat and history chat pages', () => {
  assert.match(creatChat, /<CustomerTopBar v-if="isCustomer" \/>/)
  assert.match(chatView, /<CustomerTopBar v-if="!embeddedMode && isCustomer"/)
  // 客户页不再渲染 ChatHeader，避免双顶栏。
  assert.match(chatView, /<div v-else-if="!embeddedMode" class="chat-topbar">\s*<ChatHeader :session="currentSession" \/>/)
  assert.match(topBar, /brandConfig\.productName/)
  for (const forbidden of ['WeKnora', 'weknora']) {
    assert.equal(topBar.includes(forbidden), false)
  }
})

test('logo sizing: sidebar fixed 108px for both roles, login uses runtime logoWidth, no dark invert filter', () => {
  assert.match(menuVue, /sidebarLogoSrc = computed\(\(\) => \(isCustomer\.value \? brandConfig\.logoUrl : allstarLogoUrl\)\)/)
  // U2：侧栏 logo 客户与管理员共用同一条 108px CSS，menu.vue 不再读 logoWidth
  // （代码里不允许出现 brandConfig.logoWidth，注释提法不算）。
  assert.doesNotMatch(menuVue, /brandConfig\.logoWidth/)
  assert.match(menuVue, /\.logo \{\s*width: 108px;/)
  // logoWidth 语义收窄为「登录页 logo 宽度」：Login.vue 注入 --brand-logo-width。
  assert.match(loginVue, /'--brand-logo-width': `\$\{brandConfig\.logoWidth\}px`/)
  // 深色模式只允许对菜单图标做 invert；logo 不再走反色滤镜（AC-5 深色可辨）。
  assert.doesNotMatch(menuVue, /html\[theme-mode="dark"\][\s\S]{0,200}\.logo[\s\S]{0,200}(?:invert|hue-rotate)/)
  assert.doesNotMatch(menuVue, /\.logo\s*\{[^}]*filter:/)
})
