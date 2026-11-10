import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as vue from 'vue'

const { descriptor } = parse(readFileSync(new URL('./GraphSettings.vue', import.meta.url), 'utf8'))
const script = compileScript(descriptor, { id: 'graph-settings-test', inlineTemplate: true }).content
const compiled = ts.transpileModule(script.replace('import.meta.env.VITE_KG_GUIDE_URL', 'undefined'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

type Host = { type: string; props: Record<string, any>; children: Host[]; text: string; parent?: Host }
const node = (type: string): Host => ({ type, props: {}, children: [], text: '' })
const renderer = vue.createRenderer<Host, Host>({
  createElement: node,
  createText: text => ({ ...node('#text'), text }),
  createComment: text => ({ ...node('#comment'), text }),
  patchProp: (el, key, _old, value) => { el.props[key] = value },
  setText: (el, text) => { el.text = text },
  setElementText: (el, text) => { el.children = []; el.text = text },
  parentNode: el => el.parent || null,
  nextSibling: el => el.parent?.children[el.parent.children.indexOf(el) + 1] || null,
  insert(el, parent, anchor) {
    if (el.parent) el.parent.children.splice(el.parent.children.indexOf(el), 1)
    el.parent = parent
    const index = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(index < 0 ? parent.children.length : index, 0, el)
  },
  remove(el) {
    el.parent?.children.splice(el.parent.children.indexOf(el), 1)
    el.parent = undefined
  },
})

const textOf = (el: Host): string => el.type === '#comment' ? '' : el.text + el.children.map(textOf).join('')
const all = (el: Host, predicate: (node: Host) => boolean): Host[] => [
  ...(predicate(el) ? [el] : []), ...el.children.flatMap(child => all(child, predicate)),
]
const hasClass = (el: Host, name: string) => String(el.props.class || '').split(' ').includes(name)
const flush = async () => {
  await new Promise<void>(resolve => setImmediate(resolve))
  await vue.nextTick()
}

type Request = { force: boolean; resolve: (info: unknown) => void; reject: (error: Error) => void }

function fixture(t: TestContext, embedded: boolean) {
  const requests: Request[] = []
  const events: unknown[] = []
  const opened: unknown[][] = []
  const graphExtract = {
    enabled: true,
    text: 'A sample passage',
    tags: ['Author'],
    nodes: [{ name: 'Book', attributes: ['Fiction'] }],
    relations: [{ node1: 'Book', node2: 'Writer', type: 'Author' }],
    customInstructions: 'Extract authors and their books.',
  }
  const snapshot = structuredClone(graphExtract)
  const store = {
    systemInfo: null as unknown,
    ensureSystemInfo(force = false) {
      return new Promise<unknown>((resolve, reject) => requests.push({ force, resolve, reject }))
        .then(info => { store.systemInfo = info })
    },
  }
  const exports: { default?: vue.Component } = {}
  runInNewContext(compiled, {
    exports,
    console: { error() {} },
    window: { open: (...args: unknown[]) => opened.push(args) },
    require(name: string) {
      if (name === 'vue') return vue
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/stores/editorResources') return { useEditorResourcesStore: () => store }
      if (name === '@/stores/auth') return { useAuthStore: () => ({ hasRole: () => true }) }
      if (name === '@/utils/docsUrl') return { docsUrl: () => 'https://example.com/knowledge-graph' }
      if (name === 'tdesign-vue-next') return { MessagePlugin: {} }
      if (name === '@/api/initialization') return {
        extractTextRelations: () => assert.fail('Availability checks must not extract relations'),
        fabriText: () => assert.fail('Availability checks must not generate text'),
        fabriTag: () => assert.fail('Availability checks must not generate tags'),
      }
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  const app = renderer.createApp(exports.default!, {
    graphExtract,
    modelId: 'model',
    embedded,
    'onUpdate:graphExtract': (value: unknown) => events.push(value),
  })
  for (const name of ['t-button', 't-icon', 't-input', 't-option', 't-select', 't-switch', 't-textarea']) {
    app.component(name, vue.defineComponent({
      inheritAttrs: false,
      setup: (_props, { attrs, slots }) => () => vue.h(name, attrs, slots.message?.() ?? slots.default?.()),
    }))
  }
  const root = node('root')
  app.mount(root)
  t.after(() => {
    app.unmount()
    assert.deepEqual(graphExtract, snapshot)
    assert.deepEqual(events, [])
  })
  const elements = (type: string) => all(root, el => el.type === type)
  const classes = (name: string) => all(root, el => hasClass(el, name))
  const retry = () => {
    const button = elements('t-button').find(el => textOf(el).trim() === 'common.retry')
    assert.ok(button, 'Expected a retry button')
    assert.equal(button.props.disabled === true, false)
    return button.props.onClick as () => Promise<void>
  }
  const expectState = (state: 'loading' | 'enabled' | 'disabled' | 'error') => {
    const notices = classes('graph-disabled')
    assert.equal(notices.length, state === 'enabled' ? 0 : 1)
    if (state !== 'enabled') assert.equal(notices[0]!.props.role, state === 'loading' ? 'status' : 'alert')
    assert.equal(classes('graph-disabled--loading').length, state === 'loading' ? 1 : 0)
    assert.equal(classes('settings-group').length, state === 'enabled' ? 1 : 0)
    assert.equal(classes('section-header').length, embedded ? 0 : 1)
    assert.equal(elements('t-button').filter(el => textOf(el).trim() === 'graphSettings.howToEnable').length, state === 'disabled' && !embedded ? 1 : 0)
    const messages = {
      loading: 'graphSettings.checkingAvailability',
      disabled: 'graphSettings.disabledWarning',
      error: 'graphSettings.checkAvailabilityFailed',
    }
    for (const [status, message] of Object.entries(messages)) {
      assert.equal(textOf(root).includes(message), status === state, `Unexpected ${message} in ${state}`)
    }
    assert.equal(elements('t-button').filter(el => textOf(el).trim() === 'common.retry').length, state === 'error' ? 1 : 0)
    if (state === 'enabled') {
      assert.equal(elements('t-switch')[0]?.props.modelValue, snapshot.enabled)
      const inputValue = (type: string, placeholder: string) => elements(type)
        .find(el => el.props.placeholder === `graphSettings.${placeholder}`)?.props.modelValue
      assert.equal(inputValue('t-textarea', 'customInstructionsPlaceholder'), snapshot.customInstructions)
      assert.equal(inputValue('t-textarea', 'sampleTextPlaceholder'), snapshot.text)
      assert.deepEqual([...inputValue('t-select', 'tagsPlaceholder')], snapshot.tags)
      assert.deepEqual(classes('node-name-input').map(el => el.props.modelValue), snapshot.nodes.map(value => value.name))
      assert.deepEqual(classes('attribute-input').map(el => el.props.modelValue), snapshot.nodes.flatMap(value => value.attributes))
      assert.deepEqual(classes('relation-select').map(el => el.props.modelValue), snapshot.relations.flatMap(value => [value.node1, value.type, value.node2]))
    }
  }
  assert.equal(requests.length, 1)
  assert.equal(requests[0]!.force, false)
  return { requests, elements, opened, retry, expectState }
}

for (const embedded of [false, true]) {
  const mode = embedded ? 'embedded' : 'standalone'

  test(`${mode}: pending system information does not show a disabled database warning`, async t => {
    const f = fixture(t, embedded)
    await vue.nextTick()
    f.expectState('loading')
  })

  for (const engine of ['Neo4j', 'Memgraph', ' FutureEngine ']) {
    test(`${mode}: ${engine.trim()} enables graph settings`, async t => {
      const f = fixture(t, embedded)
      f.requests[0]!.resolve({ graph_database_engine: engine })
      await flush()
      f.expectState('enabled')
    })
  }

  test(`${mode}: only an explicit disabled engine shows the setup warning`, async t => {
    const f = fixture(t, embedded)
    f.requests[0]!.resolve({ graph_database_engine: 'Not Enabled' })
    await flush()
    f.expectState('disabled')
    if (!embedded) {
      f.elements('t-button').find(el => textOf(el).trim() === 'graphSettings.howToEnable')!.props.onClick()
      assert.deepEqual(f.opened, [['https://example.com/knowledge-graph', '_blank', 'noopener']])
    }
  })

  for (const error of [new Error('HTTP 500'), new Error('HTTP 403'), new Error('Request timed out')]) {
    test(`${mode}: ${error.message} shows a retryable failure instead of disabled`, async t => {
      const f = fixture(t, embedded)
      f.requests[0]!.reject(error)
      await flush()
      f.expectState('error')
    })
  }

  const invalidResponses: Array<[string, unknown]> = [
    ['null response', null],
    ['undefined response', undefined],
    ['missing engine', {}],
    ['null engine', { graph_database_engine: null }],
    ['empty engine', { graph_database_engine: '' }],
    ['whitespace engine', { graph_database_engine: '  ' }],
    ['boolean engine', { graph_database_engine: true }],
    ['numeric engine', { graph_database_engine: 42 }],
  ]
  for (const [label, info] of invalidResponses) {
    test(`${mode}: ${label} is a retryable failure`, async t => {
      const f = fixture(t, embedded)
      f.requests[0]!.resolve(info)
      await flush()
      f.expectState('error')
    })
  }

  test(`${mode}: retry forces a refresh and restores settings without changing saved configuration`, async t => {
    const f = fixture(t, embedded)
    f.requests[0]!.reject(new Error('HTTP 500'))
    await flush()
    f.expectState('error')
    f.retry()()
    await vue.nextTick()
    f.expectState('loading')
    assert.deepEqual(f.requests.map(request => request.force), [false, true])
    f.requests[1]!.resolve({ graph_database_engine: 'Neo4j' })
    await flush()
    f.expectState('enabled')
  })

  for (const staleOutcome of ['failure', 'disabled'] as const) {
    test(`${mode}: an older ${staleOutcome} cannot replace a successful retry`, async t => {
      const f = fixture(t, embedded)
      f.requests[0]!.reject(new Error('HTTP 500'))
      await flush()
      const retry = f.retry()
      retry()
      retry()
      assert.deepEqual(f.requests.map(request => request.force), [false, true, true])
      f.requests[2]!.resolve({ graph_database_engine: 'Neo4j' })
      await flush()
      f.expectState('enabled')
      if (staleOutcome === 'failure') f.requests[1]!.reject(new Error('Late HTTP 500'))
      else f.requests[1]!.resolve({ graph_database_engine: 'Not Enabled' })
      await flush()
      f.expectState('enabled')
    })
  }
}
