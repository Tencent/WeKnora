import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import * as vue from 'vue'
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc'
import ts from 'typescript'
import { createWorkbenchApi } from '../../../../api/sandbox-workbench.ts'
import * as workbenchUtils from '../../../../utils/sandboxWorkbench.ts'

const read = file => readFileSync(new URL(file, import.meta.url), 'utf8')
const flush = () => new Promise(setImmediate)
const emptyComponent = { render: () => null }

function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function evaluate(source, imports, globals = {}) {
  const exports = {}
  const { outputText } = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  })
  runInNewContext(outputText, {
    exports, AbortController, console, ...globals,
    require(name) {
      if (name === 'vue') return vue
      if (name === 'vue-i18n') return { useI18n: () => ({ t: key => key, te: () => true, locale: vue.ref('en-US') }) }
      assert.ok(name in imports, `Unexpected import: ${name}`)
      return imports[name]
    },
  })
  return exports
}

function compileComponent(file, imports, globals = {}, renderTemplate = false) {
  const { descriptor } = parse(read(file), { filename: file })
  const script = compileScript(descriptor, { id: file })
  const component = evaluate(script.content, imports, globals).default
  if (renderTemplate) {
    const template = compileTemplate({
      source: descriptor.template.content,
      filename: file,
      id: file,
      compilerOptions: { bindingMetadata: script.bindings },
    })
    assert.deepEqual(template.errors, [])
    component.render = evaluate(template.code, imports, globals).render
  } else {
    component.render = () => null
  }
  return component
}

function mount(component, props = {}) {
  const renderer = vue.createRenderer({
    createElement: tag => ({ tag, children: [], style: {} }),
    createText: text => ({ text }),
    createComment: text => ({ text }),
    insert(child, parent, anchor) {
      if (child.parent) child.parent.children = child.parent.children.filter(item => item !== child)
      const index = anchor ? parent.children.indexOf(anchor) : -1
      parent.children.splice(index < 0 ? parent.children.length : index, 0, child)
      child.parent = parent
    },
    remove(child) {
      if (child.parent) child.parent.children = child.parent.children.filter(item => item !== child)
      child.parent = null
    },
    parentNode: node => node?.parent,
    nextSibling: node => node.parent?.children[node.parent.children.indexOf(node) + 1] ?? null,
    setText(node, text) { node.text = text },
    setElementText(node, text) { node.children = []; node.text = text },
    patchProp(node, key, _, value) { if (key !== 'style') node[key] = value },
  })
  const passthrough = { setup: (_, { slots }) => () => vue.h('div', slots.default?.()) }
  const app = renderer.createApp(component, props)
  for (const name of ['t-drawer', 't-button', 't-select', 't-tabs', 't-tab-panel', 't-icon', 't-loading', 't-textarea', 't-tag']) {
    app.component(name, passthrough)
  }
  const root = { children: [] }
  app.mount(root)
  function find(predicate, node = root) {
    if (predicate(node)) return node
    for (const child of node.children || []) {
      const found = find(predicate, child)
      if (found) return found
    }
  }
  return { app, state: app._instance.setupState, find }
}

test('command textarea uses one TDesign keydown callback with composition-safe shortcuts', async t => {
  const commands = []
  class TerminalStub {
    options = {}
    loadAddon() {}
    open() {}
    onData() {}
    onBinary() {}
    onResize() {}
    focus() {}
    dispose() {}
  }
  const component = compileComponent('./WorkbenchTerminal.vue', {
    '@xterm/xterm': { Terminal: TerminalStub },
    '@xterm/addon-fit': { FitAddon: class {} },
    '@xterm/xterm/css/xterm.css': {},
    '@/utils/api-base': { getApiBaseUrl: () => '/api/v1' },
    '@/utils/sandboxWorkbench': workbenchUtils,
    '@/utils/sandboxTerminal': {
      SandboxTerminal: class {
        constructor(options) { this.options = options }
        connect() { this.options.onPhase('ready') }
        command(value) { commands.push(value); return true }
        dispose() {}
      },
    },
  }, {
    window: { location: { href: 'https://weknora.test/chat/session' } },
    ResizeObserver: class { observe() {} disconnect() {} },
    requestAnimationFrame: () => 1,
    cancelAnimationFrame() {},
  }, true)
  const h = mount(component, { api: {}, signal: new AbortController().signal, active: true })
  t.after(() => h.app.unmount())
  await flush()
  const textarea = h.find(node => node.onKeydown !== undefined)
  assert.equal(typeof textarea.onKeydown, 'function', 'TDesign rejects arrays of keydown callbacks')
  for (const [event, submit] of [
    [{ key: 'Enter', ctrlKey: true }, true],
    [{ key: 'Enter', metaKey: true }, true],
    [{ key: 'Enter', ctrlKey: true, metaKey: true }, true],
    [{ key: 'Enter' }, false],
    [{ key: 'a', ctrlKey: true }, false],
    [{ key: 'Enter', ctrlKey: true, isComposing: true }, false],
    [{ key: 'Enter', metaKey: true, keyCode: 229 }, false],
  ]) {
    h.state.command = 'printf shortcut'
    const before = commands.length
    let prevented = false
    textarea.onKeydown(h.state.command, { e: { ...event, preventDefault() { prevented = true } } })
    assert.equal(commands.length, before + Number(submit), JSON.stringify(event))
    assert.equal(prevented, submit, JSON.stringify(event))
    assert.equal(h.state.command, submit ? '' : 'printf shortcut')
  }
})

