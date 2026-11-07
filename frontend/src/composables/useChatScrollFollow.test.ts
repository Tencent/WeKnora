import assert from 'node:assert/strict'
import test from 'node:test'
import { createRenderer, nextTick, ref, shallowRef } from 'vue'
import { useChatScrollFollow } from './useChatScrollFollow'

function mountViewport() {
  const listeners = new Map<string, EventListener>()
  let top = 1000
  const element = {
    firstElementChild: {}, scrollHeight: 1600, clientHeight: 600,
    get scrollTop() { return top },
    set scrollTop(value: number) { top = Math.max(0, Math.min(value, this.scrollHeight - this.clientHeight)) },
    addEventListener(name: string, handler: EventListener) { listeners.set(name, handler) },
    removeEventListener(name: string) { listeners.delete(name) },
  }
  const renderer = createRenderer<any, any>({
    patchProp() {}, insert() {}, remove() {}, setText() {}, setElementText() {},
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    parentNode: () => null, nextSibling: () => null,
  })
  const reading = ref(false), active = ref(true)
  let follow!: ReturnType<typeof useChatScrollFollow>
  const app = renderer.createApp({ setup() {
    follow = useChatScrollFollow(shallowRef(element as unknown as HTMLElement), reading, active)
    return () => null
  } })
  app.mount({})
  const wheel = (deltaY: number) => listeners.get('wheel')!({ deltaY } as unknown as Event)
  const scroll = (to: number) => { element.scrollTop = to; follow.onScroll() }
  return { app, element, reading, active, follow, listeners, wheel, scroll }
}

test('parent and child follow policy respects upward intent, queued output, collapse and return to bottom', async () => {
  const original = globalThis.ResizeObserver
  let resize!: () => void
  globalThis.ResizeObserver = class {
    constructor(callback: () => void) { resize = callback }
    observe() {} disconnect() {}
  } as unknown as typeof ResizeObserver
  const chat = mountViewport()
  try {
    // A token queues a scroll, then the user scrolls up before Vue flushes.
    chat.follow.scrollToBottom()
    chat.wheel(-12)
    chat.scroll(988)
    await nextTick()
    assert.equal(chat.reading.value, true, 'even a small upward gesture leaves the live edge')
    assert.equal(chat.element.scrollTop, 988)
    chat.element.scrollHeight += 100
    chat.follow.scrollToBottom(true)
    resize()
    await nextTick()
    assert.equal(chat.element.scrollTop, 988, 'resumed runs and delayed rendering cannot force a jump')

    // A collapse clamps the scrollbar to the bottom, but is not a user return.
    chat.element.scrollHeight = 1200
    chat.scroll(600)
    assert.equal(chat.reading.value, true)
    chat.element.scrollHeight += 200
    resize()
    assert.equal(chat.element.scrollTop, 600)
    chat.wheel(200)
    chat.scroll(800)
    assert.equal(chat.reading.value, false)
    chat.element.scrollHeight += 100
    resize()
    assert.equal(chat.element.scrollTop, 900, 'follow delayed content after returning to bottom')

    chat.active.value = false
    chat.element.scrollHeight += 100
    chat.follow.scrollToBottom(true)
    resize()
    await nextTick()
    assert.equal(chat.element.scrollTop, 900, 'hidden tabs do not scroll')
    chat.active.value = true
    chat.reading.value = true
    chat.follow.resume()
    await nextTick()
    assert.equal(chat.reading.value, false)
    assert.equal(chat.element.scrollTop, 1000)
  } finally { chat.app.unmount(); globalThis.ResizeObserver = original }
  assert.equal(chat.listeners.size, 0)
})

test('scrollbar and keyboard history navigation pause without requiring a wheel', async () => {
  const chat = mountViewport()
  try {
    chat.listeners.get('pointerdown')!({} as Event)
    chat.scroll(500)
    assert.equal(chat.reading.value, true)
    chat.follow.scrollToBottom(true)
    await nextTick()
    assert.equal(chat.element.scrollTop, 500)
    chat.follow.resume()
    await nextTick()
    chat.listeners.get('keydown')!({ key: 'PageUp' } as unknown as Event)
    assert.equal(chat.reading.value, true)
    chat.follow.scrollToBottom()
    await nextTick()
    assert.equal(chat.reading.value, true)
  } finally { chat.app.unmount() }
})
