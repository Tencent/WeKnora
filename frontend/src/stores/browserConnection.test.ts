import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import { createPinia } from 'pinia'

let server: ViteDevServer
let useBrowserConnectionStore: typeof import('./browserConnection').useBrowserConnectionStore

before(async () => {
  server = await createServer({
    configFile: false,
    plugins: [{
      name: 'browser-connection-store-test',
      enforce: 'pre',
      resolveId(id) { if (id.endsWith('/utils/request')) return '\0offline-request' },
      load(id) {
        if (id === '\0offline-request') {
          return 'const request = () => { throw new Error("Unexpected network request") }; export { request as get, request as post, request as put, request as del };'
        }
      },
    }],
    optimizeDeps: { noDiscovery: true, entries: [] },
    resolve: { alias: { '@': fileURLToPath(new URL('../', import.meta.url)) } },
    server: { middlewareMode: true, hmr: false },
    appType: 'custom',
  })
  ;({ useBrowserConnectionStore } = await server.ssrLoadModule('/src/stores/browserConnection.ts'))
})
after(async () => { await server?.close() })

test('composer keeps the browser source available until account status loads', () => {
  const store = useBrowserConnectionStore(createPinia())
  assert.equal(store.loaded, false)
  assert.equal(store.online, false)
  assert.equal(store.knownOffline, false)
})

test('an offline or unpaired extension cannot stay selected as a live source', () => {
  const store = useBrowserConnectionStore(createPinia())
  store.apply({
    enabled: true,
    connected: false,
    extension_available: true,
    device: { id: 'chrome', label: 'Chrome', last_seen_at: '' },
  })
  assert.equal(store.loaded, true)
  assert.equal(store.online, false)
  assert.equal(store.knownOffline, true)

  store.apply({ enabled: true, connected: false, extension_available: true })
  assert.equal(store.knownOffline, true)

  store.apply({ enabled: false, connected: false, extension_available: false })
  assert.equal(store.knownOffline, true)

  store.apply({
    enabled: true,
    connected: true,
    extension_available: true,
    device: { id: 'chrome', label: 'Chrome', last_seen_at: '' },
  })
  assert.equal(store.online, true)
  assert.equal(store.knownOffline, false)
})
