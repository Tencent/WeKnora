import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import vm from 'node:vm'
import { after, before, test } from 'node:test'
import { createPinia } from 'pinia'
import { createRenderer, defineComponent, h, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { createMemoryHistory, createRouter, isNavigationFailure, NavigationFailureType, onBeforeRouteUpdate, RouterView, useRoute } from 'vue-router'
import { createServer, type ViteDevServer } from 'vite'
import { parse } from '@vue/compiler-sfc'
import ts from 'typescript'

const input = parse(readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')).descriptor.scriptSetup!.content
const inputSource = ts.createSourceFile('input.ts', input, ts.ScriptTarget.ES2022, true)
const mount = inputSource.statements.find(node => ts.isExpressionStatement(node)
  && ts.isCallExpression(node.expression) && node.expression.expression.getText(inputSource) === 'onMounted')!
const routeUpdate = inputSource.statements.find(node => ts.isExpressionStatement(node)
  && ts.isCallExpression(node.expression) && node.expression.expression.getText(inputSource) === 'onBeforeRouteUpdate')!
const focus = inputSource.statements.find(node => ts.isVariableStatement(node)
  && node.declarationList.declarations.some(item => item.name.getText(inputSource) === 'focusInput'))!
const startSource = ts.createSourceFile('start.ts', readFileSync(new URL('./GlobalCommandPalette/useStartChat.ts', import.meta.url), 'utf8'), ts.ScriptTarget.ES2022, true)
const start = startSource.statements.find(node => ts.isFunctionDeclaration(node) && node.name?.text === 'useStartChat')!
const routerSource = readFileSync(new URL('../router/index.ts', import.meta.url), 'utf8')
const referenceGuard = routerSource.slice(routerSource.indexOf('router.beforeResolve('), routerSource.indexOf('router.afterEach('))
const execute = (code: string, context: Record<string, unknown>) => vm.runInNewContext(ts.transpileModule(code, {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText, context)

let server: ViteDevServer
let useSettingsStore: typeof import('../stores/settings').useSettingsStore
let useMenuStore: typeof import('../stores/menu').useMenuStore
const savedStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
const storage = new Map<string, string>()

before(async () => {
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
    removeItem: (key: string) => storage.delete(key),
  } })
  server = await createServer({ configFile: false,
    plugins: [{ name: 'offline-prefill-test', enforce: 'pre',
      resolveId(id) {
        if (id.endsWith('/utils/request')) return '\0offline-request'
        if (id.endsWith('/i18n')) return '\0offline-i18n'
      },
      load(id) {
        if (id === '\0offline-request') return 'const request = () => { throw new Error("Unexpected network request") }; export { request as get, request as post, request as put, request as del };'
        if (id === '\0offline-i18n') return 'import {ref} from "vue"; export default {global:{locale:ref("en-US"),t:key=>key}}'
      },
    }],
    resolve: { alias: { '@': fileURLToPath(new URL('../', import.meta.url)) } },
    optimizeDeps: { noDiscovery: true, entries: [] },
    server: { middlewareMode: true, hmr: false }, appType: 'custom',
  })
  ;({ useSettingsStore } = await server.ssrLoadModule('/src/stores/settings.ts'))
  ;({ useMenuStore } = await server.ssrLoadModule('/src/stores/menu.ts'))
})

after(async () => {
  await server?.close()
  if (savedStorage) Object.defineProperty(globalThis, 'localStorage', savedStorage)
  else Reflect.deleteProperty(globalThis, 'localStorage')
})

type Host = { parent: Host | null; children: Host[] }
const node = (): Host => ({ parent: null, children: [] })
const renderer = createRenderer<Host, Host>({
  createElement: node, createText: node, createComment: node,
  insert(child, parent, anchor) {
    if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1)
    child.parent = parent
    const index = anchor ? parent.children.indexOf(anchor) : -1
    if (index < 0) parent.children.push(child)
    else parent.children.splice(index, 0, child)
  },
  remove(child) {
    if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1)
    child.parent = null
  },
  parentNode: el => el.parent, nextSibling: el => el.parent?.children[el.parent.children.indexOf(el) + 1] || null,
  setText() {}, setElementText() {}, patchProp() {},
})
const flush = async () => { await new Promise(resolve => setImmediate(resolve)); await nextTick(); await nextTick() }

