import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'
import {
  buildClaudeCodeCommand,
  buildHttpClientSnippet,
  buildMcpEndpointUrl,
  buildStdioBridgeSnippet,
  buildVsCodeClientSnippet,
  groupTools,
} from './mcpServerIntegration.ts'

const endpoint = {
  id: 'ep-1',
  tenant_id: 1,
  name: 'Docs',
  description: '',
  enabled: true,
  token_hint: 'mcp_…',
  knowledge_base_ids: ['kb-1'],
  tools: [] as string[],
  default_agent_id: '',
  rate_limit_per_minute: 60,
  path: '/mcp/ep-1',
  created_at: '2026-11-19T00:00:00Z',
  updated_at: '2026-11-19T00:00:00Z',
}

function setup(t: TestContext) {
  const f = fixture(t)
  const chatResources = vue.reactive({
    rawKnowledgeBases: [] as Array<{ id: string; name: string }>,
    agents: [] as Array<{ id: string; name: string }>,
    async ensureKnowledgeBases() {
      chatResources.rawKnowledgeBases = [
        { id: 'kb-1', name: 'Alpha KB' },
        { id: 'kb-2', name: 'Beta KB' },
      ]
    },
    async ensureAgents() {
      chatResources.agents = [{ id: 'agent-1', name: 'Agent One' }]
    },
  })
  const orgStore = vue.reactive({
    sharedKnowledgeBases: [] as Array<{ knowledge_base: { id: string; name: string } }>,
    error: null as unknown,
    async fetchSharedKnowledgeBases() {
      orgStore.sharedKnowledgeBases = [{ knowledge_base: { id: 'kb-3', name: 'Shared KB' } }]
    },
  })
  const component = f.component(new URL('./McpServerIntegrationSettings.vue', import.meta.url), {
    vue: { ...vue, onMounted() {} },
    'vue-router': { useRouter: () => ({}) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': {
      MessagePlugin: { error() {}, success() {}, warning() {} },
    },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/utils/clipboard': { copyWithToast: async () => true },
    '@/composables/useApiBaseUrlDisplay': {
      useApiBaseUrlDisplay: () => ({ apiBaseUrlDisplay: vue.ref('https://kb.example.com/api/v1') }),
    },
    '@/stores/chatResources': { useChatResourcesStore: () => chatResources },
    '@/stores/organization': { useOrganizationStore: () => orgStore },
    '@/api/mcp-endpoint': {
      listMcpEndpoints: async () => ({ data: [] }),
      getMcpEndpointToolCatalog: async () => ({ data: { groups: [], tools: [], default_tools: [] } }),
      createMcpEndpoint: async () => ({ data: null }),
      updateMcpEndpoint: async () => ({ data: null }),
      deleteMcpEndpoint: async () => ({ data: null }),
      rotateMcpEndpointToken: async () => ({ data: null }),
    },
    './mcpServerIntegration': {
      buildClaudeCodeCommand,
      buildHttpClientSnippet,
      buildMcpEndpointUrl,
      buildStdioBridgeSnippet,
      buildVsCodeClientSnippet,
      groupTools,
    },
  })
  return { ...component, chatResources, orgStore }
}

test('returning to the config step from connection config loads the KB candidates', async t => {
  const { vm } = setup(t)
  // "连接配置" entry point: openEdit(ep, 1) lands on the connection step.
  vm.openEdit(endpoint, 1)
  await flush()
  await flush()
  assert.equal(vm.wizardStep, 1)
  assert.equal(vm.kbOptions.length, 0, 'nothing loaded yet on the connection step')

  // "上一步" / step-header click back to the config step.
  vm.goToWizardStep(0)
  await flush()
  await flush()
  assert.equal(vm.wizardStep, 0)
  assert.deepEqual(
    [...vm.kbOptions.map((option: { label: string; value: string }) => option.label)],
    ['Alpha KB', 'Beta KB', 'Shared KB'],
    'selected KBs must resolve to names and the dropdown must offer candidates',
  )
  assert.deepEqual(
    [...vm.kbOptions.map((option: { label: string; value: string }) => option.value)],
    ['kb-1', 'kb-2', 'kb-3'],
  )
  assert.deepEqual([...vm.form.knowledge_base_ids], ['kb-1'], 'existing scope selection is preserved')
  assert.equal(vm.kbLoading, false)
})

test('opening the config step directly still loads the KB candidates (control)', async t => {
  const { vm } = setup(t)
  vm.openEdit(endpoint)
  await flush()
  await flush()
  assert.equal(vm.wizardStep, 0)
  assert.deepEqual(
    [...vm.kbOptions.map((option: { label: string; value: string }) => option.label)],
    ['Alpha KB', 'Beta KB', 'Shared KB'],
  )
})

test('going back to the config step twice does not duplicate KB candidates', async t => {
  const { vm } = setup(t)
  vm.openEdit(endpoint, 1)
  await flush()
  vm.goToWizardStep(0)
  await flush()
  await flush()
  vm.goToWizardStep(1)
  vm.goToWizardStep(0)
  await flush()
  await flush()
  assert.equal(vm.kbOptions.length, 3)
  assert.deepEqual(
    [...vm.kbOptions.map((option: { label: string; value: string }) => option.value)],
    ['kb-1', 'kb-2', 'kb-3'],
  )
})
