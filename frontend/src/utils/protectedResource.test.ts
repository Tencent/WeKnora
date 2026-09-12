import assert from 'node:assert/strict'
import test from 'node:test'
import { hydrateProtectedFileImages, protectProviderImageSrcInHTML } from './security.ts'
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
  const root: any = { replacement: null, querySelectorAll: () => root.replacement ? [] : [img] }
  const attrs: Record<string, string> = { src: source }
  const img: any = {
    alt: '比赛 PPT', dataset: {}, src: '',
    getAttribute: (key: string) => attrs[key] || '',
    setAttribute: (key: string, value: string) => { attrs[key] = value },
    removeAttribute: (key: string) => { delete attrs[key] },
    replaceWith: (link: any) => { root.replacement = link },
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
