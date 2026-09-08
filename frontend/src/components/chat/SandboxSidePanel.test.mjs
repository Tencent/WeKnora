import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const panel = readFileSync(new URL('./SandboxSidePanel.vue', import.meta.url), 'utf8')

test('artifacts is the first sandbox panel tab', () => {
  const artifacts = panel.indexOf("id: 'artifacts' as SandboxPanelTab")
  const terminal = panel.indexOf("id: 'terminal' as SandboxPanelTab")
  const desktop = panel.indexOf("id: 'desktop' as SandboxPanelTab")
  assert.notEqual(artifacts, -1)
  assert.ok(artifacts < terminal)
  assert.ok(terminal < desktop)
  assert.match(panel, /<ChatArtifactsPanel/)
  assert.doesNotMatch(panel, /chat-sandbox-panel__title/)
  assert.doesNotMatch(panel, /<h3/)
})
