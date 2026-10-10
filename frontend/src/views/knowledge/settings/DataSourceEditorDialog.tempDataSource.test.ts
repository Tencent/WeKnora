import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'

// The draft data source the resource picker lists through. A picker cannot call
// the resource endpoint before a data source row exists, so the dialog creates a
// paused one on first use — but only when the create endpoint could accept it:
// a request without a knowledge base (ErrKnowledgeBaseNotFound) or without a
// registered connector type (ErrConnectorNotFound) is rejected before the
// connector is reached, so it is never sent and never reported. These tests pin
// that contract for the generic picker and for the Feishu/Lark Drive root
// loader, which creates the same draft for its own listing: the four scenarios
// (normal load, missing parameters, retry after restoring them, error report),
// plus the three things a shared draft has to get right — one creation for
// concurrent callers, an answer without an id treated as a failure, and a failed
// attempt that the next one can retry.
const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./DataSourceEditorDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'datasource-editor-tempds-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

// A lazy tree of two roots, which is all the picker needs to show that a
// listing went through.
const TREE_RESOURCES = [
  { external_id: 'page-1', name: 'Handbook', type: 'page', has_children: false },
  { external_id: 'page-2', name: 'Policies', type: 'page', has_children: false },
]

// A Drive root listing: the folder token is the root, and the single row below
// it is what makes loadDriveRoot reveal the tree.
const DRIVE_RESOURCES = [
  {
    external_id: 'folderTokenSynthetic:fileA', name: 'A.docx', type: 'file',
    parent_id: 'folderTokenSynthetic', has_children: false,
  },
]

async function fixture({
  resources = [] as any[],
  createResult = { id: 'temp-one' } as any,
  createFails = false,
  listFails = null as Error | null,
} = {}) {
  const calls: Array<{ method: string; args: any[] }> = []
  // Resource listings are counted apart from the data source writes: the tests
  // assert which id a listing went through (and that a skipped load sent none).
  const resourceCalls: string[] = []
  // MessagePlugin.error texts, so "reported nothing" and "reported this" are
  // both assertions rather than assumptions.
  const messages: string[] = []
  // Mutable per test: a case flips the create result or the failure on to show
  // what the next attempt does with it.
  const state = { createResult, createFails, listFails }
  const api = {
    async createDataSource(data: any) {
      calls.push({ method: 'createDataSource', args: [JSON.parse(JSON.stringify(data))] })
      if (state.createFails) throw new Error('create failed')
      return state.createResult
    },
    async updateDataSource(id: string, data: any) {
      calls.push({ method: 'updateDataSource', args: [id, JSON.parse(JSON.stringify(data))] })
    },
    async listResources(id: string, parentId?: string) {
      resourceCalls.push(parentId ?? id)
      if (state.listFails) throw state.listFails
      return { data: resources }
    },
    async resolveResourceAncestors() { return { data: { ancestors: [] } } },
    async deleteDataSource() {},
    async triggerSync() {},
    async validateConnection() {},
    async validateCredentials() {},
    async putDataSourceCredentials() {},
    async deleteDataSourceCredentials() {},
  }
  const props = reactive({
    visible: false, kbId: 'kb-one',
    // Create mode: no data source row exists yet, so the picker has to create
    // the draft it lists through.
    dataSource: null,
  })
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'tdesign-vue-next') {
        return {
          MessagePlugin: {
            warning() {}, success() {},
            error(msg: string) { messages.push(msg) },
          },
        }
      }
      if (name === '@/api/datasource') return api
      return { default: {} }
    },
    URL, console,
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance }) })
  app.mount({})
  props.visible = true
  await nextTick()
  const vm = instance.value
  const creates = () => calls.filter(call => call.method === 'createDataSource').length
  return { vm, props, calls, resourceCalls, messages, state, creates, close: () => app.unmount() }
}

