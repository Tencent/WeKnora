import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8')
const panel = read('./SandboxWorkbench.vue')
const terminal = read('./WorkbenchTerminal.vue')
const chat = read('../../index.vue')
const preview = read('../../../../components/document-preview.vue')
const workbenchPreview = read('../../../../components/WorkbenchDocumentPreview.vue')

test('scope invalidation includes session, route, user, tenant, logout, close and unmount', () => {
  for (const source of ['props.sessionId', 'route.fullPath', 'auth.user?.id', 'auth.effectiveTenantId', 'auth.isLoggedIn']) assert.ok(panel.includes(source), source)
  assert.match(panel, /flush: 'sync'/)
  assert.match(panel, /function close\(\) \{ dispose\(\); emit\('close'\) \}/)
  assert.match(panel, /onBeforeUnmount\(\(\) => \{\s*dispose\(\)/)
  assert.match(chat, /workbenchRef.value\?\.dispose\(\)/)
  assert.match(chat, /watch\(referencesDrawerVisible, visible => \{ if \(visible\) closeWorkbench\(\)/)
  assert.match(chat, /referencesDrawer.close\(\);\s*workbenchVisible.value = true/)
})

test('upstream panel owns the header entry and exposes a distinct feature-gated workbench action', () => {
  const header = read('../../../../components/ChatHeader.vue')
  const sidePanel = read('../../../../components/chat/SandboxSidePanel.vue')
  assert.doesNotMatch(header, /sandbox-workbench-toggle|toggle-workbench/)
  assert.match(chat, /class="sandbox-header-toggle__btn"/)
  assert.match(chat, /@click="sandboxPanel.open\(\)"/)
  assert.match(chat, /:workbench-enabled="workbenchEnabled" @open-workbench="openWorkbench"/)
  assert.match(sidePanel, /v-if="workbenchEnabled"/)
  assert.match(sidePanel, /id="sandbox-workbench-toggle"/)
  assert.match(sidePanel, /t\('workbench.open'\)/)
  assert.match(sidePanel, /@click="emit\('open-workbench'\)"/)
  assert.match(chat, /<SandboxSidePanel v-if="!embeddedMode && !workbenchVisible"/)
  assert.match(chat, /watch\(sandboxPanel.visible, visible => \{ if \(visible\) closeWorkbench\(\)/)
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
  assert.match(workbenchPreview, /:srcdoc="restrictedHtml"[^>]*sandbox=""[^>]*referrerpolicy="no-referrer"/)
  assert.doesNotMatch(preview, /workbenchPreview|workbenchOfficePreview|sandboxWorkbench/)
})

test('live files and artifacts share validated Office rendering without changing baseline viewers', () => {
  const files = read('./WorkbenchFiles.vue')
  const artifacts = read('./WorkbenchArtifacts.vue')
  const drawer = read('../ChatArtifactsDrawer.vue')
  assert.match(files, /WorkbenchDocumentPreview/)
  assert.match(artifacts, /restricted-preview/)
  assert.match(drawer, /WorkbenchDocumentPreview/)
  assert.match(workbenchPreview, /prepareWorkbenchOfficePreview\(blob/)
  assert.match(workbenchPreview, /nextType = 'pptx'/)
  assert.match(workbenchPreview, /preparedType.value = nextType/)
  assert.match(workbenchPreview, /const parentAbort = \(\) => controller.abort\(\)/)
  assert.match(workbenchPreview, /if \(current !== generation\) return/)
  assert.match(preview, /<vue-office-pptx :key="loadedForId" :src="pptxData"/)
  assert.match(preview, /case 'pptx': \{\s*const prepared = await preparePptxPreview\(await blob.arrayBuffer\(\)\)/)
  assert.match(preview, /pptxData.value = prepared.data/)
  assert.match(preview, /@rendered="onPptxRendered"/)
  assert.match(workbenchPreview, /spreadsheetHTML/)
})

test('artifact drawer keeps restricted previews separate from the native preview toolbar', () => {
  const drawer = read('../ChatArtifactsDrawer.vue')
  const restricted = drawer.match(/<WorkbenchDocumentPreview\b[^>]*\/>/)?.[0] || ''
  const native = drawer.match(/<DocumentPreview\b[^>]*\/>/)?.[0] || ''
  assert.match(restricted, /v-if="restrictedPreview"/)
  assert.match(restricted, /:max-preview-bytes="maxPreviewBytes"/)
  assert.match(restricted, /:request-signal="requestSignal"/)
  assert.doesNotMatch(restricted, /toolbar-target/)
  assert.match(native, /v-else/)
  assert.match(native, /:toolbar-target="previewActions"/)
  assert.match(native, /fill-height/)
  for (const branch of [restricted, native]) {
    assert.match(branch, /:message-id="messageId"/)
    assert.match(branch, /:artifact-index="previewItem.index"/)
  }
  assert.match(drawer, /<div v-else ref="previewActions"/)
  assert.match(workbenchPreview, /:source-blob="preparedBlob" :source-key="preparedKey"/)
  assert.doesNotMatch(workbenchPreview, /buildHtmlPreview|allow-scripts/)
})

test('upstream browser entry and request guards coexist with the feature-gated workbench', () => {
  assert.match(chat, /<BrowserTaskPreview v-if="!embeddedMode && session_id" :key="session_id" :session-id="session_id"/)
  assert.match(chat, /local_browser_enabled: !props.embeddedMode && agentEnabled && useSettingsStoreInstance.isLocalBrowserEnabled && !useBrowserConnectionStore\(\).knownOffline/)
  assert.match(chat, /hydrateSessionInputState\(lastState, preserveDraft\)/)
})

test('known status reasons are localized instead of showing raw identifiers', () => {
  assert.doesNotMatch(panel, /\{\{ status.reason \}\}/)
  assert.match(panel, /const statusReason = computed/)
  assert.match(panel, /const key = `workbench.states.\$\{reason\}`/)
  assert.match(panel, /te\(key\) \? t\(key\) : reason/)
})

test('memory limits display both per-process AS and sampled aggregate RSS semantics', () => {
  assert.match(panel, /status.limits.memory_enforcement === 'per_process_as_and_aggregate_rss_sampled'/)
  assert.match(panel, /t\('workbench.memoryLimitHint'\)/)
  for (const locale of ['en-US', 'zh-CN', 'ja-JP', 'ko-KR', 'ru-RU']) {
    const source = read(`../../../../i18n/locales/${locale}.ts`)
    assert.match(source, /memoryLimit: '[^']*RSS[^']*AS/)
    assert.match(source, /memoryLimitHint: '[^']+RSS/)
  }
})

test('both development and production proxies carry WebSocket upgrades', () => {
  const vite = read('../../../../../vite.config.ts')
  const nginx = read('../../../../../nginx-api-proxy.conf')
  assert.equal((vite.match(/ws: true/g) || []).length, 2)
  assert.match(vite, /5173/)
  assert.match(vite, /8080/)
  assert.match(nginx, /proxy_set_header Upgrade \$http_upgrade/)
  assert.match(nginx, /proxy_set_header Connection \$workbench_connection_upgrade/)
})

test('tab spacing participates in the TDesign active-bar width calculation', () => {
  assert.match(panel, /\.workbench-tabs \.t-tabs__nav-item \{ font-size: 13px; \}/)
  assert.match(panel, /\.workbench-tabs \.t-tabs__nav-item-wrapper \{ margin: 0; padding: 0 12px; \}/)
  assert.doesNotMatch(panel, /\.workbench-tabs \.t-tabs__nav-item \{[^}]*padding:/)
})
