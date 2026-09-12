import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { isSkillInstallOutdated } from './skillUpgrade.ts'

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
    isSkillInstallOutdated,
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

test('builtin name conflicts offer explicit replacement before installing into a fresh sandbox', async () => {
  const { transpile } = await import('typescript')
  const { runInNewContext } = await import('node:vm')
  const handler = source.slice(source.indexOf('async function confirmInstall()'), source.indexOf('async function removeCatalog('))
  const requests = []
  const state = {
    installCatalog: { value: { id: '', name: 'pdf', builtin: true } },
    pendingBuiltinSkill: { value: { id: 'pdf', version: '2026.09.5' } },
    installTargetIds: { value: ['fresh-office-core'] },
    installPickRows: { value: [{ cfg: { id: 'fresh-office-core' }, selectable: true }] },
    builtinTargetsLoading: { value: false }, builtinRegistrationConflict: { value: false },
    installing: { value: false }, showInstall: { value: true },
    registerBuiltinSkill: async (id, replace) => {
      requests.push({ id, replace })
      if (!replace) throw { status: 409, message: 'different skill' }
      return { data: { id: 'existing-pdf-catalog' } }
    },
    installSkillCatalog: async (id, targets) => {
      requests.push({ id, targets: Array.from(targets) })
      return { data: { installs: { 'fresh-office-core': 'install-job' } } }
    },
    MessagePlugin: { success() {}, warning() {}, error(error) { throw new Error(error) } },
    catalogInstallFailedCount: () => 0, loadCatalog: async () => {}, prunePicks() {}, t: key => key,
  }
  runInNewContext(transpile(handler), state)
  await state.confirmInstall()
  assert.equal(state.builtinRegistrationConflict.value, true)
  assert.equal(state.installing.value, false)
  assert.equal(requests.length, 1, 'a catalog conflict must not start a sandbox installation')
  await state.confirmInstall()
  assert.deepEqual(requests, [
    { id: 'pdf', replace: false },
    { id: 'pdf', replace: true },
    { id: 'existing-pdf-catalog', targets: ['fresh-office-core'] },
  ])
  assert.equal(state.builtinRegistrationConflict.value, false)
  assert.equal(state.installCatalog.value.id, 'existing-pdf-catalog')

  // A delayed conflict must not reopen a drawer that the user has closed.
  state.installCatalog.value = { id: '', name: 'pdf', builtin: true }
  state.registerBuiltinSkill = async () => {
    state.showInstall.value = false
    throw { status: 409 }
  }
  await state.confirmInstall()
  assert.equal(state.showInstall.value, false)
  assert.equal(state.builtinRegistrationConflict.value, false)
})

test('installation feedback stays on its sandbox row and closing does not imply success', async () => {
  const { transpile } = await import('typescript')
  const { runInNewContext } = await import('node:vm')
  const failed = source.slice(source.indexOf('function sandboxPickFailed('), source.indexOf('function sandboxPickPercent('))
  const footer = source.slice(source.indexOf('const installActionText'), source.indexOf('const installConfirmDisabled'))
  const progress = source.slice(source.indexOf('const installInProgress'), source.indexOf('const addPickRows'))
  const current = { cfg: { id: 'office-core' }, install: { status: 'installing' }, busy: true, ready: false }
  const previous = { cfg: { id: 'docker-test' }, install: { status: 'failed' }, busy: false, ready: false }
  const state = {
    computed: fn => ({ get value() { return fn() } }), t: key => key,
    installTargetIds: { value: [] }, builtinRegistrationConflict: { value: false },
    installPickRows: { value: [previous, current] },
  }
  runInNewContext(transpile(failed + footer + progress + '\nglobalThis.readFooter = () => ({ text: installConfirmText.value, busy: installInProgress.value });'), state)
  assert.equal(state.sandboxPickFailed(current), false)
  assert.equal(state.sandboxPickFailed(previous), true)
  assert.equal(state.sandboxPickFailed({ ...previous, ready: true }), false)
  assert.equal(state.readFooter().text, 'common.close')
  assert.equal(state.readFooter().busy, true)
  current.install.status = 'ready'
  current.busy = false
  current.ready = true
  assert.equal(state.readFooter().busy, false, 'an old failure must not affect current progress')
  state.installTargetIds.value = ['office-core']
  assert.equal(state.readFooter().text, 'skillDiscovery.installSelected')
  state.builtinRegistrationConflict.value = true
  assert.equal(state.readFooter().text, 'skillDiscovery.installSelected')
  assert.doesNotMatch(source, /activationFailedHint|installPickRows\.filter\(row => row\.install\?\.status === 'failed'\)/)
})

