import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick } from 'vue'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./WikiBrowser.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'wiki-backlink-browser-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function page(slug: string, titles: Record<string, string> = {}) {
  return { id: slug, slug, title: slug, content: '', version: 1, in_links: Object.keys(titles), in_link_titles: titles }
}

async function fixture(view: 'browser' | 'graph' = 'browser') {
  const calls: { name: string; args: any[] }[] = []
  const dataCalls: { name: string; args: any[] }[] = []
  const errors: unknown[][] = []
  let response: any = page('first')
  let failure: Error | undefined
  const recordDataCall = (name: string, result: any) => async (...args: any[]) => {
    dataCalls.push({ name, args })
    return result
  }
  const api = {
    getWikiPage: async (...args: any[]) => {
      calls.push({ name: 'getWikiPage', args })
      if (failure) throw failure
      return { data: response }
    },
    updateWikiPage: async (...args: any[]) => {
      calls.push({ name: 'updateWikiPage', args })
      const { in_link_titles, ...updated } = response
      return { data: { ...updated, ...args[2], version: updated.version + 1 } }
    },
    listWikiIssues: recordDataCall('listWikiIssues', []),
    listWikiPages: recordDataCall('listWikiPages', { pages: [], total: 0 }),
    listWikiFolders: recordDataCall('listWikiFolders', { folders: [] }),
    getWikiIndex: recordDataCall('getWikiIndex', { groups: [] }),
    getWikiGraph: recordDataCall('getWikiGraph', {
      data: { nodes: [], edges: [], meta: { mode: 'overview', total: 0, returned: 0, truncated: false } },
    }),
    getWikiStats: recordDataCall('getWikiStats', {}),
  }
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console: { error: (...args: unknown[]) => errors.push(args) },
    setTimeout, clearTimeout, setInterval, clearInterval,
    localStorage: { getItem: () => null, setItem() {} },
    require(name: string) {
      if (name === 'vue' || name === 'marked') return require(name)
      if (name === 'vue-router') return { useRouter: () => ({}), useRoute: () => ({ query: {} }) }
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/stores/assistant') return { useAssistantStore: () => ({ visible: false }) }
      if (name === '@/composables/useAssistantPageContext') return { useAssistantPageContext() {} }
      if (name === '@/utils/assistantDataChange') return { useAssistantDataChange() {} }
      if (name === '@/stores/settings') return { useSettingsStore: () => ({}) }
      if (name === '@/api/wiki') return api
      if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error() {} } }
      // DOM sanitization has separate tests; this fixture exercises the actual
      // component's title resolution and Markdown caller before that boundary.
      if (name === '@/utils/security') return { sanitizeMarkdownHTML: (html: string) => html, hydrateProtectedFileImages: async () => {} }
      if (name.startsWith('./wiki')) return require(`${name}.ts`)
      if (name === '@/utils/markedLiteralTilde') return require('../../../utils/markedLiteralTilde.ts')
      if (name.endsWith('.vue') || ['vue-virtual-scroller', '@/api/knowledge-base', '@/api/chat'].includes(name)) return { default: {} }
      throw new Error(`Unexpected component dependency: ${name}`)
    },
  })
  exports.default.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp(exports.default, { knowledgeBaseId: 'kb-1', view })
  const vm: any = app.mount({})
  await new Promise(resolve => setImmediate(resolve))
  await nextTick()
  return {
    vm, calls, dataCalls, errors,
    respond(value: any) { response = value; failure = undefined },
    fail() { failure = new Error('page lookup failed') },
    close: () => app.unmount(),
  }
}

test('graph view loads graph data without loading the wiki browser sidebar', async () => {
  const f = await fixture('graph')
  try {
    const names = f.dataCalls.map(call => call.name)
    assert.ok(names.includes('getWikiGraph'))
    assert.ok(names.includes('getWikiStats'))
    assert.equal(names.includes('getWikiIndex'), false)
    assert.equal(names.includes('listWikiPages'), false)
    assert.equal(names.includes('listWikiFolders'), false)
  } finally { f.close() }
})

test('browser view still loads its index and sidebar data', async () => {
  const f = await fixture('browser')
  try {
    const names = f.dataCalls.map(call => call.name)
    assert.ok(names.includes('getWikiIndex'))
    assert.ok(names.includes('listWikiPages'))
    assert.ok(names.includes('listWikiFolders'))
  } finally { f.close() }
})

