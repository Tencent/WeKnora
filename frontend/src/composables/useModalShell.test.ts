import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as vue from 'vue'

const require = createRequire(import.meta.url)
const renderer = vue.createRenderer<any, any>({
  createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
  insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
  parentNode: () => null, nextSibling: () => null,
})

function fixture() {
  const dialogs: any[] = []
  const errors: unknown[][] = []
  const ui = vue.reactive({ showSettingsModal: false })
  const resources = {
    agentTypePresets: [], promptTemplates: null, storageStatus: [], skillCatalog: [],
    parserEngines: [
      { Name: 'builtin', Available: true, FileTypes: ['pdf', 'xlsx'] },
      { Name: 'anydoc', Available: true, FileTypes: ['pdf', 'xlsx'] },
    ],
    async prefetchAgentEditorDeps() {}, async ensureSkills() {}, async ensureSkillCatalog() {},
    async ensureParserEngines(_force = false) {},
  }
  const chat = {
    allModels: [{ id: 'model', type: 'KnowledgeQA', is_default: true, capabilities: { thinking_levels: ['auto', 'high'] } }],
    rawKnowledgeBases: [], webSearchProviders: [], sandboxConfigs: [],
    async ensureModels() {}, async ensureKnowledgeBases() {},
    async ensureWebSearchProviders() {}, async ensureSandboxConfigs() {},
  }
  const modules: Record<string, any> = {
    vue,
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, locale: vue.ref('en') }) },
    'vue-router': { useRouter: () => ({}) },
    pinia: { storeToRefs: vue.toRefs },
    'tdesign-vue-next': {
      DialogPlugin: { confirm: (options: any) => { dialogs.push(options); return { destroy() {} } } },
      MessagePlugin: { success() {}, error() {}, warning() {} },
    },
    '@/stores/ui': { useUIStore: () => ui },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/organization': { useOrganizationStore: () => ({ sharedKnowledgeBases: [] }) },
    '@/stores/chatResources': { useChatResourcesStore: () => chat },
    '@/stores/editorResources': { useEditorResourcesStore: () => resources },
    '@/stores/deploymentCapabilities': {
      useDeploymentCapabilitiesStore: () => ({ isSupported: () => true, async ensureLoaded() {} }),
    },
    '@/api/agent': { listIMChannels: async () => ({ data: [] }) },
    '@/api/embed': { listEmbedChannels: async () => ({ data: [] }) },
    '@/api/skill': {},
    '@/api/system': { isNamedSandboxBackend: () => true },
    '@/utils/clipboard': { copyWithToast() {} },
  }
  function evaluate(code: string) {
    const exports: any = {}
    const compiled = ts.transpileModule(code, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText
    runInNewContext(compiled, {
      exports, setTimeout, clearTimeout,
      console: { ...console, error: (...args: unknown[]) => errors.push(args) },
      window: { addEventListener() {}, removeEventListener() {}, setInterval, clearInterval },
      require(name: string) {
        if (modules[name]) return modules[name]
        if (name.endsWith('.vue')) return { default: {} }
        if (name.startsWith('@/')) return require('../' + name.slice(2))
        return require(name)
      },
    })
    return exports
  }
  modules['@/composables/useModalShell'] = evaluate(readFileSync(new URL('./useModalShell.ts', import.meta.url), 'utf8'))
  function mount(path: string, props: Record<string, any>) {
    const { descriptor } = parse(readFileSync(new URL(path, import.meta.url), 'utf8'))
    const script = compileScript(descriptor, { id: 'dirty-test' }).content
      .replace('__expose();', '')
      .replace('return __returned__', '__expose(__returned__); return __returned__')
    const component = evaluate(script).default
    component.render = () => null
    const instance = vue.ref<any>()
    const app = renderer.createApp({ render: () => vue.h(component, { ...props, ref: instance }) })
    app.mount({})
    return { vm: instance.value, unmount: () => { app.unmount(); assert.deepEqual(errors, []) } }
  }
  return { mount, dialogs, resources, ui }
}

async function settle() {
  // Drain asynchronous resource loading plus the Vue watchers it triggers.
  await new Promise<void>(resolve => setImmediate(resolve))
  await vue.nextTick()
}