function mountAudit(renderTemplate = false) {
  const requests = []
  const controller = new AbortController()
  const component = compileComponent('./WorkbenchAudit.vue', {
    '@/utils/sandboxWorkbench': workbenchUtils,
  }, {}, renderTemplate)
  return {
    ...mount(component, {
      api: { audit(afterId = 0) {
        const request = { afterId, ...deferred() }
        requests.push(request)
        return request.promise
      } },
      signal: controller.signal, active: true, revision: 0,
    }),
    requests,
    controller,
  }
}

const auditPage = (ids, next) => ({
  data: ids.map(id => ({ id, action: 'sandbox_workbench.command', outcome: 'success', created_at: '2026-09-12T00:00:00Z' })),
  next_cursor: next,
})

test('audit pagination exposes commands beyond the first hundred records', async t => {
  const h = mountAudit(true)
  t.after(() => h.app.unmount())
  h.requests[0].resolve(auditPage(Array.from({ length: 100 }, (_, i) => 120 - i), 21))
  await flush()
  const more = h.find(node => node.onClick === h.state.loadMore)
  assert.ok(more, 'a visible control must retrieve older audit records')
  const pending = more.onClick()
  assert.equal(h.requests[1].afterId, 21)
  h.requests[1].resolve(auditPage(Array.from({ length: 20 }, (_, i) => 20 - i), 1))
  await pending
  assert.equal(h.state.rows.length, 120)
  assert.equal(h.state.rows.at(-1).id, 1)
  const last = h.state.loadMore()
  h.requests[2].resolve(auditPage([], 0))
  await last
  assert.equal(h.state.cursor, 0)
  await h.state.loadMore()
  assert.equal(h.requests.length, 3)
})

for (const refreshFirst of [false, true]) {
  test(`audit refresh rejects stale pagination when refresh resolves ${refreshFirst ? 'first' : 'last'}`, async t => {
    const h = mountAudit()
    t.after(() => h.app.unmount())
    h.requests[0].resolve(auditPage([100], 100))
    await flush()
    const old = h.state.loadMore()
    const refresh = h.state.refresh()
    assert.equal(h.state.loadingMore, false)
    if (!refreshFirst) {
      h.requests[1].resolve(auditPage([99], 99))
      await old
      assert.equal(h.state.loading, true)
    }
    h.requests[2].resolve(auditPage([200], 200))
    await refresh
    const more = h.state.loadMore()
    assert.equal(h.requests[3].afterId, 200)
    if (refreshFirst) {
      h.requests[1].reject(new Error('stale failure'))
      await old
      assert.equal(h.state.loadingMore, true)
      assert.equal(h.state.error, '')
    }
    h.requests[3].resolve(auditPage([199], 199))
    await more
    assert.deepEqual(Array.from(h.state.rows, row => row.id), [200, 199])
    assert.equal(h.state.cursor, 199)
  })
}

