import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const read = (relativePath) => readFileSync(join(here, '..', relativePath), 'utf8')

const inputFieldSource = read('components/Input-field.vue')
const chatSource = read('views/chat/index.vue')
const createChatSource = read('views/creatChat/creatChat.vue')
const knowledgeBaseSource = read('views/knowledge/KnowledgeBase.vue')
const platformSource = read('views/platform/index.vue')
const menuSource = read('components/menu.vue')
const listSpaceSidebarSource = read('components/ListSpaceSidebar.vue')
const settingsSource = read('views/settings/Settings.vue')
const integrationsSource = read('views/integrations/IntegrationsModal.vue')
const kbEditorSource = read('views/knowledge/KnowledgeBaseEditorModal.vue')
const agentEditorSource = read('views/agent/AgentEditorModal.vue')
const mcpServiceDialogSource = read('views/settings/components/McpServiceDialog.vue')
const settingDrawerSource = read('components/settings/SettingDrawer.vue')
const channelPanelListSource = read('components/css/channel-panel-list.less')
const createTenantDialogSource = read('components/CreateTenantDialog.vue')
const modelEditorDialogSource = read('components/ModelEditorDialog.vue')
const docContentSource = read('components/doc-content.vue')
const ollamaSettingsSource = read('views/settings/OllamaSettings.vue')
const organizationSettingsModalSource = read('views/organization/OrganizationSettingsModal.vue')
const dataSourceEditorDialogSource = read('views/knowledge/settings/DataSourceEditorDialog.vue')
const uploadConfirmDialogSource = read('views/knowledge/components/UploadConfirmDialog.vue')
const uiStoreSource = read('stores/ui.ts')

