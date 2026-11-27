import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { clearProtectedFileCache, clearProtectedFileFailureCache, hydrateProtectedFileImages, protectProviderImageSrcInHTML, releaseProtectedFileImages } from './security.ts'
import { effectScope, nextTick, shallowRef, watch, ref } from 'vue'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { setImmediate } from 'node:timers/promises'
import { useProtectedImageLifecycle } from '../composables/useProtectedImageLifecycle.ts'
import { responseFileName } from './protectedResource.ts'
import { applyFinalArtifactContent } from './finalArtifactContent.ts'

test('completion updates both streaming timeline and persisted-style content', () => {
  const message: any = { content: 'old', agentEventStream: [
    { type: 'answer', content: 'preamble' }, { type: 'answer', content: 'old' },
  ] }
  applyFinalArtifactContent(message, 'new resource')
  assert.equal(message.content, 'new resource')
  assert.equal(message.agentEventStream[0].superseded, true)
  assert.equal(message.agentEventStream[1].content, 'new resource')
  assert.equal(message.agentEventStream[1].done, true)
  applyFinalArtifactContent(message, undefined)
  assert.equal(message.content, 'new resource')
  applyFinalArtifactContent(message, '')
  applyFinalArtifactContent(message, '   ')
  assert.equal(message.content, 'new resource')
  assert.equal(message.agentEventStream[1].content, 'new resource')
})

test('download filenames preserve Unicode and remove path components', () => {
  assert.equal(responseFileName("inline; filename*=utf-8''%E6%AF%94%E8%B5%9B.pptx", ''), '比赛.pptx')
  assert.equal(responseFileName('attachment; filename="../../deck.pptx"', ''), 'deck.pptx')
  assert.equal(responseFileName(null, 'local://1/exports/deck.pptx'), 'deck.pptx')
})

// Minimal DOM adapter exercises the actual hydration/fetch pipeline without
// adding a browser-emulation dependency to the application.
function imageRoot(source: string) {
  const root: any = {
    replacement: null, image: null, removed: false, isConnected: true,
    querySelectorAll: (selector: string) => {
      if (root.removed) return []
      if (selector.startsWith('a.')) return root.replacement ? [root.replacement] : []
      return root.replacement ? [] : [root.image]
    },
  }
  const document = { createElement: (tag: string): any => {
    if (tag === 'img') return createImage('')
    const element: any = {
      tag, dataset: {}, ownerDocument: document,
      getAttribute: (key: string) => element[key] || '',
      replaceWith: (img: any) => { root.replacement = null; root.image = img },
    }
    return element
  } }
  function createImage(src: string): any {
    const attrs: Record<string, string> = { src }
    return {
      alt: '比赛 PPT', dataset: {}, style: { display: '' },
      get src() { return attrs.src },
      set src(value: string) { attrs.src = value },
      getAttribute: (key: string) => attrs[key] || '',
      setAttribute: (key: string, value: string) => { attrs[key] = value },
      removeAttribute: (key: string) => { delete attrs[key] },
      replaceWith: (link: any) => { root.replacement = link },
      remove: () => { root.removed = true },
      ownerDocument: document,
    }
  }
  root.image = createImage(source)
  return { root, get img() { return root.image } }
}

