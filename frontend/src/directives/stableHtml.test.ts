import assert from 'node:assert/strict'
import test from 'node:test'
import { DOMParser } from '@xmldom/xmldom'
import { vStableHtml } from './stableHtml.ts'
import { clearProtectedFileFailureCache, hydrateProtectedFileImages } from '../utils/security.ts'

// Use real parsed trees and the complete directive. Adapt the small browser
// surface absent from xmldom rather than extracting the morph implementation.
function parse(html: string): any {
  const root = new DOMParser().parseFromString(`<div>${html}</div>`, 'text/xml').documentElement!
  function adapt(el: any) {
    const tag = el.tagName.toUpperCase()
    Object.defineProperties(el, {
      tagName: { value: tag },
      children: { get: () => Array.from(el.childNodes).filter((child: any) => child.nodeType === 1) },
      firstElementChild: { get: () => el.children[0] ?? null },
      parentElement: { get: () => el.parentNode?.nodeType === 1 ? el.parentNode : null },
      src: { get: () => el.getAttribute('src'), set: value => el.setAttribute('src', value) },
      alt: { get: () => el.getAttribute('alt') },
    })
    el.complete = true
    el.naturalWidth = 1
    el.classList = { contains: (name: string) => (el.getAttribute('class') || '').split(/\s+/).includes(name) }
    el.dataset = new Proxy({}, {
      get: (_, key: string) => el.getAttribute(`data-${key.replace(/[A-Z]/g, char => `-${char.toLowerCase()}`)}`),
      set: (_, key: string, value) => {
        el.setAttribute(`data-${key.replace(/[A-Z]/g, char => `-${char.toLowerCase()}`)}`, value)
        return true
      },
    })
    el.style = {
      get display() { return el.getAttribute('style')?.match(/(?:^|;)\s*display:\s*([^;]+)/)?.[1] || '' },
      set display(value: string) {
        const rest = (el.getAttribute('style') || '').replace(/(?:^|;)\s*display:[^;]*;?/g, '')
        el.setAttribute('style', `${rest}${value ? `;display:${value}` : ''}`)
      },
    }
    el.querySelectorAll = (selector: string) => Array.from(el.getElementsByTagName('img'))
      .filter((img: any) => selector.includes('src^=') || img.hasAttribute('data-protected-src'))
    for (const child of el.children) adapt(child)
  }
  adapt(root)
  return root
}

test('missing image and paragraph stay hidden across streaming morphs, then recover without new HTML', async t => {
  const previous = {
    Node: (globalThis as any).Node, HTMLImageElement: (globalThis as any).HTMLImageElement,
    document: (globalThis as any).document, window: (globalThis as any).window, fetch: globalThis.fetch,
  }
  Object.assign(globalThis, {
    Node: { ELEMENT_NODE: 1, TEXT_NODE: 3, COMMENT_NODE: 8 },
    HTMLImageElement: class { static [Symbol.hasInstance](el: any) { return el?.tagName === 'IMG' } },
    document: {
      createElement(tag: string) {
        assert.equal(tag, 'template')
        return { content: null, set innerHTML(html: string) { this.content = parse(html) } }
      },
    },
    window: { location: { origin: 'http://localhost' }, addEventListener() {} },
  })
  t.after(() => { Object.assign(globalThis, previous); clearProtectedFileFailureCache() })
  const source = 'local://2/exports/morph-hidden.png'
  const imageHTML = `<p><img src="${source}" data-protected-src="${source}" style="max-width:80%" /></p>`
  const root = parse(`${imageHTML}<p>first</p>`)
  const img = root.children[0].firstElementChild
  const paragraph = img.parentElement
  const access = { mode: 'message', sessionId: 'session', messageId: 'assistant' } as const
  let persisted = false
  globalThis.fetch = async () => persisted
    ? new Response(new Blob(['png'], { type: 'image/png' })) : new Response(null, { status: 404 })
  await hydrateProtectedFileImages(root, access)
  assert.equal(img.style.display, 'none')
  assert.equal(paragraph.style.display, 'none')
  let previousHTML = `${imageHTML}<p>first</p>`
  for (const text of ['first second', 'first second third']) {
    const html = `${imageHTML}<p>${text}</p>`
    ;(vStableHtml.updated as Function)(root, { value: html, oldValue: previousHTML })
    // Assert BEFORE another onUpdated/hydrate pass can mask a flicker.
    assert.equal(root.children[0].firstElementChild, img)
    assert.equal(img.style.display, 'none')
    assert.equal(paragraph.style.display, 'none')
    previousHTML = html
  }
  persisted = true
  clearProtectedFileFailureCache()
  await hydrateProtectedFileImages(root, access)
  assert.match(img.src, /^blob:/)
  assert.equal(img.style.display, '')
  assert.equal(paragraph.style.display, '')
  assert.equal(img.hasAttribute('data-protected-hidden'), false)
  assert.equal(paragraph.hasAttribute('data-protected-hidden'), false)

  // A new source occupying the same paragraph must not inherit hidden state.
  persisted = false
  const other = 'local://2/exports/morph-other.png'
  const otherHTML = imageHTML.replaceAll(source, other)
  ;(vStableHtml.updated as Function)(root, { value: `${otherHTML}<p>changed</p>`, oldValue: previousHTML })
  await hydrateProtectedFileImages(root, access)
  assert.equal(img.style.display, 'none')
  ;(vStableHtml.updated as Function)(root, { value: `${imageHTML}<p>restored</p>`, oldValue: `${otherHTML}<p>changed</p>` })
  assert.notEqual(img.style.display, 'none')
  assert.notEqual(paragraph.style.display, 'none')
  assert.equal(img.hasAttribute('data-protected-hidden'), false)
})