async function fixture(path = '/platform/creatChat', initial = '') {
  storage.clear()
  const pinia = createPinia()
  const menu = useMenuStore(pinia)
  const settings = useSettingsStore(pinia)
  menu.setPrefillQuery(initial)
  const instances: Array<{ embedded: boolean; query: ReturnType<typeof ref<string>>; focuses: number; connected: boolean }> = []
  const Composer = defineComponent({
    props: { embedded: Boolean },
    setup(props) {
      const state = { embedded: props.embedded, query: ref('Existing draft'), focuses: 0, connected: true }
      instances.push(state)
      const noop = () => {}
      execute(`${focus.getText(inputSource)}\n${mount.getText(inputSource)}\n${routeUpdate.getText(inputSource)}`, {
        props: { embeddedMode: props.embedded, autoFocus: false }, route: useRoute(), query: state.query,
        menuStore: menu, settingsStore: settings, selectedKbIds: { value: settings.getSelectedKnowledgeBases() },
        onMounted, watch, nextTick, onBeforeRouteUpdate,
        requestAnimationFrame: (callback: () => void) => { void nextTick(callback); return 0 },
        getTextareaEl: () => ({ isConnected: state.connected, focus() { state.focuses++ } }),
        clearvalue: () => { state.query.value = '' }, clearPendingUploads: noop,
        window: { addEventListener: noop }, document: { addEventListener: noop },
        browserConnection: { watchStatus: noop }, initChatModelSelection: noop,
        loadKnowledgeBases: noop, loadWebSearchConfig: noop, loadChatModels: noop, loadAgents: noop, loadMCPServices: noop,
        onVoiceKeydown: noop, onVoiceKeyup: noop, onVoiceWindowBlur: noop, onVoiceVisibilityChange: noop,
        CHAT_FILE_DROP_EVENT: '', handleChatFileDrop: noop, fileIdToKbId: ref({}),
        closeAgentModeSelector: noop, closeModelSelector: noop, closeMentionSelector: noop,
        showModelSelector: ref(false), showAgentModeSelector: ref(false), resizeHandler: null, scrollHandler: null,
      })
      onUnmounted(() => { state.connected = false })
      return () => h('textarea')
    },
  })
  const ChatPage = defineComponent({ render: () => h('div', [h(Composer), h(Composer, { embedded: true })]) })
  const ExistingChatPage = defineComponent({ render: () => h('div', [h(Composer), h(Composer, { embedded: true })]) })
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/platform/creatChat', name: 'globalCreatChat', component: ChatPage },
    { path: '/platform/knowledge-bases/:kbId/creatChat', name: 'kbCreatChat', component: ChatPage },
    { path: '/platform/chat/:id', component: ExistingChatPage },
    { path: '/other', component: { render: () => h('div') } },
  ] })
  execute(referenceGuard, { router, useSettingsStore: () => settings })
  await router.push(path)
  const app = renderer.createApp({ render: () => h(RouterView) })
  app.use(pinia).use(router).mount(node())
  await flush()
  const { startChat } = execute(`${start.getText(startSource).replace(/^export /, '')}\nuseStartChat()`, {
    useRouter: () => router, useMenuStore: () => menu, useSettingsStore: () => settings,
    isNavigationFailure, NavigationFailureType,
  }) as { startChat(query: string, kbIds?: string[], fileIds?: string[]): void }
  return { router, menu, settings, instances, startChat, dispose: () => app.unmount(),
    main: () => instances.slice().reverse().find(item => !item.embedded && item.connected)!,
    embedded: () => instances.slice().reverse().find(item => item.embedded && item.connected)!,
  }
}

test('mount consumes the queued search once and leaves the embedded composer alone', async t => {
  const f = await fixture('/platform/creatChat', 'Initial search')
  t.after(f.dispose)
  assert.equal(f.main().query.value, 'Initial search')
  assert.equal(f.main().focuses, 1)
  assert.equal(f.menu.prefillQuery, '')
  assert.equal(f.embedded().query.value, 'Existing draft')
  assert.equal(f.embedded().focuses, 0)
})