test('a load with a usable form creates one paused draft and lists through it', async () => {
  const f = await fixture({ resources: TREE_RESOURCES })
  try {
    f.vm.form.type = 'notion'
    await f.vm.loadResources()
    assert.equal(f.creates(), 1)
    const payload = f.calls[0].args[0]
    assert.equal(payload.type, 'notion')
    assert.equal(payload.knowledge_base_id, 'kb-one')
    assert.equal(payload.status, 'paused')
    assert.equal(f.vm.tempDsId, 'temp-one')
    assert.deepEqual(f.resourceCalls, ['temp-one'])
    assert.deepEqual([...f.vm.resources].map((r: any) => r.external_id), ['page-1', 'page-2'])
    assert.equal(f.vm.loadingResources, false)
    assert.deepEqual(f.messages, [])

    // The draft is remembered: a second load keeps it in step with the form
    // (create mode) instead of creating another row.
    await f.vm.loadResources()
    assert.equal(f.creates(), 1)
    assert.deepEqual(f.calls.map(call => call.method), ['createDataSource', 'updateDataSource'])
    assert.deepEqual(f.resourceCalls, ['temp-one', 'temp-one'])
  } finally { f.close() }
})

test('a load without a connector type sends no request and reports nothing', async () => {
  const f = await fixture({ resources: TREE_RESOURCES })
  try {
    assert.equal(f.vm.form.type, '')
    await f.vm.loadResources()
    assert.deepEqual(f.calls, [])
    assert.deepEqual(f.resourceCalls, [])
    assert.equal(f.vm.tempDsId, '')
    assert.equal(f.vm.resources.length, 0)
    assert.equal(f.vm.loadingResources, false)
    assert.deepEqual(f.messages, [])

    // Whitespace is as unusable as empty.
    f.vm.form.type = '   '
    await f.vm.loadResources()
    assert.deepEqual(f.calls, [])
    assert.deepEqual(f.resourceCalls, [])
    assert.equal(f.vm.tempDsId, '')
    assert.equal(f.vm.loadingResources, false)
    assert.deepEqual(f.messages, [])
  } finally { f.close() }
})

test('a load without a knowledge base sends no request and reports nothing', async () => {
  const f = await fixture({ resources: TREE_RESOURCES })
  try {
    f.vm.form.type = 'notion'
    f.props.kbId = '  '
    await nextTick()
    await f.vm.loadResources()
    assert.deepEqual(f.calls, [])
    assert.deepEqual(f.resourceCalls, [])
    assert.equal(f.vm.tempDsId, '')
    assert.equal(f.vm.loadingResources, false)
    assert.deepEqual(f.messages, [])
  } finally { f.close() }
})

test('restoring the parameters lets the same dialog create the draft on the next attempt', async () => {
  const f = await fixture({ resources: TREE_RESOURCES })
  try {
    await f.vm.loadResources()
    assert.equal(f.creates(), 0)

    f.vm.form.type = 'notion'
    f.props.kbId = '  '
    await nextTick()
    await f.vm.loadResources()
    assert.equal(f.creates(), 0)

    // A skipped attempt is not a failure: the form is filled in, and the very
    // next load creates the draft and lists through it.
    f.props.kbId = 'kb-one'
    await nextTick()
    await f.vm.loadResources()
    assert.equal(f.creates(), 1)
    assert.equal(f.vm.tempDsId, 'temp-one')
    assert.deepEqual(f.resourceCalls, ['temp-one'])
    assert.deepEqual([...f.vm.resources].map((r: any) => r.external_id), ['page-1', 'page-2'])
  } finally { f.close() }
})

test('two concurrent loads share one draft creation', async () => {
  const f = await fixture({ resources: TREE_RESOURCES })
  try {
    f.vm.form.type = 'notion'
    // Both loads reach the create before either has an id to show for it; the
    // second must join the first instead of creating a second row.
    await Promise.all([f.vm.loadResources(), f.vm.loadResources()])
    assert.equal(f.creates(), 1)
    assert.equal(f.vm.tempDsId, 'temp-one')
    assert.deepEqual(f.resourceCalls, ['temp-one', 'temp-one'])
  } finally { f.close() }
})

