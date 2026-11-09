import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive } from 'vue'

const require = createRequire(import.meta.url)
const { descriptor } = parse(readFileSync(process.env.ATTACHMENT_SFC || new URL('./AttachmentUpload.vue', import.meta.url), 'utf8'))
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'attachment-upload-test' }).content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const flush = async () => { await new Promise(resolve => setImmediate(resolve)); await nextTick() }
function deferred() { let resolve, reject; const promise = new Promise((r, j) => { resolve = r; reject = j }); return { promise, resolve, reject } }

async function fixture() {
  const props = reactive({ sessionId: 'A' })
  const uploads = [], polls = [], deletes = [], changes = [], timers = new Map()
  let timerId = 0, instance
  const exports = {}
  runInNewContext(compiled, { exports,
    setTimeout: callback => { const id = ++timerId; timers.set(id, callback); return id },
    clearTimeout: id => timers.delete(id),
    require(name) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: key => key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { warning() {} } }
      if (name === '@/utils') return { MAX_FILE_SIZE_MB: 20 }
      if (name === '@/stores/editorResources') return { useEditorResourcesStore: () => ({ parserEngines: [], ensureParserEngines: async () => {} }) }
      if (name === '@/api/chat/temporary-attachments') return {
        uploadTemporaryAttachment: (id, file, agent, tenant, parser, progress) => { const req = { ...deferred(), id, file, progress }; uploads.push(req); return req.promise },
        getTemporaryAttachment: (id, document) => { const req = { ...deferred(), id, document }; polls.push(req); return req.promise },
        deleteTemporaryAttachment: async (...args) => { deletes.push(args) },
      }
      if (name.endsWith('.vue')) return { default: { render: () => null } }
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  exports.default.render = () => null
  const renderer = createRenderer({ createElement: () => ({}), createText: () => ({}), createComment: () => ({}), insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {}, parentNode: () => null, nextSibling: () => null })
  const app = renderer.createApp({ setup: () => () => h(exports.default, { ...props, ref: value => { if (value) instance = value }, 'onUpdate:files': files => changes.push([...files]) }) })
  app.mount({}); await flush()
  return { props, uploads, polls, deletes, changes, timers, get instance() { return instance }, dispose: () => app.unmount() }
}

test('mounted upload in A is cleaned up from A after composer changes to B', async t => {
  const f = await fixture(); t.after(f.dispose)
  await f.instance.addFiles([{ name: 'sample.txt', size: 12, type: 'text/plain' }]); await flush()
  assert.equal(f.uploads[0].id, 'A')
  f.props.sessionId = 'B'; await flush()
  f.uploads[0].resolve({ data: { id: 'a-document', status: 'ready' } }); await flush()
  assert.deepEqual(f.deletes, [['A', 'a-document']])
  assert.equal(f.instance.attachments.length, 0)
})

test('mounted status poll from A cannot add ready attachment to B', async t => {
  const f = await fixture(); t.after(f.dispose)
  await f.instance.addFiles([{ name: 'sample.txt', size: 12, type: 'text/plain' }]); await flush()
  f.uploads[0].resolve({ data: { id: 'a-document', status: 'processing' } }); await flush()
  const timer = [...f.timers.values()][0]; timer(); await flush()
  assert.deepEqual([f.polls[0].id, f.polls[0].document], ['A', 'a-document'])
  f.props.sessionId = 'B'; await flush()
  f.polls[0].resolve({ data: { status: 'ready' } }); await flush()
  assert.equal(f.instance.attachments.length, 0)
  assert.deepEqual(f.deletes, [])
})


test('mounted ready attachment is removed immediately on switch to another session', async t => {
  const f = await fixture(); t.after(f.dispose)
  await f.instance.addFiles([{ name: 'sample.txt', size: 12, type: 'text/plain' }]); await flush()
  f.uploads[0].resolve({ data: { id: 'a-document', status: 'ready' } }); await flush()
  assert.equal(f.instance.attachments.length, 1)
  f.props.sessionId = 'B'; await flush()
  assert.equal(f.instance.attachments.length, 0)
  assert.deepEqual(f.deletes, [])
})

test('files selected before first session survive session creation', async t => {
  const f = await fixture(); t.after(f.dispose)
  f.props.sessionId = undefined; await flush()
  await f.instance.addFiles([{ name: 'local.txt', size: 12, type: 'text/plain' }]); await flush()
  f.props.sessionId = 'A'; await flush()
  assert.equal(f.instance.attachments.length, 1)
  assert.equal(f.instance.attachments[0].status, 'local')
  assert.deepEqual(f.deletes, [])
})

test('two pending uploads are independently discarded after one is removed and the session switches', async t => {
  const f = await fixture(); t.after(f.dispose)
  await f.instance.addFiles([{ name: 'one.txt', size: 12 }, { name: 'two.txt', size: 12 }]); await flush()
  const firstId = f.instance.attachments[0].id
  // Exercise the same remove control through the component setup state.
  f.instance.$.setupState.removeAttachment(firstId)
  f.props.sessionId = 'B'; await flush()
  f.uploads[1].resolve({ data: { id: 'second', status: 'ready' } })
  f.uploads[0].resolve({ data: { id: 'first', status: 'ready' } }); await flush()
  assert.deepEqual(f.deletes, [['A', 'second'], ['A', 'first']])
  assert.equal(f.instance.attachments.length, 0)
})
