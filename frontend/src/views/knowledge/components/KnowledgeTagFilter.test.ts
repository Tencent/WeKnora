import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'

const source = readFileSync(new URL('./KnowledgeTagFilter.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'tag-filter-test', inlineTemplate: true })
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const translate = (key: string, values?: { count: number }) => values ? `${key}:${values.count}` : key
const exports: { default?: vue.Component } = {}
runInNewContext(compiled, {
  exports,
  require(name: string) {
    if (name === 'vue') return vue
    if (name === 'vue-i18n') return { useI18n: () => ({ t: translate }) }
    throw new Error(`Unexpected import: ${name}`)
  },
})

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

function fixture(variant: 'documents' | 'faq') {
  const state = vue.reactive({
    variant, tags: [
      { id: 'a', name: 'Alpha', knowledge_count: 12, chunk_count: 31 },
      { id: 'b', name: 'Beta', knowledge_count: 0, chunk_count: 2 },
    ],
    selectedIds: [] as string[], total: 7, loading: false, loadingMore: false, hasMore: false,
    canManage: true, search: '', cleared: false,
  })
  const events = { changes: [] as string[][], loadMore: 0, manage: 0 }
  const root = node('root')
  const app = renderer.createApp({ setup: () => () => vue.h(exports.default!, {
    ...state,
    'onUpdate:search': (value: string) => { state.search = value },
    'onUpdate:cleared': (value: boolean) => { state.cleared = value },
    onChange: (ids: string[]) => { events.changes.push([...ids]); state.selectedIds = ids },
    onLoadMore: () => { events.loadMore++ }, onManage: () => { events.manage++ },
  }) })
  app.config.globalProperties.$t = translate as typeof app.config.globalProperties.$t
  app.component('t-popup', vue.defineComponent({
    props: ['visible'], emits: ['update:visible'],
    setup: (props, { emit, slots }) => () => vue.h('popup', {
      visible: props.visible, onOpen: () => emit('update:visible', true),
    }, [slots.default?.(), props.visible ? vue.h('overlay', {}, slots.content?.()) : null]),
  }))
  app.component('t-input', vue.defineComponent({
    props: ['modelValue', 'modelModifiers'], emits: ['update:modelValue'],
    setup: (props, { emit }) => () => vue.h('input', {
      value: props.modelValue, onInput: (value: string) => emit('update:modelValue', value),
    }),
  }))
  for (const [name, type] of [['t-button', 'button'], ['t-icon', 'icon'], ['t-skeleton', 'skeleton']]) {
    app.component(name!, vue.defineComponent({ setup: (_props, { slots }) => () => vue.h(type!, {}, slots.default?.()) }))
  }
  app.mount(root)
  const elements = (name: string) => all(root, el => hasClass(el, name))
  const one = (name: string) => {
    const el = elements(name)[0]; assert.ok(el, `missing .${name}`); return el
  }
  const fire = async (el: Host, event = 'onClick', value?: unknown) => {
    let stopped = false
    el.props[event](value ?? { stopPropagation() { stopped = true }, preventDefault() {} })
    await vue.nextTick()
    return stopped
  }
  return {
    state, events, root, elements, one, fire,
    open: () => fire(all(root, el => el.type === 'popup')[0]!, 'onOpen'),
    close: () => app.unmount(),
  }
}

for (const variant of ['documents', 'faq'] as const) {
  test(`${variant}: renders counts, labels, selection and tooltips`, async t => {
    const f = fixture(variant); t.after(f.close)
    assert.equal(textOf(f.one('doc-tag-filter-trigger__label')), 'knowledgeBase.allTags')
    await f.open()
    assert.equal(textOf(f.one('tag-filter-panel__count')), '(7)')
    assert.equal(textOf(f.elements('tag-filter-chip__count')[0]!), variant === 'documents' ? '12' : '31')
    assert.equal(f.elements('tag-filter-chip')[0]!.props.title, `Alpha (${variant === 'documents' ? 12 : 31})`)
    assert.equal(hasClass(f.one('tag-filter-panel'), 'tag-filter-panel--documents'), variant === 'documents')
    await f.fire(f.elements('tag-filter-chip')[0]!)
    assert.deepEqual(f.events.changes, [['a']])
    assert.equal(textOf(f.one('doc-tag-filter-trigger__label')), 'Alpha')
    assert.equal(f.one('doc-tag-filter-trigger').props.title, 'Alpha')
    await f.fire(f.elements('tag-filter-chip')[1]!)
    assert.equal(textOf(f.one('doc-tag-filter-trigger__label')), 'knowledgeBase.tagFilterMulti:2')
    assert.equal(f.one('doc-tag-filter-trigger').props.title, 'Alpha、Beta')
    await f.fire(f.elements('tag-filter-chip')[0]!)
    assert.deepEqual(f.events.changes.at(-1), ['b'])
    assert.equal(hasClass(f.elements('tag-filter-chip')[0]!, 'active'), false)
  })

  test(`${variant}: clearing stops propagation and parent resets restore the placeholder`, async t => {
    const f = fixture(variant); t.after(f.close)
    f.state.selectedIds = ['a']; await vue.nextTick()
    assert.equal(f.elements('t-input__clear').length, 0)
    await f.fire(f.one('doc-tag-filter-trigger'), 'onMouseenter')
    assert.equal(await f.fire(f.one('t-input__clear')), true)
    assert.equal(f.state.cleared, true)
    assert.deepEqual([...f.state.selectedIds], [])
    assert.equal(textOf(f.one('doc-tag-filter-trigger__label')), 'knowledgeBase.tagFilterPlaceholder')
    assert.equal(hasClass(f.one('doc-tag-filter-trigger'), 'is-placeholder'), true)
    await f.open(); await f.fire(f.elements('tag-filter-chip')[0]!)
    assert.equal(f.state.cleared, false)
    await f.fire(f.elements('tag-filter-chip')[0]!)
    assert.equal(textOf(f.one('doc-tag-filter-trigger__label')), 'knowledgeBase.allTags')
    f.state.cleared = true; await vue.nextTick()
    f.state.cleared = false; await vue.nextTick()
    assert.equal(hasClass(f.one('doc-tag-filter-trigger'), 'is-placeholder'), false)
  })

  test(`${variant}: search, skeletons, empty results and paging stay controlled by the parent`, async t => {
    const f = fixture(variant); t.after(f.close)
    f.state.tags = []; f.state.loading = true; f.state.total = 0
    await f.open()
    assert.equal(all(f.root, el => el.type === 'skeleton').length, 8)
    assert.equal(f.elements('tag-empty-state').length, 0)
    await f.fire(all(f.root, el => el.type === 'input')[0]!, 'onInput', '  title  ')
    assert.equal(f.state.search, 'title')
    f.state.loading = false; await vue.nextTick()
    assert.equal(textOf(f.one('tag-empty-state')), 'knowledgeBase.tagEmptyResult')
    f.state.tags = [{ id: 'a', name: 'Result', knowledge_count: 0, chunk_count: 0 }]
    f.state.hasMore = true; f.state.loadingMore = true; await vue.nextTick()
    assert.equal(textOf(f.one('tag-filter-panel__count')), '(1)')
    const button = all(f.one('tag-load-more'), el => el.type === 'button')[0]!
    assert.equal(button.props.loading, true)
    assert.equal(await f.fire(button), true)
    assert.equal(f.events.loadMore, 1)
    assert.equal(f.state.search, 'title')
  })

  test(`${variant}: viewers can filter and managers can open management`, async t => {
    const f = fixture(variant); t.after(f.close)
    f.state.canManage = false; await f.open()
    assert.equal(f.elements('tag-manage-link').length, 0)
    await f.fire(f.elements('tag-filter-chip')[0]!)
    assert.deepEqual([...f.state.selectedIds], ['a'])
    f.state.canManage = true; await vue.nextTick()
    await f.fire(f.one('tag-manage-link'))
    assert.equal(f.events.manage, 1)
    assert.equal(all(f.root, el => el.type === 'overlay').length, 0)
  })
}

test('external selection and tag pages update labels without dropping unloaded selections', async t => {
  const f = fixture('documents'); t.after(f.close)
  f.state.selectedIds = ['unloaded']; await vue.nextTick()
  assert.equal(textOf(f.one('doc-tag-filter-trigger__label')), 'knowledgeBase.allTags')
  assert.equal(f.one('doc-tag-filter-trigger').props.title, 'knowledgeBase.tagFilterTitle')
  await f.open(); await f.fire(f.elements('tag-filter-chip')[0]!)
  assert.deepEqual([...f.state.selectedIds], ['unloaded', 'a'])
  assert.equal(f.one('doc-tag-filter-trigger').props.title, 'Alpha')
  f.state.tags[0]!.name = 'Renamed'; await vue.nextTick()
  assert.equal(f.one('doc-tag-filter-trigger').props.title, 'Renamed')
})
