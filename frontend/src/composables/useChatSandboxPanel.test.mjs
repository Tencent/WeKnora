import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { ref } from 'vue'

const source = readFileSync(new URL('./useChatSandboxPanel.ts', import.meta.url), 'utf8')
const start = source.indexOf('export function provideChatSandboxPanel(')
const end = source.indexOf('\nexport function useChatSandboxPanel(', start)
assert.ok(start >= 0 && end > start)
const factory = ts.transpile(source.slice(start, end).replace('export function', 'function'))

function createPanel() {
  return vm.runInNewContext(`${factory}; provideChatSandboxPanel()`, {
    ref,
    initialPanelWidth: () => 420,
    provide() {},
    CHAT_SANDBOX_PANEL_KEY: Symbol(),
  })
}

test('clicking the same folder opens, closes, and reopens its artifacts', () => {
  const panel = createPanel()
  panel.toggleArtifacts('a')
  assert.equal(panel.visible.value, true)
  assert.equal(panel.artifactFocus.value.messageId, 'a')
  panel.toggleArtifacts('a')
  assert.equal(panel.visible.value, false)
  assert.equal(panel.artifactFocus.value, null)
  panel.toggleArtifacts('a')
  assert.equal(panel.visible.value, true)
  assert.equal(panel.artifactFocus.value.messageId, 'a')
})

test('clicking another message folder switches focus without closing', () => {
  const panel = createPanel()
  panel.toggleArtifacts('a')
  panel.toggleArtifacts('b')
  assert.equal(panel.visible.value, true)
  assert.equal(panel.artifactFocus.value.messageId, 'b')
})

test('folder entry switches from terminal or an unfocused header entry to its files', () => {
  const panel = createPanel()
  panel.open()
  panel.toggleArtifacts('a')
  assert.equal(panel.visible.value, true)
  assert.equal(panel.artifactFocus.value.messageId, 'a')
  panel.open('terminal')
  panel.toggleArtifacts('a')
  assert.equal(panel.visible.value, true)
  assert.equal(panel.activeTab.value, 'artifacts')
})

test('inline preview requests stay open even when repeated; folder click closes them', () => {
  const panel = createPanel()
  for (let i = 0; i < 2; i++) {
    panel.open('artifacts', { messageId: 'a', previewIndex: 0 })
    assert.equal(panel.visible.value, true)
    assert.equal(panel.artifactFocus.value.previewIndex, 0)
  }
  panel.toggleArtifacts('a')
  assert.equal(panel.visible.value, false)
})

test('both answer renderers toggle folders while retaining explicit preview opening', () => {
  for (const file of ['botmsg.vue', 'AgentStreamDisplay.vue']) {
    const component = readFileSync(new URL(`../views/chat/components/${file}`, import.meta.url), 'utf8')
    assert.match(component, /if \(previewIndex == null\)\s*\{\s*sandboxPanel\.toggleArtifacts\(messageIdForArtifacts\.value\)/)
    assert.match(component, /else\s*\{\s*sandboxPanel\.open\('artifacts',/)
  }
})
