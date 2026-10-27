import assert from 'node:assert/strict'
import test, { after, before } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, defineComponent, h, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createI18n } from 'vue-i18n'
import en from '../../../i18n/locales/en-US'

const tree = readFileSync(new URL('./KbFolderTree.vue', import.meta.url), 'utf8')
const batchBar = readFileSync(new URL('./DocumentBatchBar.vue', import.meta.url), 'utf8')
const cardView = readFileSync(new URL('./DocumentCardView.vue', import.meta.url), 'utf8')
const listView = readFileSync(new URL('./DocumentListView.vue', import.meta.url), 'utf8')

// The root folder's own path IS the empty string, so a falsy "nothing is being
// renamed" sentinel matches it and leaves a stray rename input on the root row.
// Two independent guards keep that from coming back.
test('the rename sentinel cannot collide with the root folder path', () => {
  assert.match(tree, /const renamingPath = ref<string \| null>\(null\)/)
  assert.doesNotMatch(tree, /const renamingPath = ref\(''\)/)
  assert.match(tree, /row\.kind === 'folder' && renamingPath\.value === row\.path/)
})

test('deep folders stop indenting so the name stays visible', () => {
  assert.match(tree, /--kb-folder-indent-max:\s*4/)
  assert.match(tree, /min\(var\(--kb-folder-depth/)
  assert.doesNotMatch(tree, /kb-folder-row__more-placeholder/)
})

test('only real folders expose a rename affordance', () => {
  assert.match(tree, /v-if="canEdit && row\.kind === 'folder'"/)
  assert.match(tree, /popup-menu-item/)
  assert.match(tree, /onFolderMenuRename/)
})

// Picking a folder is a small, reversible action, so it stays a popup: in the row
// menu it is another level of the menu that is already open, and in the batch bar
// it hangs off the button. Neither should escalate to a modal dialog.
test('the folder picker is a popup rather than a modal', () => {
  assert.match(cardView, /folderPickerItemId === item\.id/)
  assert.match(listView, /folderPickerItemId === item\.id/)
  assert.match(batchBar, /<t-popup[^>]*v-model:visible="folderPickerVisible"/)
  for (const source of [cardView, listView, batchBar]) {
    assert.doesNotMatch(source, /MoveToFolderDialog/)
  }
})

// The folder picker must render ahead of the normal action menu; otherwise
// moveMenuMode === 'normal' keeps the menu visible and clicks look dead.
test('the folder picker wins over the normal action menu', () => {
  for (const source of [cardView, listView]) {
    const pickerIdx = source.indexOf('v-if="folderPickerItemId === item.id"')
    const normalIdx = source.indexOf('v-else-if="moveMenuMode === \'normal\'"')
    assert.ok(pickerIdx >= 0)
    assert.ok(normalIdx >= 0)
    assert.ok(pickerIdx < normalIdx)
  }
})

test('folder navigation cancels stale card hover popovers', () => {
  assert.match(cardView, /if \(!cardElement\.isConnected/)
  assert.match(cardView, /watch\(\(\) => props\.items, dismissCardPopover\)/)
  assert.match(cardView, /const onOpenFolder = \(path: string\)[\s\S]*?dismissCardPopover\(\)[\s\S]*?emit\('open-folder', path\)/)
  assert.match(cardView, /@click\.stop="onOpenFolder\(item\.folder_path\)"/)
})

// The counts the sidebar shows come from the connector-produced folder tree, so
// they have to be rendered for real: a folder whose documents all sit in
// sub-folders used to count 0 here, which read as "empty". The rendered HTML is
// the assertion, not the template source, so a wrong binding cannot pass.
let server: ViteDevServer
let KbFolderTree: Component
before(async () => {
  server = await createServer({
    configFile: false,
    optimizeDeps: { noDiscovery: true, entries: [] },
    plugins: [vue()],
    resolve: { alias: { '@': fileURLToPath(new URL('../../../', import.meta.url)) } },
    server: { middlewareMode: true },
    appType: 'custom',
  })
  KbFolderTree = (await server.ssrLoadModule('/src/views/knowledge/components/KbFolderTree.vue')).default
})
after(async () => { await server?.close() })

// Mirrors the deployed DingTalk-synced knowledge base: multi-level folders, so
// the direct document count of a parent is 0 while its subtree holds 5.
const folderTree = {
  root_document_count: 3,
  total_document_count: 17,
  folders: [
    { path: '培训材料库', name: '培训材料库', document_count: 3, total_count: 17 },
    {
      path: '天诛渗透攻防',
      name: '天诛渗透攻防',
      document_count: 0,
      total_count: 5,
      children: [
        { path: '天诛渗透攻防/FAQ', name: 'FAQ', document_count: 2, total_count: 2 },
        { path: '天诛渗透攻防/售后', name: '售后', document_count: 2, total_count: 2 },
        { path: '天诛渗透攻防/2024使用培训视频', name: '2024使用培训视频', document_count: 1, total_count: 1 },
      ],
    },
  ],
}

async function renderTree(props: Record<string, unknown>): Promise<string> {
  const app = createSSRApp({ render: () => h(KbFolderTree, props) })
  app.use(createI18n({ legacy: false, locale: 'en-US', messages: { 'en-US': en } }))
  // The TDesign widgets are registered globally by the app; empty shells keep
  // the rendered row markup readable here.
  for (const name of ['t-icon', 't-tooltip', 't-popup', 't-skeleton']) {
    app.component(name, defineComponent({ setup: (_, { slots }) => () => h('span', slots.default?.()) }))
  }
  return renderToString(app)
}

/** Markup of one rendered row, isolated from the rows around it. */
function rowHtml(html: string, marker: string): string {
  const starts = [...html.matchAll(/<div class="[^"]*kb-folder-row[^"]*"/g)].map((match) => match.index!)
  const row = starts
    .map((start, index) => html.slice(start, starts[index + 1]))
    .find((markup) => markup.includes(marker))
  assert.ok(row, `row ${marker} should render`)
  return row
}

test('a folder row shows the recursive document count', async () => {
  const html = await renderTree({ tree: folderTree, selectedPath: '天诛渗透攻防' })
  const row = rowHtml(html, 'title="天诛渗透攻防"')
  assert.match(row, /kb-folder-row__count[^>]*>5</)
  assert.doesNotMatch(row, /kb-folder-row__count[^>]*>0</)
  // The tooltip keeps the direct/recursive split visible, so the number is not
  // mistaken for the folder's own contents.
  assert.match(row, /0 documents in this folder; 5 including subfolders/)
})

test('the root row shows the knowledge base total, not the root-level count', async () => {
  const html = await renderTree({ tree: folderTree, selectedPath: '' })
  const row = rowHtml(html, 'is-root')
  assert.match(row, /kb-folder-row__count[^>]*>17</)
  assert.doesNotMatch(row, /kb-folder-row__count[^>]*>3</)
})
