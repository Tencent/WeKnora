import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick, reactive } from 'vue'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./McpSettings.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'mcp-settings-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

async function fixture(update: () => Promise<void> = async () => {}, admin = true) {
  const calls: Array<{ id: string; data: unknown }> = []
  const errors: string[] = []
  const deletes: unknown[] = []
  const service = reactive({ id: 'one', name: 'Logs', enabled: true, is_builtin: false })
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console: { error() {} },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/stores/auth') return { useAuthStore: () => ({ hasRole: () => admin }) }
      if (name === '@/components/settings/useConfirmDelete') return { useConfirmDelete: () => (data: unknown) => deletes.push(data) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error: (message: string) => errors.push(message) } }
      if (name === '@/api/mcp-service') return {
        listMCPServices: async () => [service],
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
  await nextTick()
  return { vm, service, calls, errors, deletes, close: () => app.unmount() }
}

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
