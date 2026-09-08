import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8')
const panel = read('./SandboxWorkbench.vue')
const terminal = read('./WorkbenchTerminal.vue')
const chat = read('../../index.vue')
const preview = read('../../../../components/document-preview.vue')

test('scope invalidation includes session, route, user, tenant, logout, close and unmount', () => {
  for (const source of ['props.sessionId', 'route.fullPath', 'auth.user?.id', 'auth.effectiveTenantId', 'auth.isLoggedIn']) assert.ok(panel.includes(source), source)
  assert.match(panel, /flush: 'sync'/)
  assert.match(panel, /function close\(\) \{ dispose\(\); emit\('close'\) \}/)
  assert.match(panel, /onBeforeUnmount\(\(\) => \{\s*dispose\(\)/)
  assert.match(chat, /workbenchRef.value\?\.dispose\(\)/)
  assert.match(chat, /watch\(referencesDrawerVisible, visible => \{ if \(visible\) closeWorkbench\(\)/)
  assert.match(chat, /referencesDrawer.close\(\);\s*workbenchVisible.value = true/)
})

test('terminal output and resizing are bounded and idle input stays disabled', () => {
  assert.match(terminal, /scrollback: 3000/)
  assert.match(terminal, /TERMINAL_OUTPUT_BYTES - writtenBytes/)
  assert.match(terminal, /bytes.subarray\(0, remaining\)/)
  assert.match(terminal, /disableStdin = value !== 'running'/)
  assert.match(terminal, /new ResizeObserver\(fitTerminal\)/)
  assert.match(terminal, /observer\?\.disconnect\(\)/)
  assert.match(terminal, /terminal\?\.reset\(\)/)
})

test('workbench previews use sourceBlob revisions and opaque CSP-isolated documents', () => {
  const files = read('./WorkbenchFiles.vue')
  assert.match(files, /:source-blob="preview.blob"/)
  assert.match(files, /:source-key="preview.key"/)
  assert.match(files, /workbenchSnapshotKey\(entry.path, \+\+revision\)/)
  assert.match(preview, /:srcdoc="restrictedHtml"[^>]*sandbox=""[^>]*referrerpolicy="no-referrer"/)
  assert.match(preview, /!props.restrictedPreview && getPreviewSourceKey\(\).startsWith\('artifact:'\)/)
})

test('live files and artifacts share validated Office rendering without changing baseline viewers', () => {
  const files = read('./WorkbenchFiles.vue')
  const artifacts = read('./WorkbenchArtifacts.vue')
  const drawer = read('../ChatArtifactsDrawer.vue')
  assert.match(files, /restricted-preview fill-height/)
  assert.match(artifacts, /restricted-preview/)
  assert.match(drawer, /:restricted-preview="restrictedPreview"/)
  assert.match(preview, /prepareWorkbenchOfficePreview\(rawBlob/)
  assert.match(preview, /if \(kind === 'pptx'\) \{\s*pptxData.value = data/)
  assert.match(preview, /<vue-office-pptx :src="pptxData"/)
  assert.match(preview, /case 'pptx': \{\s*pptxData.value = await blob.arrayBuffer\(\)/)
  assert.match(preview, /kind === 'excel' \? await renderExcel\(previewBlob, ft\)/)
})

test('known status reasons are localized instead of showing raw identifiers', () => {
  assert.doesNotMatch(panel, /\{\{ status.reason \}\}/)
  assert.match(panel, /const statusReason = computed/)
  assert.match(panel, /const key = `workbench.states.\$\{reason\}`/)
  assert.match(panel, /te\(key\) \? t\(key\) : reason/)
})

test('both development and production proxies carry WebSocket upgrades', () => {
  const vite = read('../../../../../vite.config.ts')
  const nginx = read('../../../../../nginx-api-proxy.conf')
  assert.equal((vite.match(/ws: true/g) || []).length, 2)
  assert.match(vite, /15173/)
  assert.match(vite, /18081/)
  assert.match(nginx, /proxy_set_header Upgrade \$http_upgrade/)
  assert.match(nginx, /proxy_set_header Connection \$workbench_connection_upgrade/)
})