test('outdated ready installs are selectable in the upgrade group; current and busy installs are not', async () => {
  const { transpile } = await import('typescript')
  const { runInNewContext } = await import('node:vm')
  const groups = source.slice(source.indexOf('function groupSandboxPicks('), source.indexOf('const installPickGroups'))
  const rows = source.slice(source.indexOf('function sandboxPickRows('), source.indexOf('function sandboxPickPercent('))
  const context = {
    skillConfigs: { value: ['old', 'current', 'busy'].map(id => ({ id })) },
    liveInstalls: item => item.installations,
    isSkillInstallOutdated,
    isInstallBusy: install => ['installing', 'removing'].includes(install.status),
  }
  runInNewContext(transpile(groups + rows), context)
  const picks = context.sandboxPickRows({ bundle_sha256: 'new', installations: [
    { sandbox_config_id: 'old', status: 'ready', bundle_sha256: 'old' },
    { sandbox_config_id: 'current', status: 'ready', bundle_sha256: 'new' },
    { sandbox_config_id: 'busy', status: 'installing', bundle_sha256: 'old' },
  ] })
  assert.equal(picks[0].selectable, true)
  assert.equal(picks[1].selectable, false)
  assert.equal(picks[2].selectable, false)
  assert.equal(context.groupSandboxPicks(picks)[0].key, 'updates')
  assert.equal(context.groupSandboxPicks(picks)[0].rows[0].cfg.id, 'old')
})

test('discovery upgrades keep the requested bundle while catalog progress refreshes', async () => {
  const { transpile } = await import('typescript')
  const { runInNewContext } = await import('node:vm')
  const handler = source.slice(source.indexOf('async function onDiscoveryInstall('), source.indexOf('const loading = ref'))
  const picker = source.slice(source.indexOf('const installPickRows = computed'), source.indexOf('const installInProgress'))
  const old = { id: 'existing', name: 'pdf', builtin: true, bundle_sha256: 'old', version: '1', installations: [{ sandbox_config_id: 'docker', bundle_sha256: 'old', status: 'ready' }] }
  const state = {
    catalog: { value: [old] }, installCatalog: { value: null }, pendingBuiltinSkill: { value: null }, builtinRegistrationConflict: { value: false },
    builtinTargetsLoading: { value: false }, builtinTargetStatus: { value: { docker: 'outdated' } },
    computed: fn => ({ get value() { return fn() } }), catalogItemById: id => id === old.id ? old : null,
    openInstall(item) { state.installCatalog.value = item },
    sandboxPickRows(item) { return [{ cfg: { id: 'docker' }, install: item.installations[0], upgrade: isSkillInstallOutdated(item, item.installations[0]), ready: true, selectable: false, busy: false }] },
  }
  runInNewContext(transpile(handler + picker + '\nglobalThis.picks = () => installPickRows.value;'), state)
  await state.onDiscoveryInstall({ id: 'pdf', name: 'pdf', distribution: 'builtin', bundle_sha256: 'new', version: '2', description: {} })
  assert.equal(state.installCatalog.value.id, '', 'must register the desired package before installing')
  assert.equal(state.installCatalog.value.bundle_sha256, 'new')
  assert.equal(state.builtinRegistrationConflict.value, true)
  assert.equal(state.picks()[0].selectable, true)
  assert.equal(state.picks()[0].ready, false)
  old.installations = [{ sandbox_config_id: 'docker', bundle_sha256: 'new', status: 'ready' }]
  state.builtinTargetStatus.value.docker = 'installed'
  assert.equal(state.picks()[0].selectable, false)
  assert.equal(state.picks()[0].ready, true)
})


test('install action names distinguish upgrades, installs and mixed selections', async () => {
  const { transpile } = await import('typescript')
  const { runInNewContext } = await import('node:vm')
  const footer = source.slice(source.indexOf('const installActionText'), source.indexOf('const installConfirmDisabled'))
  const state = {
    computed: fn => ({ get value() { return fn() } }), t: (key, values) => ({ key, ...values }),
    installTargetIds: { value: [] },
    installPickRows: { value: [{ cfg: { id: 'update' }, upgrade: true }, { cfg: { id: 'new' }, upgrade: false }] },
  }
  runInNewContext(transpile(footer + '\nglobalThis.action = () => installConfirmText.value;'), state)
  assert.equal(state.action().key, 'common.close')
  state.installTargetIds.value = ['update']
  assert.deepEqual(state.action(), { key: 'skillDiscovery.upgradeSelected', count: 1 })
  state.installTargetIds.value = ['new']
  assert.deepEqual(state.action(), { key: 'skillDiscovery.installSelected', count: 1 })
  state.installTargetIds.value = ['update', 'new']
  assert.deepEqual(state.action(), { key: 'skillDiscovery.installAndUpgradeSelected', installs: 1, upgrades: 1 })
})