for (const mode of ['edit', 'create'] as const) {
  test(`${mode}: initialization stays clean, while real edits require confirmation`, async () => {
    const f = fixture()
    const props = vue.reactive({
      visible: false, mode,
      agent: { id: 'agent', name: 'Original', config: { model_id: 'model', thinking: false } },
      'onUpdate:visible': (value: boolean) => { props.visible = value },
    })
    const { vm, unmount } = f.mount('../views/agent/AgentEditorModal.vue', props)
    try {
      props.visible = true
      await settle()
      assert.equal(vm.editorInitializing, false)
      assert.equal(vm.formData.config.reasoning_effort, 'auto', 'always-on model normalizes the initial off value')
      assert.equal(vm.modalShell.isDirty(), false)
      vm.modalShell.requestClose()
      assert.equal(props.visible, false)
      assert.equal(f.dialogs.length, 0)
      await settle()
      props.visible = true
      await settle()
      const name = vm.formData.name
      vm.formData.name = 'Changed'
      await vue.nextTick()
      vm.modalShell.requestClose()
      assert.equal(f.dialogs.length, 1)
      assert.equal(props.visible, true)
      f.dialogs[0].onClose()
      vm.formData.name = name
      await vue.nextTick()
      assert.equal(vm.modalShell.isDirty(), false, 'reverting an edit restores the clean state')
      vm.formData.config.temperature += 0.1
      vm.modalShell.requestClose()
      assert.equal(f.dialogs.length, 2)
      f.dialogs[1].onConfirm()
      assert.equal(props.visible, false)
    } finally { unmount() }
  })
}

test('closing during initialization does not warn or let a stale load replace the next baseline', async () => {
  const f = fixture()
  let release!: () => void
  const pending = new Promise<void>(resolve => { release = resolve })
  f.resources.ensureSkills = () => pending
  const props = vue.reactive({
    visible: false, mode: 'edit', agent: { id: 'agent', name: 'Original', config: { model_id: 'model' } },
    'onUpdate:visible': (value: boolean) => { props.visible = value },
  })
  const { vm, unmount } = f.mount('../views/agent/AgentEditorModal.vue', props)
  try {
    props.visible = true
    await settle()
    assert.equal(vm.editorInitializing, true)
    vm.modalShell.requestClose()
    assert.equal(props.visible, false)
    assert.equal(f.dialogs.length, 0)
    await settle()
    f.resources.ensureSkills = async () => {}
    props.visible = true
    await settle()
    vm.formData.name = 'Keep this edit'
    release()
    await settle()
    assert.equal(vm.modalShell.isDirty(), true)
  } finally { release(); unmount() }
})

for (const savedRules of [[], [{ file_types: ['xlsx'], engine: 'builtin', xlsx_first_row_as_header: true }]]) {
  test(`parser loading and refresh preserve ${savedRules.length ? 'partial' : 'empty'} saved rules`, async () => {
    const f = fixture()
    let release!: () => void
    f.resources.ensureParserEngines = () => new Promise<void>(resolve => { release = resolve })
    const updates: any[] = []
    const props = vue.reactive({
      parserEngineRules: savedRules,
      'onUpdate:parserEngineRules': (rules: any[]) => { updates.push(rules); props.parserEngineRules = rules },
    })
    const { vm, unmount } = f.mount('../views/knowledge/settings/KBParserSettings.vue', props)
    try {
      release()
      await settle()
      assert.equal(vm.getEngineForGroup(['pdf']), 'anydoc', 'defaults are still displayed')
      assert.equal(updates.length, 0, 'loading must not mutate the parent form')
      f.ui.showSettingsModal = true
      await vue.nextTick()
      f.ui.showSettingsModal = false
      await vue.nextTick()
      release()
      await settle()
      assert.equal(updates.length, 0, 'refreshing available engines is not a user edit')
      vm.handleEngineChange(['pdf'], 'builtin')
      await vue.nextTick()
      assert.equal(updates.length, 1)
      assert.equal(updates[0].find((r: any) => r.file_types.includes('pdf')).engine, 'builtin')
      if (savedRules.length) {
        assert.equal(updates[0].find((r: any) => r.file_types.includes('xlsx')).xlsx_first_row_as_header, true)
      }
      vm.handleXLSXFirstRowAsHeaderChange(['xlsx'], false)
      assert.equal(updates.length, 2)
      assert.equal(updates[1].find((r: any) => r.file_types.includes('xlsx')).xlsx_first_row_as_header, false)
    } finally { unmount() }
  })
}
