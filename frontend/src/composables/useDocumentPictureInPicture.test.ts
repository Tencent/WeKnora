import assert from 'node:assert/strict'
import test from 'node:test'
import { createRenderer, nextTick, ref } from 'vue'
import { useDocumentPictureInPicture } from './useDocumentPictureInPicture.ts'

function attributes() {
  const values = new Map<string, string>()
  return {
    getAttribute: (key: string) => values.get(key) ?? null,
    setAttribute: (key: string, value: string) => values.set(key, value),
    removeAttribute: (key: string) => values.delete(key),
  }
}
function fakeDocument() {
  const nodes: any[] = []
  return {
    baseURI: 'https://weknora.test/chat', title: '', nodes,
    documentElement: attributes(), body: attributes(),
    head: { append: (node: any) => nodes.push(node) },
    styleSheets: [{ href: '/assets/app.css', disabled: false, media: { mediaText: '' } }],
    createElement: (tag: string) => ({ tag }),
  }
}
function fakeWindow() {
  const events = new EventTarget()
  return Object.assign(events, {
    document: fakeDocument(), closed: false,
    close() { if (!this.closed) { this.closed = true; events.dispatchEvent(new Event('pagehide')) } },
  })
}
function harness(requestWindow?: () => Promise<any>) {
  const originals = { window: globalThis.window, document: globalThis.document, observer: globalThis.MutationObserver }
  const source = fakeDocument()
  const observers: { callback: () => void; disconnected: boolean }[] = []
  const opener: any = { isSecureContext: true, documentPictureInPicture: requestWindow ? { requestWindow } : undefined }
  opener.top = opener
  globalThis.window = opener
  globalThis.document = source as unknown as Document
  globalThis.MutationObserver = class {
    disconnected = false
    constructor(public callback: () => void) { observers.push(this) }
    observe() {}
    disconnect() { this.disconnected = true }
  } as unknown as typeof MutationObserver
  const renderer = createRenderer<any, any>({
    patchProp() {}, insert() {}, remove() {}, setText() {}, setElementText() {},
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    parentNode: () => null, nextSibling: () => null,
  })
  const selected = ref(true), title = ref('Browser preview')
  let pip!: ReturnType<typeof useDocumentPictureInPicture>
  const app = renderer.createApp({ setup() { pip = useDocumentPictureInPicture(selected, () => title.value); return () => null } })
  app.mount({})
  return { pip, selected, title, source, observers, unmount: () => app.unmount(), restore() {
    app.unmount()
    globalThis.window = originals.window
    globalThis.document = originals.document
    globalThis.MutationObserver = originals.observer
  } }
}

test('PiP preserves task selection, synchronizes title/theme and releases window resources', async () => {
  const windows: ReturnType<typeof fakeWindow>[] = []
  const h = harness(async () => { const win = fakeWindow(); windows.push(win); return win })
  try {
    h.source.documentElement.setAttribute('class', 'dark')
    await h.pip.open()
    assert.equal(h.pip.target.value, windows[0]!.document.body)
    assert.equal(windows[0]!.document.documentElement.getAttribute('class'), 'dark')
    assert.ok(windows[0]!.document.nodes.some(node => node.tag === 'link' && node.href === '/assets/app.css'))
    h.title.value = '预览'
    await nextTick()
    assert.equal(windows[0]!.document.title, '预览')
    h.source.documentElement.setAttribute('class', 'light')
    h.observers[0]!.callback()
    assert.equal(windows[0]!.document.documentElement.getAttribute('class'), 'light')
    windows[0]!.close()
    assert.equal(h.pip.target.value, null)
    assert.equal(h.selected.value, true, 'closing PiP does not stop the task')
    assert.equal(h.observers[0]!.disconnected, true)
    await h.pip.open()
    h.selected.value = false
    assert.equal(windows[1]!.closed, true, 'ending the task closes PiP')
    assert.equal(h.pip.target.value, null)
  } finally { h.restore() }
})

test('unsupported API and rejected opening keep the page preview available for retry', async () => {
  const unsupported = harness()
  try {
    assert.equal(unsupported.pip.supported, false)
    await unsupported.pip.open()
    assert.equal(unsupported.pip.target.value, null)
  } finally { unsupported.restore() }
  let reject = true
  const h = harness(async () => { if (reject) throw new Error('denied'); return fakeWindow() })
  try {
    await assert.rejects(h.pip.open(), /denied/)
    assert.equal(h.pip.target.value, null)
    assert.equal(h.pip.opening.value, false)
    assert.equal(h.selected.value, true)
    reject = false
    await h.pip.open()
    assert.ok(h.pip.target.value)
  } finally { h.restore() }
})

test('opening is single-flight and a late window is closed after unmount', async () => {
  let resolve!: (win: any) => void, requests = 0
  const h = harness(() => { requests++; return new Promise(r => { resolve = r }) })
  try {
    const opening = h.pip.open()
    await h.pip.open()
    assert.equal(requests, 1)
    assert.equal(h.pip.opening.value, true)
    h.unmount()
    const late = fakeWindow()
    resolve(late)
    await opening
    assert.equal(late.closed, true)
    assert.equal(h.pip.target.value, null)
  } finally { h.restore() }
})

test('stale opening cannot replace or close a newly selected task window', async () => {
  const pending: ((win: any) => void)[] = []
  const h = harness(() => new Promise(r => pending.push(r)))
  try {
    const oldOpen = h.pip.open()
    h.selected.value = false
    h.selected.value = true
    const newOpen = h.pip.open()
    const current = fakeWindow(), stale = fakeWindow()
    pending[1]!(current)
    await newOpen
    pending[0]!(stale)
    await oldOpen
    assert.equal(stale.closed, true)
    assert.equal(current.closed, false)
    assert.equal(h.pip.target.value, current.document.body)
  } finally { h.restore() }
})
