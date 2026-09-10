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
  assert.match(source, /installPickRows/)
  assert.match(source, /settings\.skills\.viewInstallProgress/)
  assert.match(source, /sandbox-pick--status/)
  assert.match(source, /useConfigSkillInstallProgress/)
  assert.match(source, /sandboxPickPercent/)
  assert.match(source, /sandbox-pick__progress/)
  assert.match(source, /theme="circle"/)
  assert.match(source, /onInstallDrawerConfirm/)
  assert.doesNotMatch(source, /skill-card__progress-link/)
  assert.doesNotMatch(source, /openProgressAfterInstall/)
  assert.match(source, /hide-add/)
  assert.match(source, /focus-skill-id/)
  assert.match(source, /deleteSkillCatalog/)
  assert.match(source, /askDelete/)
  assert.match(source, /skill-card__chip/)
  assert.match(source, /skill-install-panel/)
  assert.match(source, /skill-card__installs/)
  assert.match(source, /settings\.skills\.installedOnName/)
  assert.match(source, /settings\.skills\.installedCount/)
  assert.match(source, /skill-card__icon-btn/)
  assert.match(source, /FolderIcon/)
  assert.match(source, /DeleteIcon/)
  assert.match(source, /skill-card--idle/)
  assert.match(source, /skill-card--installed/)
  assert.match(source, /skill-card__chip--idle/)
  assert.match(source, /skill-card__chip--installed/)
  assert.doesNotMatch(source, /t-icon name="folder"/)
  assert.doesNotMatch(source, /t-icon name="delete"/)
  assert.match(source, /openCatalogFiles/)
  assert.match(source, /SkillFilesDrawer/)
  assert.match(source, /installPartial/)
  assert.match(source, /installOutdated/)
  assert.match(source, /skill-card__entry--stale/)
  assert.match(source, /check-circle-filled/)
  assert.doesNotMatch(source, /installSummaryIcon/)
  assert.match(source, /skillSourceSectionHint/)
  assert.match(source, /skillUploadSectionHint/)
  assert.match(source, /maxSkillBundleMB/)
  assert.match(source, /MAX_SKILL_BUNDLE_SIZE_MB/)
  assert.match(source, /skillBundleTooManyZipEntries/)
  assert.match(source, /align-items: stretch/)
  assert.match(source, /margin-top: auto/)
  assert.match(source, /installsView/)
  assert.match(source, /needsPanel/)
  assert.match(source, /catalogInstallFailedCount/)
  assert.match(source, /skill-card__heading/)
  assert.match(source, /chevron-right/)
  assert.match(source, /settings\.skills\.installPanelGroup/)
  assert.match(source, /settings\.skills\.installPanelAvailable/)
  assert.match(source, /unusedTargets/)
  assert.match(source, /openInstallTo/)
  assert.match(source, /skill-install-panel__item--available/)
  assert.match(source, /skill-install-panel__split/)
  assert.match(source, /size="sm"/)
  assert.match(source, /align-items: center/)
  assert.doesNotMatch(source, /sandbox-pick__desc/)
  assert.doesNotMatch(source, /skill-card__bar/)
  assert.doesNotMatch(source, /skill-card__sandbox/)
  assert.doesNotMatch(source, /catalogMenu/)
  assert.doesNotMatch(source, /skill-card__more/)
  assert.doesNotMatch(source, /installsMore/)
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

// Execute the production row selectors to cover ready rows that used to be
// filtered out entirely, and status updates across different backend types.
test('sandbox groups include existing installations and update by installation status', async () => {
  const { transpile } = await import('typescript')
  const { runInNewContext } = await import('node:vm')
  const groups = source.slice(source.indexOf('function groupSandboxPicks('), source.indexOf('const installPickGroups'))
  const rows = source.slice(source.indexOf('function sandboxPickRows('), source.indexOf('function sandboxPickPercent('))
  const configs = ['cube', 'e2b', 'docker', 'retry', 'busy', 'preinstalled'].map(id => ({ id, sandbox_type: id }))
  const context = {
    skillConfigs: { value: configs },
    liveInstalls: item => item.installations,
    isInstallBusy: install => ['installing', 'removing'].includes(install.status),
  }
  runInNewContext(transpile(groups + rows), context)
  const item = { installations: [
    { sandbox_config_id: 'cube', status: 'ready' },
    { sandbox_config_id: 'docker', status: 'ready' },
    { sandbox_config_id: 'retry', status: 'failed' },
    { sandbox_config_id: 'busy', status: 'installing' },
  ] }
  let picks = context.sandboxPickRows(item)
  picks.find(row => row.cfg.id === 'preinstalled').ready = true
  const grouped = context.groupSandboxPicks(picks)
  const ids = group => Array.from(group.rows, row => row.cfg.id)
  assert.deepEqual(ids(grouped[0]), ['e2b', 'retry', 'busy'])
  assert.deepEqual(ids(grouped[1]), ['cube', 'docker', 'preinstalled'])
  assert.equal(picks.find(row => row.cfg.id === 'retry').selectable, true)
  assert.equal(picks.find(row => row.cfg.id === 'busy').selectable, false)
  assert.equal(picks.find(row => row.cfg.id === 'cube').selectable, false)
  item.installations[3].status = 'ready'
  picks = context.sandboxPickRows(item)
  assert.ok(ids(context.groupSandboxPicks(picks)[1]).includes('busy'))
  assert.equal(context.groupSandboxPicks([]).length, 0)
  assert.doesNotMatch(source, /sandboxPickRows\(item,.*remaining/)
})

test('prompt advances to sandbox selection without searching, then starts the install job', async () => {
  const { transpile } = await import('typescript')
  const { runInNewContext } = await import('node:vm')
  const handler = source.slice(source.indexOf('async function handleAddPrimary()'), source.indexOf('function onFileInputChange('))
  const requests = []
  const state = {
    addPrimaryLoading: { value: false }, addPrimaryDisabled: { value: false },
    addStep: { value: 0 }, addMethod: { value: 'prompt' }, addTargetIds: { value: [] },
    skillPrompt: { value: 'Read the provided installation docs and install skill ID 3044' },
    registeredCatalog: { value: null }, promptInstallIds: { value: {} },
    addPickRows: { value: [{ cfg: { id: 'cfg-1' }, selectable: true }] },
    addRequestGeneration: 0, installing: { value: false },
    defaultAddTargets: () => ['cfg-1'],
    ensureInstallerModelIfNeeded: async () => {},
    installSkillFromPrompt: async (prompt, ids) => {
      requests.push({ prompt, ids: Array.from(ids) })
      return { data: { installs: { 'cfg-1': 'job-1' } } }
    },
    registerThenAdvance: () => { throw new Error('Prompt must not register/search first') },
    catalogInstallFailedCount: () => 0,
    MessagePlugin: { success() {}, error(error) { throw new Error(error) } },
    loadCatalog: async () => {}, prunePicks() {}, t: key => key,
  }
  runInNewContext(transpile(handler), state)
  await state.handleAddPrimary()
  assert.equal(state.addStep.value, 1)
  assert.equal(requests.length, 0)
  await state.handleAddPrimary()
  assert.deepEqual(requests, [{ prompt: state.skillPrompt.value, ids: ['cfg-1'] }])
  assert.equal(state.promptInstallIds.value['cfg-1'], 'job-1')
  assert.doesNotMatch(source, /registerSkillCatalogFromPrompt|promptSelection|promptFind/)
})
