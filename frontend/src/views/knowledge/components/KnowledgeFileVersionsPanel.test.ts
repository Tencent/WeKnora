import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'

const source = readFileSync(new URL('./KnowledgeFileVersionsPanel.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'versions-test', inlineTemplate: true }).content, {
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

const version = (number: number, knowledgeId = 'doc', current = true) => ({
  id: `version-${number}`, knowledge_id: knowledgeId, version: number, file_name: `file-${number}.pdf`,
  file_type: 'pdf', file_size: 42, file_hash: 'hash', created_at: '2026-01-01T00:00:00Z', is_current: current,
})
const response = (number: number, knowledgeId = 'doc', total = 1) => ({ success: true, data: { items: [version(number, knowledgeId)], total } })

async function fixture(options: {
  canUpload?: boolean; canDownload?: boolean; status?: string; initialFile?: File;
  list?: (...args: any[]) => Promise<any>; upload?: (...args: any[]) => Promise<any>;
} = {}) {
  const calls = { list: [] as any[][], upload: [] as any[][], download: [] as any[][], uploaded: [] as string[], warnings: [] as string[] }
  const exports: { default?: vue.Component } = {}
  runInNewContext(compiled, { exports, require(name: string) {
    if (name === 'vue') return vue
    if (name === 'vue-i18n') return { useI18n: () => ({ t: translate, locale: vue.ref('en-US') }) }
    if (name === '@/utils/files') return { formatFileSize: (value: number) => `${value} B` }
    if (name === '@/utils') return { MAX_FILE_SIZE_MB: 100 }
    if (name === '../wikiStatusRefresh') return { isKnowledgeParseInFlight: (s: string) => ['pending', 'processing', 'finalizing'].includes(s) }
    if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error() {}, warning(message: string) { calls.warnings.push(message) } } }
    if (name === '@/api/knowledge-base') return {
      listKnowledgeFileVersions: async (...args: any[]) => { calls.list.push(args); return options.list ? options.list(...args) : response(3) },
      uploadKnowledgeFileVersion: async (...args: any[]) => { calls.upload.push(args); return options.upload ? options.upload(...args) : { success: true } },
      downloadKnowledgeFileVersion: async (...args: any[]) => { calls.download.push(args); throw new Error('skip browser download') },
    }
    throw new Error('Unexpected import: ' + name)
  } })
  const props = vue.reactive({ visible: true, knowledge: { id: 'doc', file_name: 'document.pdf', parse_status: options.status || 'completed' }, canUpload: options.canUpload ?? true, canDownload: options.canDownload ?? true, initialFile: options.initialFile })
  const root = node('root')
  const app = renderer.createApp({ setup: () => () => vue.h(exports.default!, { ...props,
    onUploaded: (id: string) => calls.uploaded.push(id),
    onFileConsumed: () => { props.initialFile = undefined },
    'onUpdate:visible': (visible: boolean) => { props.visible = visible },
  }) })
  for (const [name, type] of [['t-icon', 'icon'], ['t-button', 'button'], ['t-tag', 'tag'], ['t-loading', 'loading'], ['t-pagination', 'pagination']]) {
    app.component(name!, vue.defineComponent({ setup: (_props, { slots }) => () => vue.h(type!, {}, slots.default?.()) }))
  }
  app.mount(root)
  const settle = async () => { for (let i = 0; i < 8; i++) await vue.nextTick() }
  await settle()
  const find = (predicate: (el: Host) => boolean) => { const el = all(root, predicate)[0]; assert.ok(el); return el }
  const fire = async (el: Host, event = 'onClick', value?: unknown) => { await el.props[event](value); await settle() }
  const chooseFile = async () => { const file = { name: 'revised.pdf', size: 128 }; await fire(find(el => el.type === 'input'), 'onChange', { target: { files: [file] } }); return file }
  const uploadButton = () => find(el => el.type === 'button' && textOf(el).trim() === 'knowledgeBase.fileVersions.confirmUpload')
  return { props, calls, root, find, fire, settle, chooseFile, uploadButton, close: () => app.unmount() }
}

