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
  assert.match(button, /active: browserSourceActive && browserConnection\.online/)
  assert.match(inputField, /const browserSourceActive = computed\(\(\) =>\s*props\.embeddedMode \? settingsStore\.isSidebarLocalBrowserEnabled : settingsStore\.isLocalBrowserEnabled/)
  assert.match(button, /:aria-disabled="browserConnection\.knownOffline"/)
  assert.match(inputField, /if \(browserConnection\.knownOffline\) \{\s*openBrowserConnectionSettings\(\)/)
  assert.match(inputField, /router\.push\(toolboxLocation\('browserconnection'\)\)/)
  assert.match(inputField, /browserConnection\.watchStatus\(\)/)
  assert.match(inputField, /\$t\('localBrowser\.reconnectHint'\)|\$t\(browserSourceUnavailableHint\)/)
})

test('a selected browser source keeps brand color on hover', () => {
  const css = inputField.slice(inputField.indexOf('.browser-source-btn {'))
  assert.match(css, /&:hover:not\(\.disabled\):not\(\.active\)/)
  assert.match(css, /&\.active \{[\s\S]*&:hover \{[\s\S]*color: var\(--td-brand-color\)/)
  assert.doesNotMatch(css.slice(0, css.indexOf('&.active')), /&:hover:not\(\.disabled\) \{/)
})

test('a chat turn does not request the local browser while the extension is known offline', () => {
  // The browser source is scoped per host: the KB sidebar (embeddedMode)
  // sends its own sidebarLocalBrowserEnabled flag, the main chat the global
  // one — neither may be silently dropped by an embeddedMode exclusion.
  assert.match(
    chatPage,
    /local_browser_enabled:\s*agentEnabled && \(props\.embeddedMode\s*\? useSettingsStoreInstance\.isSidebarLocalBrowserEnabled\s*: useSettingsStoreInstance\.isLocalBrowserEnabled\) && !useBrowserConnectionStore\(\)\.knownOffline/,
  )
  assert.doesNotMatch(chatPage, /local_browser_enabled:\s*!props\.embeddedMode/)
})
