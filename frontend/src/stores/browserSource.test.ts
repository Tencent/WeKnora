import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import { createPinia } from 'pinia'

let server: ViteDevServer
let useSettingsStore: typeof import('./settings').useSettingsStore
const savedStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
const items = new Map<string, string>()
before(async () => {
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => items.get(key) ?? null,
    setItem: (key: string, value: string) => items.set(key, value),
    removeItem: (key: string) => items.delete(key),
  } })
  server = await createServer({
    configFile: false,
    plugins: [{ name: 'offline-store-test', enforce: 'pre',
      resolveId(id) { if (id.endsWith('/utils/request')) return '\0offline-request' },
      load(id) { if (id === '\0offline-request') return 'const request = () => { throw new Error("Unexpected network request") }; export { request as get, request as post, request as put, request as del };' },
    }],
    optimizeDeps: { noDiscovery: true, entries: [] },
    resolve: { alias: { '@': fileURLToPath(new URL('../', import.meta.url)) } },
    server: { middlewareMode: true, hmr: false }, appType: 'custom',
  })
  ;({ useSettingsStore } = await server.ssrLoadModule('/src/stores/settings.ts'))
})
after(async () => {
  await server?.close()
  if (savedStorage) Object.defineProperty(globalThis, 'localStorage', savedStorage)
  else Reflect.deleteProperty(globalThis, 'localStorage')
})
test('browser and web search selections are independent and persisted', () => {
  const store = useSettingsStore(createPinia())
  store.toggleWebSearch(true)
  store.toggleLocalBrowser(true)
  assert.equal(store.isLocalBrowserEnabled, true)
  assert.equal(store.settings.webSearchEnabled, true)
  assert.equal(JSON.parse(items.get('WeKnora_settings')!).localBrowserEnabled, true)
  store.toggleWebSearch(false)
  assert.equal(store.isLocalBrowserEnabled, true)
  store.toggleWebSearch(true)
  store.toggleLocalBrowser(true)
  store.toggleLocalBrowser(false)
  assert.equal(store.isLocalBrowserEnabled, false)
  assert.equal(store.settings.webSearchEnabled, true)
})
test('session restore follows saved browser choice and clears it for older sessions', () => {
  const store = useSettingsStore(createPinia())
  store.applyLastRequestState({ local_browser_enabled: true, web_search_enabled: true })
  assert.equal(store.isLocalBrowserEnabled, true)
  assert.equal(store.settings.webSearchEnabled, true)
  store.applyLastRequestState({ web_search_enabled: true })
  assert.equal(store.isLocalBrowserEnabled, false)
  assert.equal(store.settings.webSearchEnabled, true)
})

test('new-session hydration preserves the createChat draft after the first query is consumed', async () => {
  items.clear()
  const store = useSettingsStore(createPinia())
  store.toggleLocalBrowser(true)
  store.toggleWebSearch(true)
  let firstQuery = '用浏览器查一下'
  const preserveDraft = Boolean(firstQuery)
  let finishLoad!: (state: Record<string, boolean>) => void
  const pendingSession = new Promise<Record<string, boolean>>(resolve => { finishLoad = resolve })
  const hydration = pendingSession.then(state => store.hydrateSessionInputState(state, preserveDraft))
  firstQuery = '' // The first send consumes the pending message before GET returns.
  finishLoad({ local_browser_enabled: false, web_search_enabled: false })
  await hydration
  assert.equal(store.isLocalBrowserEnabled, true)
  assert.equal(store.settings.webSearchEnabled, true)
  assert.equal(store._defaultsSnapshot, null)
  // Returning to an existing session still restores its saved choices.
  store.hydrateSessionInputState({ local_browser_enabled: false, web_search_enabled: false })
  assert.equal(store.isLocalBrowserEnabled, false)
  assert.equal(store.settings.webSearchEnabled, false)
  store.restoreDefaultsIfSnapshotted()
  assert.equal(store.isLocalBrowserEnabled, true)
  assert.equal(store.settings.webSearchEnabled, true)
})