test('audit pagination retries errors and ignores results after scope cancellation', async t => {
  const h = mountAudit()
  t.after(() => h.app.unmount())
  h.requests[0].resolve(auditPage([10], 10))
  await flush()
  const failed = h.state.loadMore()
  h.requests[1].reject(new Error('request failed'))
  await failed
  assert.equal(h.state.cursor, 10)
  assert.equal(h.state.loadingMore, false)
  const retry = h.state.loadMore()
  assert.equal(h.requests[2].afterId, 10)
  h.controller.abort()
  h.requests[2].resolve(auditPage([9], 9))
  await retry
  assert.deepEqual(Array.from(h.state.rows, row => row.id), [10])
  await h.state.refresh()
  assert.equal(h.requests.length, 3)
})

function mountArtifacts() {
  const requests = []
  const controller = new AbortController()
  const component = compileComponent('./WorkbenchArtifacts.vue', {
    '@/api/chat': {
      listSessionArtifacts(sessionId, options) {
        const request = { sessionId, options, ...deferred() }
        requests.push(request)
        return request.promise
      },
    },
    '../ChatArtifactsDrawer.vue': emptyComponent,
    '@/utils/artifactPreview': {},
    '@/utils/sandboxWorkbench': workbenchUtils,
  })
  return {
    ...mount(component, {
      sessionId: 'session', signal: controller.signal, active: true, revision: 0, maxBytes: 1024,
    }),
    requests,
    controller,
  }
}

function page(id, next = '') {
  return {
    success: true,
    data: [{ message_id: id, artifact_index: 0, file_name: `${id}.txt` }],
    has_more: !!next,
    next_cursor: next,
  }
}

for (const refreshFirst of [false, true]) {
  test(`artifact pagination recovers when ${refreshFirst ? 'refresh' : 'old pagination'} responds first`, async t => {
    const h = mountArtifacts()
    t.after(() => h.app.unmount())
    h.requests[0].resolve(page('initial', 'initial-next'))
    await flush()
    const oldMore = h.state.loadMore()
    const refresh = h.state.refresh()
    assert.equal(h.state.loading, true)
    assert.equal(h.state.loadingMore, false)
    if (refreshFirst) {
      h.requests[2].resolve(page('refreshed', 'refreshed-next'))
      await refresh
    } else {
      h.requests[1].resolve(page('stale', 'stale-next'))
      await oldMore
      assert.equal(h.state.loading, true, 'an old page must not clear the new refresh busy state')
      h.requests[2].resolve(page('refreshed', 'refreshed-next'))
      await refresh
    }

    const newMore = h.state.loadMore()
    assert.equal(h.requests.length, 4, 'refresh must restore pagination')
    assert.equal(h.requests[3].options.cursor, 'refreshed-next')
    assert.equal(h.state.loadingMore, true)
    if (refreshFirst) {
      h.requests[1].reject(new Error('stale page failed'))
      await oldMore
      assert.equal(h.state.loadingMore, true, 'an old page must not clear the new page busy state')
      assert.equal(h.state.error, '', 'ignore stale errors')
      await h.state.loadMore()
      assert.equal(h.requests.length, 4, 'keep duplicate pagination blocked while the new page is busy')
    }
    h.requests[3].resolve(page('latest'))
    await newMore
    assert.deepEqual(h.state.items.map(item => item.message_id), ['refreshed', 'latest'])
    assert.equal(h.state.loading, false)
    assert.equal(h.state.loadingMore, false)
    assert.equal(h.state.hasMore, false)
  })
}

test('an old refresh cannot clear a newer refresh or overwrite its pagination', async t => {
  const h = mountArtifacts()
  t.after(() => h.app.unmount())
  const latest = h.state.refresh()
  h.requests[0].resolve(page('stale', 'stale-next'))
  await flush()
  assert.equal(h.state.loading, true)
  assert.equal(h.state.items.length, 0)
  h.requests[1].resolve(page('latest', 'latest-next'))
  await latest
  assert.equal(h.state.loading, false)
  assert.equal(h.state.cursor, 'latest-next')
  assert.equal(h.state.items[0].message_id, 'latest')
})

function status(configId = '', capabilities = { terminal: false, files: false }) {
  return {
    available: capabilities.terminal || capabilities.files,
    state: configId ? 'bound' : 'unbound',
    config_id: configId,
    provider: 'docker',
    root: '/workspace/output',
    capabilities,
    limits: { max_file_bytes: 1024 },
  }
}

