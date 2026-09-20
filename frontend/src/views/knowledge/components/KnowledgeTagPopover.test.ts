import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'

const source = readFileSync(new URL('./KnowledgeTagPopover.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'tag-popover-test', inlineTemplate: true }).content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const translate = (key: string) => key
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


async function fixture(fail = false) {
  const state = vue.reactive({ visible: false, kbId: 'kb', knowledgeId: 'doc', tags: [{ id: 'a' }] })
  const requests: unknown[] = []
  let changes = 0
  const exports: { default?: vue.Component } = {}
  runInNewContext(compiled, { exports, require(name: string) {
    if (name === 'vue') return vue
    if (name === 'vue-i18n') return { useI18n: () => ({ t: translate }) }
    if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error() {} } }
    if (name === '@/api/knowledge-base') return { updateKnowledgeTagBatch: async (data: unknown) => {
      requests.push(JSON.parse(JSON.stringify(data)))
      if (fail) throw new Error('save failed')
    } }
    if (name === './KnowledgeTagPicker.vue') return { __esModule: true, default: vue.defineComponent({ props: ['selectedIds'], emits: ['update:selectedIds'], setup: (props, { emit }) => () => vue.h('picker', { selectedIds: props.selectedIds, 'onUpdate:selectedIds': (ids: string[]) => emit('update:selectedIds', ids) }) }) }
    throw new Error(name)
  } })
  const root = node('root')
  const app = renderer.createApp({ setup: () => () => vue.h(exports.default!, {
    ...state, 'onUpdate:visible': (value: boolean) => { state.visible = value }, onChanged: () => { changes++ },
  }) })
  app.config.globalProperties.$t = translate as typeof app.config.globalProperties.$t
  app.component('t-popup', vue.defineComponent({
    props: ['visible'], setup: (props, { slots }) => () => vue.h('popup', {}, props.visible ? slots.content?.() : []),
  }))
  app.component('t-button', vue.defineComponent({ setup: (_props, { slots }) => () => vue.h('button', {}, slots.default?.()) }))
  app.mount(root)
  const settle = async () => { for (let i=0;i<5;i++) await vue.nextTick() }
  const find = (predicate: (el: Host) => boolean) => { const el = all(root,predicate)[0]; assert.ok(el); return el }
  const fire = async (el: Host, event = 'onClick', value?: unknown) => { await el.props[event](value); await settle() }
  state.visible = true; await settle()
  return { state, requests, changes: () => changes, root, find, fire, settle, close: () => app.unmount() }
}

test('opening hydrates tags; refreshing the document preserves an open draft; cancel does not save', async t => {
  const f = await fixture(); t.after(f.close)
  const picker = f.find(el => el.type === 'picker')
  assert.deepEqual([...picker.props.selectedIds], ['a'])
  await f.fire(picker, 'onUpdate:selectedIds', ['a','b'])
  f.state.tags = [{ id:'other' }]; await f.settle()
  assert.deepEqual([...picker.props.selectedIds], ['a','b'])
  await f.fire(f.find(el => el.type === 'button' && textOf(el) === 'common.cancel'))
  assert.equal(f.state.visible, false)
  assert.equal(f.requests.length, 0)
  f.state.visible = true; await f.settle()
  assert.deepEqual([...f.find(el => el.type === 'picker').props.selectedIds], ['other'])
})

test('confirm saves the target document and closes only after successful save', async t => {
  const f = await fixture(); t.after(f.close)
  await f.fire(f.find(el => el.type === 'picker'), 'onUpdate:selectedIds', ['b'])
  await f.fire(f.find(el => el.type === 'button' && textOf(el) === 'common.confirm'))
  assert.deepEqual(f.requests, [{ updates: { doc: ['b'] } }])
  assert.equal(f.state.visible, false)
  assert.equal(f.changes(), 1)
})

test('a failed save preserves the popup and draft for retry', async t => {
  const f = await fixture(true); t.after(f.close)
  await f.fire(f.find(el => el.type === 'picker'), 'onUpdate:selectedIds', ['b'])
  await f.fire(f.find(el => el.type === 'button' && textOf(el) === 'common.confirm'))
  assert.equal(f.state.visible, true)
  assert.equal(f.changes(), 0)
  assert.deepEqual([...f.find(el => el.type === 'picker').props.selectedIds], ['b'])
})
