import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

/**
 * 客户输入区与助手隔离（H2 / M2 / M7 / M8 / M9）。
 *
 * 这些都是组件内部的条件分支，node 环境不能挂载整个 Vue 页面，
 * 因此沿用仓库已有的「读源码 + 正则断言真实分支」做法；
 * 行为层由真实容器走查补证（05-tests.md R1 复验章节）。
 */

const inputField = readFileSync(new URL('../Input-field.vue', import.meta.url), 'utf8')
const menuVue = readFileSync(new URL('../menu.vue', import.meta.url), 'utf8')
const userMenu = readFileSync(new URL('../UserMenu.vue', import.meta.url), 'utf8')
const chatView = readFileSync(new URL('../../views/chat/index.vue', import.meta.url), 'utf8')
const platformView = readFileSync(new URL('../../views/platform/index.vue', import.meta.url), 'utf8')
const settingsView = readFileSync(new URL('../../views/settings/Settings.vue', import.meta.url), 'utf8')

test('M7 customer send drops hidden KB / file / MCP selections', () => {
  // 发送与 steer 两条路径都按客户模式清空提及与附件。
  assert.match(inputField, /const mentionSource = props\.customerMode \? \[\] : allSelectedItems\.value/)
  assert.match(inputField, /const imageFiles = props\.customerMode \? \[\] : uploadedImages\.value\.map/)
  assert.match(inputField, /const attachmentFiles = props\.customerMode \? \[\] : uploadedAttachments\.value/)
  assert.match(
    inputField,
    /const steerMentions: MentionRequestItem\[\] = \(props\.embeddedMode \|\| props\.customerMode \? \[\]/,
  )
})

test('M8 customer blocks paste, drag-drop, @ mentions and image upload / preview', () => {
  // 拖拽
  assert.match(inputField, /const handleDroppedFiles = \(files: File\[\]\) => \{[\s\S]*?if \(props\.customerMode\) return;/)
  // 粘贴图片
  assert.match(inputField, /const onPaste = \(e: ClipboardEvent\) => \{[\s\S]*?if \(props\.customerMode\) return;/)
  // @ 提及
  assert.match(inputField, /const isMentionDisabled = computed\(\(\) => \{[\s\S]*?if \(props\.customerMode\) return true;/)
  // 图片入队与预览条
  assert.match(inputField, /const addImageFiles = \(files: File\[\]\) => \{[\s\S]*?if \(props\.customerMode\) return;/)
  assert.match(inputField, /v-if="!customerMode && uploadedImages\.length > 0"/)
  assert.match(inputField, /<AttachmentUpload v-if="!customerMode"/)
})

test('M9 session switching locks send and stop so nothing lands in the old session', () => {
  assert.match(chatView, /const sessionSwitching = ref\(false\)/)
  assert.match(chatView, /const composerLocked = computed\(\(\) =>[\s\S]*?sessionSwitching\.value/)
  assert.match(chatView, /const handleStopGeneration = \(\) => \{[\s\S]*?if \(sessionSwitching\.value\) return;/)
  assert.match(chatView, /const sendMsg = async[\s\S]*?if \(sessionSwitching\.value\) return;/)
  // 只有最后一次切换负责解锁，被顶掉的旧切换不会提前放行。
  assert.match(chatView, /if \(switchSeq === sessionSwitchSeq\) sessionSwitching\.value = false/)
})

test('H2 customer hides every WeKnora assistant entry point', () => {
  // 回答工具栏「就这条回答问助手」
  assert.match(chatView, /:can-ask-assistant="!embeddedMode && !isCustomer"/)
  assert.match(chatView, /function handleAskAssistant\(messageId\) \{[\s\S]*?if \(isCustomer\.value\) return/)
  // 助手面板 / 悬浮入口 / 页面桥接整体不挂载
  assert.match(
    platformView,
    /<template v-if="!isCustomer">[\s\S]*?<AssistantPanel \/>[\s\S]*?<AssistantLauncher \/>[\s\S]*?<AssistantPageBridge \/>/,
  )
  assert.match(platformView, /if \(customer && assistantStore\.visible\) assistantStore\.close\(\)/)
  // 侧栏助手按钮（桌面 + 窄屏）
  assert.equal((menuVue.match(/assistantStore\.entry === 'sidebar' && !isCustomer/g) ?? []).length, 2)
  // 设置里的助手分区
  assert.match(settingsView, /currentSection === 'assistant' && !isCustomerViewerActive/)
})

test('M2 customer avatar menu hides "all settings" but keeps personal settings', () => {
  assert.match(userMenu, /<div v-if="!isCustomer" class="menu-item" @click="handleSettings">/)
  assert.match(userMenu, /\{\{ \$t\('general\.allSettings'\) \}\}/)
  // 个人设置入口对客户保留（没有 v-if 门槛）。
  assert.match(userMenu, /<div class="menu-item" @click="handleQuickNav\('general'\)">/)
  const personalBlock = userMenu.slice(
    userMenu.indexOf("@click=\"handleQuickNav('general')\"") - 200,
    userMenu.indexOf("@click=\"handleQuickNav('general')\"") + 50,
  )
  assert.doesNotMatch(personalBlock, /v-if="!isCustomer"/)
})
