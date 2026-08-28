import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./SkillSettings.vue', import.meta.url), 'utf8')

test('skill settings lists the catalog instead of switching sandboxes', () => {
  assert.match(source, /listSkillCatalog/)
  assert.match(source, /settings\.skills\.addSkill/)
  assert.match(source, /settings\.skills\.emptyDesc/)
  assert.doesNotMatch(source, /sandbox-switcher/)
  assert.doesNotMatch(source, /noConfigsDesc/)
})

test('catalog cards can install onto sandboxes and manage one install', () => {
  assert.match(source, /installSkillCatalog/)
  assert.match(source, /openManage/)
  assert.match(source, /hide-add/)
  assert.match(source, /focus-skill-id/)
  assert.match(source, /deleteSkillCatalog/)
  assert.match(source, /askDelete/)
  assert.match(source, /skill-card__chip/)
  assert.match(source, /skill-card__chip--add/)
  assert.match(source, /skill-card__installs/)
  assert.match(source, /settings\.skills\.installedOn/)
  assert.match(source, /skill-card__icon-btn/)
  assert.match(source, /openCatalogFiles/)
  assert.match(source, /SkillFilesDrawer/)
  assert.match(source, /skill-card__chip-status/)
  assert.match(source, /chevron-right/)
  assert.match(source, /skill-card__heading/)
  assert.match(source, /align-items: center/)
  assert.doesNotMatch(source, /sandbox-pick__desc/)
  assert.doesNotMatch(source, /skill-card__bar/)
  assert.doesNotMatch(source, /skill-card__sandbox/)
  assert.doesNotMatch(source, /skill-card__more/)
  assert.doesNotMatch(source, /catalogMenu/)
  assert.doesNotMatch(source, /t-dropdown/)
  assert.doesNotMatch(source, /install-chip/)
})

test('adding a skill uses a two-step drawer like sandbox setup', () => {
  assert.match(source, /skill-add-steps/)
  assert.match(source, /settings\.skills\.addStepRegister/)
  assert.match(source, /settings\.skills\.addStepInstall/)
  assert.match(source, /header-extra/)
  assert.match(source, /addStep === 0/)
  assert.match(source, /handleAddPrimary/)
  assert.doesNotMatch(source, /addFromSource/)
})

test('install step shows parsed skill and sandbox backend details', () => {
  assert.match(source, /parsed-skill/)
  assert.match(source, /skill-card__badge/)
  assert.match(source, /sandboxMetaLine/)
  assert.match(source, /sandbox-pick-list/)
  assert.doesNotMatch(source, /t-alert/)
  assert.doesNotMatch(source, /registered-alert/)
})