test('a create that answers without an id is reported and not remembered', async () => {
  const f = await fixture({ resources: TREE_RESOURCES })
  try {
    f.vm.form.type = 'notion'
    f.state.createResult = { data: {} }
    await f.vm.loadResources()
    assert.equal(f.creates(), 1)
    // An id-less row cannot be listed through, so the failure is reported and
    // no listing is sent against an id that does not exist.
    assert.deepEqual(f.messages, ['datasource.saveFailed'])
    assert.deepEqual(f.resourceCalls, [])
    assert.equal(f.vm.tempDsId, '')
    assert.equal(f.vm.loadingResources, false)

    // The next attempt is free to create a usable draft — and the id it answers
    // with is normalized, so the stored one is exactly what a request may carry.
    f.state.createResult = { id: '  temp-two  ' }
    await f.vm.loadResources()
    assert.equal(f.creates(), 2)
    assert.equal(f.vm.tempDsId, 'temp-two')
    assert.deepEqual(f.resourceCalls, ['temp-two'])
    // The report belongs to the failed attempt alone.
    assert.deepEqual(f.messages, ['datasource.saveFailed'])
  } finally { f.close() }
})

test('a failed create is not remembered and is retried', async () => {
  const f = await fixture({ resources: TREE_RESOURCES })
  try {
    f.vm.form.type = 'notion'
    f.state.createFails = true
    await f.vm.loadResources()
    assert.equal(f.creates(), 1)
    assert.equal(f.vm.tempDsId, '')
    assert.deepEqual(f.resourceCalls, [])
    assert.deepEqual(f.messages, ['create failed'])
    assert.equal(f.vm.loadingResources, false)

    f.state.createFails = false
    await f.vm.loadResources()
    assert.equal(f.creates(), 2)
    assert.equal(f.vm.tempDsId, 'temp-one')
    assert.deepEqual(f.resourceCalls, ['temp-one'])
    assert.deepEqual(f.messages, ['create failed'])
  } finally { f.close() }
})

test('a Drive root load creates the draft, lists the folder and reveals the tree', async () => {
  for (const type of ['feishu_drive', 'lark_drive']) {
    const f = await fixture({ resources: DRIVE_RESOURCES })
    try {
      f.vm.form.type = type
      f.vm.driveFolderToken = 'folderTokenSynthetic'
      await f.vm.loadDriveRoot()
      assert.equal(f.creates(), 1, type)
      assert.equal(f.calls[0].args[0].type, type)
      assert.equal(f.calls[0].args[0].knowledge_base_id, 'kb-one')
      assert.equal(f.calls[0].args[0].status, 'paused')
      assert.equal(f.vm.tempDsId, 'temp-one', type)
      assert.deepEqual(f.resourceCalls, ['temp-one'], type)
      assert.equal(f.vm.driveRootLoaded, true, type)
      assert.deepEqual([...f.vm.resources].map((r: any) => r.external_id), ['folderTokenSynthetic:fileA'], type)
      assert.deepEqual(f.messages, [], type)
    } finally { f.close() }
  }
})

test('a Drive root load without a connector type or a knowledge base sends nothing', async () => {
  for (const type of ['feishu_drive', 'lark_drive']) {
    const f = await fixture({ resources: DRIVE_RESOURCES })
    try {
      f.vm.driveFolderToken = 'folderTokenSynthetic'
      // No connector type picked yet: the token is kept, the request is not sent.
      await f.vm.loadDriveRoot()
      assert.deepEqual(f.calls, [], type)
      assert.deepEqual(f.resourceCalls, [], type)
      assert.equal(f.vm.tempDsId, '', type)
      assert.equal(f.vm.driveRootLoaded, false, type)
      assert.equal(f.vm.driveFolderToken, 'folderTokenSynthetic', type)
      assert.deepEqual(f.messages, [], type)

      f.vm.form.type = '   '
      await f.vm.loadDriveRoot()
      assert.deepEqual(f.calls, [], type)
      assert.deepEqual(f.resourceCalls, [], type)

      f.vm.form.type = type
      f.props.kbId = '  '
      await nextTick()
      await f.vm.loadDriveRoot()
      assert.deepEqual(f.calls, [], type)
      assert.deepEqual(f.resourceCalls, [], type)
      assert.equal(f.vm.driveRootLoaded, false, type)
      assert.deepEqual(f.messages, [], type)
      assert.equal(f.vm.loadingResources, false, type)
    } finally { f.close() }
  }
})