function workbenchServer() {
  let pinnedConfig = ''
  let capabilities = { terminal: false, files: false }
  const gets = [], posts = []
  return {
    gets, posts,
    transport: {
      async get(url) {
        gets.push(url)
        return { success: true, data: status(pinnedConfig, capabilities) }
      },
      post(url, body) {
        pinnedConfig ||= body.config_id
        const request = { url, configId: body.config_id, ...deferred() }
        posts.push(request)
        return request.promise
      },
    },
    succeed() {
      capabilities = { terminal: true, files: true }
      posts.at(-1).resolve({ success: true, data: status(pinnedConfig, capabilities) })
    },
  }
}

function mountWorkbench(server) {
  const component = compileComponent('./SandboxWorkbench.vue', {
    'vue-router': { useRoute: () => ({ fullPath: '/chat/session' }) },
    '@/stores/auth': {
      useAuthStore: () => ({ user: { id: 'user' }, effectiveTenantId: 'tenant', isLoggedIn: true }),
    },
    '@/api/sandbox-workbench': { createWorkbenchApi },
    '@/api/system': {
      listSandboxConfigs: async () => ({ data: [{ id: 'pinned-config', name: 'Sandbox', sandbox_type: 'docker' }] }),
    },
    '@/utils/request': server.transport,
    '@/utils/sandboxWorkbench': workbenchUtils,
    './WorkbenchTerminal.vue': emptyComponent,
    './WorkbenchFiles.vue': emptyComponent,
    './WorkbenchArtifacts.vue': emptyComponent,
    './WorkbenchAudit.vue': emptyComponent,
  }, {
    window: {
      matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
      addEventListener() {}, removeEventListener() {},
    },
  }, true)
  return mount(component, { sessionId: 'session' })
}

for (const reopen of [false, true]) {
  test(`failed binding can ${reopen ? 'reopen' : 'refresh'} and explicitly retry the pinned configuration`, async t => {
    const server = workbenchServer()
    let h = mountWorkbench(server)
    t.after(() => h.app.unmount())
    await flush()
    assert.equal(server.posts.length, 0, 'opening only reads status')
    h.state.configId = 'pinned-config'
    const initial = h.state.bind()
    server.posts[0].reject(new Error('initialization failed'))
    await initial
    assert.equal(h.state.binding, false)
    assert.match(h.state.configError, /initialization failed/)

    if (reopen) {
      h.state.close()
      h.app.unmount()
      h = mountWorkbench(server)
      await flush()
    } else {
      await h.state.refreshStatus()
      await flush()
    }
    assert.equal(h.state.status.state, 'bound')
    assert.equal(h.state.status.config_id, 'pinned-config')
    assert.equal(h.state.canUseTerminal, false)
    assert.equal(h.state.canUseFiles, false)
    assert.equal(server.posts.length, 1, 'refresh and reopen must not initialize a sandbox')
    assert.equal(server.gets.length, 2)
    const retryButton = () => h.find(node => node['data-workbench-reinitialize'] !== undefined)
    assert.ok(retryButton(), 'a bound session without capabilities must expose retry')
    assert.equal(h.find(node => node.tag === 'form'), undefined, 'do not offer another configuration')
    h.state.configId = 'different-config'
    const retry = retryButton().onClick()
    await flush()
    assert.equal(server.posts[1].configId, 'pinned-config')
    assert.equal(retryButton().loading, true)
    await h.state.bind()
    assert.equal(server.posts.length, 2, 'do not duplicate initialization while busy')
    const reads = server.gets.length
    await h.state.refreshStatus()
    assert.equal(server.gets.length, reads, 'a refresh must not race with initialization')
    server.posts[1].reject(new Error('still unavailable'))
    await retry
    await flush()
    assert.equal(retryButton().loading, false)
    assert.match(h.state.configError, /still unavailable/)
    assert.ok(h.find(node => node.role === 'alert'), 'bound retry failures stay visible')

    const recovered = retryButton().onClick()
    server.succeed()
    await recovered
    await flush()
    assert.equal(h.state.configError, '')
    assert.equal(h.state.status.config_id, 'pinned-config')
    assert.equal(h.state.canUseTerminal, true)
    assert.equal(h.state.canUseFiles, true)
    assert.equal(h.state.runtimeRevision, 1, 'recreate runtime children after initialization')
    assert.ok(retryButton(), 'keep explicit recovery available when static capabilities outlive an expired sandbox')
    assert.deepEqual(server.posts.map(request => request.configId), [
      'pinned-config', 'pinned-config', 'pinned-config',
    ])
    assert.ok(server.posts.every(request => request.url === '/api/v1/sessions/session/sandbox/workbench'))
  })
}

