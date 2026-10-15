import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import * as pinia from 'pinia'
import ts from 'typescript'

const filename = fileURLToPath(new URL('./AssistantDrawer.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const drawerScript = compileScript(descriptor, { id: 'assistant-drawer-test', inlineTemplate: true }).content
const storeScript = readFileSync(new URL('../../stores/assistant.ts', import.meta.url), 'utf8')

function evaluate(source: string, dependencies: Record<string, unknown>, globals = {}) {
  const exports: any = {}
  runInNewContext(ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText, {
    exports, console, ...globals,
    require(name: string) {
      if (name in dependencies) return dependencies[name]
      throw new Error(`Unexpected component dependency: ${name}`)
    },
  })
  return exports
}

function storage() {
  const values = new Map<string, string>()
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) },
    removeItem: (key: string) => { values.delete(key) },
  }
}

const flush = async () => {
  await new Promise(resolve => setImmediate(resolve))
  await vue.nextTick()
}

async function fixture(createSession = async () => ({ data: { id: 'new-session' } })) {
  const auth = vue.reactive({ user: { id: 'user-a' }, effectiveTenantId: 1 })
  const sessionStorage = storage()
  sessionStorage.setItem('weknora:assistant-session:user-a:2', 'workspace-b-session')
  sessionStorage.setItem('weknora:assistant-session:user-b:1', 'user-b-session')
  const stores = evaluate(storeScript, {
    vue, pinia, './auth': { useAuthStore: () => auth },
  }, { sessionStorage, localStorage: storage() })
  const storeRoot = pinia.createPinia()
  pinia.setActivePinia(storeRoot)
  const assistant = stores.useAssistantStore()
  const mounted: Array<{ sessionId: string; query: string }> = []
  const sent: Array<{ sessionId: string; query: string; scope: any }> = []
  const menuEntries: unknown[] = []
  const emptyComponent = vue.defineComponent({ inheritAttrs: false, render: () => null })
  const ChatView = vue.defineComponent({
    inheritAttrs: false,
    props: ['session_id', 'initialQuery', 'hostScope'],
    setup(props) {
      vue.onMounted(() => {
        mounted.push({ sessionId: props.session_id, query: props.initialQuery })
        // ChatView sends initialQuery on mount and reads hostScope during send.
        if (props.initialQuery) {
          sent.push({ sessionId: props.session_id, query: props.initialQuery, scope: props.hostScope() })
        }
      })
      return () => null
    },
  })
  const component = evaluate(drawerScript, {
    vue,
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { error: assert.fail } },
    '@/views/chat/index.vue': ChatView,
    '@/components/Input-field.vue': emptyComponent,
    './AssistantContextChip.vue': emptyComponent,
    '@/api/chat': { createSessions: createSession },
    '@/api/agent': { BUILTIN_WEKNORA_ASSISTANT_ID: 'builtin-weknora-assistant' },
    '@/stores/menu': { useMenuStore: () => ({ updataMenuChildren: (entry: unknown) => menuEntries.push(entry) }) },
    '@/stores/auth': { useAuthStore: () => auth },
    '@/stores/assistant': stores,
    '@/components/sessionMutations': { SESSION_MUTATION_EVENT: 'session-mutation' },
  }, { window: { addEventListener() {}, removeEventListener() {} } }).default
  type HostNode = { children: HostNode[]; parent?: HostNode }
  const node = (): HostNode => ({ children: [] })
  const remove = (child: HostNode) => {
    if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1)
    child.parent = undefined
  }
  const renderer = vue.createRenderer<HostNode, HostNode>({
    createElement: node, createText: node, createComment: node,
    insert(child, parent, anchor) {
      remove(child)
      const index = anchor ? parent.children.indexOf(anchor) : -1
      parent.children.splice(index < 0 ? parent.children.length : index, 0, child)
      child.parent = parent
    },
    remove, setElementText() {}, setText() {}, patchProp() {},
    parentNode: child => child.parent ?? null,
    nextSibling: child => child.parent?.children[child.parent.children.indexOf(child) + 1] ?? null,
  })
  const app = renderer.createApp(component)
  app.use(storeRoot)
  // TDrawer retains its content when hidden, which is essential to this regression.
  app.component('t-drawer', vue.defineComponent({
    inheritAttrs: false,
    setup: (_, { slots }) => () => slots.default?.(),
  }))
  for (const name of ['t-tooltip', 't-button', 't-icon']) app.component(name, emptyComponent)
  app.mount(node())
  await flush()
  return { auth, assistant, mounted, sent, menuEntries, sessionStorage, close: () => app.unmount() }
}

for (const identity of ['workspace', 'user']) {
  test(`restoring another ${identity}'s session does not resend the old first message`, async () => {
    const f = await fixture()
    try {
      f.assistant.publishPageContext(Symbol(), { knowledgeBaseId: 'kb-a', wikiIssueIds: ['issue-1', 'issue-2'] })
      f.assistant.open({ query: 'Fix this issue', focus: { wikiIssueIds: ['issue-2'] } })
      await flush()
      assert.equal(f.sent.length, 1)
      assert.equal(f.sent[0].query, 'Fix this issue')
      assert.deepEqual(Array.from(f.sent[0].scope.pageContext.wiki_issue_ids), ['issue-2'])
      f.assistant.close()
      if (identity === 'workspace') f.auth.effectiveTenantId = 2
      else f.auth.user = { id: 'user-b' }
      await flush()
      const restored = identity === 'workspace' ? 'workspace-b-session' : 'user-b-session'
      assert.deepEqual(f.mounted.at(-1), { sessionId: restored, query: '' })
      assert.equal(f.sent.length, 1, 'restoring a session must load history instead of sending')
      f.assistant.open()
      await flush()
      assert.equal(f.sent.length, 1, 'reopening the retained drawer must not resend either')
    } finally { f.close() }
  })
}

test('a new conversation still sends its own first message after restoring a session', async () => {
  const f = await fixture()
  try {
    f.auth.effectiveTenantId = 2
    await flush()
    f.assistant.open({ query: 'New request' })
    await flush()
    assert.equal(f.sent.length, 1)
    assert.equal(f.sent[0].sessionId, 'new-session')
    assert.equal(f.sent[0].query, 'New request')
    assert.equal(f.sessionStorage.getItem('weknora:assistant-session:user-a:2'), 'new-session')
  } finally { f.close() }
})

test('session creation finishing after a workspace switch cannot replace the restored session', async () => {
  let resolve!: (value: { data: { id: string } }) => void
  const f = await fixture(() => new Promise(done => { resolve = done }))
  try {
    f.assistant.open({ query: 'Old workspace request' })
    await flush()
    f.auth.effectiveTenantId = 2
    await flush()
    resolve({ data: { id: 'old-workspace-session' } })
    await flush()
    assert.equal(f.assistant.sessionId, 'workspace-b-session')
    assert.equal(f.sessionStorage.getItem('weknora:assistant-session:user-a:2'), 'workspace-b-session')
    assert.equal(f.sent.length, 0)
    assert.equal(f.menuEntries.length, 0)
  } finally { f.close() }
})
