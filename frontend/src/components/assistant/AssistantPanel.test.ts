import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import * as pinia from 'pinia'
import ts from 'typescript'
import * as childAgentTabs from '../../composables/useChildAgentTabs'

const filename = fileURLToPath(new URL('./AssistantPanel.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const panelScript = compileScript(descriptor, { id: 'assistant-panel-test', inlineTemplate: true }).content
const storeScript = readFileSync(new URL('../../stores/assistant.ts', import.meta.url), 'utf8')
const groupingScript = readFileSync(new URL('../sessionGrouping.ts', import.meta.url), 'utf8')
const assistantApiScript = readFileSync(new URL('../../api/assistant.ts', import.meta.url), 'utf8')
const dragScript = readFileSync(new URL('../../composables/useFloatingPreviewDrag.ts', import.meta.url), 'utf8')

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
  let workspaceRefreshes = 0
  let workspaceChanged: (() => void) | undefined
  const auth = vue.reactive({ user: { id: 'user-a' }, effectiveTenantId: 1, refreshFromAuthMe: async () => { workspaceRefreshes++; return true } })
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
  const viewport = { innerWidth: 1440, innerHeight: 1000, addEventListener() {}, removeEventListener() {} }
  const route = vue.reactive({ name: '', params: {} as Record<string, string> })
  const floatingDrag = evaluate(dragScript, { vue }, {
    window: viewport,
    ResizeObserver: class { observe() {} disconnect() {} },
  })
  const component = evaluate(panelScript, {
    vue,
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, te: () => true }) },
    'vue-router': { useRoute: () => route },
    'tdesign-vue-next': {
      MessagePlugin: { error: (message: string) => errors.push(message) },
      DialogPlugin: { confirm: (options: any) => { dialogs.push(options); return { destroy() {} } } },
    },
    '@/views/chat/index.vue': ChatView,
    '@/views/chat/components/ChildAgentTabBar.vue': emptyComponent,
    '@/composables/useChildAgentTabs': childAgentTabs,
    '@/composables/useFloatingPreviewDrag': floatingDrag,
    '@/components/Input-field.vue': emptyComponent,
    '@/components/PanelResizeHandle.vue': emptyComponent,
    '@/components/icons/ResourceIcon.vue': emptyComponent,
    './AssistantContextMenu.vue': emptyComponent,
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
    '@/utils/assistantDataChange': { useAssistantDataChange: (callback: () => void, options: { areas: string[] }) => {
      assert.deepEqual(Array.from(options.areas), ['workspace'])
      workspaceChanged = callback
    } },
    '@/components/sessionMutations': {
      SESSION_MUTATION_EVENT: 'session-mutation',
      removeSession: async (id: string) => {
        if (api.removeSession) await api.removeSession(id)
        deleted.push(id)
      },
    },
  }, { window: viewport }).default
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
    querySelector: selector => selector === 'body' ? body : null,
  })
  const app = renderer.createApp(component)
  app.use(storeRoot)
  // The panel keeps its content mounted while hidden, which is essential to this regression.
  app.component('t-icon', emptyComponent)
  app.component('t-loading', emptyComponent)
  app.component('t-tooltip', vue.defineComponent({ inheritAttrs: false, setup: (_, { slots }) => () => slots.default?.() }))
  app.component('t-button', vue.defineComponent({ setup: (_, { slots }) => () => vue.h('button', slots.default?.()) }))
  const body = node('body')
  const root = node('app-root')
  root.parent = body
  body.children.push(root)
  app.mount(root)
  const findAll = (predicate: (node: HostNode) => boolean): HostNode[] => {
    const found: HostNode[] = []
    const walk = (node: HostNode) => {
      if (predicate(node)) found.push(node)
      node.children.forEach(walk)
    }
    walk(body)
    return found
  }
  const click = async (label: string) => {
    const button = findAll(node => node.props['aria-label'] === label)[0]
    assert.ok(button, `missing button ${label}`)
    button.props.onClick()
    await flush()
  }
  await flush()
  return { workspaceChanged: () => workspaceChanged?.(), workspaceRefreshes: () => workspaceRefreshes, route, auth, assistant, mounted, sent, menuEntries, sessionStorage, localStorage, findAll, click, listCalls, deleted, dialogs, errors, close: () => app.unmount() }
}