test('upload pins the current revision and retains it while paging older versions', async t => {
  const f = await fixture({ list: async (_id, offset) => offset ? { success: true, data: { items: [version(1, 'doc', false)], total: 21 } } : response(3, 'doc', 21) }); t.after(f.close)
  const file = await f.chooseFile()
  await f.fire(f.find(el => el.type === 'pagination'), 'onCurrentChange', 2)
  assert.equal(f.uploadButton().props.disabled, false)
  await f.fire(f.uploadButton())
  assert.deepEqual(f.calls.upload, [['doc', file, 3]])
  assert.deepEqual(f.calls.uploaded, ['doc'])
  assert.deepEqual(f.calls.list, [['doc', 0, 20], ['doc', 20, 20], ['doc', 0, 20]])
  assert.equal(f.props.visible, true)
  assert.ok(textOf(f.root).includes('knowledgeBase.fileVersions.upload'))
})

test('readers see history without original-file download or upload controls', async t => {
  const f = await fixture({ canUpload: false, canDownload: false }); t.after(f.close)
  assert.ok(textOf(f.root).includes('v3'))
  assert.equal(all(f.root, el => el.type === 'input').length, 0)
  assert.equal(all(f.root, el => el.type === 'button' && String(el.props['aria-label']).startsWith('common.download')).length, 0)
  assert.deepEqual(f.calls.upload, [])
})

test('processing documents cannot upload and a revision conflict refreshes the expected version', async t => {
  let revision = 3
  let conflict = true
  const f = await fixture({ status: 'processing', list: async () => response(revision), upload: async () => {
    if (conflict) { conflict = false; revision = 4; throw { status: 409 } }
    return { success: true }
  } }); t.after(f.close)
  assert.equal(f.find(el => el.type === 'button' && textOf(el).trim() === 'knowledgeBase.fileVersions.upload').props.disabled, true)
  await f.chooseFile()
  assert.equal(all(f.root, el => el.type === 'button' && textOf(el).trim() === 'knowledgeBase.fileVersions.confirmUpload').length, 0)
  assert.equal(f.calls.upload.length, 0)
  f.props.knowledge.parse_status = 'completed'; await f.settle()
  await f.chooseFile()
  await f.fire(f.uploadButton())
  assert.deepEqual(f.calls.warnings, ['knowledgeBase.fileVersions.conflict'])
  assert.equal(f.calls.uploaded.length, 0)
  await f.fire(f.uploadButton())
  assert.equal(f.calls.upload[1]![2], 4)
  assert.deepEqual(f.calls.uploaded, ['doc'])
})

test('an old history request cannot replace the newly selected document', async t => {
  let resolveOld!: (value: any) => void
  const f = await fixture({ list: async id => id === 'doc' ? new Promise(resolve => { resolveOld = resolve }) : response(9, id) }); t.after(f.close)
  f.props.knowledge = { id: 'new-doc', file_name: 'new.pdf', parse_status: 'completed' }; await f.settle()
  resolveOld(response(3)); await f.settle()
  assert.ok(textOf(f.root).includes('v9'))
  assert.equal(textOf(f.root).includes('v3'), false)
  await f.chooseFile(); await f.fire(f.uploadButton())
  assert.equal(f.calls.upload[0]![0], 'new-doc')
  assert.equal(f.calls.upload[0]![2], 9)
})

test('history download targets the selected file revision', async t => {
  const f = await fixture({ list: async () => ({ success: true, data: { items: [version(3), version(2, 'doc', false)], total: 2 } }) }); t.after(f.close)
  const previousRow = f.find(el => hasClass(el, 'version-row') && textOf(el).includes('v2'))
  const download = all(previousRow, el => el.type === 'button' && el.props['aria-label'] === 'common.download · v2')[0]!
  await f.fire(download)
  assert.deepEqual(f.calls.download, [['doc', 2]])
})


test('switching documents clears a selected upload', async t => {
  const f = await fixture(); t.after(f.close)
  await f.chooseFile()
  assert.ok(textOf(f.root).includes('revised.pdf'))
  f.props.knowledge = { id: 'another-doc', file_name: 'another.pdf', parse_status: 'completed' }
  await f.settle()
  assert.equal(textOf(f.root).includes('revised.pdf'), false)
  assert.ok(textOf(f.root).includes('knowledgeBase.fileVersions.upload'))
})

