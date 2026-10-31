import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'
import * as filePreview from '../utils/filePreview'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./document-preview.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'preview-lifecycle-test' }).content
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

function fixture(extra: Record<string, any> = {}) {
  const requests: Array<{ id: string; result: ReturnType<typeof deferred<Blob>> }> = []
  const created: string[] = [], revoked: string[] = []
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue' || name === 'highlight.js') return require(name)
      if (name === '@/utils/filePreview') return filePreview
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/api/knowledge-base/index') return {
        previewKnowledgeFile(id: string) {
          const result = deferred<Blob>(); requests.push({ id, result }); return result.promise
        },
      }
      if (name === '@/utils/security') return { sanitizeHTML: (html: string) => html, sanitizeMarkdownHTML: (html: string) => html }
      return extra[name] ?? {}
    },
    Blob, TextDecoder, console, setTimeout, clearTimeout,
    URL: {
      createObjectURL(blob: Blob) { const url = URL.createObjectURL(blob); created.push(url); return url },
      revokeObjectURL(url: string) { revoked.push(url); URL.revokeObjectURL(url) },
    },
    document: { addEventListener() {}, removeEventListener() {}, activeElement: {}, createElement: () => ({ childNodes: [] }) },
    window: { addEventListener() {}, removeEventListener() {} },
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const props = reactive<any>({ knowledgeId: 'a', fileName: 'a.txt', fileType: 'txt', active: true })
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance }) })
  app.mount({})
  return { vm: instance.value, props, requests, created, revoked, close: () => app.unmount() }
}
const text = (value: string) => new Blob([value], { type: 'text/plain' })

test('a late download cannot replace the newer document', async () => {
  const f = fixture()
  try {
    f.props.knowledgeId = 'b'; await settle()
    f.requests[1]!.result.resolve(text('new document')); await settle()
    assert.equal(f.vm.textContent, 'new document')
    f.requests[0]!.result.resolve(text('old document')); await settle()
    assert.equal(f.vm.textContent, 'new document')
    assert.equal(f.vm.error, '')
  } finally { f.close() }
})

test('A to B to A uses the latest request even when the source key is equal again', async () => {
  const f = fixture()
  try {
    f.props.knowledgeId = 'b'; await settle()
    f.props.knowledgeId = 'a'; await settle()
    f.requests[2]!.result.resolve(text('latest a')); await settle()
    f.requests[0]!.result.resolve(text('first a')); await settle()
    f.requests[1]!.result.resolve(text('b')); await settle()
    assert.equal(f.vm.textContent, 'latest a')
  } finally { f.close() }
})

test('obsolete failures neither clear a newer loading state nor hide its successful result', async () => {
  const f = fixture()
  try {
    f.props.knowledgeId = 'b'; await settle()
    f.requests[0]!.result.reject(new Error('old failure')); await settle()
    assert.equal(f.vm.loading, true)
    assert.equal(f.vm.error, '')
    f.requests[1]!.result.resolve(text('current')); await settle()
    assert.equal(f.vm.loading, false)
    assert.equal(f.vm.textContent, 'current')
  } finally { f.close() }
})

test('hide and reopen releases the old URL and rejects a late hidden download', async () => {
  const f = fixture()
  try {
    f.props.fileType = 'png'; await settle()
    f.requests[1]!.result.resolve(new Blob(['image'], { type: 'image/png' })); await settle()
    const visibleUrl = f.vm.blobUrl
    assert.ok(visibleUrl)
    f.props.active = false; await settle()
    assert.equal(f.vm.blobUrl, '')
    assert.ok(f.revoked.includes(visibleUrl))
    f.requests[0]!.result.resolve(text('old')); await settle()
    assert.equal(f.vm.textContent, '')
    f.props.active = true; await settle()
    assert.equal(f.requests.length, 3)
    f.requests[2]!.result.resolve(new Blob(['fresh'], { type: 'image/png' })); await settle()
    assert.notEqual(f.vm.blobUrl, visibleUrl)
  } finally { f.close() }
  assert.deepEqual(f.revoked.sort(), f.created.sort())
})

