import assert from 'node:assert/strict'
import test from 'node:test'
import { reactive } from 'vue'
import { change, fixture, flush } from '../../test-utils/vueFixture.ts'

test('assistant provider changes refresh settings and the shared picker snapshot', async t => {
  const f = fixture(t)
  let saved = [{ id: 'search', name: 'Old', provider: 'duckduckgo', is_default: false }]
  const resources = reactive({
    webSearchProviders: saved,
    ensureWebSearchProviders: t.mock.fn(async (_force: boolean) => { resources.webSearchProviders = saved }),
  })
  const { vm, close } = f.component(new URL('./WebSearchSettings.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { error: assert.fail } },
    'tdesign-icons-vue-next': {},
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/chatResources': { useChatResourcesStore: () => resources },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => ({}) },
    './providerLogos': { providerLogo: () => '' },
    '@/api/web-search-provider': { listWebSearchProviderTypes: async () => [] },
  })
  await flush()
  assert.equal(vm.providerEntities[0].name, 'Old')
  saved = [{ id: 'search', name: 'Team search', provider: 'duckduckgo', is_default: true }]
  f.emit(change('web_search_provider.update'))
  f.emit(change('web_search_provider.update'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(resources.ensureWebSearchProviders.mock.callCount(), 2)
  assert.equal(vm.providerEntities[0].name, 'Team search')
  assert.equal(vm.providerEntities[0].is_default, true)
  close()
  f.emit(change('web_search_provider.delete'))
  t.mock.timers.tick(300)
  assert.equal(resources.ensureWebSearchProviders.mock.callCount(), 2)
})
