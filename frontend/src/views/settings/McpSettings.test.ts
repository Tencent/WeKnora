import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick, reactive, watch } from 'vue'
import { matchesResourceQuery } from '../../utils/resourceListSearch'
import { countResourcesByCategory, hasToolboxCategory } from '../../utils/toolboxCategories'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./McpSettings.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'mcp-settings-test' }).content
  .replace(/__expose\([^\n]*\)/g, '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

async function fixture(update: () => Promise<void> = async () => {}, admin = true) {
  const calls: Array<{ id: string; data: unknown }> = []
  const errors: string[] = []
  const deletes: unknown[] = []
  const counts: Array<Record<string, number>> = []
  const service = reactive({ id: 'one', name: 'Logs', enabled: true, is_builtin: false })
  let readServices = async (): Promise<any[]> => [service]
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console: { error() {} },
    require(name: string) {
      if (name === '@/utils/resourceListSearch') return { matchesResourceQuery }
      if (name === '@/utils/toolboxCategories') return { countResourcesByCategory, hasToolboxCategory }
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/stores/auth') return { useAuthStore: () => ({ hasRole: () => admin }) }
      if (name === '@/components/settings/useConfirmDelete') return { useConfirmDelete: () => (data: unknown) => deletes.push(data) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error: (message: string) => errors.push(message) } }
      if (name === '@/api/mcp-service') return {
        listMCPServices: () => readServices(),
        updateMCPService: async (id: string, data: unknown) => { calls.push({ id, data }); await update() },
      }
      if (name === '@/api/toolbox-category') return {
        listToolboxCategories: async () => [],
        replaceMCPServiceCategories: async (_id: string, ids: string[]) => ids.map(id => ({ id, name: id })),
      }
      return { default: {} }
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
  return { vm, service, calls, errors, deletes, counts, readWith: (read: typeof readServices) => { readServices = read }, close: () => app.unmount() }
}

test('MCP tab counts update after tag assignment and ignore keyword search', async () => {
  const f = await fixture()
  try {
    assert.deepEqual(f.counts.at(-1), { '': 1 })
    f.vm.openCategoryAssignment(f.vm.services[0])
    await f.vm.saveCategoryAssignment(['contracts'])
    await nextTick()
    assert.deepEqual(f.counts.at(-1), { '': 1, contracts: 1 })
    const emitted = f.counts.length
    f.vm.query = 'no matches'
    await nextTick()
    assert.equal(f.vm.filteredServices.length, 0)
    assert.equal(f.counts.length, emitted)
  } finally { f.close() }
})

test('edit and tools buttons select the correct drawer step; add resets it', async () => {
  const f = await fixture()
  try {
    f.vm.handleEdit(f.service, 1)
    assert.equal(f.vm.dialogInitialStep, 1)
    assert.equal(f.vm.currentService.id, 'one')
    f.vm.handleEdit(f.service)
    assert.equal(f.vm.dialogInitialStep, 0)
    f.vm.handleEdit(f.service, 1)
    f.vm.handleAdd()
    assert.equal(f.vm.dialogInitialStep, 0)
    assert.equal(f.vm.currentService, null)
    assert.equal(f.calls.length, 0)
  } finally { f.close() }
})

test('refreshing tags keeps the existing MCP panel mounted', async () => {
  const f = await fixture()
  const loadingStates: boolean[] = []
  const stop = watch(() => f.vm.loading, value => loadingStates.push(value), { flush: 'sync' })
  try {
    await f.vm.loadCategories()
    assert.deepEqual(loadingStates, [])
    assert.deepEqual(ids(f.vm), ['one'])
  } finally { stop(); f.close() }
})

test('saving tags targets the latest MCP row after list replacement', async () => {
  const f = await fixture()
  try {
    f.vm.openCategoryAssignment(f.vm.services[0])
    f.vm.services = [{ ...f.service, categories: [] }]
    f.vm.selectedCategoryId = 'contracts'
    await f.vm.saveCategoryAssignment(['contracts'])
    assert.deepEqual(ids(f.vm), ['one'])
    assert.equal(f.vm.categoryDialogVisible, false)
  } finally { f.close() }
})

test('an older MCP list response cannot undo a successful tag assignment', async () => {
  const f = await fixture()
  let finishRead!: (value: any[]) => void
  const stale = { ...f.service, categories: [] }
  try {
    f.readWith(() => new Promise(resolve => { finishRead = resolve }))
    const reading = f.vm.loadServices(true)
    f.vm.openCategoryAssignment(f.vm.services[0])
    await f.vm.saveCategoryAssignment(['contracts'])
    finishRead([stale])
    await reading
    f.vm.selectedCategoryId = 'contracts'
    assert.deepEqual(ids(f.vm), ['one'])
  } finally { f.close() }
})

test('adding in a tag preselects it; saving outside the filter reveals the resource', async () => {
  const f = await fixture()
  try {
    f.vm.selectedCategoryId = 'contracts'
    f.vm.query = 'no match'
    f.vm.handleAdd()
    assert.deepEqual(Array.from(f.vm.initialCategoryIds), ['contracts'])
    Object.assign(f.service, { categories: [{ id: 'contracts', name: 'Contracts' }] })
    await f.vm.handleDialogCreated(f.service)
    assert.equal(f.vm.selectedCategoryId, 'contracts')
    assert.equal(f.vm.query, '')
    assert.deepEqual(ids(f.vm), ['one'])
    Object.assign(f.service, { categories: [] })
    await f.vm.handleDialogCreated(f.service)
    assert.equal(f.vm.selectedCategoryId, '')
    assert.deepEqual(ids(f.vm), ['one'])
  } finally { f.close() }
})

test('status changes after save and repeated clicks cannot race', async () => {
  let resolve!: () => void
  const pending = new Promise<void>(done => { resolve = done })
  const f = await fixture(() => pending)
  try {
    const saving = f.vm.handleToggleEnabled(f.service)
    assert.equal(f.service.enabled, true)
    assert.equal(f.vm.togglingIds.has('one'), true)
    await f.vm.handleToggleEnabled(f.service)
    assert.equal(f.calls.length, 1)
    assert.deepEqual({ ...f.calls[0]?.data as object }, { enabled: false })
    resolve()
    await saving
    assert.equal(f.service.enabled, false)
    assert.equal(f.vm.togglingIds.size, 0)
  } finally { f.close() }
})

test('failed toggle preserves the displayed status and allows a retry', async () => {
  const f = await fixture(async () => { throw new Error('unavailable') })
  try {
    await f.vm.handleToggleEnabled(f.service)
    assert.equal(f.service.enabled, true)
    assert.equal(f.vm.togglingIds.size, 0)
    assert.deepEqual(f.errors, ['mcpSettings.toasts.updateStateFailed'])
  } finally { f.close() }
})

test('viewer controls and builtin mutations cannot update services', async () => {
  const viewer = await fixture(undefined, false)
  const builtin = await fixture()
  try {
    viewer.vm.handleEdit(viewer.service, 1)
    await viewer.vm.handleToggleEnabled(viewer.service)
    viewer.vm.handleDelete(viewer.service)
    assert.equal(viewer.vm.dialogVisible, false)
    assert.equal(viewer.calls.length, 0)
    assert.equal(viewer.deletes.length, 0)
    builtin.service.is_builtin = true
    await builtin.vm.handleToggleEnabled(builtin.service)
    builtin.vm.handleDelete(builtin.service)
    assert.equal(builtin.calls.length, 0)
    assert.equal(builtin.deletes.length, 0)
  } finally { viewer.close(); builtin.close() }
})

const rows = () => [
  { id: 'builtin', name: 'Knowledge', description: 'Search docs', enabled: false, is_builtin: true, transport_type: 'http-streamable' },
  { id: 'logs', name: '日志 Logs', description: 'hidden legacy text', usage_instructions: 'Audit REPORT', enabled: true, transport_type: 'sse', headers: { Authorization: 'secret-fixture' } },
  { id: 'local', name: 'Local', description: '', enabled: false, transport_type: 'stdio' },
]
const ids = (vm: any) => Array.from(vm.filteredServices, (row: any) => row.id)

test('MCP search matches visible metadata and clearing restores every service', async () => {
  const f = await fixture()
  try {
    f.vm.services = rows()
    f.vm.query = ' 日志   report '
    assert.deepEqual(ids(f.vm), ['logs'])
    f.vm.query = 'knowledge docs'
    assert.deepEqual(ids(f.vm), ['builtin'])
    for (const query of ['secret-fixture', 'hidden legacy', 'missing']) {
      f.vm.query = query
      assert.deepEqual(ids(f.vm), [])
    }
    f.vm.query = '  '
    assert.deepEqual(ids(f.vm), ['builtin', 'logs', 'local'])
  } finally { f.close() }
})

test('MCP tag filter supports shared multi-tag assignments', async () => {
  const f = await fixture()
  try {
    const services = rows()
    Object.assign(services[0]!, { categories: [{ id: 'legal', name: 'Contracts' }, { id: 'crm', name: 'Customers' }] })
    Object.assign(services[1]!, { categories: [{ id: 'crm', name: 'Customers' }] })
    f.vm.services = services
    f.vm.selectedCategoryId = 'legal'
    assert.deepEqual(ids(f.vm), ['builtin'])
    f.vm.selectedCategoryId = 'crm'
    assert.deepEqual(ids(f.vm), ['builtin', 'logs'])
    f.vm.query = 'logs'
    assert.deepEqual(ids(f.vm), ['logs'])
    f.vm.clearFilters()
    assert.deepEqual(ids(f.vm), ['builtin', 'logs', 'local'])
  } finally { f.close() }
})

test('search results keep service identity for editing and toggling', async () => {
  const f = await fixture()
  try {
    f.vm.services = rows()
    f.vm.query = 'logs'
    const service = f.vm.filteredServices[0]
    assert.equal(service, f.vm.services[1])
    f.vm.handleEdit(service)
    assert.equal(f.vm.currentService.id, 'logs')
    await f.vm.handleToggleEnabled(service)
    assert.equal(f.calls[0]?.id, 'logs')
    assert.equal(service.enabled, false)
    assert.deepEqual(ids(f.vm), ['logs'])
    assert.equal(f.vm.services.length, 3)
    service.name = 'Renamed'
    assert.deepEqual(ids(f.vm), [])
  } finally { f.close() }
})

test('a failed MCP save keeps the row in the search results', async () => {
  const f = await fixture(async () => { throw new Error('unavailable') })
  try {
    f.vm.services = rows()
    f.vm.query = 'logs'
    await f.vm.handleToggleEnabled(f.vm.filteredServices[0])
    assert.deepEqual(ids(f.vm), ['logs'])
    assert.equal(f.vm.filteredServices[0].enabled, true)
    assert.deepEqual(f.errors, ['mcpSettings.toasts.updateStateFailed'])
  } finally { f.close() }
})