test('page navigation consumes all response titles without a separate title request', async () => {
  const f = await fixture()
  try {
    const titles = Object.fromEntries(Array.from({ length: 2507 }, (_, i) => [`concept/${i}-${'a'.repeat(200)}`, `标题 ${i}`]))
    f.respond(page('first', titles))
    await f.vm.selectPage({ id: 'first', slug: 'first' })
    await nextTick()
    for (const [slug, title] of Object.entries(titles)) assert.equal(f.vm.slugDisplayName(slug), title)
    assert.equal(f.calls.length, 1)
    assert.deepEqual(f.calls[0], { name: 'getWikiPage', args: ['kb-1', 'first'] })
    assert.equal(f.errors.length, 0)
  } finally { f.close() }
})

test('navigation, same-page refresh, history and clearing use the matching page map', async () => {
  const f = await fixture()
  try {
    f.respond(page('first', { 'concept/shared': 'First title' }))
    await f.vm.selectPage({ id: 'first', slug: 'first' })
    f.respond(page('second', { 'concept/shared': 'Second title' }))
    await f.vm.navigateToSlug('second')
    assert.equal(f.vm.slugDisplayName('concept/shared'), 'Second title')
    f.respond(page('second', { 'concept/shared': 'Refreshed title' }))
    await f.vm.refreshSelectedPage()
    assert.equal(f.vm.slugDisplayName('concept/shared'), 'Refreshed title')
    f.vm.goBack()
    assert.equal(f.vm.selectedPage.slug, 'first')
    assert.equal(f.vm.slugDisplayName('concept/shared'), 'First title')
    f.vm.selectedPage = null
    assert.equal(f.vm.slugDisplayName('concept/shared'), 'shared')
    assert.equal(f.errors.length, 0)
  } finally { f.close() }
})

test('Markdown caller handles prototype names, missing titles and explicit labels', async () => {
  const f = await fixture()
  try {
    f.respond(page('current'))
    await f.vm.navigateToSlug('current')
    for (const slug of ['constructor', 'toString', '__proto__', 'hasOwnProperty']) {
      const html = f.vm.renderMarkdown(`[[${slug}]]`)
      assert.ok(html.includes(`data-slug="${slug}"`))
      assert.ok(html.includes(`>${require('marked').marked.parseInline(slug)}</a>`))
      assert.match(f.vm.renderMarkdown(`[[${slug}|Explicit label]]`), />Explicit label<\/a>/)
    }
    f.vm.pages = [{ slug: 'concept/loaded', title: 'Loaded title' }]
    assert.match(f.vm.renderMarkdown('[[concept/loaded]] [[concept/missing]]'), />Loaded title<\/a>.*>missing<\/a>/)
    f.respond(page('current', JSON.parse('{"constructor":"构造函数","__proto__":"原型"}')))
    await f.vm.refreshSelectedPage()
    assert.match(f.vm.renderMarkdown('[[constructor]] [[__proto__]]'), />构造函数<\/a>.*>原型<\/a>/)
    assert.equal(f.errors.length, 0)
  } finally { f.close() }
})

test('saving page edits retains titles omitted from the mutation response', async () => {
  const f = await fixture()
  try {
    f.respond(page('current', { 'concept/unloaded': 'Unloaded title' }))
    await f.vm.navigateToSlug('current')
    await nextTick()
    f.vm.startEditPage()
    f.vm.editForm.title = 'Edited title'
    await f.vm.savePageEdit()
    assert.equal(f.vm.selectedPage.title, 'Edited title')
    assert.equal(f.vm.slugDisplayName('concept/unloaded'), 'Unloaded title')
    assert.equal(f.errors.length, 0)
  } finally { f.close() }
})

test('failed navigation retains the displayed page and reports the failure', async () => {
  const f = await fixture()
  try {
    f.respond(page('first', { 'concept/shared': 'First title' }))
    await f.vm.navigateToSlug('first')
    f.fail()
    await f.vm.navigateToSlug('second')
    assert.equal(f.vm.selectedPage.slug, 'first')
    assert.equal(f.vm.slugDisplayName('concept/shared'), 'First title')
    assert.equal(f.errors.length, 1)
  } finally { f.close() }
})
