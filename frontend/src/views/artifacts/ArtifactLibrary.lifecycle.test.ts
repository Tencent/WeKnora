import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, ref } from 'vue'
import * as artifactLibrary from '../../utils/artifactLibrary'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./ArtifactLibrary.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'artifact-lifecycle' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const settle = async () => { await nextTick(); await new Promise(resolve => setImmediate(resolve)); await nextTick() }
const row = (index: number) => ({ message_id: `m${index}`, index: 0, file_name: `${index}.txt`, created_at: '2026-10-30T00:00:00Z' })
function fixture() {
  const requests: Array<{ page: number; result: ReturnType<typeof deferred<any>> }> = []
  const deletions: Array<ReturnType<typeof deferred<void>>> = []
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require(name)
      if (name === 'vue-router') return { useRoute: () => ({ query: {} }), useRouter: () => ({ replace() {} }) }
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { error() {}, success() {} } }
      if (name === '@/utils/artifactLibrary') return artifactLibrary
      if (name === '@/components/settings/useConfirmDelete') return { useConfirmDelete: () => () => {} }
      if (name === '@/api/artifacts') return {
        listArtifactLibrary(options: { page: number }) { const result = deferred<any>(); requests.push({ page: options.page, result }); return result.promise },
        deleteArtifactLibraryItem() { const result = deferred<void>(); deletions.push(result); return result.promise },
      }
      return {}
    },
    console: { error() {} }, setTimeout, clearTimeout,
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ref: instance }) })
  app.mount({})
  return { vm: instance.value, requests, deletions, close: () => app.unmount() }
}

test('successful deletion cannot be resurrected by a pending first-page response', async () => {
  const f = fixture()
  try {
    f.requests[0]!.result.resolve({ data: [row(0), row(1)], total: 2 }); await settle()
    void f.vm.fetchPage(1)
    const deletion = f.vm.performDelete(row(0))
    f.deletions[0]!.resolve(); await deletion
    f.requests[1]!.result.resolve({ data: [row(0), row(1)], total: 2 }); await settle()
    assert.equal(f.vm.items.some((r: any) => r.message_id === 'm0'), false)
    assert.equal(f.requests.length, 3)
    f.requests[2]!.result.resolve({ data: [row(1)], total: 1 }); await settle()
    assert.equal(f.vm.total, 1)
    assert.equal(f.vm.loading, false)
  } finally { f.close() }
})

test('deleting during pagination refetches the shifted offset without skipping a row', async () => {
  const f = fixture()
  try {
    f.requests[0]!.result.resolve({ data: Array.from({ length: 30 }, (_, i) => row(i)), total: 61 }); await settle()
    f.vm.loadMore()
    const deletion = f.vm.performDelete(row(0))
    f.deletions[0]!.resolve(); await deletion
    f.requests[1]!.result.resolve({ data: Array.from({ length: 30 }, (_, i) => row(i + 30)), total: 61 }); await settle()
    assert.equal(f.vm.total, 60)
    assert.equal(f.requests.length, 3)
    assert.equal(f.requests[2]!.page, 1)
    f.requests[2]!.result.resolve({ data: Array.from({ length: 30 }, (_, i) => row(i + 1)), total: 60 }); await settle()
    assert.equal(f.vm.items.length, 30)
    assert.equal(f.vm.items[29].message_id, 'm30')
    f.vm.loadMore()
    assert.equal(f.requests[3]!.page, 2)
    f.requests[3]!.result.resolve({ data: Array.from({ length: 30 }, (_, i) => row(i + 31)), total: 60 }); await settle()
    assert.equal(f.vm.items.length, 60)
    assert.equal(f.vm.hasMore, false)
  } finally { f.close() }
})

test('failed deletion does not invalidate a valid pending list response', async () => {
  const f = fixture()
  try {
    f.requests[0]!.result.resolve({ data: [row(0)], total: 2 }); await settle()
    void f.vm.fetchPage(1)
    const deletion = f.vm.performDelete(row(0))
    f.deletions[0]!.reject(new Error('delete failed')); await deletion
    f.requests[1]!.result.resolve({ data: [row(0), row(1)], total: 2 }); await settle()
    assert.equal(f.requests.length, 2)
    assert.equal(f.vm.items.length, 2)
    assert.equal(f.vm.loading, false)
  } finally { f.close() }
})

test('a newer filter supersedes a stale pre-delete list without extra requests', async () => {
  const f = fixture()
  try {
    f.requests[0]!.result.resolve({ data: [row(0)], total: 2 }); await settle()
    void f.vm.fetchPage(1)
    const deletion = f.vm.performDelete(row(0))
    f.deletions[0]!.resolve(); await deletion
    f.vm.category = 'image'; await settle()
    f.requests[2]!.result.resolve({ data: [row(2)], total: 1 }); await settle()
    f.requests[1]!.result.resolve({ data: [row(0), row(1)], total: 2 }); await settle()
    assert.equal(f.requests.length, 3)
    assert.equal(f.vm.items[0].message_id, 'm2')
    assert.equal(f.vm.total, 1)
  } finally { f.close() }
})

test('unmount prevents a stale pre-delete response from starting a refetch', async () => {
  const f = fixture()
  f.requests[0]!.result.resolve({ data: [row(0)], total: 2 }); await settle()
  void f.vm.fetchPage(1)
  const deletion = f.vm.performDelete(row(0))
  f.deletions[0]!.resolve(); await deletion
  f.close()
  f.requests[1]!.result.resolve({ data: [row(0), row(1)], total: 2 }); await settle()
  assert.equal(f.requests.length, 2)
})

test('refetch failure releases loading and allows the user to retry', async () => {
  const f = fixture()
  try {
    f.requests[0]!.result.resolve({ data: [row(0), row(1)], total: 3 }); await settle()
    f.vm.loadMore()
    const deletion = f.vm.performDelete(row(0))
    f.deletions[0]!.resolve(); await deletion
    f.requests[1]!.result.resolve({ data: [row(0), row(1)], total: 3 }); await settle()
    assert.equal(f.requests.length, 3)
    f.requests[2]!.result.reject(new Error('list failed')); await settle()
    assert.equal(f.vm.loading, false)
    assert.equal(f.vm.loadError, true)
    assert.equal(f.vm.items[0].message_id, 'm1')
    f.vm.loadMore()
    assert.equal(f.requests.length, 4)
    f.requests[3]!.result.resolve({ data: [row(1), row(2)], total: 2 }); await settle()
    assert.equal(f.vm.items.length, 2)
    assert.equal(f.vm.loadError, false)
  } finally { f.close() }
})

test('a second successful deletion invalidates the replacement request too', async () => {
  const f = fixture()
  try {
    f.requests[0]!.result.resolve({ data: [row(0), row(1), row(2)], total: 3 }); await settle()
    void f.vm.fetchPage(1)
    const first = f.vm.performDelete(row(0))
    f.deletions[0]!.resolve(); await first
    f.requests[1]!.result.resolve({ data: [row(0), row(1), row(2)], total: 3 }); await settle()
    assert.equal(f.requests.length, 3)
    const second = f.vm.performDelete(row(1))
    f.deletions[1]!.resolve(); await second
    f.requests[2]!.result.resolve({ data: [row(1), row(2)], total: 2 }); await settle()
    assert.equal(f.requests.length, 4)
    assert.equal(f.vm.items.length, 1)
    assert.equal(f.vm.items[0].message_id, 'm2')
    f.requests[3]!.result.resolve({ data: [row(2)], total: 1 }); await settle()
    assert.equal(f.vm.total, 1)
    assert.equal(f.vm.loading, false)
  } finally { f.close() }
})