test('unmount while downloading does not create an orphaned object URL', async () => {
  const f = fixture()
  f.props.fileType = 'png'; await settle()
  f.close()
  f.requests[1]!.result.resolve(new Blob(['image'], { type: 'image/png' })); await settle()
  f.requests[0]!.result.resolve(text('old')); await settle()
  assert.equal(f.created.length, 0)
})

test('same-name same-size Blob replacement reloads the actual object', async () => {
  const f = fixture()
  try {
    f.props.sourceBlob = text('one'); await settle()
    assert.equal(f.vm.textContent, 'one')
    f.props.sourceBlob = text('two'); await settle()
    assert.equal(f.vm.textContent, 'two')
    assert.equal(f.requests.length, 1)
  } finally { f.close() }
})

test('an obsolete EPUB renderer releases its own image URLs without publishing its HTML', async () => {
  const ready = deferred<{ html: string; objectUrls: string[] }>()
  const f = fixture({ '@/utils/epubPreview': { renderEpubPreview: () => ready.promise } })
  try {
    f.props.fileType = 'epub'; await settle()
    f.requests[1]!.result.resolve(new Blob(['book'])); await settle()
    f.props.knowledgeId = 'b'; f.props.fileType = 'txt'; await settle()
    f.requests[2]!.result.resolve(text('current')); await settle()
    const orphan = URL.createObjectURL(new Blob(['image']))
    ready.resolve({ html: '<p>old</p>', objectUrls: [orphan] }); await settle()
    assert.ok(f.revoked.includes(orphan))
    assert.equal(f.vm.epubHtml, '')
    assert.equal(f.vm.textContent, 'current')
  } finally { f.close() }
})

test('a delayed text decoder cannot overwrite the next document', async () => {
  const ready = deferred<string>()
  class SlowText extends Blob { override text() { return ready.promise } }
  const f = fixture()
  try {
    f.requests[0]!.result.resolve(new SlowText(['old'], { type: 'text/plain' })); await settle()
    f.props.knowledgeId = 'b'; await settle()
    f.requests[1]!.result.resolve(text('new')); await settle()
    ready.resolve('old decoded text'); await settle()
    assert.equal(f.vm.textContent, 'new')
  } finally { f.close() }
})

test('superseded DOCX rendering stays detached from the current document', async () => {
  const jobs: Array<{ staging: any; ready: ReturnType<typeof deferred<void>> }> = []
  const f = fixture({
    'docx-preview': { renderAsync(_blob: Blob, staging: any) {
      const ready = deferred<void>(); jobs.push({ staging, ready }); return ready.promise
    } },
    '@/utils/sourceImage': { indexEmbeddedSourceImages: async () => {} },
  })
  try {
    const target = { childNodes: [] as string[], replaceChildren(...children: string[]) { this.childNodes = children }, innerHTML: '' }
    f.vm.docxContainer = target
    f.props.fileType = 'docx'; await settle()
    f.requests[1]!.result.resolve(new Blob(['old'])); await settle()
    assert.equal(jobs.length, 1)
    f.props.knowledgeId = 'b'; await settle()
    f.requests[2]!.result.resolve(new Blob(['new'])); await settle()
    assert.equal(jobs.length, 2)
    jobs[1]!.staging.childNodes = ['current document']
    jobs[1]!.ready.resolve(); await settle()
    jobs[0]!.staging.childNodes = ['obsolete document']
    jobs[0]!.ready.resolve(); await settle()
    assert.deepEqual(target.childNodes, ['current document'])
  } finally { f.close() }
})

test('a current download failure stays visible and an explicit retry succeeds', async () => {
  const f = fixture()
  try {
    f.requests[0]!.result.reject(new Error('current failure')); await settle()
    assert.equal(f.vm.error, 'current failure')
    assert.equal(f.vm.loading, false)
    const retry = f.vm.loadPreview()
    f.requests[1]!.result.resolve(text('recovered'))
    await retry
    assert.equal(f.vm.error, '')
    assert.equal(f.vm.textContent, 'recovered')
  } finally { f.close() }
})
