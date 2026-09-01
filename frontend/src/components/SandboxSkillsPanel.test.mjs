import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./SandboxSkillsPanel.vue', import.meta.url), 'utf8')
const manageBlock = source.slice(
  source.indexOf('mode === \'list\' && focusSkillId'),
  source.indexOf('v-else-if="mode === \'list\'"'),
)

test('focused skill management expands env vars and transcript without a mid-page save button', () => {
  assert.match(manageBlock, /skillHasDeclaredEnvs\(managedSkill\)/)
  assert.match(manageBlock, /onEnvFieldBlur/)
  assert.match(source, /isBusy\(skill\) \|\| !hasEnvEdits\(skill\)/)
  assert.match(manageBlock, /SkillInstallTimeline/)
  assert.match(manageBlock, /settings\.skills\.manageUninstall/)
  assert.match(manageBlock, /skill-manage__section--danger/)
  assert.match(manageBlock, /managedSkill\.status !== 'installing'/)
  assert.match(manageBlock, /skill-manage__section--remove/)
  assert.match(manageBlock, /askRemove/)
  assert.match(manageBlock, /skillRemoveDone/)
  assert.match(manageBlock, /progressStageText/)
  assert.doesNotMatch(manageBlock, /t-icon name="delete"/)
  assert.doesNotMatch(manageBlock, /skill-manage__footer/)
  assert.doesNotMatch(manageBlock, /settings\.sandbox\.skillEnv\.save/)
})