test('closing a workbench invalidates a pending initialization response', async () => {
  const server = workbenchServer()
  const h = mountWorkbench(server)
  await flush()
  h.state.configId = 'pinned-config'
  const pending = h.state.bind()
  h.state.close()
  h.app.unmount()
  server.succeed()
  await pending
  assert.equal(h.state.scope.signal.aborted, true)
  assert.equal(h.state.runtimeRevision, 0)
  assert.equal(h.state.status.state, 'unbound')
})

function mountChatPanels() {
  const { provideChatSandboxPanel } = evaluate(read('../../../../composables/useChatSandboxPanel.ts'), {}, {
    localStorage: { getItem: () => null, setItem() {} },
    window: { innerWidth: 1440 },
  })
  const { descriptor } = parse(read('../../index.vue'))
  const source = descriptor.scriptSetup.content
  const start = source.indexOf('const workbenchEnabled =')
  const end = source.indexOf('const loadSessionAndHydrate =')
  assert.ok(start >= 0 && end > start)
  const flag = vue.ref(true)
  const props = vue.reactive({ embeddedMode: false })
  const sessionId = vue.ref('session')
  const referencesVisible = vue.ref(false)
  let panel
  const h = mount({
    setup() {
      panel = provideChatSandboxPanel()
      return runInNewContext(`${source.slice(start, end)}
        ({ openWorkbench, closeWorkbench, workbenchVisible, workbenchRef })`, {
        ref: vue.ref, computed: vue.computed, watch: vue.watch,
        deploymentCapabilities: { isSupported: () => flag.value },
        session_id: sessionId, props,
        route: { fullPath: '/chat/session' },
        authStore: { user: { id: 'user' }, effectiveTenantId: 'tenant', isLoggedIn: true },
        sandboxPanel: panel,
        referencesDrawer: { close: () => { referencesVisible.value = false } },
        referencesDrawerVisible: referencesVisible,
      })
    },
    render: () => null,
  })
  return { ...h, panel, flag, props, sessionId, referencesVisible }
}

test('chat panel entry and inline artifact focus synchronously close the workbench and vice versa', t => {
  const h = mountChatPanels()
  t.after(() => h.app.unmount())
  let disposed = 0
  h.panel.open('terminal')
  h.referencesVisible.value = true
  h.state.openWorkbench()
  assert.equal(h.state.workbenchVisible, true)
  assert.equal(h.panel.visible.value, false)
  assert.equal(h.referencesVisible.value, false)
  h.state.workbenchRef = { dispose: () => { disposed++ } }

  h.panel.open()
  assert.equal(h.state.workbenchVisible, false)
  assert.equal(disposed, 1, 'opening the upstream header panel disposes the workbench synchronously')
  h.state.openWorkbench()
  h.panel.open('artifacts', { messageId: 'answer', previewIndex: 2 })
  assert.equal(h.state.workbenchVisible, false)
  assert.equal(h.panel.artifactFocus.value.previewIndex, 2)
  h.state.openWorkbench()
  assert.equal(h.panel.artifactFocus.value, null, 'switching to workbench clears stale upstream focus')
  h.panel.toggleArtifacts('another-answer')
  assert.equal(h.state.workbenchVisible, false)
  assert.equal(h.panel.artifactFocus.value.messageId, 'another-answer')
})

test('workbench panel retains deployment, embedded and session guards', t => {
  const h = mountChatPanels()
  t.after(() => h.app.unmount())
  h.state.openWorkbench()
  h.flag.value = false
  assert.equal(h.state.workbenchVisible, false)
  h.panel.open()
  h.state.openWorkbench()
  assert.equal(h.panel.visible.value, true, 'a disabled workbench must not affect upstream entry')
  assert.equal(h.state.workbenchVisible, false)
  h.flag.value = true
  h.props.embeddedMode = true
  h.state.openWorkbench()
  assert.equal(h.state.workbenchVisible, false)
  h.props.embeddedMode = false
  h.sessionId.value = ''
  h.state.openWorkbench()
  assert.equal(h.state.workbenchVisible, false)
})