test('restoring the parameters lets the Drive root load proceed', async () => {
  for (const type of ['feishu_drive', 'lark_drive']) {
    const f = await fixture({ resources: DRIVE_RESOURCES })
    try {
      f.vm.driveFolderToken = 'folderTokenSynthetic'
      await f.vm.loadDriveRoot()
      assert.equal(f.creates(), 0, type)

      f.vm.form.type = type
      f.props.kbId = 'kb-one'
      await nextTick()
      await f.vm.loadDriveRoot()
      assert.equal(f.creates(), 1, type)
      assert.equal(f.vm.tempDsId, 'temp-one', type)
      assert.deepEqual(f.resourceCalls, ['temp-one'], type)
      assert.equal(f.vm.driveRootLoaded, true, type)
      assert.deepEqual(f.messages, [], type)
    } finally { f.close() }
  }
})

test('a Drive root load on an existing draft updates it instead of creating one', async () => {
  for (const type of ['feishu_drive', 'lark_drive']) {
    const f = await fixture({ resources: DRIVE_RESOURCES })
    try {
      f.vm.form.type = type
      // A draft already exists (edit mode, or a previous listing): the guard
      // must not stand in the way of persisting the new folder token.
      f.vm.tempDsId = 'source-one'
      f.vm.driveFolderToken = 'folderTokenSynthetic'
      await f.vm.loadDriveRoot()
      assert.equal(f.creates(), 0, type)
      assert.deepEqual(f.calls.map(call => call.method), ['updateDataSource'], type)
      assert.equal(f.calls[0].args[0], 'source-one', type)
      assert.equal(f.calls[0].args[1].config.resource_ids[0], 'folderTokenSynthetic', type)
      assert.deepEqual(f.resourceCalls, ['source-one'], type)
      assert.equal(f.vm.driveRootLoaded, true, type)
    } finally { f.close() }
  }
})

test('a Drive root load that fails reports the error and clears the spinner', async () => {
  const f = await fixture({ resources: DRIVE_RESOURCES })
  try {
    f.vm.form.type = 'feishu_drive'
    f.vm.driveFolderToken = 'folderTokenSynthetic'
    // A folder the app was never shared with: the user gets the actionable
    // hint, not the raw Feishu body.
    f.state.listFails = new Error('status=403 forbidden')
    await f.vm.loadDriveRoot()
    assert.deepEqual(f.messages, ['datasource.drive.loadForbiddenHint'])
    assert.equal(f.vm.driveRootLoaded, false)
    assert.equal(f.vm.loadingResources, false)
  } finally { f.close() }
})

test('a Drive root load whose draft create fails reports it and keeps no draft', async () => {
  const f = await fixture({ resources: DRIVE_RESOURCES })
  try {
    f.vm.form.type = 'feishu_drive'
    f.vm.driveFolderToken = 'folderTokenSynthetic'
    f.state.createFails = true
    await f.vm.loadDriveRoot()
    assert.deepEqual(f.messages, ['create failed'])
    assert.equal(f.vm.tempDsId, '')
    assert.deepEqual(f.resourceCalls, [])
    assert.equal(f.vm.driveRootLoaded, false)
    assert.equal(f.vm.loadingResources, false)

    // The failure is not remembered: the next attempt creates the draft.
    f.state.createFails = false
    await f.vm.loadDriveRoot()
    assert.equal(f.creates(), 2)
    assert.equal(f.vm.tempDsId, 'temp-one')
    assert.deepEqual(f.resourceCalls, ['temp-one'])
    assert.equal(f.vm.driveRootLoaded, true)
  } finally { f.close() }
})

test('a Drive root load with no folder token reports the inline error and sends nothing', async () => {
  const f = await fixture({ resources: DRIVE_RESOURCES })
  try {
    f.vm.form.type = 'feishu_drive'
    f.vm.driveFolderToken = ''
    await f.vm.loadDriveRoot()
    assert.equal(f.vm.driveFolderTokenError, 'datasource.drive.folderTokenRequired')
    assert.deepEqual(f.calls, [])
    assert.deepEqual(f.resourceCalls, [])
    assert.deepEqual(f.messages, [])
  } finally { f.close() }
})
