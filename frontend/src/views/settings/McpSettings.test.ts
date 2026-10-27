import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick, reactive } from 'vue'
import { matchesResourceQuery } from '../../utils/resourceListSearch'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./McpSettings.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'mcp-settings-test' }).content
  .replace(/__expose\([^;]*\);/g, '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

async function fixture(
  update: () => Promise<void> = async () => {}, admin = true,
  options: { query?: Record<string, string>; draft?: unknown } = {},
) {
  const calls: Array<{ id: string; data: unknown }> = []
  const errors: string[] = []
  const deletes: unknown[] = []
  const service = reactive({ id: 'one', name: 'Logs', enabled: true, is_builtin: false })
  let pageContext: () => unknown = () => null
  const route = reactive({ path: '/platform/toolbox/mcp', query: { ...(options.query || {}) } as Record<string, string> })
  const replaced: unknown[] = []
  const warnings: string[] = []
  let lists = 0
  let dataChange: { handler: () => void; areas: string[] } | null = null
  const assistant = reactive({
    pageDraft: options.draft ? { kind: 'mcp_service', draft: options.draft } : null as any,
    takePageDraft(kind: string) {
      const waiting = assistant.pageDraft
      if (!waiting || waiting.kind !== kind) return null
      assistant.pageDraft = null
      return waiting.draft
    },
  })
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console: { error() {} },
    require(name: string) {
      if (name === '@/utils/resourceListSearch') return { matchesResourceQuery }
      if (name === 'vue-router') return {
        useRoute: () => route,
        useRouter: () => ({ replace: (to: any) => { replaced.push(to); route.query = { ...to.query } } }),
      }
      if (name === '@/stores/assistant') return { useAssistantStore: () => assistant }
      if (name === '@/composables/useAssistantPageContext') return {
        useAssistantPageContext: (source: () => unknown) => { pageContext = source },
      }
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/stores/auth') return { useAuthStore: () => ({ hasRole: () => admin }) }
      if (name === '@/components/settings/useConfirmDelete') return { useConfirmDelete: () => (data: unknown) => deletes.push(data) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: {
        success() {}, error: (message: string) => errors.push(message), warning: (message: string) => warnings.push(message),
      } }
      if (name === '@/utils/assistantDataChange') return {
        useAssistantDataChange: (handler: () => void, opts: { areas: string[] }) => {
          dataChange = { handler, areas: opts.areas }
        },
      }
      if (name === '@/api/mcp-service') return {
        listMCPServices: async () => { lists++; return [service] },
        updateMCPService: async (id: string, data: unknown) => { calls.push({ id, data }); await update() },
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
  const app = renderer.createApp(component)
  const vm: any = app.mount({})
  await new Promise<void>(resolve => setImmediate(resolve))
  await nextTick()
  return {
    vm, service, calls, errors, deletes, route, replaced, warnings, assistant,
    pageContext: () => JSON.parse(JSON.stringify(pageContext())),
    lists: () => lists, dataChange: () => dataChange,
    close: () => app.unmount(),
  }
}

test('an assistant change to MCP services reloads the list', async () => {
  const f = await fixture()
  try {
    assert.equal(f.lists(), 1)
    assert.deepEqual([...f.dataChange()?.areas || []], ['mcp_service'])
    f.dataChange()?.handler()
    await new Promise<void>(resolve => setImmediate(resolve))
    assert.equal(f.lists(), 2)
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

test('the assistant sees the MCP page and the service open for editing', async () => {
  const f = await fixture()
  try {
    assert.deepEqual(f.pageContext(), { page: 'mcp_services' })
    f.vm.handleAdd()
    assert.deepEqual(f.pageContext(), { page: 'mcp_services' }, 'a service being created has no id yet')
    f.vm.handleEdit(f.service)
    assert.deepEqual(f.pageContext(), { page: 'mcp_services', mcpServiceId: 'one', mcpServiceName: 'Logs' })
    f.vm.dialogVisible = false
    assert.deepEqual(f.pageContext(), { page: 'mcp_services' })
  } finally { f.close() }
})

test('an assistant draft opens a filled-in new service without credentials', async () => {
  const draft = {
    name: 'Jira', transport_type: 'http-streamable', url: 'https://mcp.example.com/mcp',
    auth_type: 'api_key', api_key_header: 'X-Jira-Key', usage_instructions: 'Tickets',
  }
  const f = await fixture(undefined, true, { draft })
  try {
    assert.equal(f.vm.dialogVisible, true)
    assert.equal(f.vm.dialogMode, 'add')
    assert.equal(f.vm.currentService.id, '', 'a draft is not an existing service')
    assert.equal(f.vm.currentService.url, 'https://mcp.example.com/mcp')
    assert.equal(JSON.stringify(f.vm.currentService.auth_config), JSON.stringify({
      auth_type: 'api_key', api_key_header: 'X-Jira-Key',
    }))
    assert.equal(f.assistant.pageDraft, null, 'the draft is used once')
    assert.deepEqual(f.pageContext(), { page: 'mcp_services' }, 'a new service has no id to report')
  } finally { f.close() }
  const viewer = await fixture(undefined, false, { draft })
  try {
    assert.equal(viewer.vm.dialogVisible, false, 'viewers cannot add services')
  } finally { viewer.close() }
})

test('a service link opens that service and drops the query', async () => {
  const f = await fixture(undefined, true, { query: { service_id: 'one', keep: 'x' } })
  try {
    assert.equal(f.vm.dialogVisible, true)
    assert.equal(f.vm.dialogMode, 'edit')
    assert.equal(f.vm.currentService.id, 'one')
    assert.equal(JSON.stringify(f.route.query), JSON.stringify({ keep: 'x' }))
  } finally { f.close() }
  const missing = await fixture(undefined, true, { query: { service_id: 'gone' } })
  try {
    assert.equal(missing.vm.dialogVisible, false)
    assert.deepEqual(missing.warnings, ['mcpSettings.toasts.linkedServiceMissing'])
  } finally { missing.close() }
})