test('empty uploads are rejected before enabling confirmation', async t => {
  const f = await fixture(); t.after(f.close)
  await f.fire(f.find(el => el.type === 'input'), 'onChange', { target: { files: [{ name: 'empty.pdf', size: 0 }] } })
  assert.deepEqual(f.calls.warnings, ['knowledgeBase.fileVersions.invalidSize'])
  assert.equal(all(f.root, el => el.type === 'button' && textOf(el).trim() === 'knowledgeBase.fileVersions.confirmUpload').length, 0)
  assert.equal(f.calls.upload.length, 0)
})


test('a file selected from the card menu is confirmed once and is not restored when history reopens', async t => {
  const file = { name: 'menu-selection.pdf', size: 512 } as File
  const f = await fixture({ initialFile: file }); t.after(f.close)
  assert.ok(textOf(f.root).includes(file.name))
  assert.equal(f.props.initialFile, undefined)
  assert.equal(f.calls.upload.length, 0)
  await f.fire(f.uploadButton())
  assert.deepEqual(f.calls.upload, [['doc', file, 3]])
  f.props.visible = false; await f.settle()
  f.props.visible = true; await f.settle()
  assert.equal(textOf(f.root).includes(file.name), false)
})

test('menu-selected files cannot bypass the processing guard', async t => {
  const f = await fixture({ status: 'processing', initialFile: { name: 'menu.pdf', size: 512 } as File }); t.after(f.close)
  assert.equal(textOf(f.root).includes('menu.pdf'), false)
  assert.equal(f.props.initialFile, undefined)
  assert.equal(f.calls.upload.length, 0)
})

async function menuFixture(canMutateKnowledge: boolean, canDownload: boolean, type = 'file', parse_status = 'completed') {
  const { descriptor } = parse(readFileSync(new URL('./DocumentActionMenu.vue', import.meta.url), 'utf8'))
  const code = ts.transpileModule(compileScript(descriptor, { id: 'menu-test', inlineTemplate: true }).content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const exports: { default?: vue.Component } = {}
  runInNewContext(code, { exports, require: (name: string) => { assert.equal(name, 'vue'); return vue } })
  const root = node('root')
  let uploads = 0
  const app = renderer.createApp({ setup: () => () => vue.h(exports.default!, {
    item: { id: 'doc', type, parse_status }, canMutateKnowledge, canDownload, traceVisible: false,
    onUploadVersion: () => uploads++,
  }) })
  app.config.globalProperties.$t = translate
  for (const name of ['t-icon', 't-popconfirm']) app.component(name, { setup: (_props: any, { slots }: any) => () => vue.h('span', {}, slots.default?.()) })
  app.mount(root); await vue.nextTick()
  return { root, uploads: () => uploads, close: () => app.unmount() }
}

test('upload menu entry requires file type, edit permission and original download permission', async () => {
  for (const [edit, download, type, expected] of [
    [true, true, 'file', 1], [false, true, 'file', 0], [true, false, 'file', 0], [true, true, 'manual', 0],
  ] as const) {
    const f = await menuFixture(edit, download, type)
    try {
      const entries = all(f.root, el => hasClass(el, 'doc-action-menu-item') && textOf(el).trim() === 'knowledgeBase.fileVersions.upload')
      assert.equal(entries.length, expected)
      if (entries[0]) { entries[0].props.onClick({ stopPropagation() {} }); assert.equal(f.uploads(), 1) }
    } finally { f.close() }
  }
})

test('processing disables the upload menu entry', async () => {
  const f = await menuFixture(true, true, 'file', 'processing')
  try {
    const entry = all(f.root, el => hasClass(el, 'doc-action-menu-item') && textOf(el).trim() === 'knowledgeBase.fileVersions.upload')[0]!
    assert.equal(entry.props['aria-disabled'], true)
    entry.props.onClick({ stopPropagation() {} })
    assert.equal(f.uploads(), 0)
  } finally { f.close() }
})