test('cross-turn files hydrate as downloadable preview cards; images remain images; denied requests do not render files', async () => {
  const originalFetch = globalThis.fetch
  const originalWindow = (globalThis as any).window
  ;(globalThis as any).window = { location: { origin: 'http://localhost' }, addEventListener() {} }
  const requests: string[] = []
  globalThis.fetch = async (url) => {
    requests.push(String(url))
    if (String(url).includes('denied')) return new Response(null, { status: 403 })
    const image = String(url).includes('image')
    return new Response(new Blob(['bytes'], { type: image ? 'image/png' : 'application/vnd.openxmlformats-officedocument.presentationml.presentation' }), {
      headers: { 'Content-Disposition': 'inline; filename="deck.pptx"' },
    })
  }
  try {
    const old = imageRoot('resource://dHZ_fFslfs0GgJGaJZGjGA')
    const current = imageRoot('resource://4N1nAo-FZZoDEExDQz2yoA')
    for (const { root } of [old, current]) {
      await hydrateProtectedFileImages(root, { mode: 'message', sessionId: 's', messageId: 'm2' })
      assert.equal(root.replacement.tag, 'a')
      assert.equal(root.replacement.download, 'deck.pptx')
      assert.equal(root.replacement.textContent, '比赛 PPT')
      assert.match(root.replacement.href, /^blob:/)
    }
    assert.notEqual(old.root.replacement.href, current.root.replacement.href)
    assert.ok(requests.every(url => url.startsWith('/api/v1/sessions/s/messages/m2/files?')))
    const image = imageRoot('local://1/exports/image.png')
    await hydrateProtectedFileImages(image.root)
    assert.equal(image.root.replacement, null)
    assert.match(image.img.src, /^blob:/)
    const denied = imageRoot('local://1/exports/denied.pptx')
    await hydrateProtectedFileImages(denied.root)
    assert.equal(denied.root.replacement, null)
    assert.equal(denied.img.dataset.authHydrated, '0')
    const rerenderSource = 'resource://rerender-pptx'
    const first = imageRoot(rerenderSource)
    await hydrateProtectedFileImages(first.root, { mode: 'message', sessionId: 's', messageId: 'm2' })
    const rewritten = protectProviderImageSrcInHTML(`<img alt="比赛 PPT" src="${rerenderSource}">`)
    assert.match(rewritten, /protected-resource-card/)
    assert.match(rewritten, /download=/)
    assert.match(rewritten, /href="blob:/)
    assert.doesNotMatch(rewritten, /data-img-loading/)
    assert.doesNotMatch(rewritten, /<img/i)
  } finally {
    clearProtectedFileCache()
    globalThis.fetch = originalFetch
    ;(globalThis as any).window = originalWindow
  }
})

function hydrationEnvironment(t: TestContext) {
  const originalWindow = (globalThis as any).window
  const originalFetch = globalThis.fetch
  ;(globalThis as any).window = { location: { origin: 'http://localhost' }, addEventListener() {} }
  clearProtectedFileCache()
  t.after(() => {
    globalThis.fetch = originalFetch
    ;(globalThis as any).window = originalWindow
    clearProtectedFileCache()
  })
}

const messageAccess = { mode: 'message', sessionId: 'shared-session', messageId: 'assistant' } as const
const pngResponse = () => new Response(new Blob(['png bytes'], { type: 'image/png' }))

for (const status of [403, 404]) {
  test(`completion retries an in-flight ${status} and hydrates all waiting image elements`, async (t) => {
    hydrationEnvironment(t)
    const source = `local://2/exports/pending-${status}.png`
    const first = imageRoot(source)
    const second = imageRoot(source)
    let finishOld!: (response: Response) => void
    let calls = 0
    globalThis.fetch = async () => {
      calls++
      if (calls === 1) return new Promise<Response>(resolve => { finishOld = resolve })
      return pngResponse()
    }
    const streaming = hydrateProtectedFileImages(first.root, messageAccess)
    // Completion can run while the same DOM node is marked authHydrated=1.
    clearProtectedFileFailureCache()
    const completion = hydrateProtectedFileImages(first.root, messageAccess)
    const otherOccurrence = hydrateProtectedFileImages(second.root, messageAccess)
    finishOld(new Response(null, { status }))
    await Promise.all([streaming, completion, otherOccurrence])
    assert.equal(calls, 2, 'exactly one shared retry after persistence')
    assert.match(first.img.src, /^blob:/)
    assert.equal(second.img.src, first.img.src)
    assert.equal(first.root.removed, false)
    await hydrateProtectedFileImages(first.root, messageAccess)
    assert.equal(calls, 2, 'decoded image stays cached')
  })
}

test('completion clears a settled failure cooldown without waiting for another render', async (t) => {
  hydrationEnvironment(t)
  const image = imageRoot('local://2/exports/settled.png')
  let calls = 0
  globalThis.fetch = async () => ++calls === 1 ? new Response(null, { status: 403 }) : pngResponse()
  await hydrateProtectedFileImages(image.root, messageAccess)
  await hydrateProtectedFileImages(image.root, messageAccess)
  assert.equal(calls, 1)
  clearProtectedFileFailureCache()
  await hydrateProtectedFileImages(image.root, messageAccess)
  assert.equal(calls, 2)
  assert.match(image.img.src, /^blob:/)
})

test('a settled 404 recovers on the same DOM node without a Markdown change', async (t) => {
  hydrationEnvironment(t)
  const image = imageRoot('local://2/exports/settled-404.png')
  const paragraph = {
    tagName: 'P', textContent: '', children: [image.img], style: { display: '' },
    setAttribute() {}, removeAttribute() {},
  }
  image.img.parentElement = paragraph
  let calls = 0
  globalThis.fetch = async () => ++calls === 1 ? new Response(null, { status: 404 }) : pngResponse()
  await hydrateProtectedFileImages(image.root, messageAccess)
  assert.equal(image.root.removed, false)
  assert.equal(image.img.style.display, 'none')
  assert.equal(paragraph.style.display, 'none', 'no empty image paragraph is left visible')
  await hydrateProtectedFileImages(image.root, messageAccess)
  assert.equal(calls, 1, 'known-missing requests remain cached')
  clearProtectedFileFailureCache()
  await hydrateProtectedFileImages(image.root, messageAccess)
  assert.equal(calls, 2)
  assert.match(image.img.src, /^blob:/)
  assert.equal(image.img.style.display, '')
  assert.equal(paragraph.style.display, '')
})

test('a stale message 404 cannot remove an image loaded with the corrected message ID', async (t) => {
  hydrationEnvironment(t)
  const image = imageRoot('local://2/exports/corrected-id.png')
  let finishOld!: (response: Response) => void
  globalThis.fetch = async url => String(url).includes('/temporary/')
    ? new Promise<Response>(resolve => { finishOld = resolve }) : pngResponse()
  const old = hydrateProtectedFileImages(image.root, { ...messageAccess, messageId: 'temporary' })
  await hydrateProtectedFileImages(image.root, messageAccess)
  const loaded = image.img.src
  finishOld(new Response(null, { status: 404 }))
  await old
  assert.equal(image.root.removed, false)
  assert.match(loaded, /^blob:/)
  assert.equal(image.img.src, loaded)
})

test('a missing request does not suppress the same source under another message', async (t) => {
  hydrationEnvironment(t)
  const source = 'local://2/exports/scoped-missing.png'
  const old = imageRoot(source)
  globalThis.fetch = async url => String(url).includes('/temporary/')
    ? new Response(null, { status: 404 }) : pngResponse()
  await hydrateProtectedFileImages(old.root, { ...messageAccess, messageId: 'temporary' })
  assert.equal(old.img.style.display, 'none')
  assert.match(protectProviderImageSrcInHTML(`<img src="${source}">`), /data-protected-src/)
  const corrected = imageRoot(source)
  await hydrateProtectedFileImages(corrected.root, messageAccess)
  assert.match(corrected.img.src, /^blob:/)
})

test('revoked sharing remains denied and completion retries are bounded', async (t) => {
  hydrationEnvironment(t)
  const image = imageRoot('local://2/exports/revoked-share.png')
  let finishOld!: (response: Response) => void
  let calls = 0
  globalThis.fetch = async () => {
    calls++
    if (calls === 1) return new Promise<Response>(resolve => { finishOld = resolve })
    // Even another completion signal while retrying cannot create a loop.
    clearProtectedFileFailureCache()
    return new Response(null, { status: 403 })
  }
  const pending = hydrateProtectedFileImages(image.root, messageAccess)
  clearProtectedFileFailureCache()
  finishOld(new Response(null, { status: 403 }))
  await pending
  assert.equal(calls, 2)
  assert.match(image.img.src, /^data:image\/gif/)
  assert.equal(image.img.dataset.authHydrated, '0')
  await hydrateProtectedFileImages(image.root, messageAccess)
  assert.equal(calls, 2, 'denial cooldown is retained')
})

test('shared images stay readable until the last rendering root releases them', async t => {
  hydrationEnvironment(t)
  const source = 'local://2/exports/shared-lifecycle.png'
  const first = imageRoot(source), second = imageRoot(source)
  const fetchBlob = globalThis.fetch
  let calls = 0
  globalThis.fetch = async () => { calls++; return pngResponse() }
  const revoke = t.mock.method(URL, 'revokeObjectURL')
  await Promise.all([
    hydrateProtectedFileImages(first.root, messageAccess),
    hydrateProtectedFileImages(second.root, messageAccess),
  ])
  const url = first.img.src
  assert.equal(calls, 1)
  assert.equal(second.img.src, url)
  releaseProtectedFileImages(first.root)
  assert.equal(revoke.mock.callCount(), 0)
  assert.equal(await (await fetchBlob(url)).text(), 'png bytes')
  await hydrateProtectedFileImages(second.root, messageAccess)
  assert.equal(calls, 1)
  releaseProtectedFileImages(second.root)
  releaseProtectedFileImages(second.root)
  assert.equal(revoke.mock.callCount(), 1)
  assert.equal(revoke.mock.calls[0].arguments[0], url)
  await assert.rejects(fetchBlob(url))
  assert.doesNotMatch(protectProviderImageSrcInHTML(`<img src="${source}">`), /src="blob:/)
})

test('a released request cannot repopulate the cache or delete a newer shared request', async t => {
  hydrationEnvironment(t)
  const source = 'local://2/exports/late-lifecycle.png'
  const first = imageRoot(source), second = imageRoot(source), third = imageRoot(source)
  const responses: Array<(response: Response) => void> = []
  const signals: AbortSignal[] = []
  globalThis.fetch = async (_url, init) => {
    signals.push(init!.signal!)
    // Deliberately ignore abort, as a response/body may already be queued.
    return new Promise<Response>(resolve => responses.push(resolve))
  }
  const create = t.mock.method(URL, 'createObjectURL')
  const old = hydrateProtectedFileImages(first.root, messageAccess)
  const shared = hydrateProtectedFileImages(second.root, messageAccess)
  releaseProtectedFileImages(first.root)
  assert.equal(signals[0].aborted, false)
  releaseProtectedFileImages(second.root)
  assert.equal(signals[0].aborted, true)
  const fresh = hydrateProtectedFileImages(second.root, messageAccess)
  responses[0](pngResponse())
  await Promise.all([old, shared])
  assert.equal(create.mock.callCount(), 0)
  const another = hydrateProtectedFileImages(third.root, messageAccess)
  assert.equal(signals.length, 2, 'old finally must not delete the new in-flight request')
  responses[1](pngResponse())
  await Promise.all([fresh, another])
  assert.equal(create.mock.callCount(), 1)
  assert.equal(second.img.src, third.img.src)
  assert.match(first.img.src, /^data:image\/gif/)
})

test('release during body decoding prevents allocating an object URL', async t => {
  hydrationEnvironment(t)
  const image = imageRoot('local://2/exports/late-body.png')
  let finishBody!: (blob: Blob) => void
  let readingBody!: () => void
  const bodyStarted = new Promise<void>(resolve => { readingBody = resolve })
  globalThis.fetch = async () => ({
    ok: true, headers: new Headers(),
    blob: () => { readingBody(); return new Promise<Blob>(resolve => { finishBody = resolve }) },
  } as Response)
  const create = t.mock.method(URL, 'createObjectURL')
  const pending = hydrateProtectedFileImages(image.root, messageAccess)
  await bodyStarted
  releaseProtectedFileImages(image.root)
  finishBody(new Blob(['png'], { type: 'image/png' }))
  await pending
  assert.equal(create.mock.callCount(), 0)
})

test('removing an image from an existing root releases its cache entry', async t => {
  hydrationEnvironment(t)
  const image = imageRoot('local://2/exports/removed-image.png')
  globalThis.fetch = async () => pngResponse()
  const revoke = t.mock.method(URL, 'revokeObjectURL')
  await hydrateProtectedFileImages(image.root, messageAccess)
  const url = image.img.src
  image.root.removed = true
  await hydrateProtectedFileImages(image.root, messageAccess)
  assert.deepEqual(revoke.mock.calls.map(call => call.arguments[0]), [url])
})

test('download cards keep owning their URL after replacing the image element', async t => {
  hydrationEnvironment(t)
  const image = imageRoot('local://2/exports/owned-card.txt')
  globalThis.fetch = async () => new Response(new Blob(['document'], { type: 'text/plain' }))
  const revoke = t.mock.method(URL, 'revokeObjectURL')
  await hydrateProtectedFileImages(image.root, messageAccess)
  const url = image.root.replacement.href
  await hydrateProtectedFileImages(image.root, messageAccess)
  assert.equal(revoke.mock.callCount(), 0)
  releaseProtectedFileImages(image.root)
  assert.deepEqual(revoke.mock.calls.map(call => call.arguments[0]), [url])
})

test('session cleanup releases loaded files and cancels pending fetches without poisoning retries', async t => {
  hydrationEnvironment(t)
  const loaded = imageRoot('local://2/exports/logout-loaded.png')
  const pending = imageRoot('local://2/exports/logout-pending.png')
  globalThis.fetch = async () => pngResponse()
  await hydrateProtectedFileImages(loaded.root, messageAccess)
  const url = loaded.img.src
  const revoke = t.mock.method(URL, 'revokeObjectURL')
  let signal!: AbortSignal
  globalThis.fetch = async (_url, init) => {
    signal = init!.signal!
    return new Promise<Response>((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
    })
  }
  const task = hydrateProtectedFileImages(pending.root, messageAccess)
  clearProtectedFileCache()
  assert.equal(signal.aborted, true)
  await task
  assert.deepEqual(revoke.mock.calls.map(call => call.arguments[0]), [url])
  globalThis.fetch = async () => pngResponse()
  await hydrateProtectedFileImages(pending.root, messageAccess)
  assert.match(pending.img.src, /^blob:/)
  await hydrateProtectedFileImages(loaded.root, messageAccess)
  assert.notEqual(loaded.img.src, url, 'old DOM hydration flags must not skip reloading')
})

test('Vue root replacement and scope disposal release resources; late detached hydration is ignored', async t => {
  hydrationEnvironment(t)
  const first = imageRoot('local://2/exports/first-root.png')
  const second = imageRoot('local://2/exports/second-root.png')
  const root = shallowRef<ParentNode | null>(first.root)
  const scope = effectScope()
  scope.run(() => useProtectedImageLifecycle(() => root.value))
  t.after(() => scope.stop())
  let calls = 0
  globalThis.fetch = async () => { calls++; return pngResponse() }
  const revoke = t.mock.method(URL, 'revokeObjectURL')
  await hydrateProtectedFileImages(first.root, messageAccess)
  const firstURL = first.img.src
  root.value = second.root
  await nextTick()
  assert.deepEqual(revoke.mock.calls.map(call => call.arguments[0]), [firstURL])
  await hydrateProtectedFileImages(second.root, messageAccess)
  const secondURL = second.img.src
  scope.stop()
  assert.deepEqual(revoke.mock.calls.map(call => call.arguments[0]), [firstURL, secondURL])
  second.root.isConnected = false
  await hydrateProtectedFileImages(second.root, messageAccess)
  assert.equal(calls, 2)
})

test('a pre-rendered card reloads after its previous root released the cached URL', async t => {
  hydrationEnvironment(t)
  const source = 'local://2/exports/transferred-card.txt'
  const first = imageRoot(source), second = imageRoot(source)
  let calls = 0
  globalThis.fetch = async () => {
    calls++
    return new Response(new Blob(['document'], { type: 'text/plain' }))
  }
  await hydrateProtectedFileImages(first.root, messageAccess)
  const oldURL = first.root.replacement.href
  const card = second.img.ownerDocument.createElement('a')
  card.href = oldURL
  card.dataset.protectedResource = source
  card.textContent = 'Original label'
  second.root.replacement = card
  releaseProtectedFileImages(first.root)
  await hydrateProtectedFileImages(second.root, messageAccess)
  assert.equal(calls, 2)
  assert.notEqual(second.root.replacement.href, oldURL)
  assert.equal(second.root.replacement.textContent, 'Original label')
  await hydrateProtectedFileImages(second.root, messageAccess)
  assert.equal(calls, 2, 'the recovered card remains cached')
})

test('detached owners are discarded before a late response allocates a Blob URL', async t => {
  hydrationEnvironment(t)
  const image = imageRoot('local://2/exports/detached-response.png')
  let finish!: (response: Response) => void
  globalThis.fetch = async () => new Promise<Response>(resolve => { finish = resolve })
  const create = t.mock.method(URL, 'createObjectURL')
  const pending = hydrateProtectedFileImages(image.root, messageAccess)
  image.root.isConnected = false
  finish(pngResponse())
  await pending
  assert.equal(create.mock.callCount(), 0)
})


test('preview clicks resolve the live card URL across message contexts', async t => {
  hydrationEnvironment(t)
  let click!: (event: any) => void
  let preview: any
  Object.assign((globalThis as any).window, {
    addEventListener: (_name: string, handler: any) => { click = handler },
    dispatchEvent: (event: any) => { preview = event.detail; return false },
  })
  const oldAnchor = (globalThis as any).HTMLAnchorElement
  const oldCustomEvent = (globalThis as any).CustomEvent
  ;(globalThis as any).HTMLAnchorElement = Object
  ;(globalThis as any).CustomEvent = class { detail: any; constructor(_type: string, options: any) { this.detail = options.detail } }
  t.after(() => {
    ;(globalThis as any).HTMLAnchorElement = oldAnchor
    ;(globalThis as any).CustomEvent = oldCustomEvent
  })
  globalThis.fetch = async () => new Response(new Blob(['document'], { type: 'text/plain' }))
  const first = imageRoot('local://2/exports/shared-context.txt')
  const second = imageRoot('local://2/exports/shared-context.txt')
  await hydrateProtectedFileImages(first.root, messageAccess)
  await hydrateProtectedFileImages(second.root, { ...messageAccess, messageId: 'second' })
  const firstURL = first.root.replacement.href
  const event = () => ({
    target: { closest: () => first.root.replacement },
    preventDefault() {}, stopPropagation() {},
  })
  click(event())
  assert.equal(preview?.blobURL, firstURL, 'the first card must preview its own authorized file')
  releaseProtectedFileImages(second.root)
  preview = undefined
  click(event())
  assert.equal(preview?.blobURL, firstURL, 'releasing another context must preserve the preview')
})

test('document Markdown root owns resources until it is replaced or disposed', async t => {
  hydrationEnvironment(t)
  const source = readFileSync(new URL('../components/doc-content.vue', import.meta.url), 'utf8')
  const setup = source.slice(source.indexOf('const docMarkdownRoot ='), source.indexOf("let url = ref('')", source.indexOf('const docMarkdownRoot =')))
  const code = ts.transpileModule(setup + '\n;({ docMarkdownRoot, getMarkdownRenderRoot })', {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText
  const scope = effectScope()
  t.after(() => scope.stop())
  const state = scope.run(() => vm.runInNewContext(code, { ref: shallowRef, mdContentWrap: shallowRef(null), useProtectedImageLifecycle }))
  const image = imageRoot('local://2/exports/doc-root.png')
  state.docMarkdownRoot.value = image.root
  await nextTick()
  globalThis.fetch = async () => pngResponse()
  const revoke = t.mock.method(URL, 'revokeObjectURL')
  await hydrateProtectedFileImages(state.getMarkdownRenderRoot(), messageAccess)
  const url = image.img.src
  state.docMarkdownRoot.value = null
  await nextTick()
  assert.deepEqual(revoke.mock.calls.map(call => call.arguments[0]), [url])
})

test('Wiki drawer rehydrates unchanged Markdown after its body remounts', async t => {
  hydrationEnvironment(t)
  const source = readFileSync(new URL('../views/chat/components/AgentStreamDisplay.vue', import.meta.url), 'utf8')
  const start = source.indexOf('\nwatch(', source.indexOf('const wikiDrawerContent ='))
  const snippet = source.slice(start, source.indexOf('const openWikiDrawer =', start))
  const code = ts.transpileModule(snippet, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText
  const body = shallowRef<ParentNode | null>(null)
  const content = ref('')
  const scope = effectScope()
  t.after(() => scope.stop())
  const tasks: Promise<void>[] = []
  scope.run(() => {
    useProtectedImageLifecycle(() => body.value)
    vm.runInNewContext(code, { watch, nextTick, wikiDrawerContent: content, wikiDrawerBodyRef: body,
      protectedFileAccess: ref(messageAccess), hydrateProtectedFileImages: (...args: Parameters<typeof hydrateProtectedFileImages>) => {
        const task = hydrateProtectedFileImages(...args); tasks.push(task); return task
      } })
  })
  let calls = 0
  globalThis.fetch = async () => { calls++; return pngResponse() }
  const first = imageRoot('local://2/exports/wiki-remount.png')
  body.value = first.root
  content.value = 'unchanged Markdown'
  await nextTick()
  await setImmediate()
  await Promise.all(tasks)
  assert.match(first.img.src, /^blob:/)
  body.value = null
  await nextTick()
  const second = imageRoot('local://2/exports/wiki-remount.png')
  body.value = second.root
  await nextTick()
  await setImmediate()
  await Promise.all(tasks)
  assert.match(second.img.src, /^blob:/)
  assert.equal(calls, 2)
})
