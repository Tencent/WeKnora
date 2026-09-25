import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const panel = readFileSync(new URL('./SandboxSidePanel.vue', import.meta.url), 'utf8')

test('artifacts is the first sandbox panel tab', () => {
  const artifacts = panel.indexOf("id: 'artifacts'")
  const terminal = panel.indexOf("id: 'terminal'")
  const desktop = panel.indexOf("id: 'desktop'")
  assert.notEqual(artifacts, -1)
  assert.ok(artifacts < terminal)
  assert.ok(terminal < desktop)
  assert.match(panel, /<ChatArtifactsPanel/)
  assert.doesNotMatch(panel, /chat-sandbox-panel__title/)
  assert.doesNotMatch(panel, /<h3/)
})

test('closing the panel drops the terminal mount flag so Files reopen does not reconnect', () => {
  assert.match(panel, /if\s*\(!visible\)/)
  assert.match(panel, /terminalMounted\.value = false/)
  assert.match(panel, /tab === 'terminal'/)
})

test('desktop tab is omitted unless the selected agent sandbox has desktop_enabled', () => {
  assert.match(panel, /ensureSandboxConfigs/)
  assert.match(panel, /desktopTabVisible/)
  assert.match(panel, /desktop_enabled/)
  assert.match(panel, /if \(desktopTabVisible\.value\)/)
  assert.match(panel, /desktopMounted && desktopTabVisible/)
})

test('panel slide-in is clipped to the viewport so it cannot create a document scrollbar', () => {
  assert.match(panel, /chat-sandbox-panel-clip/)
  assert.match(panel, /sandbox-panel-enter-from \.chat-sandbox-panel/)
  assert.match(panel, /translateX\(100%\)/)
  assert.match(panel, /:duration/)
})
