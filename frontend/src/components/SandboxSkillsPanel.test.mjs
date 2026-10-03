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

test('the focused skill offers an upgrade when the catalog has moved on', () => {
  assert.match(source, /catalogItem\?: SkillCatalogItem \| null/)
  assert.match(source, /installUpgradable\(catalog, skill\)/)
  assert.match(manageBlock, /v-if="managedUpgradeHint"/)
  assert.match(manageBlock, /upgradeSkill\(managedSkill\)/)
  assert.match(manageBlock, /settings\.skills\.upgradeRowTitle/)
  // The upgrade is the catalog install; the retry replays this sandbox's own archive.
  const upgrade = source.slice(source.indexOf('async function upgradeSkill('), source.indexOf('async function stopSkill('))
  assert.match(upgrade, /installSkillCatalog\(catalog\.id, \[configId\]\)/)
  assert.doesNotMatch(upgrade, /reinstallConfigSkill/)
})

test('a skill mid-upgrade or after a failed upgrade says the previous version still runs', () => {
  assert.match(source, /servedPreviousText\(t, managedSkill\.value\)/)
  assert.match(manageBlock, /v-if="managedServedNote"/)
  assert.match(source, /\.skill-manage__served \{/)
})

test('an install the catalog has moved past is offered the upgrade, not the retries', () => {
  assert.match(source, /v-if="managedSkill\.status === 'failed' && !managedUpgradable"/)
  assert.match(manageBlock, /:can-retry="\(managedSkill\.status === 'ready' \|\| managedSkill\.status === 'failed'\) && !managedUpgradable"/)
  assert.match(source, /settings\.skills\.upgradeRowHintFailedVersions/)
})