test('welcome descriptions and questions change with pages and toolbox tabs without starting a conversation', async () => {
  const f = await fixture()
  try {
    f.assistant.open()
    await flush()
    const questions = () => f.findAll(node => node.props.class === 'assistant-starter')
      .map(node => node.text?.replace('assistant.starters.', ''))
    const description = () => f.findAll(node => node.props.class === 'assistant-welcome-desc')[0]?.text
    const general = ['releaseHighlights', 'gettingStarted', 'workflow']
    assert.deepEqual(questions(), general)
    assert.equal(description(), 'assistant.welcomeDesc')
    const pages: Array<[string, Record<string, string>, string, string[]]> = [
      ['knowledgeBaseList', {}, 'knowledgeBases', ['knowledgeOverview', 'knowledgeType', 'knowledgeOrganize']],
      ['knowledgeBaseDetail', {}, 'knowledgeBase', ['knowledgeTopics', 'recentActivity', 'knowledgeGaps']],
      ['artifactLibrary', {}, 'artifacts', ['artifactRecent', 'artifactSources', 'artifactCreate']],
      ['agentList', {}, 'agents', ['agents', 'agentCreate', 'agentChoose']],
      ['organizationList', {}, 'organizations', ['organizationShared', 'organizationJoin', 'organizationCreate']],
      ['automationList', {}, 'automation', ['automationIdeas', 'automationCreate', 'automationOverview']],
      ['automationList', { automationId: 'weekly' }, 'automation', ['automationSummary', 'automationSchedule', 'automationImprove']],
      ['toolbox', { section: 'skills' }, 'skills', ['skillFind', 'skillAdd', 'skillUse']],
      ['toolbox', { section: 'mcp' }, 'mcp', ['mcpFind', 'mcpSetup', 'mcpExamples']],
      ['toolbox', { section: 'browserconnection' }, 'browser', ['browserConnect', 'browserTasks', 'browserReconnect']],
      ['chat', {}, 'chat', ['chatSummary', 'chatCheck', 'chatFollowup']],
    ]
    for (const [name, params, intro, expected] of pages) {
      f.route.name = name
      f.route.params = params
      f.assistant.setPageTitle(`${name}/${params.section || params.automationId || ''}`)
      await flush()
      assert.deepEqual(questions(), expected, `questions for ${name}/${JSON.stringify(params)}`)
      assert.equal(description(), `assistant.welcomeDescriptions.${intro}`)
    }
    f.assistant.dismissPageContext()
    await flush()
    assert.deepEqual(questions(), general, 'dismissed page context does not influence suggestions')
    assert.equal(description(), 'assistant.welcomeDesc', 'dismissed page context does not influence the introduction')
    f.assistant.restorePageContext()
    f.assistant.setPageAccess(false)
    await flush()
    assert.deepEqual(questions(), general, 'page access off also disables route-based suggestions')
    assert.equal(description(), 'assistant.welcomeDesc', 'page access off also disables the contextual introduction')
    assert.equal(f.mounted.length, 0)
    assert.equal(f.sent.length, 0)
  } finally { f.close() }
})

