import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const read = (relativePath) => readFileSync(new URL(relativePath, import.meta.url), 'utf8')

const platform = read('./platform/index.vue')
const chat = read('./chat/index.vue')
const createChat = read('./creatChat/creatChat.vue')
const app = read('../App.vue')
const inputField = read('../components/Input-field.vue')
const menu = read('../components/menu.vue')
const header = read('../components/ChatHeader.vue')
const login = read('./auth/Login.vue')
const agentSelector = read('../components/AgentSelector.vue')
const knowledgeBaseSelector = read('../components/KnowledgeBaseSelector.vue')
const referencesDrawer = read('../components/ChatReferencesDrawer.vue')
const attachmentDrawer = read('../components/ChatAttachmentPreviewDrawer.vue')
const settingsPage = read('./settings/Settings.vue')
const settingDrawer = read('../components/settings/SettingDrawer.vue')
const commandPalette = read('../components/GlobalCommandPalette.vue')
const knowledgeBaseList = read('./knowledge/KnowledgeBaseList.vue')
const knowledgeBaseDetail = read('./knowledge/KnowledgeBase.vue')
const knowledgeBaseEditor = read('./knowledge/KnowledgeBaseEditorModal.vue')
const agentList = read('./agent/AgentList.vue')
const agentEditor = read('./agent/AgentEditorModal.vue')
const organizationList = read('./organization/OrganizationList.vue')
const organizationEditor = read('./organization/OrganizationEditorModal.vue')
const documentDrawer = read('../components/doc-content.vue')
const contextualGuide = read('../components/ContextualGuide.vue')
const spotlightGuide = read('../components/SpotlightGuide.vue')
const newUserGuide = read('../components/NewUserGuide.vue')
const attachmentUpload = read('../components/AttachmentUpload.vue')
const focusTrap = read('../utils/focusTrap.ts')
const uiStore = read('../stores/ui.ts')
const responsiveViewport = read('../composables/useResponsiveViewport.ts')
const responsiveViewportMetrics = read('../composables/responsiveViewportMetrics.ts')
const responsiveLess = read('../assets/responsive.less')
const popoverPosition = read('../utils/popoverPosition.ts')
const viewportObserver = read('../utils/viewportChangeObserver.ts')
const indexHtml = read('../../index.html')
const embedHtml = read('../../embed.html')

const sources = [
  platform,
  chat,
  createChat,
  app,
  inputField,
  menu,
  header,
  login,
  agentSelector,
  knowledgeBaseSelector,
  referencesDrawer,
  attachmentDrawer,
  settingsPage,
  settingDrawer,
  commandPalette,
  knowledgeBaseList,
  knowledgeBaseDetail,
  knowledgeBaseEditor,
  agentList,
  agentEditor,
  organizationList,
  organizationEditor,
  documentDrawer,
  contextualGuide,
  spotlightGuide,
  newUserGuide,
  attachmentUpload,
  focusTrap,
  uiStore,
  responsiveViewport,
  responsiveViewportMetrics,
  responsiveLess,
  popoverPosition,
  viewportObserver,
]

test('responsive breakpoint contract stays synchronized between TypeScript and Less', () => {
  const tsBreakpoints = Object.fromEntries(
    [...responsiveViewport.matchAll(/export const (PHONE|COMPACT|TABLET)_MAX_WIDTH = (\d+)/g)]
      .map(([, name, value]) => [name.toLowerCase(), Number(value)]),
  )
  const lessBreakpoints = Object.fromEntries(
    [...responsiveLess.matchAll(/@(phone|compact|tablet)-max:\s*(\d+)px/g)]
      .map(([, name, value]) => [name, Number(value)]),
  )

  assert.deepEqual(tsBreakpoints, lessBreakpoints)
  assert.match(responsiveLess, /\.phone-landscape\(@rules\)/)
  assert.match(responsiveLess, /prefers-reduced-motion/)
})

