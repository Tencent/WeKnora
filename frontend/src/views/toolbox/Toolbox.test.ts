import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick, reactive } from 'vue'
import * as toolbox from '../../config/toolbox'
import { countResourcesByCategory } from '../../utils/toolboxCategories'

const require = createRequire(import.meta.url)
const { descriptor } = parse(readFileSync(new URL('./Toolbox.vue', import.meta.url), 'utf8'))
const script = compileScript(descriptor, { id: 'toolbox-flow-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

async function settleReads() {
  await new Promise<void>(resolve => setImmediate(resolve))
  await nextTick()
}

async function fixture(readMCP: () => Promise<any[]> = async () => []) {
  const route = reactive({ params: { section: 'skills' }, query: {} })
  const auth = reactive({ effectiveTenantId: 1, currentTenantRole: 'admin', hasRole: () => true })
  const exports: any = {}
  const errors: string[] = []
  runInNewContext(compiled, { exports, console: { error() {} }, require(name: string) {
    if (name === 'vue') return require('vue')
    if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
    if (name === 'vue-router') return {
      useRoute: () => route,
      useRouter: () => ({ replace: async ({ path }: { path: string }) => { route.params.section = path.split('/').at(-1)! } }),
    }
    if (name === '@/config/toolbox') return toolbox
    if (name === '@/utils/toolboxCategories') return { countResourcesByCategory }
    if (name === 'tdesign-vue-next') return { MessagePlugin: { error: (message: string) => errors.push(message) } }
    if (name === '@/stores/auth') return { useAuthStore: () => auth }
    if (name === '@/stores/browserConnection') return { useBrowserConnectionStore: () => ({ loaded: true }) }
    if (name === '@/stores/deploymentCapabilities') return { useDeploymentCapabilitiesStore: () => ({ isSupported: () => true }) }
    if (name === '@/api/skill') return { listSkillCatalog: async () => ({ data: [] }) }
    if (name === '@/api/mcp-service') return { listMCPServices: readMCP }
    if (name.endsWith('.vue')) return { default: {} }
    assert.fail(`Unexpected import: ${name}`)
  } })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp(component)
  const vm: any = app.mount({})
  await settleReads()
  return { vm, auth, errors, close: () => app.unmount() }
}

test('toolbox shares the tag across tabs and clears it on workspace changes', async () => {
  const { vm, auth, close } = await fixture()
  try {
    vm.selectedCategoryId = 'contracts'
    vm.select('mcp')
    await nextTick()
    assert.equal(vm.selectedItem.key, 'mcp')
    assert.equal(vm.selectedCategoryId, 'contracts')
    vm.select('skills')
    await nextTick()
    assert.equal(vm.selectedCategoryId, 'contracts')
    auth.effectiveTenantId = 2
    await nextTick()
    assert.equal(vm.selectedCategoryId, '')
  } finally { close() }
})

test('both tab badges follow the shared tag, including empty subsets and all resources', async () => {
  const a = { id: 'a', name: 'Contracts' }, b = { id: 'b', name: 'Customers' }
  const { vm, close } = await fixture(async () => [{ categories: [a] }, {}])
  try {
    vm.updateCounts('skills', countResourcesByCategory([{ categories: [a, b] }, { categories: [a] }, {}]))
    assert.deepEqual({ ...vm.counts }, { skills: 3, mcp: 2 })
    vm.selectedCategoryId = 'a'
    assert.deepEqual({ ...vm.counts }, { skills: 2, mcp: 1 })
    vm.select('mcp')
    await nextTick()
    assert.deepEqual({ ...vm.counts }, { skills: 2, mcp: 1 })
    vm.selectedCategoryId = 'b'
    assert.deepEqual({ ...vm.counts }, { skills: 1, mcp: 0 })
    vm.selectedCategoryId = 'empty'
    assert.deepEqual({ ...vm.counts }, { skills: 0, mcp: 0 })
    vm.selectedCategoryId = ''
    assert.deepEqual({ ...vm.counts }, { skills: 3, mcp: 2 })
  } finally { close() }
})

test('an old unopened-tab response cannot overwrite counts published after editing', async () => {
  let resolve!: (rows: any[]) => void
  const { vm, close } = await fixture(() => new Promise(done => { resolve = done }))
  try {
    vm.updateCounts('mcp', { '': 2, a: 2 })
    vm.selectedCategoryId = 'a'
    resolve([])
    await settleReads()
    assert.equal(vm.counts.mcp, 2)
  } finally { close() }
})

test('workspace switches discard old counts and ignore the previous workspace response', async () => {
  const pending: Array<(rows: any[]) => void> = []
  const { vm, auth, close } = await fixture(() => new Promise(done => pending.push(done)))
  try {
    vm.updateCounts('skills', { '': 3, a: 2 })
    vm.selectedCategoryId = 'a'
    auth.effectiveTenantId = 2
    await nextTick()
    assert.equal(vm.selectedCategoryId, '')
    assert.deepEqual({ ...vm.counts }, {})
    pending[1]!([{}, {}])
    await settleReads()
    assert.equal(vm.counts.mcp, 2)
    pending[0]!([])
    await settleReads()
    assert.equal(vm.counts.mcp, 2)
  } finally { close() }
})

test('failed summary reads report an error instead of presenting a zero count', async () => {
  const { vm, errors, close } = await fixture(async () => { throw new Error('offline') })
  try {
    assert.equal(vm.counts.mcp, undefined)
    assert.deepEqual(errors, ['mcpSettings.toasts.loadFailed'])
  } finally { close() }
})
