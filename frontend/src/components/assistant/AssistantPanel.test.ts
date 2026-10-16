import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import * as pinia from 'pinia'
import ts from 'typescript'

const filename = fileURLToPath(new URL('./AssistantPanel.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const panelScript = compileScript(descriptor, { id: 'assistant-panel-test', inlineTemplate: true }).content
const storeScript = readFileSync(new URL('../../stores/assistant.ts', import.meta.url), 'utf8')
const groupingScript = readFileSync(new URL('../sessionGrouping.ts', import.meta.url), 'utf8')
const assistantApiScript = readFileSync(new URL('../../api/assistant.ts', import.meta.url), 'utf8')

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

async function fixture(
  createSession: (data?: any) => Promise<any> = async () => ({ data: { id: 'new-session' } }),
  workspaceConfig = { display_name: '', max_access_level: 'confirm' },
  api: Record<string, (...args: any[]) => Promise<any>> = {},
) {
  const auth = vue.reactive({ user: { id: 'user-a' }, effectiveTenantId: 1 })
  const assistantApi = evaluate(assistantApiScript, {
    '@/utils/request': { get: async () => ({ success: true, data: workspaceConfig }), put: assert.fail },
  })
  const sessionStorage = storage()
  sessionStorage.setItem('weknora:assistant-session:user-a:2', 'workspace-b-session')
  sessionStorage.setItem('weknora:assistant-session:user-b:1', 'user-b-session')
  const localStorage = storage()
  const stores = evaluate(storeScript, {
    vue, pinia, './auth': { useAuthStore: () => auth }, '@/api/assistant': assistantApi,
  }, { sessionStorage, localStorage })
  const storeRoot = pinia.createPinia()
  pinia.setActivePinia(storeRoot)
  const assistant = stores.useAssistantStore()
  const mounted: Array<{ sessionId: string; query: string }> = []
  const sent: Array<{ sessionId: string; query: string; scope: any }> = []
  const menuEntries: unknown[] = []
  const errors: string[] = []
  const dialogs: any[] = []
  const listCalls: any[][] = []
  const deleted: string[] = []
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
  const component = evaluate(panelScript, {
    vue,
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': {
      MessagePlugin: { error: (message: string) => errors.push(message) },
      DialogPlugin: { confirm: (options: any) => { dialogs.push(options); return { destroy() {} } } },
    },
    '@/views/chat/index.vue': ChatView,
    '@/components/Input-field.vue': emptyComponent,
    '@/components/PanelResizeHandle.vue': emptyComponent,
    './AssistantContextChip.vue': emptyComponent,
    './AssistantSelectionChip.vue': emptyComponent,
    './AssistantAccessPicker.vue': emptyComponent,
    '@/api/chat': {
      createSessions: createSession,
      getSession: api.getSession || (async () => ({ success: true, data: { kind: 'assistant' } })),
      getSessionsList: async (...args: any[]) => {
        listCalls.push(args)
        return api.getSessionsList ? api.getSessionsList(...args) : { success: true, data: [], total: 0 }
      },
    },
    '@/components/sessionGrouping': evaluate(groupingScript, {}),
    '@/api/agent': { BUILTIN_WEKNORA_ASSISTANT_ID: 'builtin-weknora-assistant' },
    '@/stores/menu': { useMenuStore: () => ({ updataMenuChildren: (entry: unknown) => menuEntries.push(entry) }) },
    '@/stores/auth': { useAuthStore: () => auth },
    '@/stores/assistant': stores,
    '@/components/sessionMutations': {
      SESSION_MUTATION_EVENT: 'session-mutation',
      removeSession: async (id: string) => {
        if (api.removeSession) await api.removeSession(id)
        deleted.push(id)
      },
    },
  }, { window: { addEventListener() {}, removeEventListener() {} } }).default
  type HostNode = { children: HostNode[]; parent?: HostNode; tag?: string; props: Record<string, any>; style: Record<string, any>; text?: string }
  const node = (tag?: string): HostNode => ({ children: [], tag, props: {}, style: {} })
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
    remove, setElementText(node, text) { node.text = text }, setText(node, text) { node.text = text },
    patchProp(node, key, _prev, next) { node.props[key] = next },
    parentNode: child => child.parent ?? null,
    nextSibling: child => child.parent?.children[child.parent.children.indexOf(child) + 1] ?? null,
  })
  const app = renderer.createApp(component)
  app.use(storeRoot)
  // The panel keeps its content mounted while hidden, which is essential to this regression.
  app.component('t-icon', emptyComponent)
  app.component('t-loading', emptyComponent)
  app.component('t-tooltip', vue.defineComponent({ inheritAttrs: false, setup: (_, { slots }) => () => slots.default?.() }))
  app.component('t-button', vue.defineComponent({ setup: (_, { slots }) => () => vue.h('button', slots.default?.()) }))
  const root = node()
  app.mount(root)
  const findAll = (predicate: (node: HostNode) => boolean): HostNode[] => {
    const found: HostNode[] = []
    const walk = (node: HostNode) => {
      if (predicate(node)) found.push(node)
      node.children.forEach(walk)
    }
    walk(root)
    return found
  }
  const click = async (label: string) => {
    const button = findAll(node => node.props['aria-label'] === label)[0]
    assert.ok(button, `missing button ${label}`)
    button.props.onClick()
    await flush()
  }
  await flush()
  return { auth, assistant, mounted, sent, menuEntries, sessionStorage, localStorage, findAll, click, listCalls, deleted, dialogs, errors, close: () => app.unmount() }
}