for (const path of ['/platform/creatChat', '/platform/creatChat?cmdk=travel', '/platform/knowledge-bases/old-kb/creatChat', '/platform/chat/old-session']) {
  test(`search fills the destination from ${path} without sending or retaining old references`, async t => {
    const f = await fixture(path)
    t.after(f.dispose)
    f.settings.selectKnowledgeBases(['old-kb'])
    f.settings.addFile('old-file')
    f.settings.addTag({ id: 'old-tag', name: 'Old tag', kbId: 'old-kb' })
    f.startChat('  New search question  ', ['search-kb'], ['search-file'])
    await flush()
    assert.equal(f.router.currentRoute.value.fullPath, '/platform/creatChat')
    assert.equal(f.main().query.value, 'New search question')
    assert.equal(f.menu.prefillQuery, '')
    assert.deepEqual(f.settings.getSelectedKnowledgeBases(), ['search-kb'])
    assert.deepEqual(f.settings.getSelectedFiles(), ['search-file'])
    assert.deepEqual(f.settings.settings.selectedTags, [])
    assert.equal(f.menu.firstQuery, '', 'prefilling must not queue an automatic send')
    assert.equal(f.embedded().query.value, 'Existing draft')
  })
}

test('successive searches, including identical text, replace the draft; empty input leaves it alone', async t => {
  const f = await fixture()
  t.after(f.dispose)
  for (const text of ['First search', 'Second search', 'Second search']) {
    f.main().query.value = 'Edited draft'
    f.startChat(text)
    await flush()
    assert.equal(f.main().query.value, text)
    assert.equal(f.menu.prefillQuery, '')
  }
  const focused = f.main().focuses
  f.startChat('  ')
  await flush()
  assert.equal(f.main().query.value, 'Second search')
  assert.equal(f.main().focuses, focused)
})

test('cancelled navigation keeps the current draft and permits a later search retry', async t => {
  const f = await fixture('/platform/chat/existing')
  t.after(f.dispose)
  const removeGuard = f.router.beforeEach(() => false)
  f.startChat('Cancelled search')
  await flush()
  assert.equal(f.router.currentRoute.value.path, '/platform/chat/existing')
  assert.equal(f.main().query.value, 'Existing draft')
  assert.equal(f.menu.prefillQuery, '')
  removeGuard()
  f.startChat('Retried search')
  await flush()
  assert.equal(f.main().query.value, 'Retried search')
})

test('a superseded search cannot consume the latest navigation references', async t => {
  const f = await fixture('/platform/chat/existing')
  t.after(f.dispose)
  let release!: () => void
  let entered!: () => void
  const started = new Promise<void>(resolve => { entered = resolve })
  let attempts = 0
  f.router.beforeEach(to => {
    if (to.path === '/platform/creatChat' && ++attempts === 1) {
      entered()
      return new Promise<void>(resolve => { release = resolve })
    }
  })
  f.startChat('First search', ['first-kb'], ['first-file'])
  await started
  f.startChat('Latest search', ['latest-kb'], ['latest-file'])
  release()
  await flush()
  assert.equal(f.main().query.value, 'Latest search')
  assert.deepEqual(f.settings.getSelectedKnowledgeBases(), ['latest-kb'])
  assert.deepEqual(f.settings.getSelectedFiles(), ['latest-file'])
})

test('a redirected search does not leave text for an unrelated later new chat', async t => {
  const f = await fixture('/platform/chat/existing')
  t.after(f.dispose)
  const removeGuard = f.router.beforeEach(to => to.path === '/platform/creatChat' ? '/other' : undefined)
  f.startChat('Redirected search')
  await flush()
  assert.equal(f.router.currentRoute.value.path, '/other')
  assert.equal(f.menu.prefillQuery, '')
  removeGuard()
  await f.router.push('/platform/creatChat')
  await flush()
  assert.equal(f.main().query.value, 'Existing draft')
})

test('unmount stops prefill observation so the destination mount consumes the text', async t => {
  const f = await fixture()
  t.after(f.dispose)
  const old = f.main()
  await f.router.push('/other')
  f.menu.setPrefillQuery('Pending destination')
  await flush()
  assert.equal(old.query.value, 'Existing draft')
  assert.equal(f.menu.prefillQuery, 'Pending destination')
  await f.router.push('/platform/creatChat')
  await flush()
  assert.equal(f.main().query.value, 'Pending destination')
  assert.equal(f.menu.prefillQuery, '')
})
