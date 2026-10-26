import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./SandboxConfigEditorDrawer.vue', import.meta.url), 'utf8')

test('runtime step offers the allocation switch without the shared-root warning', () => {
  const runtime = source.slice(source.lastIndexOf("currentStepKey === 'runtime'"))
  for (const key of ['allocationModeRiskDelete', 'allocationModeRiskGit', 'allocationModeRiskResources', 'allocationModeRiskQuota', 'allocationModeSkillRolloutNote', 'allocationModeRiskTitle']) {
    assert.equal(runtime.includes(key), false, `${key} must not be shown`)
  }
  assert.equal(runtime.includes("skillRollout === 'new_session'"), false)
  assert.match(runtime, /:min="1" :max="90"/)
})

test('payload always sends allocation_mode so switching back to session is explicit', () => {
  assert.match(source, /allocation_mode:\s*allocationMode\.value/)
})

test('choosing shared allocation scrolls the retention field into view', () => {
  assert.match(source, /ref="userAllocationPanelRef"/)
  assert.match(source, /scrollIntoView\(\{ behavior: 'smooth', block: 'end', inline: 'nearest' \}\)/)
  assert.match(source, /if \(mode !== 'user' \|\| previous === 'user'\) return/)
})
