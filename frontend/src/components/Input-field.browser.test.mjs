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