test('open resources and dialogs have their own welcome content and return to page content when closed', async () => {
  const f = await fixture()
  try {
    f.route.name = 'agentList'
    f.assistant.setPageTitle('Agents')
    const owner = Symbol('open-resource')
    const cases: Array<[Record<string, string>, string, string[]]> = [
      [{ page: 'settings_models', modelId: 'model' }, 'welcomeDescriptions.model', ['modelFit', 'modelCheck', 'modelUsage']],
      [{ page: 'settings_models' }, 'welcomeDescriptions.model', ['modelSetup', 'modelTypes', 'modelUsage']],
      [{ page: 'mcp_services', mcpServiceId: 'mcp' }, 'welcomeDescriptions.mcp', ['mcpTools', 'mcpUsage', 'mcpCheck']],
      [{ page: 'mcp_services' }, 'welcomeDescriptions.mcp', ['mcpFind', 'mcpSetup', 'mcpExamples']],
      [{ agentId: 'agent' }, 'agentWelcomeDesc', ['agentConfig', 'agentModel', 'agentKnowledge']],
      [{ knowledgeBaseId: 'kb', wikiSlug: 'page' }, 'welcomeDescriptions.wiki', ['wikiSummary', 'wikiSources', 'wikiIssues']],
      [{ knowledgeBaseId: 'kb', knowledgeId: 'document' }, 'welcomeDescriptions.document', ['documentSummary', 'documentSteps', 'documentFaq']],
      [{ knowledgeBaseId: 'kb', section: 'faq' }, 'welcomeDescriptions.faq', ['faqCoverage', 'faqDuplicates', 'faqImprove']],
      [{ knowledgeBaseId: 'kb' }, 'welcomeDescriptions.knowledgeBase', ['knowledgeTopics', 'recentActivity', 'knowledgeGaps']],
      [{ page: 'chat', chatSessionId: 'chat' }, 'welcomeDescriptions.chat', ['chatSummary', 'chatCheck', 'chatFollowup']],
      [{ page: 'settings', section: 'members' }, 'settingsPages.members.description', ['settingsPages.members.q1', 'settingsPages.members.q2', 'settingsPages.members.q3']],
      [{ page: 'settings', section: 'integration-im' }, 'settingsPages.integration-im.description', ['settingsPages.integration-im.q1', 'settingsPages.integration-im.q2', 'settingsPages.integration-im.q3']],
      [{ chatSessionId: 'chat', messageId: 'answer' }, 'welcomeDescriptions.message', ['messageSources', 'messageCheck', 'messageReuse']],
    ]
    const questions = () => f.findAll(node => node.props.class === 'assistant-starter')
      .map(node => node.text?.replace(/^assistant\.(starters\.)?/, ''))
    const description = () => f.findAll(node => node.props.class === 'assistant-welcome-desc')[0]?.text
    for (const [context, intro, expected] of cases) {
      f.assistant.publishPageContext(owner, context)
      await flush()
      assert.deepEqual(questions(), expected, `questions for ${JSON.stringify(context)}`)
      assert.equal(description(), `assistant.${intro}`)
    }
    f.assistant.retractPageContext(owner)
    await flush()
    assert.deepEqual(questions(), ['agents', 'agentCreate', 'agentChoose'])
    assert.equal(description(), 'assistant.welcomeDescriptions.agents')
    f.route.name = 'toolbox'
    f.route.params = { section: 'mcp' }
    f.assistant.publishPageContext(owner, { agentId: 'agent' })
    await flush()
    assert.deepEqual(questions(), ['agentConfig', 'agentModel', 'agentKnowledge'], 'an open agent takes precedence over the underlying MCP tab')
    assert.equal(description(), 'assistant.agentWelcomeDesc')
    assert.equal(f.sent.length, 0)
  } finally { f.close() }
})

test('each settings section has its own welcome questions', async () => {
  const f = await fixture()
  try {
    f.assistant.open()
    const owner = Symbol('settings')
    const questions = () => f.findAll(node => node.props.class === 'assistant-starter')
      .map(node => node.text?.replace(/^assistant\.(starters\.)?/, ''))
    for (const section of ['general', 'members', 'sandbox', 'websearch', 'integration-api', 'runtime-queues', 'system-global-users']) {
      f.assistant.publishPageContext(owner, { page: 'settings', section })
      await flush()
      assert.deepEqual(questions(), [
        `settingsPages.${section}.q1`,
        `settingsPages.${section}.q2`,
        `settingsPages.${section}.q3`,
      ], section)
    }
  } finally { f.close() }
})