test('layout and width persist, with the width kept within bounds', async () => {
  const f = await fixture()
  try {
    assert.equal(f.assistant.layout, 'docked')
    f.assistant.setLayout('floating')
    assert.equal(f.localStorage.getItem('weknora:assistant-layout'), 'floating')
    f.assistant.setWidth(10_000)
    assert.equal(f.assistant.width, 760)
    f.assistant.setWidth(12)
    assert.equal(f.assistant.width, 360)
    assert.equal(f.localStorage.getItem('weknora:assistant-width'), '360')
  } finally { f.close() }
})

test('a run asks for the chosen access level, capped by the workspace ceiling', async () => {
  const f = await fixture(undefined, { display_name: ' Kora ', max_access_level: 'confirm' })
  try {
    assert.equal(f.assistant.displayName, 'Kora')
    f.assistant.setAccessLevel('auto_write')
    assert.equal(f.assistant.effectiveAccessLevel, 'confirm')
    f.assistant.open({ query: 'Clean up the wiki' })
    await flush()
    assert.equal(f.sent[0].scope.assistantAccess, 'confirm')
    f.assistant.setWorkspaceConfig({ display_name: '', max_access_level: 'auto_write' })
    assert.equal(f.assistant.effectiveAccessLevel, 'auto_write', 'raising the ceiling restores the choice')
  } finally { f.close() }
})

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
      assert.equal(f.sent.length, 1, 'reopening the retained panel must not resend either')
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

test('assistant creation marks the kind and never inserts a sidebar entry', async () => {
  const requests: any[] = []
  const f = await fixture(async body => {
    requests.push(body)
    return { data: { id: 'assistant-session' } }
  })
  try {
    f.assistant.open({ query: 'Help me' })
    await flush()
    assert.equal(requests.length, 1)
    assert.equal(requests[0].kind, 'assistant')
    assert.equal(f.menuEntries.length, 0)
    assert.equal(f.sent[0].sessionId, 'assistant-session')
  } finally { f.close() }
})

