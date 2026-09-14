import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { ref } from 'vue'

const source = readFileSync(new URL('./document-preview.vue', import.meta.url), 'utf8')
const start = source.indexOf('async function loadPreview()')
const end = source.indexOf('\nwatch(', start)
assert.ok(start >= 0 && end > start)
const loadSource = ts.transpile(source.slice(start, end))

function deferred() {
  let resolve
  const promise = new Promise(done => { resolve = done })
  return { promise, resolve }
}

function setup(kind) {
  const pending = deferred()
  const started = deferred()
  const sourceBlob = new Blob([kind], { type: kind === 'html' ? 'text/html' : 'application/octet-stream' })
  const htmlCalls = []
  const textCalls = []
  const urls = []
  const blobUrl = ref('')
  const pptxData = ref(null)
  const props = { active: true }
  const refs = Object.fromEntries([
    'loading', 'error', 'previewType', 'htmlViewMode', 'textContent', 'highlightedCode',
    'markdownHtml', 'excelHtml', 'mermaidSvg', 'imageNaturalWidth', 'imageNaturalHeight', 'docxContainer',
  ].map(key => [key, ref(null)]))
  if (kind === 'html') sourceBlob.text = () => { started.resolve(); return pending.promise }
  const state = vm.runInNewContext(`let loadedForId = ''; let previewGeneration = 0; let pptxSlideCount = 0;
    ${loadSource}; ({ loadPreview, cleanup })`, {
    ...refs, props, blobUrl, pptxData, console,
    getPreviewSourceKey: () => 'artifact:session:message:0',
    allowsHtmlScriptPreview: () => true,
    resolveFilePreviewExt: () => kind,
    resolvePreviewKind: () => kind,
    fetchPreviewBlob: async () => sourceBlob,
    ensureBlobType: blob => blob,
    nextTick: async () => {},
    buildHtmlPreview: html => { htmlCalls.push(html); return `prepared:${html}` },
    preparePptxPreview: () => { started.resolve(); return pending.promise },
    renderText: async blob => { textCalls.push(blob) },
    Blob,
    URL: {
      createObjectURL: blob => { urls.push(blob); return `blob:${urls.length}` },
      revokeObjectURL() {},
    },
  })
  return { ...state, pending, started, props, blobUrl, pptxData, htmlCalls, textCalls, urls }
}

test('native artifact HTML still uses the upstream helper and retains source text', async () => {
  const ctx = setup('html')
  const loading = ctx.loadPreview()
  await ctx.started.promise
  ctx.pending.resolve('<p>chart</p>')
  await loading
  assert.deepEqual(ctx.htmlCalls, ['<p>chart</p>'])
  assert.equal(ctx.textCalls.length, 1)
  assert.equal(await ctx.urls[0].text(), 'prepared:<p>chart</p>')
})

for (const cancel of ['generation', 'abort']) {
  test(`pending HTML preparation cannot publish after ${cancel}`, async () => {
    const ctx = setup('html')
    const loading = ctx.loadPreview()
    await ctx.started.promise
    if (cancel === 'generation') ctx.cleanup()
    else ctx.props.requestSignal = AbortSignal.abort()
    ctx.pending.resolve('<p>stale</p>')
    await loading
    assert.equal(ctx.blobUrl.value, '')
    assert.deepEqual(ctx.htmlCalls, [])
    assert.deepEqual(ctx.textCalls, [])
    assert.deepEqual(ctx.urls, [])
  })
}

test('native PPTX receives the upstream prepared data', async () => {
  const ctx = setup('pptx')
  const loading = ctx.loadPreview()
  await ctx.started.promise
  const data = new ArrayBuffer(4)
  ctx.pending.resolve({ data, slideCount: 2 })
  await loading
  assert.equal(ctx.pptxData.value, data)
})

for (const cancel of ['generation', 'abort']) {
  test(`pending PPTX preparation cannot publish after ${cancel} even with the same source key`, async () => {
    const ctx = setup('pptx')
    const loading = ctx.loadPreview()
    await ctx.started.promise
    if (cancel === 'generation') ctx.cleanup()
    else ctx.props.requestSignal = AbortSignal.abort()
    ctx.pending.resolve({ data: new ArrayBuffer(4), slideCount: 2 })
    await loading
    assert.equal(ctx.pptxData.value, null)
  })
}
