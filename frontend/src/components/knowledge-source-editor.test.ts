import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive } from 'vue'

const require = createRequire(import.meta.url)
const { descriptor } = parse(readFileSync(new URL('./knowledge-source-editor.vue', import.meta.url), 'utf8'))
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'source-editor-test' }).content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const flush = async () => { await new Promise(resolve => setImmediate(resolve)); await nextTick() }

function deferred() {
  let resolve!: (value: any) => void
  let reject!: (error: Error) => void
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

async function fixture() {
  const requests: Array<ReturnType<typeof deferred>> = []
  const saves: Array<{ id: string; content: string; revision: string } & ReturnType<typeof deferred>> = []
  const dialogs: any[] = [], events: string[] = [], routeGuards: Array<() => Promise<boolean>> = []
  const props = reactive({ knowledgeId: 'A' })
  const listeners = new Map<string, (event: any) => void>()
  const exports: any = {}
  runInNewContext(compiled, {
    exports, TextEncoder,
    window: {
      addEventListener: (key: string, listener: (event: any) => void) => listeners.set(key, listener),
      removeEventListener: (key: string) => listeners.delete(key),
    },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'vue-router') return { onBeforeRouteLeave: (guard: () => Promise<boolean>) => routeGuards.push(guard) }
      if (name === 'tdesign-vue-next') return {
        MessagePlugin: { success() {} },
        DialogPlugin: { confirm(options: any) { dialogs.push(options); return { destroy() {} } } },
      }
      if (name === '@/api/knowledge-base/knowledgeSource') return {
        MAX_SOURCE_BYTES: 2 * 1024 * 1024,
        getKnowledgeSource() { const request = deferred(); requests.push(request); return request.promise },
        saveKnowledgeSource(id: string, content: string, revision: string) {
          const request = { ...deferred(), id, content, revision }; saves.push(request); return request.promise
        },
      }
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  exports.default.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  let instance: any
  const app = renderer.createApp({ setup: () => () => h(exports.default, {
    ...props, ref: (value: any) => { if (value) instance = value },
    onSaved: () => events.push('saved'), onClose: () => events.push('close'),
  }) })
  app.mount({})
  await flush()
  return {
    state: instance.$.setupState, props, requests, saves, dialogs, events, listeners, routeGuards,
    dispose: () => app.unmount(),
  }
}

test('save sends the loaded revision and waits for completion before closing', async t => {
  const f = await fixture(); t.after(f.dispose)
  f.requests[0]!.resolve({ data: { content: 'old', revision: 'version-A' } }); await flush()
  assert.equal(Boolean(f.state.canSave), false)
  f.state.content = '中文修改'
  assert.equal(f.state.byteCount, 12)
  const saving = f.state.save()
  assert.equal(f.saves[0]!.revision, 'version-A')
  assert.equal(f.saves[0]!.content, '中文修改')
  assert.equal(f.state.saving, true)
  f.state.requestClose()
  assert.deepEqual(f.events, [])
  f.saves[0]!.resolve({ data: { id: 'A', parse_status: 'pending' } }); await saving
  assert.deepEqual(f.events, ['saved', 'close'])
})

test('conflict keeps the draft; reloading requires explicit discard', async t => {
  const f = await fixture(); t.after(f.dispose)
  f.requests[0]!.resolve({ data: { content: 'old', revision: 'version-A' } }); await flush()
  f.state.content = 'my draft'
  const saving = f.state.save()
  f.saves[0]!.reject(new Error('Source changed; reload it before saving')); await saving
  assert.equal(f.state.content, 'my draft')
  assert.match(f.state.error, /Source changed/)
  assert.deepEqual(f.events, [])
  f.state.discardThen(() => f.state.load())
  assert.equal(f.requests.length, 1)
  f.dialogs[0]!.onConfirm()
  assert.equal(f.requests.length, 2)
  f.requests[1]!.resolve({ data: { content: 'other editor', revision: 'version-B' } }); await flush()
  assert.equal(f.state.content, 'other editor')
  assert.equal(f.state.revision, 'version-B')
})

test('late responses cannot overwrite another document and unsaved drafts guard page exit', async t => {
  const f = await fixture(); t.after(f.dispose)
  f.props.knowledgeId = 'B'; await flush()
  f.requests[1]!.resolve({ data: { content: 'B', revision: 'version-B' } }); await flush()
  f.requests[0]!.resolve({ data: { content: 'A', revision: 'version-A' } }); await flush()
  assert.equal(f.state.content, 'B')
  f.state.content = 'unsaved'
  let prevented = false
  f.listeners.get('beforeunload')!({ preventDefault: () => { prevented = true }, returnValue: undefined })
  assert.equal(prevented, true)
  f.state.requestClose()
  assert.deepEqual(f.events, [])
  f.dialogs[0]!.onConfirm()
  assert.deepEqual(f.events, ['close'])
})

test('blank and oversized drafts cannot be submitted', async t => {
  const f = await fixture(); t.after(f.dispose)
  f.requests[0]!.resolve({ data: { content: 'old', revision: 'version-A' } }); await flush()
  for (const content of ['  ', '中'.repeat(700000)]) {
    f.state.content = content
    assert.equal(Boolean(f.state.canSave), false)
    await f.state.save()
  }
  assert.equal(f.saves.length, 0)
})

test('route navigation also requires discarding a dirty draft', async t => {
  const f = await fixture(); t.after(f.dispose)
  f.requests[0]!.resolve({ data: { content: 'old', revision: 'version-A' } }); await flush()
  f.state.content = 'draft'
  const cancelled = f.routeGuards[0]!()
  f.dialogs[0]!.onClose()
  assert.equal(await cancelled, false)
  const accepted = f.routeGuards[0]!()
  f.dialogs[1]!.onConfirm()
  assert.equal(await accepted, true)
})