test('the current-version question starts one conversation only when selected', async () => {
  const f = await fixture()
  try {
    f.assistant.open()
    await flush()
    assert.equal(f.sent.length, 0)
    const question = f.findAll(node => node.text === 'assistant.starters.releaseHighlights')[0]
    assert.ok(question)
    question.props.onClick()
    await flush()
    assert.equal(f.sent.length, 1)
    assert.equal(f.sent[0].query, 'assistant.starters.releaseHighlights')
  } finally { f.close() }
})

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

test('floating panels escape the app root without remounting the conversation', async () => {
  const f = await fixture()
  try {
    f.assistant.open({ query: 'Explain this connection error' })
    await flush()
    const panel = f.findAll(node => node.tag === 'aside')[0]
    assert.equal(panel.parent?.tag, 'app-root')
    f.assistant.setLayout('floating')
    await flush()
    assert.equal(panel.parent?.tag, 'body')
    f.assistant.setLayout('docked')
    await flush()
    assert.equal(panel.parent?.tag, 'app-root')
    assert.equal(f.mounted.length, 1)
    assert.equal(f.sent.length, 1)
  } finally { f.close() }
})

test('the floating header moves the panel without intercepting controls or remounting the conversation', async () => {
  const f = await fixture()
  try {
    f.assistant.open({ query: 'Keep this conversation running' })
    await flush()
    const panel = f.findAll(node => node.tag === 'aside')[0]
    const header = f.findAll(node => node.tag === 'header')[0]
    Object.defineProperties(panel, {
      offsetWidth: { get: () => parseFloat(panel.props.style.width) },
      offsetHeight: { value: 720 },
      offsetLeft: { get: () => 1440 - parseFloat(panel.props.style.width) - parseFloat(panel.props.style.right || '12') },
      offsetTop: { get: () => parseFloat(panel.props.style.top || '268') },
      getBoundingClientRect: { value: () => ({ width: parseFloat(panel.props.style.width) }) },
    })
    let captured: number | null = null
    Object.assign(header, {
      setPointerCapture: (id: number) => { captured = id },
      hasPointerCapture: (id: number) => captured === id,
      releasePointerCapture: () => { captured = null },
    })
    const pointer = (clientX: number, clientY: number, control = false) => ({
      clientX, clientY, pointerId: 1, isPrimary: true, button: 0,
      currentTarget: header, target: { closest: () => control ? {} : null }, preventDefault() {},
    })
    header.props.onPointerdown(pointer(1000, 280))
    assert.equal(captured, null, 'docked headers cannot drag')
    f.assistant.setLayout('floating')
    await flush()
    header.props.onPointerdown(pointer(1000, 280, true))
    assert.equal(captured, null, 'buttons and conversation tabs retain their normal interactions')
    header.props.onPointerdown(pointer(1000, 280))
    header.props.onPointermove(pointer(800, 180))
    await flush()
    assert.equal(captured, 1)
    assert.match(panel.props.class, /is-dragging/)
    assert.equal(panel.props.style.right, '212px')
    assert.equal(panel.props.style.top, '168px')
    header.props.onPointerup(pointer(800, 180))
    await flush()
    assert.equal(captured, null)
    assert.doesNotMatch(panel.props.class, /is-dragging/)

    f.assistant.close()
    await flush()
    f.assistant.open()
    await flush()
    assert.equal(panel.props.style.right, '212px')
    assert.equal(panel.props.style.top, '168px')
    f.assistant.setLayout('docked')
    await flush()
    assert.equal(panel.props.style.right, undefined)
    assert.equal(panel.props.style.top, undefined)
    assert.equal(f.mounted.length, 1)
    assert.equal(f.sent.length, 1)
  } finally { f.close() }
})

