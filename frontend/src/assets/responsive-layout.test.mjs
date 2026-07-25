import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const read = (relativePath) =>
  readFileSync(new URL(relativePath, import.meta.url), 'utf8')

const responsive = read('./responsive.less')
const settingsSurface = read('./responsive-settings-surface.less')
const uiStore = read('../stores/ui.ts')
const platform = read('../views/platform/index.vue')
const menu = read('../components/menu.vue')
const knowledgeBase = read('../views/knowledge/KnowledgeBase.vue')
const embedApi = read('../api/embed/index.ts')
const apiIntegration = read('../views/integrations/ApiIntegrationSettings.vue')
const router = read('../router/index.ts')
const integrationLanding = read('../views/integrations/integration-landing.less')
const cloudSettings = read('../views/settings/WeKnoraCloudSettings.vue')
const systemInfo = read('../views/settings/SystemInfo.vue')

test('mobile platform navigation uses a drawer without persisting desktop collapse state', () => {
  assert.match(uiStore, /compactViewport:\s*false/)
  assert.match(uiStore, /mobileSidebarOpen:\s*false/)
  assert.match(
    uiStore,
    /toggleSidebar\(\)\s*\{\s*if \(this\.compactViewport\) \{[\s\S]*?return\s*\}[\s\S]*?localStorage\.setItem/,
  )
  assert.match(platform, /matchMedia\('\(max-width: 768px\)'\)/)
  assert.match(platform, /class="compact-topbar"/)
  assert.match(menu, /aside_box--mobile-open/)
  assert.match(menu, /class="sidebar-mobile-backdrop"/)
})

test('teleported dialogs, drawers, popups and tables stay inside compact viewports', () => {
  assert.match(responsive, /\.t-dialog,[\s\S]*?\.t-drawer,[\s\S]*?\.t-popup/)
  assert.match(responsive, /width:\s*calc\(100vw - 24px\)\s*!important/)
  assert.match(responsive, /\.t-drawer__content-wrapper,[\s\S]*?width:\s*100vw\s*!important/)
  assert.match(responsive, /\.t-table__content\s*\{[\s\S]*?overflow-x:\s*auto/)
})

test('settings-style editors become full-screen with horizontal compact navigation', () => {
  assert.match(settingsSurface, /\.settings-modal\s*\{[\s\S]*?width:\s*100vw/)
  assert.match(settingsSurface, /\.settings-container\s*\{[\s\S]*?flex-direction:\s*column/)
  assert.match(settingsSurface, /\.settings-nav\s*\{[\s\S]*?overflow-x:\s*auto/)

  for (const path of [
    '../views/settings/Settings.vue',
    '../views/knowledge/KnowledgeBaseEditorModal.vue',
    '../views/agent/AgentEditorModal.vue',
    '../views/organization/OrganizationEditorModal.vue',
    '../views/organization/OrganizationSettingsModal.vue',
  ]) {
    const source = read(path)
    assert.match(source, /responsive-settings-surface\.less/)
    assert.match(source, /\.responsive-settings-surface\(\)/)
  }
})

test('responsive card grids can shrink below their desktop target width', () => {
  for (const path of [
    '../views/settings/ModelSettings.vue',
    '../views/settings/McpSettings.vue',
    '../views/settings/ParserEngineSettings.vue',
    '../views/settings/StorageBackendSettings.vue',
    '../views/settings/StorageEngineSettings.vue',
    '../views/settings/VectorStoreSettings.vue',
    '../views/settings/WebSearchSettings.vue',
    '../views/knowledge/settings/DataSourceSettings.vue',
  ]) {
    assert.match(
      read(path),
      /grid-template-columns:\s*repeat\(auto-fill,\s*minmax\(min\(\d+px,\s*100%\),\s*1fr\)\)/,
    )
  }
})

test('knowledge chat input no longer uses fixed desktop translations', () => {
  assert.doesNotMatch(knowledgeBase, /translateX\(-(?:329|250|182|164)px\)/)
  assert.match(knowledgeBase, /\.answers-input\s*\{[\s\S]*?max-width:\s*960px/)
})

test('generated iframe snippets fit narrow host containers', () => {
  assert.match(embedApi, /style="width:100%;max-width:400px;/)
  assert.doesNotMatch(embedApi, /style="width:400px;/)
})

test('API integration collapses desktop rows for tablet settings widths', () => {
  assert.match(
    apiIntegration,
    /@media \(max-width: 1023px\) \{[\s\S]*?\.row \{[\s\S]*?grid-template-columns:\s*1fr/,
  )
})

test('settings route reuses the single platform-level modal', () => {
  assert.match(router, /const SettingsRoutePlaceholder = \{ render: \(\) => null \}/)
  assert.match(
    router,
    /path:\s*["']settings["'][\s\S]*?component:\s*SettingsRoutePlaceholder/,
  )
  assert.doesNotMatch(
    router,
    /path:\s*["']settings["'][\s\S]*?import\(["']\.\.\/views\/settings\/Settings\.vue["']\)/,
  )
})

test('nested integration pages collapse before the tablet content pane becomes cramped', () => {
  assert.match(
    integrationLanding,
    /\.landing-body \{[\s\S]*?@media \(max-width: 1023px\) \{[\s\S]*?grid-template-columns:\s*1fr/,
  )
})

test('cloud credentials and system information stack on phones', () => {
  assert.match(
    cloudSettings,
    /@media \(max-width: 768px\) \{[\s\S]*?\.setting-row \{[\s\S]*?flex-direction:\s*column/,
  )
  assert.match(
    systemInfo,
    /@media \(max-width: 768px\) \{[\s\S]*?\.setting-control \{[\s\S]*?min-width:\s*0/,
  )
})
