import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'

const source = readFileSync(new URL('./BatchTagDialog.vue', import.meta.url), 'utf8')
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


async function fixture() {
  const state = vue.reactive({ visible: false, kbId: 'kb', count: 2, preSelectedTagIds: ['a'], confirmLoading: false })
  const confirmations: string[][] = []
  const exports: { default?: vue.Component } = {}
  runInNewContext(compiled, { exports, require(name: string) {
    if (name === 'vue') return vue
    if (name === './KnowledgeTagPicker.vue') return { __esModule: true, default: vue.defineComponent({
      props: ['selectedIds'], emits: ['update:selectedIds', 'busy-change'],
      setup: (props, { emit }) => () => vue.h('picker', { selectedIds: props.selectedIds,
        onSelect: (ids: string[]) => emit('update:selectedIds', ids), onBusy: (busy: boolean) => emit('busy-change', busy),
      }),
    }) }
    throw new Error(name)
  } })
  const root = node('root')
  const app = renderer.createApp({ setup: () => () => vue.h(exports.default!, {
    ...state, 'onUpdate:visible': (value: boolean) => { state.visible = value },
    onConfirm: (ids: string[]) => confirmations.push([...ids]),
  }) })
  app.config.globalProperties.$t = translate as typeof app.config.globalProperties.$t
  for (const [name, type] of [['t-dialog','dialog'],['t-button','button'],['t-icon','icon']]) {
    app.component(name!, vue.defineComponent({ setup: (_props,{slots}) => () => vue.h(type!,{},slots.default?.()) }))
  }
  app.mount(root)
  const settle = async () => { for (let i=0;i<5;i++) await vue.nextTick() }
  const find = (predicate: (el: Host) => boolean) => { const el=all(root,predicate)[0]; assert.ok(el); return el }
  const fire = async (el: Host,event='onClick',value?: unknown) => { await el.props[event](value); await settle() }
  state.visible=true; await settle()
  return { state, confirmations, find, fire, settle, close: () => app.unmount() }
}

test('batch tagging starts with common tags and keeps the draft until the parent completes saving', async t => {
  const f=await fixture(); t.after(f.close)
  assert.deepEqual([...f.find(el=>el.type==='picker').props.selectedIds], ['a'])
  await f.fire(f.find(el=>el.type==='picker'),'onSelect',['a','b'])
  f.state.preSelectedTagIds=['c']; await f.settle()
  await f.fire(f.find(el=>el.type==='button'&&textOf(el)==='common.confirm'))
  assert.deepEqual(f.confirmations, [['a','b']])
  assert.equal(f.state.visible,true)
  f.state.visible=false; await f.settle()
  f.state.visible=true; await f.settle()
  assert.deepEqual([...f.find(el=>el.type==='picker').props.selectedIds], ['c'])
})

test('tag mutations and batch saving prevent duplicate confirms and closing', async t => {
  const f=await fixture(); t.after(f.close)
  await f.fire(f.find(el=>el.type==='picker'),'onBusy',true)
  await f.fire(f.find(el=>el.type==='button'&&textOf(el)==='common.confirm'))
  await f.fire(f.find(el=>el.type==='button'&&textOf(el)==='common.cancel'))
  assert.equal(f.confirmations.length,0)
  assert.equal(f.state.visible,true)
  await f.fire(f.find(el=>el.type==='picker'),'onBusy',false)
  f.state.confirmLoading=true; await f.settle()
  await f.fire(f.find(el=>el.type==='button'&&textOf(el)==='common.confirm'))
  await f.fire(f.find(el=>el.type==='button'&&textOf(el)==='common.cancel'))
  assert.equal(f.confirmations.length,0)
  assert.equal(f.state.visible,true)
})
