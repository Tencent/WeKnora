import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { clearProtectedFileFailureCache, hydrateProtectedFileImages, protectProviderImageSrcInHTML } from './security.ts'
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
  const root: any = { replacement: null, removed: false, querySelectorAll: () => root.replacement || root.removed ? [] : [img] }
  const attrs: Record<string, string> = { src: source }
  const img: any = {
    alt: '比赛 PPT', dataset: {}, style: { display: '' },
    get src() { return attrs.src },
    set src(value: string) { attrs.src = value },
    getAttribute: (key: string) => attrs[key] || '',
    setAttribute: (key: string, value: string) => { attrs[key] = value },
    removeAttribute: (key: string) => { delete attrs[key] },
    replaceWith: (link: any) => { root.replacement = link },
    remove: () => { root.removed = true },
    ownerDocument: { createElement: (tag: string) => ({ tag, dataset: {}, addEventListener() {} }) },
  }
  return { root, img }
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
    globalThis.fetch = originalFetch
    ;(globalThis as any).window = originalWindow
  }
})

function hydrationEnvironment(t: TestContext) {
  const originalWindow = (globalThis as any).window
  const originalFetch = globalThis.fetch
  ;(globalThis as any).window = { location: { origin: 'http://localhost' }, addEventListener() {} }
  clearProtectedFileFailureCache()
  t.after(() => {
    globalThis.fetch = originalFetch
    ;(globalThis as any).window = originalWindow
    clearProtectedFileFailureCache()
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
