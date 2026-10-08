import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick, ref } from 'vue'
import { matchesResourceQuery } from '../../utils/resourceListSearch'
import { countResourcesByCategory, hasToolboxCategory } from '../../utils/toolboxCategories'
import * as skillTarget from '../../utils/skillTarget'
import * as skillUpgrade from '../../utils/skillUpgrade'
import type { SkillCatalogItem } from '../../api/skill'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./SkillSettings.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'skill-filters-test' }).content
  .replace(/__expose\([^\n]*\)/g, '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function item(id: string, statuses: string[] = []): SkillCatalogItem {
  return {
    id, name: id, description: '', created_at: '', updated_at: '',
    installations: statuses.map((status, index) => ({
      skill_id: `${id}-${index}`, sandbox_config_id: `sandbox-${index}`,
      status, enabled: false, updated_at: '',
    })),
  }
}

async function fixture(initial: SkillCatalogItem[], host = false, assign: () => Promise<void> = async () => {}) {
  let catalog = initial
  let readCatalog = async () => ({ data: JSON.parse(JSON.stringify(catalog)) })
  const registrations: string[] = []
  const assignments: Array<{ id: string; ids: string[] }> = []
  const errors: string[] = []
  const counts: Array<Record<string, number>> = []
  const exports: any = {}
  const mocks: Record<string, unknown> = {
    'vue': require('vue'),
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, te: () => false }) },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error: (text: string) => errors.push(text) } },
    '@/stores/ui': { useUIStore: () => ({}) },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/deploymentCapabilities': {
      useDeploymentCapabilitiesStore: () => ({
        ensureLoaded: async () => {},
        isSupported: (key: string) => key.endsWith('.host') ? host : !host,
      }),
    },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => () => {} },
    '@/composables/useSkillInstallerModel': {
      useSkillInstallerModel: () => ({ installerModelId: ref(''), savingInstallerModel: ref(false), loadInstallerModel: async () => {} }),
    },
    '@/composables/useConfigSkillInstallProgress': {
      useConfigSkillInstallProgress: () => ({ percentOf: () => null, sync() {}, stopAll() {} }),
    },
    '@/api/skill': {
      listSkillCatalog: () => readCatalog(),
      registerSkillCatalogFromSource: async (source: string) => {
        registrations.push(source)
        const registered = catalog.find(row => row.id === 'created') || item('created')
        if (!catalog.includes(registered)) catalog.push(registered)
        return { data: registered }
      },
    },
    '@/api/toolbox-category': {
      listToolboxCategories: async () => [],
      replaceSkillCategories: async (id: string, ids: string[]) => {
        assignments.push({ id, ids: Array.from(ids) })
        await assign()
        const categories = ids.map(id => ({ id, name: id }))
        catalog.find(row => row.id === id)!.categories = categories
        return categories
      },
    },
    '@/api/system': { listSandboxConfigs: async () => ({ data: [] }) },
    '@/utils/resourceListSearch': { matchesResourceQuery },
    '@/utils/toolboxCategories': { countResourcesByCategory, hasToolboxCategory },
    '@/utils/skillTarget': skillTarget,
    '@/utils/skillUpgrade': skillUpgrade,
    '@/utils': {},
    '@/types/mention': { SKILL_ICON: 'tools' },
    'tdesign-icons-vue-next': {},
  }
  runInNewContext(compiled, {
    exports,
    window: { setInterval: () => 1, clearInterval() {} },
    require(name: string) {
      if (name.endsWith('.vue')) return { default: {} }
      assert.ok(name in mocks, `Unexpected import: ${name}`)
      return mocks[name]
    },
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp(component, { 'onCategory-counts': (value: Record<string, number>) => counts.push(value) })
  const vm: any = app.mount({})
  await new Promise<void>(resolve => setImmediate(resolve))
  await nextTick()
  return {
    vm, errors, registrations, assignments, counts, close: () => app.unmount(),
    readWith: (read: typeof readCatalog) => { readCatalog = read },
    reload: async (rows: SkillCatalogItem[]) => { catalog = rows; await vm.loadCatalog(true) },
  }
}

test('Skill tab counts update after tag assignment and ignore keyword search', async () => {
  const f = await fixture([item('one')])
  try {
    assert.deepEqual(f.counts.at(-1), { '': 1 })
    f.vm.openCategoryAssignment(f.vm.catalog[0])
    await f.vm.saveCategoryAssignment(['contracts'])
    await nextTick()
    assert.deepEqual(f.counts.at(-1), { '': 1, contracts: 1 })
    const emitted = f.counts.length
    f.vm.query = 'no matches'
    await nextTick()
    assert.equal(f.vm.filteredCatalog.length, 0)
    assert.equal(f.counts.length, emitted)
  } finally { f.close() }
})

const ids = (vm: any) => Array.from(vm.filteredCatalog, (row: any) => row.id)

test('skill search matches names and descriptions without changing order or row identity', async () => {
  const data = [item('财务 Report', ['ready']), item('未安装'), item('mixed', ['ready', 'failed'])]
  data[2]!.description = '财务 REPORT'
  const f = await fixture(data)
  try {
    f.vm.query = ' 财务 report '
    assert.deepEqual(ids(f.vm), ['财务 Report', 'mixed'])
    assert.equal(f.vm.filteredCatalog[1], f.vm.catalog[2])
    f.vm.openCatalogFiles(f.vm.filteredCatalog[1])
    assert.equal(f.vm.filesCatalogId, 'mixed')
    assert.equal(f.vm.catalog.length, 3)
    f.vm.query = 'missing'
    assert.deepEqual(ids(f.vm), [])
    f.vm.query = '  '
    assert.deepEqual(ids(f.vm), data.map(row => row.id))
  } finally { f.close() }
})

test('adding a skill inherits the selected tag and keeps the new entry visible', async () => {
  const f = await fixture([])
  try {
    f.vm.selectedCategoryId = 'contracts'
    f.vm.query = 'unrelated'
    await f.vm.openAdd()
    assert.deepEqual(Array.from(f.vm.addCategoryIds), ['contracts'])
    f.vm.sourceInput = 'https://example.com/skill'
    await f.vm.registerThenAdvance()
    assert.deepEqual(f.assignments, [{ id: 'created', ids: ['contracts'] }])
    assert.equal(f.vm.selectedCategoryId, 'contracts')
    assert.equal(f.vm.query, '')
    assert.deepEqual(ids(f.vm), ['created'])
    assert.equal(f.vm.addStep, 1)
    assert.deepEqual(f.errors, [])
  } finally { f.close() }
})

test('failed skill tag assignment retains the registration and retries without reimporting', async () => {
  let fail = true
  const f = await fixture([], false, async () => { if (fail) throw new Error('unavailable') })
  try {
    f.vm.selectedCategoryId = 'contracts'
    await f.vm.openAdd()
    f.vm.sourceInput = 'https://example.com/skill'
    await f.vm.registerThenAdvance()
    assert.equal(f.vm.addStep, 0)
    assert.equal(f.vm.registeredCatalog.id, 'created')
    assert.deepEqual(f.errors, ['toolboxCategories.resourceSavedAssignmentFailed'])
    assert.deepEqual(ids(f.vm), ['created'])
    f.vm.goToAddStep(1)
    await new Promise<void>(resolve => setImmediate(resolve))
    assert.equal(f.vm.addStep, 0, 'the step header must not bypass failed classification')
    fail = false
    await f.vm.registerThenAdvance()
    assert.equal(f.registrations.length, 1)
    assert.equal(f.assignments.length, 3)
    assert.equal(f.vm.addStep, 1)
  } finally { f.close() }
})

test('editing the preselection reveals the skill outside its initial tag and preserves existing tags on reimport', async () => {
  const existing = item('created')
  existing.categories = [{ id: 'crm', name: 'CRM' }]
  const f = await fixture([existing])
  try {
    f.vm.selectedCategoryId = 'contracts'
    await f.vm.openAdd()
    f.vm.sourceInput = 'https://example.com/skill'
    f.vm.addCategoryIds = []
    await f.vm.registerThenAdvance()
    assert.equal(f.vm.selectedCategoryId, '')
    assert.deepEqual(ids(f.vm), ['created'])
    assert.deepEqual(f.assignments, [])
    assert.deepEqual(Array.from(f.vm.addCategoryIds), ['crm'])
  } finally { f.close() }
})

test('skill tag filter supports multiple tags and intersects with search', async () => {
  const contracts = item('contracts')
  contracts.categories = [{ id: 'legal', name: 'Contracts' }, { id: 'crm', name: 'Customers' }]
  const customers = item('customers')
  customers.categories = [{ id: 'crm', name: 'Customers' }]
  const f = await fixture([contracts, customers, item('uncategorized')])
  try {
    f.vm.selectedCategoryId = 'legal'
    assert.deepEqual(ids(f.vm), ['contracts'])
    f.vm.selectedCategoryId = 'crm'
    assert.deepEqual(ids(f.vm), ['contracts', 'customers'])
    f.vm.query = 'customers'
    assert.deepEqual(ids(f.vm), ['customers'])
    f.vm.clearFilters()
    assert.deepEqual(ids(f.vm), ['contracts', 'customers', 'uncategorized'])
  } finally { f.close() }
})

test('saving tags after a catalog refresh updates the current card and filter', async () => {
  const original = item('contracts', ['installing'])
  original.categories = [{ id: 'legal', name: 'Contracts' }]
  const f = await fixture([original])
  try {
    f.vm.openCategoryAssignment(f.vm.catalog[0])
    const refreshed = item('contracts', ['ready'])
    refreshed.categories = [...original.categories]
    await f.reload([refreshed])
    f.vm.selectedCategoryId = 'crm'
    assert.deepEqual(ids(f.vm), [])
    await f.vm.saveCategoryAssignment(['legal', 'crm'])
    assert.deepEqual(ids(f.vm), ['contracts'])
    assert.deepEqual(Array.from(f.vm.catalog[0].categories, (tag: any) => tag.id), ['legal', 'crm'])
    assert.equal(f.vm.categoryDialogVisible, false)
  } finally { f.close() }
})

test('a catalog response started before saving cannot undo tags or remove them on the next edit', async () => {
  const original = item('contracts', ['installing'])
  original.categories = [{ id: 'legal', name: 'Contracts' }]
  const f = await fixture([original])
  let finishRead!: (value: { data: SkillCatalogItem[] }) => void
  const stale = JSON.parse(JSON.stringify(original))
  stale.installations = []
  try {
    f.readWith(() => new Promise(resolve => { finishRead = resolve }))
    const reading = f.vm.loadCatalog(true)
    f.vm.openCategoryAssignment(f.vm.catalog[0])
    await f.vm.saveCategoryAssignment(['legal', 'crm'])
    finishRead({ data: [stale] })
    await reading
    f.vm.selectedCategoryId = 'crm'
    assert.deepEqual(ids(f.vm), ['contracts'])
    f.vm.openCategoryAssignment(f.vm.catalog[0])
    await f.vm.saveCategoryAssignment([...f.vm.categoryResource.categories.map((tag: any) => tag.id), 'finance'])
    assert.deepEqual(f.assignments.at(-1)?.ids, ['legal', 'crm', 'finance'])
  } finally { f.close() }
})

test('slow overlapping catalog reads still make progress and cannot replace a newer result', async () => {
  const f = await fixture([item('initial')])
  const pending: Array<(value: { data: SkillCatalogItem[] }) => void> = []
  try {
    f.readWith(() => new Promise(resolve => { pending.push(resolve) }))
    const first = f.vm.loadCatalog(true)
    const second = f.vm.loadCatalog(true)
    pending[0]!({ data: [item('first')] })
    await first
    assert.deepEqual(ids(f.vm), ['first'], 'a slower next poll must not starve completed reads')
    const third = f.vm.loadCatalog(true)
    pending[2]!({ data: [item('latest')] })
    await third
    pending[1]!({ data: [item('older')] })
    await second
    assert.deepEqual(ids(f.vm), ['latest'])
  } finally { f.close() }
})

for (const host of [false, true]) {
  test(`skill search updates on catalog reload (${host ? 'Lite' : 'Standard'})`, async () => {
    const f = await fixture([item('report'), item('other')], host)
    try {
      f.vm.query = 'report'
      assert.deepEqual(ids(f.vm), ['report'])
      const updated = item('new')
      updated.description = 'Report tools'
      await f.reload([item('other'), updated])
      assert.deepEqual(ids(f.vm), ['new'])
      assert.equal(f.vm.query, 'report')
      await f.reload([])
      assert.deepEqual(ids(f.vm), [])
    } finally { f.close() }
  })
}
