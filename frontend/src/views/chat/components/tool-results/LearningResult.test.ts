import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'
import type { LearningToolData } from '../../../../types/tool-results'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./LearningResult.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'learning-result-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function fixture() {
  const auth = reactive({ isLoggedIn: true, effectiveTenantId: 'tenant', currentUserId: 'user' })
  const settings = reactive({ selectedAgentSourceTenantId: '' })
  const organizations = reactive({ sharedKnowledgeBases: [] as { knowledge_base: { id: string } }[] })
  const props = reactive<{ data: LearningToolData }>({
    data: { display_type: 'learning_quiz', knowledge_base_id: 'kb', quiz_id: 'quiz' },
  })
  let context!: () => { kbId: string; available: boolean; quizId?: string }
  const routes: any[] = []
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-router') return { useRouter: () => ({ push: (route: unknown) => routes.push(route) }) }
      if (name === '@/stores/auth') return { useAuthStore: () => auth }
      if (name === '@/stores/settings') return { useSettingsStore: () => settings }
      if (name === '@/stores/organization') return { useOrganizationStore: () => organizations }
      if (name === '@/composables/useLearning') return {
        useLearning: (value: typeof context) => { context = value; return { state: { error: '' } } },
      }
      if (name.endsWith('/LearningPanel.vue')) return { default: {} }
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance }) })
  app.mount({})
  return { auth, settings, organizations, props, routes, context: () => context(), vm: instance.value, close: () => app.unmount() }
}

test('Agent learning cards block shared contexts, missing KBs and logout reactively', () => {
  const f = fixture()
  try {
    assert.equal(f.context().available, true)
    assert.equal(f.context().quizId, 'quiz')
    f.settings.selectedAgentSourceTenantId = 'foreign'
    assert.equal(f.context().available, false)
    f.settings.selectedAgentSourceTenantId = ''
    f.organizations.sharedKnowledgeBases.push({ knowledge_base: { id: 'kb' } })
    assert.equal(f.context().available, false)
    f.organizations.sharedKnowledgeBases = []
    assert.equal(f.context().available, true)
    f.auth.isLoggedIn = false
    assert.equal(f.context().available, false)
    f.auth.isLoggedIn = true
    f.props.data.knowledge_base_id = ''
    assert.equal(f.context().available, false)
  } finally { f.close() }
})

test('Agent cards scope data and navigation to the current principal and tool reference', async () => {
  const f = fixture()
  try {
    const original = f.vm.scopeKey
    f.auth.currentUserId = 'other-user'
    assert.notEqual(f.vm.scopeKey, original)
    f.auth.effectiveTenantId = 'other-tenant'
    f.props.data = { display_type: 'learning_recommendations', knowledge_base_id: 'other-kb', quiz_id: 'ignored' }
    await nextTick()
    assert.equal(f.context().kbId, 'other-kb')
    assert.equal(f.context().quizId, undefined)
    assert.deepEqual(JSON.parse(f.vm.scopeKey), ['other-tenant', 'other-user', 'other-kb', true])
    f.vm.openPage('concept/topic')
    f.vm.openSource('document')
    assert.deepEqual(JSON.parse(JSON.stringify(f.routes)), [
      { name: 'knowledgeBaseDetail', params: { kbId: 'other-kb' }, query: { tab: 'wiki', slug: 'concept/topic' } },
      { name: 'knowledgeBaseDetail', params: { kbId: 'other-kb' }, query: { tab: 'documents', knowledge_id: 'document' } },
    ])
  } finally { f.close() }
})
