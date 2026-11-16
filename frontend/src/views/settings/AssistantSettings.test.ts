import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import * as pinia from 'pinia'
import ts from 'typescript'
import type { AssistantConfig } from '@/api/assistant'

const source = readFileSync(new URL('./AssistantSettings.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'assistant-settings-test' }).content
const storeScript = readFileSync(new URL('../../stores/assistant.ts', import.meta.url), 'utf8')
const apiScript = readFileSync(new URL('../../api/assistant.ts', import.meta.url), 'utf8')

function evaluate(source: string, dependencies: Record<string, unknown>, globals = {}) {
  const exports: any = {}
  runInNewContext(ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText, {
    exports, console, ...globals,
    require(name: string) {
      assert.ok(name in dependencies, `Unexpected dependency: ${name}`)
      return dependencies[name]
    },
  })
  return exports
}

const flush = async () => {
  await new Promise(resolve => setImmediate(resolve))
  await vue.nextTick()
}

async function fixture() {
  const auth = vue.reactive({
    user: { id: 'user-a' }, effectiveTenantId: 1,
    canAccessAllTenants: false, hasRole: () => true,
  })
  const writes: AssistantConfig[] = []
  const notifications: string[] = []
  let resolveSave!: (value: { data: AssistantConfig }) => void
  let rejectSave!: (error: Error) => void
  const oldConfig: AssistantConfig = { display_name: 'A', max_access_level: 'auto_write' }
  const newConfig: AssistantConfig = { display_name: 'B', max_access_level: 'read_only' }
  const api = evaluate(apiScript, {
    '@/utils/request': {
      get: async () => ({ data: auth.user.id === 'user-a' && auth.effectiveTenantId === 1 ? oldConfig : newConfig }),
      put: (_url: string, body: AssistantConfig) => {
        writes.push(JSON.parse(JSON.stringify(body)))
        return new Promise((resolve, reject) => { resolveSave = resolve; rejectSave = reject })
      },
    },
  })
  const storage = { getItem: () => null, setItem() {}, removeItem() {} }
  const stores = evaluate(storeScript, {
    vue, pinia, './auth': { useAuthStore: () => auth }, '@/api/assistant': api,
  }, { localStorage: storage, sessionStorage: storage })
  const root = pinia.createPinia()
  pinia.setActivePinia(root)
  const assistant = stores.useAssistantStore()
  let mounted!: () => void
  const component = evaluate(script, {
    vue: { ...vue, onMounted: (callback: () => void) => { mounted = callback } },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: {
      success: () => notifications.push('success'), error: () => notifications.push('error'),
    } },
    '@/stores/auth': { useAuthStore: () => auth },
    '@/stores/assistant': stores,
    '@/api/assistant': api,
  }).default
  const scope = vue.effectScope()
  const state = scope.run(() => component.setup({}, { expose() {} })) as {
    draft: AssistantConfig
    save: () => Promise<void>
  }
  mounted()
  await flush()
  return {
    auth, assistant, state, writes, notifications, newConfig,
    resolveSave: (config: AssistantConfig) => resolveSave({ data: config }),
    rejectSave: () => rejectSave(new Error('old workspace failed')),
    close: () => { scope.stop(); pinia.disposePinia(root) },
  }
}

for (const identity of ['workspace', 'user']) {
  for (const outcome of ['success', 'failure']) {
    test(`a save ${outcome} from the previous ${identity} does not affect the current settings`, async () => {
      const f = await fixture()
      try {
        f.state.draft.display_name = 'A edited'
        const pending = f.state.save()
        assert.equal(f.writes.length, 1)
        if (identity === 'workspace') f.auth.effectiveTenantId = 2
        else f.auth.user = { id: 'user-b' }
        await flush()
        assert.equal(f.assistant.displayName, 'B')
        f.state.draft.display_name = 'B unsaved'
        if (outcome === 'success') f.resolveSave(f.writes[0])
        else f.rejectSave()
        await pending
        await flush()
        assert.equal(f.assistant.displayName, 'B')
        assert.equal(f.assistant.accessCeiling, 'read_only')
        assert.equal(f.state.draft.display_name, 'B unsaved', 'an old response must not reset current edits')
        assert.equal(f.state.draft.max_access_level, 'read_only')
        assert.deepEqual(f.notifications, [])
        const next = f.state.save()
        assert.deepEqual(f.writes[1], { display_name: 'B unsaved', max_access_level: 'read_only' })
        f.resolveSave(f.writes[1])
        await next
      } finally { f.close() }
    })
  }
}

test('a save in the current workspace updates the store and draft', async () => {
  const f = await fixture()
  try {
    f.state.draft.display_name = ' A edited '
    const pending = f.state.save()
    f.resolveSave(f.writes[0])
    await pending
    await flush()
    assert.equal(f.assistant.displayName, 'A edited')
    assert.equal(f.state.draft.display_name, 'A edited')
    assert.deepEqual(f.notifications, ['success'])
  } finally { f.close() }
})