test('history loads, paginates, highlights and switches the current session', async () => {
  const f = await fixture(undefined, undefined, {
    getSessionsList: async page => ({
      success: true, total: 21,
      data: Array.from({ length: page === 1 ? 20 : 1 }, (_, i) => ({
        id: `history-${(page - 1) * 20 + i}`, title: '', updated_at: new Date().toISOString(),
      })),
    }),
  })
  try {
    f.assistant.setSession('history-0')
    await flush()
    await f.click('assistant.history')
    assert.deepEqual(f.listCalls[0], [1, 20, undefined, 'assistant'])
    assert.equal(f.mounted.length, 1, 'history keeps the current transcript mounted')
    assert.equal(f.findAll(n => n.props.class?.includes('is-current')).length, 1)
    assert.equal(f.findAll(n => n.text === 'assistant.untitledConversation').length, 20)
    const loadMore = f.findAll(n => n.tag === 'button' && n.props.onClick && n.props.variant === 'text')[0]
    loadMore.props.onClick()
    await flush()
    assert.deepEqual(f.listCalls[1], [2, 20, undefined, 'assistant'])
    const rows = f.findAll(n => n.props.class === 'assistant-history__select')
    assert.equal(rows.length, 21)
    rows[20].props.onClick()
    await flush()
    assert.equal(f.assistant.sessionId, 'history-20')
    assert.equal(f.mounted.at(-1)?.sessionId, 'history-20')
    assert.equal(f.sent.length, 0)
  } finally { f.close() }
})

test('deleting the current history session requires confirmation and returns to welcome', async () => {
  const f = await fixture(undefined, undefined, {
    getSessionsList: async () => ({ success: true, total: 1, data: [{ id: 'current', title: 'Current' }] }),
  })
  try {
    f.assistant.setSession('current')
    await flush()
    await f.click('assistant.history')
    await f.click('common.delete')
    assert.equal(f.deleted.length, 0)
    await f.dialogs[0].onConfirm()
    await flush()
    assert.deepEqual(f.deleted, ['current'])
    assert.equal(f.assistant.sessionId, '')
    assert.equal(f.sessionStorage.getItem('weknora:assistant-session:user-a:1'), null)
    assert.equal(f.findAll(n => n.props.class === 'assistant-welcome').length, 1)
  } finally { f.close() }
})

for (const status of [403, 404, 500]) {
  test(`stored session load with HTTP ${status} handles stale ids without mounting a transcript`, async () => {
    const f = await fixture(undefined, undefined, {
      getSession: async () => { throw { $httpStatus: status } },
    })
    try {
      f.auth.effectiveTenantId = 2
      await flush()
      assert.equal(f.assistant.sessionId, status === 500 ? 'workspace-b-session' : '')
      assert.equal(f.mounted.length, 0)
      if (status === 500) assert.equal(f.findAll(n => n.props.role === 'alert').length, 1)
    } finally { f.close() }
  })
}

test('stored ordinary session is rejected instead of mounting it as assistant history', async () => {
  const f = await fixture(undefined, undefined, {
    getSession: async () => ({ success: true, data: { id: 'workspace-b-session', kind: '' } }),
  })
  try {
    f.auth.effectiveTenantId = 2
    await flush()
    assert.equal(f.assistant.sessionId, '')
    assert.equal(f.sessionStorage.getItem('weknora:assistant-session:user-a:2'), null)
    assert.equal(f.mounted.length, 0)
  } finally { f.close() }
})

test('history distinguishes empty and failed loads and retries the same page', async () => {
  let failed = true
  const f = await fixture(undefined, undefined, {
    getSessionsList: async () => {
      if (failed) throw new Error('offline')
      return { success: true, total: 0, data: [] }
    },
  })
  try {
    await f.click('assistant.history')
    assert.equal(f.findAll(n => n.props.role === 'alert').length, 1)
    failed = false
    f.findAll(n => n.tag === 'button' && n.props.variant === 'text')[0].props.onClick()
    await flush()
    assert.equal(f.listCalls[1][0], 1)
    assert.equal(f.findAll(n => n.text === 'assistant.historyEmpty').length, 1)
  } finally { f.close() }
})

