import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import ts from 'typescript'
import * as pageLink from '../../../utils/assistantPageLink'

const { descriptor } = parse(readFileSync(new URL('./OpenPageCard.vue', import.meta.url), 'utf8'))
const script = compileScript(descriptor, { id: 'open-page-card-test' }).content

function setup(link: unknown, initialPath = '/platform/knowledge-bases') {
  const pushed: unknown[] = []
  const drafts: unknown[] = []
  const exports: any = {}
  const resolver = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: {} }] })
  const currentRoute = vue.shallowRef(resolver.resolve(initialPath))
  const router = {
    currentRoute,
    resolve: resolver.resolve,
    push: async (to: any) => {
      pushed.push(to)
      currentRoute.value = resolver.resolve(to)
    },
  }
  runInNewContext(ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS },
  }).outputText, {
    exports,
    require: (name: string) => ({
      vue,
      'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
      'vue-router': { useRouter: () => router },
      '@/stores/assistant': { useAssistantStore: () => ({ setPageDraft: (d: unknown) => drafts.push(d) }) },
      '@/utils/assistantPageLink': pageLink,
    } as Record<string, unknown>)[name],
  })
  const state = exports.default.setup({ link }, { expose() {} })
  return { state, pushed, drafts, router }
}

test('opening a new-model link hands its form to the page, then navigates', () => {
  const { state, pushed, drafts } = setup({
    display_type: 'open_page', page: 'settings_models', label: 'Models',
    model_draft: { type: 'chat', base_url: 'https://api.example/v1' },
  })
  assert.equal(state.icon.value, 'control-platform')
  assert.equal(state.endpoint.value, 'https://api.example/v1')
  state.open()
  assert.equal(JSON.stringify(drafts), JSON.stringify([{ kind: 'model', draft: { type: 'chat', base_url: 'https://api.example/v1' } }]))
  assert.equal(JSON.stringify(pushed), JSON.stringify([{ path: '/platform/settings', query: { section: 'models' } }]))
})

test('a plain link clears any waiting form', () => {
  const { state, pushed, drafts } = setup({
    display_type: 'open_page', page: 'agent_editor', label: 'Bot', agent_id: 'a', section: 'knowledge',
  })
  state.open()
  assert.deepEqual(drafts, [null], 'a stale draft must not open on another page')
  assert.equal(JSON.stringify(pushed), JSON.stringify([{ path: '/platform/agents', query: { edit: 'a', section: 'knowledge' } }]))
})

test('one action toggles between returning from the assistant-opened page and opening it again', () => {
  const { state, router, drafts } = setup({
    display_type: 'open_page', page: 'settings', section: 'system', label: 'Settings',
    navigated: true, return_path: '/platform/agents',
  }, '/platform/settings?section=system')
  assert.equal(state.actionLabel.value, 'agentStream.openPage.back')
  state.activate()
  assert.equal(router.currentRoute.value.fullPath, '/platform/agents')
  assert.equal(state.actionLabel.value, 'agentStream.openPage.open')
  assert.deepEqual(drafts, [], 'returning must not set a page draft')
  state.activate()
  assert.equal(router.currentRoute.value.fullPath, '/platform/settings?section=system')
  assert.equal(state.actionLabel.value, 'agentStream.openPage.back')
})

test('manual opening remembers the current page and the label follows navigation elsewhere', async () => {
  const { state, router } = setup({ display_type: 'open_page', page: 'settings', section: 'system', label: 'Settings' })
  assert.equal(state.canReturn.value, false)
  state.activate()
  assert.equal(state.canReturn.value, true)
  state.activate()
  assert.equal(router.currentRoute.value.fullPath, '/platform/knowledge-bases')
  await router.push('/platform/settings?section=models')
  assert.equal(state.canReturn.value, false, 'another settings section is not the target')
  state.activate()
  state.activate()
  assert.equal(router.currentRoute.value.fullPath, '/platform/settings?section=models')
})

test('unsafe or identical return destinations do not turn the action into return', () => {
  for (const return_path of ['//evil.example', '/platform/settings?section=system']) {
    const { state } = setup({
      display_type: 'open_page', page: 'settings', section: 'system', label: 'Settings', navigated: true, return_path,
    }, '/platform/settings?section=system')
    assert.equal(state.canReturn.value, false)
  }
})
