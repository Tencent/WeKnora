import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, defineComponent, h, markRaw, nextTick, reactive, ref, toRefs } from 'vue'

const require = createRequire(import.meta.url)
const flush = async () => {
  await new Promise(resolve => setImmediate(resolve))
  await nextTick()
}

type Host = {
  tag: string
  props: Record<string, any>
  children: Host[]
  parent: Host | null
  addEventListener(): void
  querySelector(): null
}

async function fixture(options: { kbId?: string; unsupported?: boolean; missingKb?: boolean } = {}) {
  const route = reactive({ fullPath: '/platform/creatChat', params: { kbId: options.kbId || '' } })
  const pushed: unknown[] = [], requests: string[] = [], recent: string[] = [], events: any[] = [], chats: unknown[][] = []
  const store = reactive({
    open: true, initialQuery: '', recentQueries: [] as string[],
    closePalette() { this.open = false },
    openPalette() { this.open = true },
    pushRecent(query: string) { recent.push(query) }, clearRecent() {},
  })
  const chunks = [
    { id: 'taxi', content: 'Search snippet for taxis', match_type: 'keyword', score: 1 },
    { id: 'train', content: 'Search snippet for trains', match_type: 'keyword', score: 0.9 },
  ]
  const group = { knowledgeId: 'handbook', kbId: options.missingKb ? '' : 'travel-kb', title: 'Travel handbook', kbName: 'Travel', chunks }
  const query = ref('travel')
  const search = {
    query, loading: ref(false), hasSearched: ref(true), searchFailures: ref([]), retrySearch() {},
    fileGroups: ref([group]), messageGroups: ref([]), kbMatches: ref([]), knowledgeBases: ref([]),
    agentMatches: ref([]), sessionMatches: ref([]), totalChunks: ref(2), totalMessages: ref(0), clearResults() {},
  }
  let preview: any = null
  const Preview = defineComponent({
    props: { knowledgeId: String, locate: Object },
    setup(props) { preview = props; return () => h('preview') },
  })
  const passSlots = defineComponent({
    setup(_props, { slots }) { return () => h('group', slots.default?.()) },
  })
  const ResultItem = defineComponent({
    props: { index: Number }, emits: ['primary', 'hover'],
    setup(props, { emit, slots }) {
      return () => h('result', { index: props.index, onClick: () => emit('primary') }, slots.title?.())
    },
  })
  const router = { push: (target: unknown) => pushed.push(target), resolve: () => ({ href: '/knowledge' }) }
  const window = { addEventListener() {}, removeEventListener() {}, dispatchEvent: (event: any) => events.push(event) }
  class CustomEvent {
    type: string
    detail: unknown
    constructor(type: string, options: { detail: unknown }) { this.type = type; this.detail = options.detail }
  }
  function component(filename: string): any {
    const { descriptor } = parse(readFileSync(new URL(filename, import.meta.url), 'utf8'))
    const compiled = ts.transpileModule(compileScript(descriptor, {
      id: filename, inlineTemplate: true,
    }).content, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText
    const exports: any = {}
    runInNewContext(compiled, {
      exports, console, window, CustomEvent,
      require(name: string) {
        if (name === 'vue') return require('vue')
        if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
        if (name === 'vue-router') return { useRouter: () => router, useRoute: () => route }
        if (name === 'pinia') return { storeToRefs: () => toRefs(store) }
        if (name === '@/stores/commandPalette') return { useCommandPaletteStore: () => store }
        if (name === '@/stores/auth') return { useAuthStore: () => ({ hasRole: () => false }) }
        if (name === '@/stores/deploymentCapabilities') {
          return { useDeploymentCapabilitiesStore: () => ({ isSupported: () => true }) }
        }
        if (name === './GlobalCommandPalette/useSearch') return { useCmdkSearch: () => search }
        if (name === './GlobalCommandPalette/useHighlight') return { highlightText: (text: string) => text }
        if (name === './GlobalCommandPalette/useStartChat') return { useStartChat: () => ({ startChat: (...args: unknown[]) => chats.push(args) }) }
        if (name === './GlobalCommandPalette/commands') return { buildCommands: () => [], filterCommands: () => [] }
        if (name === './GlobalCommandPalette/ResultItem.vue') return ResultItem
        if (name === './GlobalCommandPalette/ResultGroup.vue') return passSlots
        if (name === './ChatReferenceSourceView.vue') return component('./ChatReferenceSourceView.vue')
        if (name === '@/components/document-preview.vue') return Preview
        if (name === '@/api/knowledge-base/index') return {
          getKnowledgeDetails: async (id: string) => {
            requests.push(`document:${id}`)
            return { data: { type: 'file', source: 'upload', file_type: options.unsupported ? 'zip' : 'md', file_name: options.unsupported ? 'Handbook.zip' : 'Handbook.md', file_path: 'handbook' } }
          },
          getChunkByIdOnly: async (id: string) => {
            requests.push(`chunk:${id}`)
            return { data: { knowledge_id: 'handbook', chunk_type: 'text', content_revision: 0, content: `Current source passage for ${id}`, source_locators: null } }
          },
        }
        if (name.startsWith('@/utils/')) return require(`../utils/${name.slice('@/utils/'.length)}.ts`)
        if (name.endsWith('.vue')) return { render: () => null }
        throw new Error(`Unexpected import: ${name}`)
      },
    })
    return exports
  }

  const node = (tag = ''): Host => markRaw({ tag, props: {}, children: [], parent: null, addEventListener() {}, querySelector: () => null })
  const renderer = createRenderer<Host, Host>({
    createElement: tag => node(tag), createText: () => node('#text'), createComment: () => node('#comment'),
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
    setElementText() {}, setText() {}, patchProp(el, key, _old, value) { el.props[key] = value },
    parentNode: el => el.parent, nextSibling: el => el.parent?.children[el.parent.children.indexOf(el) + 1] || null,
  })
  const app = renderer.createApp(component('./GlobalCommandPalette.vue').default)
  app.component('t-dialog', defineComponent({
    props: { visible: Boolean },
    setup(props, { slots }) { return () => props.visible ? h('dialog', slots.default?.()) : null },
  }))
  app.component('t-drawer', defineComponent({
    emits: ['close'],
    setup(_props, { emit, slots }) { return () => h('drawer', [h('close-drawer', { onClick: () => emit('close') }), slots.default?.()]) },
  }))
  app.component('t-popup', passSlots)
  for (const name of ['t-icon', 't-loading', 't-button']) app.component(name, { render: () => null })
  const root = node('root')
  app.mount(root)
  await flush()
  const descendants = (el: Host): Host[] => [el, ...el.children.flatMap(descendants)]
  const find = (tag: string, index?: number) => {
    const result = descendants(root).find(el => el.tag === tag && (index === undefined || el.props.index === index))
    assert.ok(result, `expected ${tag} ${index ?? ''}`)
    return result
  }
  const click = async (tag: string, index?: number) => { find(tag, index).props.onClick(); await flush() }
  const key = async (key: string, cmd = false) => {
    find('dialog').children.find(el => el.props.class === 'cmdk')!.props.onKeydown({ key, metaKey: cmd, preventDefault() {} })
    await flush()
  }
  const open = async () => { store.initialQuery = 'travel'; store.open = true; await flush() }
  return { store, route, requests, pushed, recent, events, chats, find, click, key, open, preview: () => preview, hasDrawer: () => descendants(root).some(el => el.tag === 'drawer'), dispose: () => app.unmount() }
}

test('mouse and keyboard open different chunks of the same document using current source evidence', async t => {
  const f = await fixture()
  t.after(f.dispose)
  await f.click('result', 0)
  assert.equal(f.store.open, false)
  assert.equal(f.hasDrawer(), true)
  assert.equal(f.preview().knowledgeId, 'handbook')
  assert.equal(f.preview().locate.sourceMarkdown, 'Current source passage for taxi')
  assert.ok(f.requests.includes('chunk:taxi'))
  assert.deepEqual(f.pushed, [])
  await f.click('close-drawer')
  await f.open()
  await f.key('ArrowDown')
  await f.key('Enter')
  assert.equal(f.preview().locate.sourceMarkdown, 'Current source passage for train')
  assert.ok(f.requests.includes('chunk:train'))
  assert.deepEqual(f.recent, ['travel', 'travel'])
})

for (const kbId of ['', 'travel-kb']) {
  test(`unsupported sources retain document navigation${kbId ? ' on the current knowledge base' : ''}`, async t => {
    const f = await fixture({ kbId, unsupported: true })
    t.after(f.dispose)
    await f.click('result', 0)
    assert.equal(f.hasDrawer(), false)
    if (kbId) {
      assert.equal(f.events.length, 1)
      assert.equal(f.events[0].type, 'weknora:open-knowledge')
      assert.equal(f.events[0].detail.kbId, 'travel-kb')
      assert.equal(f.events[0].detail.knowledgeId, 'handbook')
      assert.deepEqual(f.pushed, [])
    } else {
      assert.equal(f.pushed.length, 1)
      const target = f.pushed[0] as any
      assert.equal(target.path, '/platform/knowledge-bases/travel-kb')
      assert.equal(target.query.knowledge_id, 'handbook')
    }
  })
}

test('route changes and reopening the palette dismiss an existing source drawer', async t => {
  const f = await fixture()
  t.after(f.dispose)
  await f.click('result', 0)
  f.route.fullPath = '/platform/knowledge-bases/other'
  await flush()
  assert.equal(f.hasDrawer(), false)
  await f.open()
  await f.click('result', 0)
  assert.equal(f.hasDrawer(), true)
  await f.open()
  assert.equal(f.hasDrawer(), false)
})

test('Command-Enter keeps starting a chat scoped to the document', async t => {
  const f = await fixture()
  t.after(f.dispose)
  await f.key('Enter', true)
  assert.equal(f.hasDrawer(), false)
  assert.equal(f.requests.length, 0)
  assert.equal(f.chats.length, 1)
  assert.equal(f.chats[0][0], 'travel')
  assert.equal((f.chats[0][1] as string[])[0], 'travel-kb')
  assert.equal((f.chats[0][2] as string[])[0], 'handbook')
})

test('a result without a knowledge base closes the palette without opening a source', async t => {
  const f = await fixture({ missingKb: true })
  t.after(f.dispose)
  await f.click('result', 0)
  assert.equal(f.hasDrawer(), false)
  assert.equal(f.requests.length, 0)
  assert.deepEqual(f.pushed, [])
})
