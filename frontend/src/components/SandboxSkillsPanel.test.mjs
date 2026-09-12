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
  assert.match(manageBlock, /Teleport/)
  assert.match(manageBlock, /settings\.skills\.manageUninstall/)
  assert.match(manageBlock, /manageUninstallConfirm/)
  assert.match(manageBlock, /showHeaderUninstall/)
  assert.match(source, /SETTING_DRAWER_HEADER_ACTIONS_ID/)
  assert.match(source, /skill\.status === 'installing'/)
  assert.match(manageBlock, /skill-manage__section--remove/)
  assert.match(manageBlock, /theme="circle"/)
  assert.doesNotMatch(manageBlock, /stroke-width="6"/)
  assert.match(manageBlock, /t-popconfirm/)
  assert.doesNotMatch(manageBlock, /askRemove/)
  assert.doesNotMatch(manageBlock, /skill-manage__section--danger/)
  assert.doesNotMatch(source, /useConfirmDelete/)
  assert.match(manageBlock, /skillRemoveDone/)
  assert.match(manageBlock, /progressStageText/)
  assert.match(source, /overlayUninstallStatus/)
  assert.match(source, /uninstallingId\.value === skill\.id/)
  assert.match(source, /MAX_SKILL_BUNDLE_SIZE_MB/)
  assert.match(source, /skillBundleTooManyZipEntries/)
  assert.doesNotMatch(manageBlock, /t-icon name="delete"/)
  assert.doesNotMatch(manageBlock, /skill-manage__footer/)
  assert.doesNotMatch(manageBlock, /settings\.sandbox\.skillEnv\.save/)
})