test('a history response from the previous workspace is discarded', async () => {
  let resolve!: (value: any) => void
  const f = await fixture(undefined, undefined, {
    getSessionsList: () => new Promise(done => { resolve = done }),
  })
  try {
    await f.click('assistant.history')
    f.auth.effectiveTenantId = 2
    await flush()
    resolve({ success: true, total: 1, data: [{ id: 'private' }] })
    await flush()
    assert.equal(f.findAll(n => n.props.class === 'assistant-history__select').length, 0)
    assert.equal(f.assistant.sessionId, 'workspace-b-session')
  } finally { f.close() }
})

test('failed history deletion retains the conversation and reports the error', async () => {
  const f = await fixture(undefined, undefined, {
    getSessionsList: async () => ({ success: true, total: 1, data: [{ id: 'current' }] }),
    removeSession: async () => { throw new Error('offline') },
  })
  try {
    f.assistant.setSession('current')
    await flush()
    await f.click('assistant.history')
    await f.click('common.delete')
    await f.dialogs[0].onConfirm()
    await flush()
    assert.equal(f.assistant.sessionId, 'current')
    assert.deepEqual(f.errors, ['chat.deleteSessionFailed'])
    assert.equal(f.findAll(n => n.props.class === 'assistant-history__select').length, 1)
  } finally { f.close() }
})

test('a pending delete confirmation cannot delete a session after switching workspaces', async () => {
  const f = await fixture(undefined, undefined, {
    getSessionsList: async () => ({ success: true, total: 1, data: [{ id: 'old' }] }),
  })
  try {
    await f.click('assistant.history')
    await f.click('common.delete')
    f.auth.effectiveTenantId = 2
    await flush()
    await f.dialogs[0].onConfirm()
    assert.deepEqual(f.deleted, [])
    assert.equal(f.assistant.sessionId, 'workspace-b-session')
  } finally { f.close() }
})

test('agent layers merge, dismissal follows the agent id, and the payload has no KB', async () => {
  const f = await fixture()
  try {
    const owner = Symbol()
    const nested = Symbol()
    f.assistant.publishPageContext(owner, { agentId: 'agent-a', agentName: 'A' })
    f.assistant.publishPageContext(nested, { agentName: 'Edited' })
    assert.equal(f.assistant.pageContext.agentName, 'Edited')
    f.assistant.retractPageContext(nested)
    assert.equal(f.assistant.pageContext.agentName, 'A')
    f.assistant.dismissPageContext()
    assert.equal(f.assistant.contextAttached, false)
    f.assistant.publishPageContext(owner, { agentId: 'agent-b', agentName: 'B' })
    assert.equal(f.assistant.contextAttached, true)
    f.assistant.open({ query: 'Check this agent' })
    await flush()
    assert.equal(f.sent[0].scope.pageContext.agent_id, 'agent-b')
    assert.equal(f.sent[0].scope.knowledgeBaseIds.length, 0)
    assert.equal(f.sent[0].scope.pageContext.knowledge_base_id, undefined)
    f.assistant.publishPageContext(nested, { knowledgeBaseId: 'kb-a' })
    assert.equal(f.assistant.pageContext.agentId, 'agent-b')
    assert.equal(f.assistant.pageContext.knowledgeBaseId, 'kb-a')
    f.assistant.retractPageContext(owner)
    assert.equal(f.assistant.pageContext.knowledgeBaseId, 'kb-a')
    f.assistant.retractPageContext(nested)
    assert.equal(f.assistant.pageContext, null)
  } finally { f.close() }
})