test('responsive viewport observer is singleton, synchronous, and lifecycle-safe', () => {
  assert.match(app, /useResponsiveViewport\(\)/)
  assert.match(responsiveViewport, /let consumers = 0/)
  assert.match(responsiveViewport, /if \(consumers === 1\) startListening\(\)/)
  assert.match(responsiveViewport, /if \(consumers === 0\) stopListening\(\)/)
  assert.match(responsiveViewport, /if \(typeof window !== 'undefined'\) measureResponsiveViewport\(\)/)
  assert.match(responsiveViewport, /window\.visualViewport\?\.addEventListener\('resize'/)
  assert.match(responsiveViewport, /focusMeasureTimers\.forEach/)
  assert.match(responsiveViewport, /--app-keyboard-inset/)
})

test('pinch zoom keeps the application viewport stable without faking keyboard state', () => {
  assert.match(responsiveViewport, /visualScale: viewport\?\.scale/)
  assert.match(responsiveViewport, /app-pinch-zoomed/)
  assert.match(responsiveViewport, /isKeyboardOpen\.value = !nextIsPinchZoomed/)
  assert.match(responsiveViewportMetrics, /Math\.abs\(scale - 1\) > PINCH_ZOOM_EPSILON/)
  assert.match(responsiveViewportMetrics, /if \(isPinchZoomed\)[\s\S]*?visualWidth: layoutWidth[\s\S]*?visualHeight: layoutHeight/)
  assert.match(responsiveViewportMetrics, /visualOffsetTop: 0[\s\S]*?keyboardInset: 0/)
})
test('compact sidebar preserves desktop preference and behaves as a temporary dialog', () => {
  assert.match(uiStore, /compactViewport:\s*matchesCompactLayout\(\)/)
  assert.match(uiStore, /effectiveSidebarCollapsed:[\s\S]*?mobileSidebarOpen[\s\S]*?sidebarCollapsed/)
  assert.match(platform, /class="mobile-sidebar-backdrop"/)
  assert.match(platform, /:inert="uiStore\.compactViewport && uiStore\.mobileSidebarOpen"/)
  assert.match(platform, /trapTabKey/)
  assert.match(platform, /setBodyScrollLock/)
  assert.match(platform, /watch\(\(\) => route\.fullPath[\s\S]*?closeMobileSidebar/)
  assert.match(menu, /:role="uiStore\.compactViewport && uiStore\.mobileSidebarOpen \? 'dialog' : 'navigation'"/)
  assert.match(menu, /data-sidebar-close/)
  assert.match(menu, /aria-controls="platform-sidebar"/)
  assert.match(menu, /\.aside_box--compact-viewport\.aside_box--collapsed[\s\S]*?width: 44px/)
})

test('mobile chat surfaces use semantic responsive mixins and avoid legacy fixed hacks', () => {
  for (const source of [chat, createChat, inputField, header]) {
    assert.match(source, /@import ['"]@\/assets\/responsive\.less['"]/)
    assert.match(source, /\.compact\(\{/)
  }
  assert.match(chat, /\.phone-landscape\(\{/)
  assert.match(createChat, /\.phone-landscape\(\{/)
  assert.match(inputField, /\.phone-landscape\(\{/)
  assert.doesNotMatch(platform, /min-width:\s*600px/)
  assert.doesNotMatch(chat, /min-width:\s*400px/)
  assert.doesNotMatch(createChat, /translateX\(-(?:329|250|182|164)px\)/)
  assert.doesNotMatch(createChat, /width:\s*(?:654|500|340|300)px\s*!important/)
})

test('phone login header reserves space and respects safe areas', () => {
  assert.match(login, /min-height:\s*var\(--app-viewport-height, 100dvh\)/)
  assert.match(login, /padding:\s*calc\(78px \+ env\(safe-area-inset-top\)\)/)
  assert.match(login, /overflow-x:\s*hidden/)
  assert.match(login, /> \.header-link[\s\S]*?width:\s*40px[\s\S]*?height:\s*40px/)
  assert.match(login, /@media \(max-width: 900px\) and \(max-height: 520px\)[\s\S]*?\.showcase-section\s*\{[\s\S]*?display:\s*none/)
})

test('critical input controls stay pinned and popovers share hardened viewport logic', () => {
  assert.match(inputField, /<div class="control-right">[\s\S]*?class="model-display"[\s\S]*?class="control-btn send-btn"/)
  assert.match(inputField, /\.control-left\s*\{[\s\S]*?overflow-x:\s*auto/)
  assert.match(inputField, /computeAnchoredPopoverLayout/)
  assert.match(agentSelector, /computeAnchoredPopoverLayout/)
  assert.match(knowledgeBaseSelector, /computeAnchoredPopoverLayout/)
  assert.match(inputField, /observeViewportChanges/)
  assert.match(agentSelector, /observeViewportChanges/)
  assert.match(knowledgeBaseSelector, /observeViewportChanges/)
  assert.match(viewportObserver, /requestAnimationFrame/)
  assert.match(viewportObserver, /cancelAnimationFrame/)
  assert.match(popoverPosition, /finiteNonNegative/)
})

test('compact knowledge picker never borrows the chat textarea for filtering', () => {
  assert.match(inputField, /const \{ isCompact \} = useResponsiveViewport\(\)/)
  assert.match(inputField, /const releaseChatInputFocus = \(\) => \{[\s\S]*?textarea\.blur\(\)/)
  assert.match(inputField, /if \(isCompact\.value\) \{[\s\S]*?showMention\.value = false[\s\S]*?showKbSelector\.value = nextVisible/)
  assert.match(inputField, /if \(nextVisible\) releaseChatInputFocus\(\)/)
  assert.match(knowledgeBaseSelector, /if \(!isCompact\.value\) searchInput\.value\?\.focus/)
  assert.match(knowledgeBaseSelector, /if \(isCompact\.value\) searchInput\.value\?\.blur\(\)/)
  assert.match(knowledgeBaseSelector, /const close = \(\) => \{[\s\S]*?searchInput\.value\?\.blur\(\)/)
})
test('drawers lock background scrolling and become compact full-screen surfaces', () => {
  for (const drawer of [referencesDrawer, attachmentDrawer]) {
    assert.match(drawer, /setBodyScrollLock/)
    assert.match(drawer, /releaseBodyScrollLock/)
  }
  assert.match(referencesDrawer, /const width = layoutWidth\.value/)
  assert.doesNotMatch(referencesDrawer, /return window\.innerWidth < /)
  assert.match(referencesDrawer, /\.compact\(\{/)
  assert.match(attachmentDrawer, /setBodyScrollLock\(ATTACHMENT_DRAWER_SCROLL_LOCK, open && compact\)/)
  assert.match(attachmentDrawer, /v-if="visible && !isCompact"/)
  assert.match(attachmentDrawer, /app-viewport-height|100dvh/)
})


test('mobile document detail is a full-screen, single-scroll reading surface', () => {
  assert.match(documentDrawer, /const mainDrawerSize = computed\(\(\) => isCompact\.value \? '100%' :/)
  assert.match(documentDrawer, /const timelineDrawerSize = computed\(\(\) => isCompact\.value \? '100%' :/)
  assert.match(documentDrawer, /v-if="visible && !isCompact"/)
  assert.match(documentDrawer, /v-if="timelineDrawerVisible && !isCompact"/)
  assert.match(documentDrawer, /'doc-main-drawer--compact': isCompact/)
  assert.match(documentDrawer, /'kp-secondary-drawer--compact': isCompact/)
  assert.match(documentDrawer, /setBodyScrollLock\(DOC_DRAWER_SCROLL_LOCK, visible && compact\)/)
  assert.match(documentDrawer, /\.t-drawer\.doc-main-drawer--compact[\s\S]*?width: 100% !important[\s\S]*?app-viewport-height[\s\S]*?overflow-x: hidden/)
  assert.match(documentDrawer, /\.doc-content-section-head[\s\S]*?position: sticky/)
  assert.match(documentDrawer, /\.view-mode-buttons[\s\S]*?width: 100%/)
  assert.match(documentDrawer, /:deep\(img\)[\s\S]*?max-width: 100%/)
  assert.match(documentDrawer, /:deep\(table\)[\s\S]*?overflow-x: auto/)
})


test('remaining primary routes use compact mobile surfaces instead of desktop geometry', () => {
  assert.match(app, /Compact safety net for teleported TDesign surfaces/)
  assert.match(app, /html\.app-compact-viewport \.t-dialog__ctx[\s\S]*?width: min\(100%, 560px\) !important/)
  assert.match(app, /html\.app-compact-viewport \.t-drawer[\s\S]*?width: 100% !important/)

  assert.match(settingsPage, /mobile-settings-header/)
  assert.match(settingsPage, /settings-sidebar--mobile-open/)
  assert.match(settingsPage, /setBodyScrollLock\(SETTINGS_SCROLL_LOCK, open && compact\)/)
  assert.match(settingsPage, /height: var\(--app-viewport-height, 100dvh\)/)
  assert.match(settingsPage, /width: min\(84vw, 320px\)/)

  assert.match(settingDrawer, /isCompact\.value \? '100%'/)
  assert.match(settingDrawer, /drawerVisible && resizable && !isCompact/)
  assert.match(settingDrawer, /setting-drawer--compact/)
  assert.match(settingDrawer, /setBodyScrollLock\(SETTING_DRAWER_SCROLL_LOCK, visible && compact\)/)

  assert.match(commandPalette, /@import ['"]@\/assets\/responsive\.less['"]/)
  assert.match(commandPalette, /calc\(var\(--app-viewport-height, 100dvh\) - 24px\)/)

  for (const listPage of [knowledgeBaseList, agentList, organizationList]) {
    assert.match(listPage, /@import ['"]@\/assets\/responsive\.less['"]/)
    assert.match(listPage, /\.compact\(\{/)
    assert.doesNotMatch(listPage, /padding-top:\s*40vh\s*!important/)
  }

  assert.doesNotMatch(knowledgeBaseDetail, /translateX\(-(?:329|250|182|164)px\)/)
  assert.doesNotMatch(knowledgeBaseDetail, /width:\s*(?:654|500|340|300)px\s*!important/)

  for (const editor of [knowledgeBaseEditor, organizationEditor, agentEditor]) {
    assert.match(editor, /@import ['"]@\/assets\/responsive\.less['"]/)
    assert.match(editor, /setBodyScrollLock/)
    assert.match(editor, /height: var\(--app-viewport-height, 100dvh\)/)
    assert.match(editor, /flex-direction: column/)
    assert.match(editor, /overflow-x: auto/)
  }

  assert.match(menu, /\.menu_top,[\s\S]*?\.menu_bottom[\s\S]*?display: none/)
  assert.match(menu, /\.sidebar-toggle-item[\s\S]*?width: 40px/)
  assert.match(app, /--app-font-family: "TencentSans"/)
  assert.match(agentEditor, /AGENT_EDITOR_SCROLL_LOCK/)
  assert.match(agentEditor, /\.phone\(\{[\s\S]*?\.setting-row[\s\S]*?flex-direction: column/)
})

test('optional guides cannot crash or destabilize compact route rendering', () => {
  assert.match(contextualGuide, /const \{ isCompact \} = useResponsiveViewport\(\)/)
  assert.match(contextualGuide, /const canOpen = \(\) => \{[\s\S]*?!props\.when[\s\S]*?isCompact\.value[\s\S]*?!compactExiting[\s\S]*?\}/)
  assert.match(contextualGuide, /\[\(\) => props\.when, isCompact\]/)
  assert.match(newUserGuide, /if \(isCompact\.value\) return/)
  assert.match(newUserGuide, /clearAutoOpenTimer/)
  assert.match(spotlightGuide, /const safeT =/)
  assert.match(spotlightGuide, /Failed to render guide translation/)
  assert.match(spotlightGuide, /return key/)
  assert.match(spotlightGuide, /rectToCssPx\(el\.getBoundingClientRect\(\), zoom\)/)
  assert.match(spotlightGuide, /observeViewportChanges\(onViewportChange\)/)
  assert.match(spotlightGuide, /setBodyScrollLock\(GUIDE_SCROLL_LOCK, val\)/)
  assert.match(spotlightGuide, /const dismiss = \(\) => \{[\s\S]*?emit\('dismiss'\)[\s\S]*?close\(\)/)
  assert.doesNotMatch(spotlightGuide, /const dismiss = \(\) => \{[\s\S]*?finish\(\)/)
})

test('mobile lifecycle cleanup prevents stale async and focus state', () => {
  assert.match(createChat, /onUnmounted\(\(\) => \{[\s\S]*?suggestedQuestionsFetchId \+= 1[\s\S]*?clearTimeout\(debounceTimer\)/)
  assert.match(attachmentUpload, /if \(disposed \|\| !attachments\.value\.some/)
  assert.match(focusTrap, /element\.closest\('\[inert\], \[aria-hidden=/)
  assert.match(focusTrap, /style\.visibility !== 'hidden'/)
  assert.match(platform, /watch\(\(\) => uiStore\.compactViewport && uiStore\.mobileSidebarOpen,[\s\S]*?\{ immediate: true \}\)/)
})

test('standalone and embedded pages declare safe-area and keyboard-aware viewport behavior', () => {
  for (const html of [indexHtml, embedHtml]) {
    assert.match(html, /viewport-fit=cover/)
    assert.match(html, /interactive-widget=resizes-content/)
  }
})

test('scoped global selectors keep the full descendant selector inside :global()', () => {
  const scopedSources = [agentSelector, inputField, chat, createChat]
  for (const source of scopedSources) {
    // Vue SFC's scoped CSS compiler drops descendants written after :global(...).
    // That previously compiled :global(html.app-coarse-pointer) .panel into
    // html.app-coarse-pointer { ... } and hid the entire application on phones.
    assert.doesNotMatch(source, /:global\([^)]*\)\s+(?:[.#:]|[a-z])/i)
  }

  assert.match(app, /html\s*\{[\s\S]*?display:\s*block\s*!important/)
  assert.match(agentSelector, /:global\(html\.app-coarse-pointer \.agent-detail-panel\)/)
  assert.match(agentSelector, /:global\(html\.app-compact-viewport \.agent-selector-dropdown\)/)
  assert.match(inputField, /:global\(html\.app-keyboard-open \.answers-input\)/)
  assert.match(createChat, /:global\(html\.app-keyboard-open \.dialogue-title\)/)
})
test('mobile adaptation sources contain no replacement characters from encoding damage', () => {
  for (const source of sources) assert.doesNotMatch(source, /\uFFFD/)
})
