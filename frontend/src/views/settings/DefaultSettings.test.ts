import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { reactive } from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture'
import { isTenantWebSearchReady } from '../../utils/agentWebSearch'
import type { DefaultSettings } from '../../api/default-settings'
import type { WebSearchProviderEntity } from '../../api/web-search-provider'

const initial: DefaultSettings = {
  chat_model_id: 'chat', utility_model_id: 'utility', embedding_model_id: 'embed',
  rerank_model_id: 'rerank', vlm_model_id: 'vision', asr_model_id: 'asr',
}

function setup(t: TestContext, read = async () => ({ data: { ...initial } })) {
  const f = fixture(t)
  const api = {
    getTenantDefaultSettings: t.mock.fn(read),
    updateTenantDefaultSettings: t.mock.fn(async (_settings: DefaultSettings) => ({})),
    patchWebSearchProvider: t.mock.fn(async (_id: string, _patch: { is_default: boolean }) => ({})),
  }
  const store = reactive({
    allModels: [], webSearchProviders: [{ id: 'search', name: 'Search', is_default: false }] as WebSearchProviderEntity[],
    ensureModels: t.mock.fn(async (_force?: boolean) => {}),
    ensureWebSearchProviders: t.mock.fn(async (_force?: boolean) => {}),
  })
  const { vm } = f.component(new URL('./DefaultSettings.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error() {} } },
    '@/api/default-settings': api,
    '@/api/web-search-provider': api,
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/ui': { useUIStore: () => ({}) },
    '@/stores/chatResources': { useChatResourcesStore: () => store },
    '@/stores/deploymentCapabilities': { useDeploymentCapabilitiesStore: () => ({ isSupported: () => true }) },
  })
  return { vm, store, api }
}

test('blocks saves until all persisted slots have loaded', async t => {
  let resolve!: (value: { data: DefaultSettings }) => void
  const { vm, api } = setup(t, () => new Promise(done => { resolve = done }))
  assert.equal(vm.settingsLoaded, false)
  await vm.onModelChange('chat_model_id', 'new-chat')
  assert.equal(api.updateTenantDefaultSettings.mock.callCount(), 0)
  resolve({ data: initial })
  await flush()
  assert.equal(vm.settingsLoaded, true)
  await vm.onModelChange('chat_model_id', 'new-chat')
  const saved = api.updateTenantDefaultSettings.mock.calls[0].arguments[0]
  assert.deepEqual({ ...saved }, { ...initial, chat_model_id: 'new-chat' })
})

test('a failed initial load stays read-only until a successful retry', async t => {
  const { vm, api } = setup(t, async () => { throw new Error('offline') })
  await flush()
  assert.equal(vm.settingsLoadFailed, true)
  await vm.onModelChange('chat_model_id', 'new-chat')
  assert.equal(api.updateTenantDefaultSettings.mock.callCount(), 0)
  api.getTenantDefaultSettings.mock.mockImplementation(async () => ({ data: initial }))
  await vm.loadSettings()
  assert.equal(vm.settingsLoaded, true)
  assert.equal(vm.settingsLoadFailed, false)
  await vm.onModelChange('chat_model_id', 'new-chat')
  assert.equal(api.updateTenantDefaultSettings.mock.callCount(), 1)
})

test('does not roll back a saved setting when model refresh fails', async t => {
  const { vm, api, store } = setup(t)
  await flush()
  store.ensureModels.mock.mockImplementation(async () => { throw new Error('offline') })
  await vm.onModelChange('chat_model_id', 'new-chat')
  assert.equal(api.updateTenantDefaultSettings.mock.callCount(), 1)
  assert.equal(vm.settings.chat_model_id, 'new-chat')
})

test('saving and clearing web search updates the state consumed by chat', async t => {
  const { vm, api, store } = setup(t)
  await flush()
  assert.equal(isTenantWebSearchReady(store.webSearchProviders), false)
  await vm.onWebSearchChange('search')
  assert.equal(isTenantWebSearchReady(store.webSearchProviders), true)
  assert.equal(vm.webSearchDefaultId, 'search')
  await vm.onWebSearchChange('')
  assert.equal(isTenantWebSearchReady(store.webSearchProviders), false)
  assert.equal(api.patchWebSearchProvider.mock.callCount(), 2)
  assert.equal(store.ensureWebSearchProviders.mock.callCount(), 3)
})
