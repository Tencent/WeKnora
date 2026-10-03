import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'

const source = readFileSync(new URL('./KnowledgeTagPicker.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'picker-test', inlineTemplate: true }).content, {
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
const hasClass = (el: Host, name: string) => String(el.props.class || '').split(' ').includes(name)


async function fixture() {
  const calls: { create: unknown[][]; rename: unknown[][]; remove: unknown[][] } = { create: [], rename: [], remove: [] }
  let rows = [{ id: 'used', seq_id: 1, name: 'Used', knowledge_count: 2 }, { id: 'free', seq_id: 2, name: 'Free', knowledge_count: 0 }]
  const exports: { default?: vue.Component } = {}
  runInNewContext(compiled, { exports, setTimeout, clearTimeout, require(name: string) {
    if (name === 'vue') return vue
    if (name === 'vue-i18n') return { useI18n: () => ({ t: translate }) }
    if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error() {}, warning() {} } }
    if (name === '@/api/knowledge-base') return {
      listKnowledgeTags: async (_kb: string, params: { keyword?: string }) => {
        const filtered = rows.filter(r => !params.keyword || r.name.includes(params.keyword))
        return { data: { data: filtered.map(r => ({ ...r })), total: filtered.length } }
      },
      createKnowledgeBaseTag: async (...args: any[]) => {
        calls.create.push(args)
        const tag = { id: 'new', seq_id: 3, name: args[1].name, knowledge_count: 0 }
        rows.push(tag)
        return { data: tag }
      },
      updateKnowledgeBaseTag: async (...args: any[]) => { calls.rename.push(args); rows.find(r => r.id === args[1])!.name = args[2].name },
      deleteKnowledgeBaseTag: async (...args: any[]) => { calls.remove.push(args); rows = rows.filter(r => r.seq_id !== args[1]) },
    }
    throw new Error('Unexpected import: ' + name)
  } })
  const selectedIds = vue.ref<string[]>([])
  const changes: unknown[] = []
  const root = node('root')
  const app = renderer.createApp({ setup: () => () => vue.h(exports.default!, {
    kbId: 'kb', selectedIds: selectedIds.value,
    'onUpdate:selectedIds': (ids: string[]) => { selectedIds.value = ids },
    onChanged: (payload: unknown) => changes.push(payload),
  }) })
  app.config.globalProperties.$t = translate as typeof app.config.globalProperties.$t
  for (const [name, type] of [['t-input', 'input'], ['t-button', 'button'], ['t-checkbox', 'checkbox'], ['t-popconfirm', 'popconfirm'], ['t-tooltip', 'tooltip'], ['t-icon', 'icon'], ['t-loading', 'loading']]) {
    app.component(name!, vue.defineComponent({ setup: (_props, { slots }) => () => vue.h(type!, {}, slots.default?.()) }))
  }
  app.component('t-popup', vue.defineComponent({
    props: ['visible'], emits: ['visible-change'],
    setup: (props, { slots, emit }) => () => vue.h('popup', { onOpen: () => emit('visible-change', true) }, [slots.default?.(), props.visible ? slots.content?.() : null]),
  }))
  app.mount(root)
  const settle = async () => { for (let i = 0; i < 6; i++) await vue.nextTick() }
  await settle()
  const find = (predicate: (el: Host) => boolean) => { const el = all(root, predicate)[0]; assert.ok(el); return el }
  const fire = async (el: Host, event = 'onClick', value?: unknown) => { await el.props[event](value); await settle() }
  const search = async (value: string) => {
    await fire(find(el => el.type === 'input'), 'onUpdate:modelValue', value)
    await new Promise(resolve => setTimeout(resolve, 270))
    await settle()
  }
  return { root, calls, selectedIds, changes, find, fire, search, close: () => app.unmount() }
}

