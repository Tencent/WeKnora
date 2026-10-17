import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import ts from 'typescript'
import * as pageLink from '../../../utils/assistantPageLink'

const { descriptor } = parse(readFileSync(new URL('./OpenPageCard.vue', import.meta.url), 'utf8'))
const script = compileScript(descriptor, { id: 'open-page-card-test' }).content

function setup(link: unknown) {
  const pushed: unknown[] = []
  const drafts: unknown[] = []
  const exports: any = {}
  runInNewContext(ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS },
  }).outputText, {
    exports,
    require: (name: string) => ({
      vue,
      'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
      'vue-router': { useRouter: () => ({ push: async (to: unknown) => { pushed.push(to) } }) },
      '@/stores/assistant': { useAssistantStore: () => ({ setPageDraft: (d: unknown) => drafts.push(d) }) },
      '@/utils/assistantPageLink': pageLink,
    } as Record<string, unknown>)[name],
  })
  const state = exports.default.setup({ link }, { expose() {} })
  return { state, pushed, drafts }
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