test('input field caps the absolute composer width to the viewport', () => {
  assert.match(
    inputFieldSource,
    /\.answers-input\s*\{[\s\S]*width:\s*min\(800px,\s*(95vw|calc\(100vw - 24px\))\)/
  )
})

test('chat surfaces avoid hard-coded breakpoint offsets for the composer', () => {
  for (const source of [createChatSource, knowledgeBaseSource]) {
    assert.doesNotMatch(source, /translateX\(-329px\)|translateX\(-250px\)|translateX\(-182px\)|translateX\(-164px\)/)
    assert.doesNotMatch(source, /width:\s*(654|500|340|300)px\s*!important/)
  }
})

test('platform shell can shrink below the legacy 600px minimum width', () => {
  assert.doesNotMatch(platformSource, /min-width:\s*600px/)
})

test('chat surface can shrink below the legacy 400px minimum width', () => {
  assert.doesNotMatch(chatSource, /min-width:\s*400px/)
})

test('chat page constrains the non-embedded composer to its container width', () => {
  assert.match(
    chatSource,
    /&:not\(\.is-embedded\)\s*:deep\(\.answers-input\)\s*\{[\s\S]*?width:\s*100%[\s\S]*?max-width:\s*100%[\s\S]*?\.t-textarea__inner/
  )
})

test('responsive sidebar keeps a separate mobile drawer state', () => {
  assert.match(uiStoreSource, /mobileSidebarOpen:\s*false/)
  assert.match(
    uiStoreSource,
    /effectiveSidebarCollapsed:\s*\(state\)\s*=>\s*state\.responsiveSidebarCollapsed\s*\?\s*!state\.mobileSidebarOpen\s*:\s*state\.sidebarCollapsed/
  )
})

test('menu renders a mobile drawer/backdrop shell below the 960px breakpoint', () => {
  assert.match(menuSource, /sidebar-mobile-backdrop/)
  assert.match(menuSource, /aside_box--mobile-open/)
  assert.match(menuSource, /@media\s*\(max-width:\s*960px\)/)
})

test('space sidebars collapse into a horizontal strip on narrow screens', () => {
  assert.match(listSpaceSidebarSource, /@media\s*\(max-width:\s*720px\)/)
  assert.match(listSpaceSidebarSource, /flex-direction:\s*row/)
  assert.match(listSpaceSidebarSource, /overflow-x:\s*auto/)
})

test('settings-style modals have dedicated mobile breakpoints', () => {
  for (const source of [settingsSource, integrationsSource]) {
    assert.match(source, /@media\s*\(max-width:\s*900px\)/)
    assert.match(source, /@media\s*\(max-width:\s*720px\)/)
    assert.match(source, /flex-direction:\s*column/)
  }
})

test('mobile platform shell owns a simple top bar with centered logo', () => {
  assert.match(platformSource, /mobile-topbar/)
  assert.match(platformSource, /mobile-sidebar-icon-button/)
  assert.match(platformSource, /mobile-topbar-logo/)
  assert.match(menuSource, /\.aside_box--mobile\.aside_box--collapsed\s*~\s*\.sidebar-mobile-backdrop/)
})

test('mobile chat composer places tool controls above the input surface', () => {
  assert.match(
    createChatSource,
    /@media\s*\(max-width:\s*960px\)\s*\{[\s\S]*?:deep\(\.answers-input\)\s*\{[\s\S]*?margin-top:\s*auto/
  )
  assert.match(
    inputFieldSource,
    /@media\s*\(max-width:\s*960px\)\s*\{[\s\S]*?\.rich-input-container\s*\{[\s\S]*?display:\s*flex[\s\S]*?flex-direction:\s*column/
  )
  assert.match(
    inputFieldSource,
    /@media\s*\(max-width:\s*960px\)\s*\{[\s\S]*?\.control-bar\s*\{[\s\S]*?position:\s*static[\s\S]*?order:\s*-1/
  )
})

test('chat input keeps a neutral border when focused on mobile', () => {
  assert.match(
    inputFieldSource,
    /&:focus-within\s*\{[\s\S]*?border-color:\s*var\(--td-component-stroke,\s*#dcdcdc\)/
  )
})

test('settings and integrations use horizontally scrollable mobile tabs', () => {
  for (const source of [settingsSource, integrationsSource]) {
    assert.match(
      source,
      /@media\s*\(max-width:\s*720px\)\s*\{[\s\S]*?\.settings-nav\s*\{[\s\S]*?display:\s*flex[\s\S]*?overflow-x:\s*auto/
    )
    assert.match(source, /@media\s*\(max-width:\s*720px\)\s*\{[\s\S]*?\.nav-group-title\s*\{[\s\S]*?display:\s*none/)
  }
})

test('knowledge and agent editors become page-like mobile editors', () => {
  for (const source of [kbEditorSource, agentEditorSource]) {
    assert.match(source, /@media\s*\(max-width:\s*720px\)/)
    assert.match(source, /height:\s*100dvh/)
    assert.match(source, /border-radius:\s*0/)
    assert.match(source, /\.settings-container\s*\{[\s\S]*?flex-direction:\s*column/)
    assert.match(source, /\.settings-nav\s*\{[\s\S]*?display:\s*flex[\s\S]*?overflow-x:\s*auto/)
    assert.match(source, /\.nav-group-title\s*\{[\s\S]*?display:\s*none/)
  }
})

test('shared drawers and channel cards have mobile-specific layouts', () => {
  assert.match(settingDrawerSource, /@media\s*\(max-width:\s*720px\)/)
  assert.match(settingDrawerSource, /\.setting-drawer-resize-handle\s*\{[\s\S]*?display:\s*none/)
  assert.match(settingDrawerSource, /\.t-drawer\.setting-drawer\s+\.t-drawer__content-wrapper,[\s\S]*?\.t-drawer\.setting-drawer\s+\.t-drawer__content\s*\{[\s\S]*?width:\s*100vw/)
  assert.match(channelPanelListSource, /@media\s*\(max-width:\s*720px\)/)
  assert.match(channelPanelListSource, /\.channel-grid\s*\{[\s\S]*?grid-template-columns:\s*1fr/)
})

test('create tenant dialog can occupy the mobile viewport', () => {
  assert.match(createTenantDialogSource, /class="create-tenant-dialog"/)
  assert.match(createTenantDialogSource, /create-tenant-mobile-nav/)
  assert.match(createTenantDialogSource, /@media\s*\(max-width:\s*900px\)/)
  assert.match(createTenantDialogSource, /width:\s*100vw\s*!important/)
})

test('mcp service segmented controls wrap inside the mobile drawer', () => {
  assert.match(mcpServiceDialogSource, /@media\s*\(max-width:\s*720px\)/)
  assert.match(
    mcpServiceDialogSource,
    /\.source-options\s*\{[\s\S]*?display:\s*grid[\s\S]*?grid-template-columns:\s*repeat\(2,\s*minmax\(0,\s*1fr\)\)/
  )
  assert.match(mcpServiceDialogSource, /\.source-option__label\s*\{[\s\S]*?white-space:\s*normal[\s\S]*?overflow-wrap:\s*anywhere/)
})

test('knowledge document drawers switch to full-width compact mode on mobile', () => {
  assert.match(docContentSource, /const isCompactDrawerLayout = computed\(\(\) => viewportWidth\.value <= 960\)/)
  assert.match(docContentSource, /:size="mainDrawerSize"/)
  assert.match(docContentSource, /:size="timelineDrawerSize"/)
  assert.match(docContentSource, /@media\s*\(max-width:\s*960px\)/)
})

test('model provider picker clamps dropdown width on mobile', () => {
  assert.match(modelEditorDialogSource, /overlayClassName:\s*'provider-select-popup',\s*attach:\s*'body'/)
  assert.match(modelEditorDialogSource, /max-width:\s*calc\(100vw - 28px\)\s*!important/)
})

test('ollama settings stack metadata above controls on narrow screens', () => {
  assert.match(
    ollamaSettingsSource,
    /@media\s*\(max-width:\s*900px\)\s*\{[\s\S]*?\.setting-row,[\s\S]*?flex-direction:\s*column/
  )
  assert.match(
    ollamaSettingsSource,
    /@media\s*\(max-width:\s*900px\)\s*\{[\s\S]*?\.setting-control\s*\{[\s\S]*?width:\s*100%/
  )
})

test('integrations modal anchors the close button at the top level', () => {
  assert.match(integrationsSource, /<div class="settings-modal">\s*<button class="close-btn"/)
})

test('organization settings modal switches to a page-like mobile layout', () => {
  assert.match(organizationSettingsModalSource, /@media\s*\(max-width:\s*900px\)/)
  assert.match(organizationSettingsModalSource, /@media\s*\(max-width:\s*720px\)/)
  assert.match(
    organizationSettingsModalSource,
    /\.settings-container\s*\{[\s\S]*?flex-direction:\s*column/
  )
  assert.match(
    organizationSettingsModalSource,
    /\.settings-nav\s*\{[\s\S]*?display:\s*flex[\s\S]*?overflow-x:\s*auto/
  )
  assert.match(
    organizationSettingsModalSource,
    /\.setting-row\s*\{[\s\S]*?flex-direction:\s*column/
  )
})

test('knowledge document toolbar and upload confirmation flow adapt on mobile', () => {
  assert.match(knowledgeBaseSource, /@media\s*\(max-width:\s*560px\)/)
  assert.match(knowledgeBaseSource, /overlayClassName:\s*'doc-date-range-popup',\s*attach:\s*'body'/)
  assert.match(
    knowledgeBaseSource,
    /\.doc-filter-actions\s*\{[\s\S]*?display:\s*flex[\s\S]*?justify-content:\s*flex-end/
  )
  assert.match(
    knowledgeBaseSource,
    /\.doc-filter-field--wide\s*\{[\s\S]*?width:\s*100%[\s\S]*?flex-basis:\s*100%/
  )
  assert.match(uploadConfirmDialogSource, /@media\s*\(max-width:\s*900px\)/)
  assert.match(uploadConfirmDialogSource, /@media\s*\(max-width:\s*720px\)/)
  assert.match(
    uploadConfirmDialogSource,
    /\.upload-confirm-container\s*\{[\s\S]*?flex-direction:\s*column/
  )
  assert.match(
    uploadConfirmDialogSource,
    /\.overview-row\s*\{[\s\S]*?grid-template-areas:\s*"label chevron"/
  )
  assert.match(knowledgeBaseSource, /\.doc-date-range-popup[\s\S]*?@media\s*\(max-width:\s*720px\)/)
})

test('knowledge datasource editor exposes a dedicated close button', () => {
  assert.match(dataSourceEditorDialogSource, /<template #headerActions>/)
  assert.match(dataSourceEditorDialogSource, /class="datasource-close-btn"/)
})