test('search, create and select happen in one panel without losing previous selection', async t => {
  const f = await fixture(); t.after(f.close)
  await f.fire(f.find(el => el.type === 'checkbox'), 'onChange', true)
  assert.deepEqual([...f.selectedIds.value], ['used'])
  await f.search('New')
  await f.fire(f.find(el => hasClass(el, 'tag-picker-create')))
  assert.deepEqual(JSON.parse(JSON.stringify(f.calls.create)), [['kb', { name: 'New' }]])
  assert.deepEqual([...f.selectedIds.value], ['used', 'new'])
  assert.equal(f.changes.length, 1)
})

test('rename saves in place without changing selection', async t => {
  const f = await fixture(); t.after(f.close)
  f.selectedIds.value = ['used']; await vue.nextTick()
  await f.fire(f.find(el => el.type === 'popup'), 'onOpen')
  await f.fire(f.find(el => el.type === 'button' && textOf(el) === 'knowledgeBase.tagEditAction'))
  const input = f.find(el => el.type === 'input' && el.props['aria-label'] === 'knowledgeBase.tagEditAction')
  await f.fire(input, 'onUpdate:modelValue', 'Renamed')
  await f.fire(f.find(el => el.type === 'button' && el.props['aria-label'] === 'common.save'))
  assert.deepEqual(JSON.parse(JSON.stringify(f.calls.rename)), [['kb', 'used', { name: 'Renamed' }]])
  assert.deepEqual([...f.selectedIds.value], ['used'])
  assert.ok(textOf(f.root).includes('Renamed'))
})

test('selected tags move above unselected tags and search preserves selection', async t => {
  const f = await fixture(); t.after(f.close)
  const headings = () => all(f.root, el => hasClass(el, 'tag-picker-group-title')).map(textOf)
  const labels = () => all(f.root, el => el.type === 'checkbox').map(textOf)
  assert.deepEqual(headings(), ['knowledgeBase.tagPickerUnselected2'])
  await f.fire(f.find(el => el.type === 'checkbox' && textOf(el) === 'Free'), 'onChange', true)
  assert.deepEqual(headings(), ['knowledgeBase.tagPickerSelected1', 'knowledgeBase.tagPickerUnselected1'])
  assert.deepEqual(labels(), ['Free', 'Used'])
  await f.search('Used')
  assert.deepEqual(headings(), ['knowledgeBase.tagPickerUnselected1'])
  assert.deepEqual([...f.selectedIds.value], ['free'])
  await f.search('')
  assert.deepEqual(labels(), ['Free', 'Used'])
  await f.fire(f.find(el => el.type === 'checkbox' && textOf(el) === 'Used'), 'onChange', true)
  assert.deepEqual(headings(), ['knowledgeBase.tagPickerSelected2'])
  await f.fire(f.find(el => el.type === 'checkbox' && textOf(el) === 'Free'), 'onChange', false)
  assert.deepEqual(headings(), ['knowledgeBase.tagPickerSelected1', 'knowledgeBase.tagPickerUnselected1'])
  assert.deepEqual(labels(), ['Used', 'Free'])
})

test('used tags cannot be deleted; unused deletion never requests cascading content removal', async t => {
  const f = await fixture(); t.after(f.close)
  await f.fire(f.find(el => el.type === 'popup'), 'onOpen')
  const deleteButtons = all(f.root, el => el.type === 'button' && textOf(el) === 'knowledgeBase.tagDeleteAction')
  assert.ok('disabled' in deleteButtons[0]!.props && deleteButtons[0]!.props.disabled !== false)
  assert.equal(all(f.root, el => el.type === 'popconfirm').length, 0)
  await f.fire(all(f.root, el => el.type === 'popup')[1]!, 'onOpen')
  assert.equal(all(f.root, el => el.type === 'popconfirm').length, 1)
  f.selectedIds.value = ['used', 'free']; await vue.nextTick()
  await f.fire(f.find(el => el.type === 'popconfirm'), 'onConfirm')
  assert.deepEqual(JSON.parse(JSON.stringify(f.calls.remove)), [['kb', 2]])
  assert.deepEqual([...f.selectedIds.value], ['used'])
  assert.equal(all(f.root, el => hasClass(el, 'tag-picker-row')).length, 1)
})
