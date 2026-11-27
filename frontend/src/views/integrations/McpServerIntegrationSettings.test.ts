import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as vue from 'vue'
import * as integration from './mcpServerIntegration'

const filename = new URL('./McpServerIntegrationSettings.vue', import.meta.url)
const { descriptor } = parse(readFileSync(filename, 'utf8'))
const script = compileScript(descriptor, { id: 'mcp-server-settings-test' }).content
  .replace(/__expose\([^;]*\);/g, '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

const flush = async () => {
  await new Promise<void>(resolve => setImmediate(resolve))
  await vue.nextTick()
}

async function fixture() {
  const calls = { knowledgeBases: 0, sharedKnowledgeBases: 0, agents: 0 }
  const resources = vue.reactive({
    rawKnowledgeBases: [] as { id: string; name: string }[],
    agents: [] as { id: string; name: string }[],
    async ensureKnowledgeBases() {
      calls.knowledgeBases++
      resources.rawKnowledgeBases = [{ id: 'own-kb', name: 'Own knowledge base' }]
    },
    async ensureAgents() {
      calls.agents++
      resources.agents = [{ id: 'agent', name: 'Default agent' }]
    },
  })
  const organization = vue.reactive({
    error: null,
    sharedKnowledgeBases: [] as { knowledge_base: { id: string; name: string } }[],
    async fetchSharedKnowledgeBases() {
      calls.sharedKnowledgeBases++
      organization.sharedKnowledgeBases = [
        { knowledge_base: { id: 'shared-kb', name: 'Shared knowledge base' } },
      ]
    },
  })
  const endpoint = {
    id: 'endpoint', name: 'Docs', description: '', enabled: true,
    knowledge_base_ids: ['shared-kb'], tools: ['ask'], default_agent_id: 'agent',
    rate_limit_per_minute: 60,
  }
  const dependencies: Record<string, unknown> = {
    vue,
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { error: assert.fail } },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/utils/clipboard': { copyWithToast: assert.fail },
    '@/utils/mcpBaseUrl': { resolveMcpBaseUrl: () => '' },
    '@/composables/useApiBaseUrlDisplay': { useApiBaseUrlDisplay: () => ({ apiBaseUrlDisplay: vue.ref('https://example.com') }) },
    '@/components/settings/SettingDrawer.vue': { default: {} },
    '@/stores/chatResources': { useChatResourcesStore: () => resources },
    '@/stores/organization': { useOrganizationStore: () => organization },
    '@/api/mcp-endpoint': {
      listMcpEndpoints: async () => ({ data: [endpoint] }),
      getMcpEndpointToolCatalog: async () => ({ data: {
        groups: ['chat'], tools: [{ name: 'ask', group: 'chat', destructive: false }], default_tools: ['ask'],
      } }),
    },
    './mcpServerIntegration': integration,
  }
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      assert.ok(name in dependencies, `Unexpected dependency: ${name}`)
      return dependencies[name]
    },
  })
  const component = exports.default
  component.render = () => null
  const renderer = vue.createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp(component)
  const state: any = app.mount({})
  await flush()
  return { state, endpoint, calls, close: () => app.unmount() }
}

test('connection settings -> configuration loads named own/shared KBs and agents', async t => {
  const f = await fixture()
  t.after(f.close)
  f.state.handleMenuClick({ value: 'connect' }, f.endpoint)
  assert.equal(f.state.wizardStep, 1)
  assert.deepEqual(f.calls, { knowledgeBases: 0, sharedKnowledgeBases: 0, agents: 0 })

  // Both the Back button and the configuration step use this handler.
  f.state.goToWizardStep(0)
  assert.equal(f.state.wizardStep, 0)
  assert.equal(f.state.kbLoading, true)
  await flush()

  assert.deepEqual(f.calls, { knowledgeBases: 1, sharedKnowledgeBases: 1, agents: 1 })
  assert.deepEqual(Array.from(f.state.kbOptions, (option: unknown) => ({ ...option as object })), [
    { value: 'own-kb', label: 'Own knowledge base' },
    { value: 'shared-kb', label: 'Shared knowledge base' },
  ])
  assert.equal(f.state.agentOptions[0].label, 'Default agent')
  assert.deepEqual([...f.state.form.knowledge_base_ids], ['shared-kb'])
  assert.equal(f.state.kbLoading, false)

  f.state.goToWizardStep(0)
  await flush()
  assert.deepEqual(f.calls, { knowledgeBases: 1, sharedKnowledgeBases: 1, agents: 1 },
    'clicking the active configuration step must not reload options')
})

for (const mode of ['create', 'edit'] as const) {
  test(`direct ${mode} still loads configuration options`, async t => {
    const f = await fixture()
    t.after(f.close)
    if (mode === 'create') f.state.openCreate()
    else f.state.openEdit(f.endpoint)
    await flush()
    assert.equal(f.state.wizardStep, 0)
    assert.deepEqual(f.calls, { knowledgeBases: 1, sharedKnowledgeBases: 1, agents: 1 })
    assert.equal(f.state.kbOptions.length, 2)
    assert.deepEqual([...f.state.form.knowledge_base_ids], mode === 'create' ? [] : ['shared-kb'])
  })
}

test('returning from connection settings preserves unsaved configuration', async t => {
  const f = await fixture()
  t.after(f.close)
  f.state.openEdit(f.endpoint)
  await flush()
  f.state.form.name = 'Edited name'
  f.state.form.knowledge_base_ids = ['own-kb', 'shared-kb']
  f.state.form.default_agent_id = ''

  f.state.goToWizardStep(1)
  f.state.goToWizardStep(0)
  await flush()

  assert.equal(f.state.form.name, 'Edited name')
  assert.deepEqual([...f.state.form.knowledge_base_ids], ['own-kb', 'shared-kb'])
  assert.equal(f.state.form.default_agent_id, '')
  assert.deepEqual([...f.endpoint.knowledge_base_ids], ['shared-kb'])
  assert.deepEqual(f.calls, { knowledgeBases: 2, sharedKnowledgeBases: 2, agents: 2 },
    'returning to configuration must reload options even after an earlier load')
})
