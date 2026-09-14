import assert from 'node:assert/strict'
import test from 'node:test'
import { createRenderer, ref, shallowRef } from 'vue'
import { useStickyBottomOnResize } from './useStickyBottomOnResize.ts'

function mountChat() {
  const renderer = createRenderer<any, any>({
    patchProp() {}, insert() {}, remove() {}, setText() {}, setElementText() {},
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    parentNode: () => null, nextSibling: () => null,
  })
  const messageList = {} as HTMLElement
  let top = 400
  const element = {
    firstElementChild: messageList,
    scrollHeight: 1000,
    clientHeight: 600,
    get scrollTop() { return top },
    set scrollTop(value: number) {
      top = Math.max(0, Math.min(value, this.scrollHeight - this.clientHeight))
    },
  }
  const container = shallowRef(element as unknown as HTMLElement | null)
  const detached = ref(false)
  const app = renderer.createApp({
    setup() {
      useStickyBottomOnResize(container, detached)
      return () => null
    },
  })
  app.mount({})
  return { app, element, container, detached, messageList }
}

test('keeps a trailing steer bubble at the live edge before each streaming frame paints', () => {
  const originalObserver = globalThis.ResizeObserver
  const originalRequest = globalThis.requestAnimationFrame
  const originalCancel = globalThis.cancelAnimationFrame
  let onResize!: ResizeObserverCallback
  let observed: Element | undefined
  let disconnected = false
  let deferredFrames = 0
  globalThis.ResizeObserver = class {
    constructor(callback: ResizeObserverCallback) { onResize = callback }
    observe(target: Element) { observed = target }
    unobserve() {}
    disconnect() { disconnected = true }
  } as typeof ResizeObserver
  globalThis.requestAnimationFrame = () => ++deferredFrames
  globalThis.cancelAnimationFrame = () => {}
  const chat = mountChat()
  const resize = () => onResize([], {} as ResizeObserver)
  try {
    assert.equal(observed, chat.messageList)
    // ResizeObserver runs after layout and before paint. An rAF requested here
    // runs in the NEXT frame, exposing the pushed-down bubble in this frame.
    for (let frame = 0; frame < 120; frame++) {
      chat.element.scrollHeight += frame % 5 === 4 ? -12 : 24
      resize()
      const bubbleBottom = chat.element.scrollHeight - chat.element.scrollTop
      assert.equal(bubbleBottom, chat.element.clientHeight,
        `steer bubble must stay at the viewport bottom in frame ${frame}`)
    }
    assert.equal(deferredFrames, 0, 'resize follow must finish before the current paint')

    chat.detached.value = true
    chat.element.scrollTop -= 200
    const readingPosition = chat.element.scrollTop
    chat.element.scrollHeight += 100
    resize()
    assert.equal(chat.element.scrollTop, readingPosition, 'preserve intentional upward scrolling')

    chat.detached.value = false
    resize()
    assert.equal(chat.element.scrollHeight - chat.element.scrollTop, chat.element.clientHeight)
    chat.container.value = null
    assert.doesNotThrow(resize)
  } finally {
    chat.app.unmount()
    globalThis.ResizeObserver = originalObserver
    globalThis.requestAnimationFrame = originalRequest
    globalThis.cancelAnimationFrame = originalCancel
  }
  assert.equal(disconnected, true)
})