for (const existing of [false, true]) {
  test(`contextual actions open inside the ${existing ? 'existing conversation' : 'welcome panel'} and send only on selection`, async () => {
    let creations = 0
    const f = await fixture(async () => { creations++; return { data: { id: 'new-session' } } })
    try {
      if (existing) f.assistant.setSession('ongoing-session')
      f.assistant.publishPageContext(Symbol(), { knowledgeBaseId: 'kb-a', knowledgeId: 'document-a' })
      f.assistant.setPageSelection({ text: 'Selected content', field: 'Document' })
      await flush()
      const mounts = f.mounted.length
      await f.click('assistant.history')
      f.assistant.showActionSuggestions({ label: 'Read this document', actions: [
        { key: 'summary', label: 'Summarize', query: 'Summarize document-a' },
        { key: 'compare', label: 'Compare', query: 'Compare documents', disabled: true },
      ] })
      await flush()
      assert.equal(f.assistant.visible, true)
      assert.equal(creations, 0, 'opening actions must not create a conversation or send a prompt')
      assert.equal(f.sent.length, 0)
      assert.equal(f.mounted.length, mounts, 'existing conversations stay mounted')
      assert.equal(f.findAll(node => node.props.class === 'assistant-history').length, 0)
      const buttons = f.findAll(node => node.tag === 'button')
      const summary = buttons.find(node => node.text === 'Summarize')
      const compare = buttons.find(node => node.text === 'Compare')
      assert.ok(summary)
      assert.equal(compare?.props.disabled, true)
      f.assistant.askSuggestedAction('compare')
      f.assistant.askSuggestedAction('missing')
      assert.equal(f.assistant.queuedMessage, null)
      assert.equal(creations, 0)
      summary.props.onClick()
      await flush()
      assert.equal(f.assistant.actionSuggestions, null)
      if (existing) {
        assert.equal(f.assistant.sessionId, 'ongoing-session')
        assert.equal(f.assistant.queuedMessage.query, 'Summarize document-a')
        assert.equal(creations, 0)
      } else {
        assert.equal(creations, 1)
        assert.equal(f.sent[0].query, 'Summarize document-a')
        assert.equal(f.sent[0].scope.pageContext.knowledge_id, 'document-a')
        assert.equal(f.sent[0].scope.pageContext.selection.text, 'Selected content')
      }
    } finally { f.close() }
  })
}

test('contextual actions expire when their page, conversation, workspace or access changes', async () => {
  const f = await fixture()
  try {
    const owner = Symbol()
    f.assistant.publishPageContext(owner, { knowledgeBaseId: 'kb-a', knowledgeId: 'document-a' })
    const suggest = () => f.assistant.showActionSuggestions({ label: 'Document actions', actions: [
      { key: 'summary', label: 'Summarize', query: 'Summarize document-a' },
    ] })
    for (const change of [
      () => f.assistant.publishPageContext(owner, { knowledgeBaseId: 'kb-a', knowledgeId: 'document-b' }),
      () => f.assistant.setPageView('Another dialog'),
      () => f.assistant.dismissPageContext(),
      () => f.assistant.startNewConversation(),
      () => f.assistant.setSession('another-session'),
      () => { f.auth.effectiveTenantId = 2 },
      () => f.assistant.setPageAccess(false),
    ]) {
      suggest()
      assert.ok(f.assistant.actionSuggestions)
      change()
      await flush()
      assert.equal(f.assistant.actionSuggestions, null)
    }
    suggest()
    assert.equal(f.assistant.actionSuggestions, null, 'page access must be enabled')
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
    assert.deepEqual(Array.from(f.sent[0].scope.knowledgeBaseIds), [],
      'the current page must not become an explicit KB selection')
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

 test('workspace changes refresh the shared profile and workspace selector even while the panel is hidden', async () => {
  const f = await fixture()
  try {
    assert.equal(f.assistant.visible, false)
    assert.equal(f.workspaceRefreshes(), 0)
    f.workspaceChanged()
    await flush()
    assert.equal(f.workspaceRefreshes(), 1)
  } finally { f.close() }
})
