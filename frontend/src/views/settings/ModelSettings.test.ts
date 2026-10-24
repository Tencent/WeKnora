import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { reactive } from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'
import * as contextWindow from '../../utils/contextWindow.ts'
import { matchesResourceQuery } from '../../utils/resourceListSearch.ts'

function modelSettings(t: TestContext, options: { admin?: boolean; fail?: boolean } = {}) {
  const models = [
    { id: 'chat', name: 'Chat', type: 'KnowledgeQA', status: 'active', is_default: true, parameters: {} },
    { id: 'builtin', name: 'Shared', type: 'KnowledgeQA', status: 'active', is_default: false, is_builtin: true, parameters: {} },
    { id: 'embedding', name: 'Embedding', type: 'Embedding', status: 'active', is_default: true, parameters: {} },
    { id: 'downloading', name: 'Downloading', type: 'KnowledgeQA', status: 'downloading', is_default: false, parameters: {} },
  ]
  const api = {
    listModels: async () => models,
    setDefaultModel: t.mock.fn(async (id: string) => {
      if (options.fail) throw new Error('write failed')
      return { ...models.find(model => model.id === id), is_default: true }
    }),
    updateModel: t.mock.fn(),
  }
  const resources = { replaceModels: t.mock.fn() }
  const messages = { success: t.mock.fn(), error: t.mock.fn() }
  const route = reactive({ path: '/settings', query: {}, name: 'settings' })
  const f = fixture(t)
  const component = f.component(new URL('./ModelSettings.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh-CN' } }) },
    'vue-router': { useRoute: () => route, useRouter: () => ({ currentRoute: { value: route } }) },
    'tdesign-vue-next': { MessagePlugin: messages },
    'tdesign-icons-vue-next': {},
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/stores/assistant': { useAssistantStore: () => ({ takePageDraft: () => null }) },
    '@/stores/auth': { useAuthStore: () => ({ isSystemAdmin: false, hasRole: () => options.admin !== false }) },
    '@/stores/ui': { useUIStore: () => ({}) },
    '@/stores/chatResources': { useChatResourcesStore: () => resources },
    '@/stores/modelProviders': { useModelProvidersStore: () => ({
      ensureLoaded: async () => {}, labelFor: () => '', iconFor: () => '',
    }) },
    '@/api/model': api,
    '@/utils/modelCopyName': {},
    '@/config/contextualGuides': {},
    '@/utils/docsUrl': {},
    '@/utils/resourceListSearch': { matchesResourceQuery },
    '@/utils/contextWindow': contextWindow,
  })
  return { ...component, api, resources, messages }
}

test('model management exposes effective defaults and lets workspace admins select builtin models', async t => {
  const { vm, api, resources, messages } = modelSettings(t)
  await flush()
  const chat = vm.allLegacyModels.find((model: any) => model.id === 'chat')
  const builtin = vm.allLegacyModels.find((model: any) => model.id === 'builtin')
  assert.equal(chat.isDefault, true)
  assert.equal(builtin.isDefault, false)
  assert.equal(vm.canEditModel(builtin), false)
  assert.equal(vm.canManageModel(builtin), true)
  assert.deepEqual(Array.from(vm.getModelOptions('chat', builtin), (option: any) => option.value), ['default-chat-builtin'])

  await vm.setDefaultModel(builtin)
  assert.equal(api.setDefaultModel.mock.callCount(), 1)
  assert.equal(api.updateModel.mock.callCount(), 0, 'choosing a builtin must not edit shared configuration')
  assert.equal(vm.allLegacyModels.find((model: any) => model.id === 'chat').isDefault, false)
  assert.equal(vm.allLegacyModels.find((model: any) => model.id === 'builtin').isDefault, true)
  assert.equal(vm.allLegacyModels.find((model: any) => model.id === 'embedding').isDefault, true)
  assert.equal(resources.replaceModels.mock.callCount(), 2, 'refresh every model picker immediately')
  const snapshot = resources.replaceModels.mock.calls[1].arguments[0] as any[]
  assert.equal(snapshot.find(model => model.id === 'builtin').is_default, true)
  assert.equal(messages.success.mock.callCount(), 1)
})

test('inactive models and the current default cannot be selected again', async t => {
  const { vm, api } = modelSettings(t)
  await flush()
  for (const model of vm.allLegacyModels.filter((model: any) => model.isDefault || model.status !== 'active')) {
    assert.equal(vm.canSetDefaultModel(model), false)
    await vm.setDefaultModel(model)
  }
  assert.equal(api.setDefaultModel.mock.callCount(), 0)
})

test('viewers can see defaults but cannot change them', async t => {
  const { vm, api } = modelSettings(t, { admin: false })
  await flush()
  const builtin = vm.allLegacyModels.find((model: any) => model.id === 'builtin')
  assert.equal(vm.allLegacyModels.find((model: any) => model.id === 'chat').isDefault, true)
  assert.equal(vm.canManageModel(builtin), false)
  assert.equal(vm.getModelOptions('chat', builtin).length, 0)
  await vm.setDefaultModel(builtin)
  assert.equal(api.setDefaultModel.mock.callCount(), 0)
})

test('failed default changes preserve the displayed and cached selection', async t => {
  const { vm, resources, messages } = modelSettings(t, { fail: true })
  await flush()
  await vm.setDefaultModel(vm.allLegacyModels.find((model: any) => model.id === 'builtin'))
  assert.equal(vm.allLegacyModels.find((model: any) => model.id === 'chat').isDefault, true)
  assert.equal(resources.replaceModels.mock.callCount(), 1)
  assert.equal(messages.success.mock.callCount(), 0)
  assert.equal(messages.error.mock.calls[0].arguments[0], 'write failed')
  assert.equal(vm.defaultingModelId, null)
})
