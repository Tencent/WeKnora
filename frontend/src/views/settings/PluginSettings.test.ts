import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick } from 'vue'
import { matchesResourceQuery } from '../../utils/resourceListSearch'
import * as pluginUtils from '../../utils/pluginMarketplace'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./PluginSettings.vue', import.meta.url))
const source = readFileSync(filename, 'utf8')
const { descriptor } = parse(source, { filename })
const script = compileScript(descriptor, { id: 'plugin-settings-test' }).content
  .replace(/__expose\([^;]*\);/g, '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

const flush = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise<void>(resolve => setImmediate(resolve))
    await nextTick()
  }
}

const marketplace = () => ({
  id: 'mp-1', name: 'acme-plugins', created_at: '', updated_at: '',
  plugins: [
    { name: 'neon', description: 'Neon Postgres', keywords: ['postgres'], skill_count: 2, mcp_count: 1, requires_sandbox: true, installed: true, status: 'ready' },
    { name: 'calcom', description: 'Scheduling', keywords: ['calendar'], skill_count: 0, mcp_count: 1, requires_sandbox: false, installed: false },
    { name: 'sql-style', description: 'SQL style', skill_count: 1, mcp_count: 0, requires_sandbox: true, installed: true, status: 'failed', error: 'boom' },
  ],
})

async function fixture(admin = true, marketplaces = [marketplace()]) {
  const counts: number[] = []
  const deletes: Array<{ body: string; onConfirm: () => Promise<void> }> = []
  const deleted: string[] = []
  let lists = 0
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console: { error() {} }, window: { setTimeout: () => 1, clearTimeout() {} },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error() {} } }
      if (name === '@/stores/auth') return { useAuthStore: () => ({ hasRole: () => admin }) }
      if (name === '@/components/settings/useConfirmDelete') return { useConfirmDelete: () => (d: any) => deletes.push(d) }
      if (name === '@/utils/resourceListSearch') return { matchesResourceQuery }
      if (name === '@/utils/pluginMarketplace') return pluginUtils
      if (name === '@/types/mention') return { SKILL_ICON: 'skill' }
      if (name === '@/api/plugin') return {
        listPluginMarketplaces: async () => { lists++; return { data: marketplaces } },
        deletePluginMarketplace: async (id: string) => { deleted.push(id) },
        addPluginMarketplaceFromSource: async () => ({ data: {} }),
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
  const app = renderer.createApp(component, { onCount: (n: number) => counts.push(n) })
  const vm: any = app.mount({})
  await flush()
  return { vm, counts, deletes, deleted, lists: () => lists, close: () => app.unmount() }
}

test('the tab badge counts installed plugins across marketplaces', async () => {
  const f = await fixture()
  try {
    assert.deepEqual(f.counts, [2])
  } finally { f.close() }
})

test('search narrows to matching plugins by name, description or keyword', async () => {
  const f = await fixture()
  try {
    f.vm.query = 'calendar'
    assert.deepEqual(f.vm.filtered[0].plugins.map((p: any) => p.name), ['calcom'])
    f.vm.query = 'acme'
    assert.equal(f.vm.filtered[0].plugins.length, 3, 'a marketplace match keeps every plugin')
    f.vm.query = 'nothing-matches'
    assert.equal(f.vm.filtered.length, 0)
  } finally { f.close() }
})

test('installing a plugin opens its detail', async () => {
  const f = await fixture()
  try {
    f.vm.openInstall(f.vm.marketplaces[0], f.vm.marketplaces[0].plugins[1])
    assert.equal(f.vm.installVisible, true)
    await f.vm.onInstalled({ install_id: 'i', status: 'ready', async_installation: false, mcp_oauth_states: { calcom__calcom: 'needs_oauth' } })
    assert.equal(f.vm.installVisible, false)
    assert.equal(f.vm.detailVisible, true)
    assert.equal(f.vm.target.plugin.name, 'calcom')

    f.vm.detailVisible = false
    await f.vm.onInstalled({ install_id: 'i', status: 'ready', async_installation: false, mcp_oauth_states: { calcom__calcom: 'ready' } })
    assert.equal(f.vm.detailVisible, true)
  } finally { f.close() }
})

test('deleting a marketplace asks first, then reloads', async () => {
  const f = await fixture()
  try {
    f.vm.askDeleteMarketplace(f.vm.marketplaces[0])
    assert.equal(f.deletes.length, 1)
    assert.equal(f.deleted.length, 0)
    await f.deletes[0]!.onConfirm()
    assert.deepEqual(f.deleted, ['mp-1'])
    assert.equal(f.lists(), 2)
  } finally { f.close() }
})

test('viewers cannot open the add drawer', async () => {
  const f = await fixture(false)
  try {
    f.vm.openAdd()
    assert.equal(f.vm.addVisible, false)
  } finally { f.close() }
})

test('the card shows counts, sandbox need and error state', () => {
  const template = descriptor.template!.content
  for (const needle of [
    "settings.plugins.skillCount", "settings.plugins.mcpCount", "settings.plugins.needsSandbox",
    "plugin.error",
  ]) {
    assert.ok(template.includes(needle), needle)
  }
  assert.match(template, /:disabled="marketplaceHasInstalls\(marketplaces, mp\.id\)"/,
    'a marketplace with installed plugins cannot be deleted, even when search hides them')
  assert.ok(template.includes('pluginHasNewerVersion(plugin)'), 'an outdated install is flagged')
  assert.match(template, /@click="openInstall\(mp, plugin\)"/)
  assert.doesNotMatch(template, /v-else-if="isAdmin"/, 'install is available to every member who can open the page')
})
