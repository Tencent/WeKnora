import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const inputField = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const chatPage = readFileSync(new URL('../views/chat/index.vue', import.meta.url), 'utf8')

test('composer browser button is disabled and opens settings when the extension is offline', () => {
  const button = inputField.slice(
    inputField.indexOf('class="control-btn browser-source-btn"'),
    inputField.indexOf('<BrowserIcon class="control-icon" />'),
  )
  assert.match(button, /disabled: browserConnection\.knownOffline/)
  assert.match(button, /active: settingsStore\.isLocalBrowserEnabled && browserConnection\.online/)
  assert.match(button, /:aria-disabled="browserConnection\.knownOffline"/)
  assert.match(inputField, /if \(browserConnection\.knownOffline\) \{\s*openBrowserConnectionSettings\(\)/)
  assert.match(inputField, /uiStore\.openSettings\('browserconnection'\)/)
  assert.match(inputField, /browserConnection\.watchStatus\(\)/)
  assert.match(inputField, /\$t\('localBrowser\.reconnectHint'\)|\$t\(browserSourceUnavailableHint\)/)
})

test('mention button matches icon controls and the stream artifact count badge', () => {
  const css = inputField.slice(inputField.indexOf('.kb-btn {'), inputField.indexOf('.kb-btn-text'))
  assert.match(css, /width: 28px/)
  assert.match(css, /&:hover:not\(\.disabled\):not\(\.active\)/)
  assert.doesNotMatch(css, /box-shadow: inset/)
  const count = inputField.slice(inputField.indexOf('.kb-count {'), inputField.indexOf('.kb-btn-text'))
  assert.match(count, /top: -2px/)
  assert.match(count, /right: -2px/)
  assert.match(count, /min-width: 14px/)
  assert.match(count, /height: 14px/)
  assert.match(count, /font-size: 10px/)
  assert.match(count, /border-radius: 7px/)
  assert.match(count, /font-variant-numeric: tabular-nums/)
  assert.doesNotMatch(count, /border: 2px solid/)
})

test('a selected browser source keeps brand color on hover', () => {
  const css = inputField.slice(inputField.indexOf('.browser-source-btn {'))
  assert.match(css, /&:hover:not\(\.disabled\):not\(\.active\)/)
  assert.match(css, /&\.active \{[\s\S]*&:hover \{[\s\S]*color: var\(--td-brand-color\)/)
  assert.doesNotMatch(css.slice(0, css.indexOf('&.active')), /&:hover:not\(\.disabled\) \{/)
})

test('a chat turn does not request the local browser while the extension is known offline', () => {
  assert.match(
    chatPage,
    /local_browser_enabled:\s*!props\.embeddedMode && agentEnabled && useSettingsStoreInstance\.isLocalBrowserEnabled && !useBrowserConnectionStore\(\)\.knownOffline/,
  )
})

test('context usage ring sits immediately left of the model selector', () => {
  const ring = inputField.indexOf('<ContextUsageRing')
  const composerEnd = inputField.indexOf('class="composer-end"')
  const model = inputField.indexOf('<!-- 模型显示 -->')
  assert.notEqual(ring, -1)
  assert.notEqual(composerEnd, -1)
  assert.ok(composerEnd < ring && ring < model)
  assert.match(inputField, /:usage="contextUsage"/)
  assert.match(chatPage, /:context-usage="latestContextUsage"/)
})

test('context usage ring matches the 28px composer icon buttons', () => {
  const ring = readFileSync(new URL('./ContextUsageRing.vue', import.meta.url), 'utf8')
  const css = ring.slice(ring.indexOf('.context-usage-ring {'), ring.indexOf('.context-usage-ring__track'))
  assert.match(css, /width: 28px/)
  assert.match(css, /height: 28px/)
  assert.match(css, /width: 18px/)
  assert.match(css, /height: 18px/)
  assert.match(ring, /viewBox="0 0 18 18"/)
})

test('chat thread scrollbar is a dark thumb that clears the floating composer', () => {
  assert.match(chatPage, /scrollbar-width: thin/)
  assert.match(chatPage, /scrollbar-color: rgba\(0, 0, 0, 0\.4\) transparent/)
  assert.match(chatPage, /&::-webkit-scrollbar-track \{\s*background: transparent/)
  assert.doesNotMatch(chatPage, /scrollbar-width: auto/)
  assert.doesNotMatch(chatPage, /background-color: initial !important/)

  const input = chatPage.slice(
    chatPage.indexOf('.input-container {'),
    chatPage.indexOf('.msg_list {'),
  )
  const overlay = input.slice(0, input.indexOf('&.is-embedded'))
  assert.match(overlay, /position: absolute/)
  assert.match(overlay, /pointer-events: none/)
  assert.match(overlay, /^\s*bottom: 0;/m)
  assert.match(overlay, /padding-bottom: 16px/)
  assert.match(overlay, /right: var\(--chat-right-inset/)
  // left+right stretch only if width is auto. width:100% makes CSS ignore
  // `right`, so widening the sandbox panel would leave the composer stuck.
  assert.match(overlay, /width: auto/)
  assert.doesNotMatch(overlay, /width: 100%/)
  // Opaque dock from the composer top to the viewport bottom: messages
  // disappear at the dialog's top edge, not after sliding past its bottom.
  // The last 8px stay transparent so the overlay scrollbar can be dragged
  // through to the viewport bottom.
  assert.match(overlay, /--chat-scrollbar-gutter/)
  assert.match(overlay, /linear-gradient/)
  assert.match(overlay, /transparent/)
  assert.doesNotMatch(overlay, /&::after \{/)
  assert.match(chatPage, /--chat-scrollbar-gutter: 8px/)

  assert.match(chatPage, /--chat-composer-space/)
  assert.match(chatPage, /padding-bottom: var\(--chat-composer-space/)
  assert.match(chatPage, /:class="\{ 'is-docked': !embeddedMode \}"/)
})

test('docked composer stays in flow so it cannot cover the thread scrollbar', () => {
  const start = inputField.indexOf('&.is-docked')
  assert.notEqual(start, -1)
  const docked = inputField.slice(start, inputField.indexOf('.steer-queue {', start))
  assert.match(docked, /position: relative/)
  assert.match(docked, /bottom: auto/)
  assert.match(docked, /pointer-events: auto/)
})