test('settings and chat pages send their resources without widening the search scope', async () => {
  const f = await fixture()
  try {
    const owner = Symbol()
    f.assistant.publishPageContext(owner, { page: 'chat', chatSessionId: 'chat-1', messageId: 'msg-1' })
    f.assistant.open({ query: 'Why this answer?' })
    await flush()
    assert.equal(f.sent.length, 1)
    assert.deepEqual(Array.from(f.sent[0].scope.knowledgeBaseIds), [])
    assert.equal(JSON.stringify(f.sent[0].scope.pageContext),
      JSON.stringify({ page: 'chat', session_id: 'chat-1', message_id: 'msg-1' }))

    f.assistant.publishPageContext(owner, { page: 'settings_models', section: 'chat', modelId: 'm-1', messageId: 'stale' })
    assert.ok(f.assistant.pageContext)
    f.assistant.open({ query: 'Check it' })
    await flush()
    assert.equal(JSON.stringify(f.sent.at(-1)?.scope.pageContext),
      JSON.stringify({ page: 'settings_models', section: 'chat', model_id: 'm-1' }),
      'a message without its session is not sent')

    f.assistant.publishPageContext(owner, { section: 'tools' })
    assert.equal(f.assistant.pageContext, null, 'a section alone names no page')
  } finally { f.close() }
})

test('the open dialog and the selection go with the message, the selection only once', async () => {
  const f = await fixture()
  try {
    const owner = Symbol()
    f.assistant.publishPageContext(owner, { knowledgeBaseId: 'kb-a', knowledgeBaseName: 'KB' })
    f.assistant.setPageView('Edit Markdown')
    f.assistant.setPageSelection({ text: 'Run setup.', field: 'Content', ref: 'e2', startLine: 7, endLine: 7 })
    f.assistant.open({ query: 'Polish this' })
    await flush()
    assert.equal(JSON.stringify(f.sent[0].scope.pageContext), JSON.stringify({
      knowledge_base_id: 'kb-a', view: 'Edit Markdown',
      selection: { text: 'Run setup.', field: 'Content', ref: 'e2', start_line: 7, end_line: 7 },
    }))
    assert.equal(f.assistant.pageSelection, null, 'the selection is used up by the message')

    f.assistant.retractPageContext(owner)
    f.assistant.setPageView('')
    f.assistant.setPageSelection({ text: 'page text' })
    f.assistant.open({ query: 'Explain' })
    await flush()
    assert.equal(JSON.stringify(f.sent.at(-1)?.scope.pageContext), JSON.stringify({ selection: { text: 'page text' } }),
      'a selection alone is a page context')

    f.assistant.setPageView('Edit Markdown')
    f.assistant.dismissPageContext()
    assert.equal(f.assistant.contextAttached, false)
    f.assistant.setPageView('Another dialog')
    assert.equal(f.assistant.contextAttached, true, 'a new dialog brings the context back')
  } finally { f.close() }
})

test('a page that publishes nothing still sends its route title', async () => {
  const f = await fixture()
  try {
    f.assistant.setPageTitle('Agents')
    assert.equal(f.assistant.contextAttached, true)
    f.assistant.open({ query: 'What is here?' })
    await flush()
    assert.equal(JSON.stringify(f.sent.at(-1)?.scope.pageContext), JSON.stringify({ title: 'Agents' }))

    f.assistant.dismissPageContext()
    assert.equal(f.assistant.contextAttached, false)
    f.assistant.setPageTitle('Toolbox')
    assert.equal(f.assistant.contextAttached, true, 'another page brings it back')
  } finally { f.close() }
})

test('ask sends into the open conversation and starts one when there is none', async () => {
  const f = await fixture()
  try {
    f.assistant.ask('First')
    await flush()
    assert.equal(f.sent.at(-1)?.query, 'First')
    assert.equal(f.assistant.queuedMessage, null)
    f.assistant.ask('Second')
    assert.equal(f.assistant.queuedMessage?.query, 'Second', 'the open conversation sends it')
    assert.equal(f.assistant.takeQueuedMessage()?.query, 'Second')
    assert.equal(f.assistant.queuedMessage, null)
  } finally { f.close() }
})
