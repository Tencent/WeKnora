import assert from 'node:assert/strict'
import test from 'node:test'
import { createRenderer, nextTick, shallowRef } from 'vue'
import { useFloatingPreviewDrag } from './useFloatingPreviewDrag.ts'

test('preview drags in both directions, stays bounded, and releases capture on teardown', async () => {
  const originalObserver = globalThis.ResizeObserver
  let resize!: ResizeObserverCallback
  let disconnected = false
  const observed: Element[] = []
  globalThis.ResizeObserver = class {
    constructor(callback: ResizeObserverCallback) { resize = callback }
    observe(target: Element) { observed.push(target) }
    disconnect() { disconnected = true }
  } as unknown as typeof ResizeObserver
  const renderer = createRenderer<any, any>({
    patchProp() {}, insert() {}, remove() {}, setText() {}, setElementText() {},
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    parentNode: () => null, nextSibling: () => null,
  })
  const container = { clientWidth: 1000, clientHeight: 700 }
  const preview = {
    offsetParent: container, offsetWidth: 240, offsetHeight: 250, offsetLeft: 740, offsetTop: 434,
    getBoundingClientRect() { return { width: this.offsetWidth } },
  }
  const element = shallowRef<HTMLElement | null>(null)
  let controls!: ReturnType<typeof useFloatingPreviewDrag>
  let captured: number | null = null
  const handle = {
    setPointerCapture(id: number) { captured = id },
    hasPointerCapture(id: number) { return captured === id },
    releasePointerCapture() { captured = null },
  }
  const pointer = (clientX: number, clientY: number, extra = {}) => ({
    clientX, clientY, pointerId: 1, isPrimary: true, button: 0,
    currentTarget: handle, preventDefault() {}, ...extra,
  } as unknown as PointerEvent)
  const app = renderer.createApp({ setup() { controls = useFloatingPreviewDrag(element); return () => null } })
  app.mount({})
  try {
    element.value = preview as unknown as HTMLElement
    await nextTick()
    assert.deepEqual(observed, [preview, container])
    assert.deepEqual(controls.positionStyle.value, {}, 'preserve default corner placement before dragging')
    controls.startDrag(pointer(800, 450, { button: 2 }))
    controls.startDrag(pointer(800, 450, { isPrimary: false }))
    assert.equal(captured, null)
    controls.startDrag(pointer(800, 450))
    assert.equal(captured, 1)
    controls.moveDrag(pointer(600, 150, { pointerId: 2 }))
    assert.deepEqual(controls.positionStyle.value, {})
    controls.moveDrag(pointer(600, 150))
    assert.deepEqual(controls.positionStyle.value, { left: '540px', top: '134px', right: 'auto', bottom: 'auto' })
    controls.moveDrag(pointer(-2000, -2000))
    assert.equal(controls.positionStyle.value.left, '12px')
    assert.equal(controls.positionStyle.value.top, '12px')
    controls.moveDrag(pointer(2000, 2000))
    assert.equal(controls.positionStyle.value.left, '748px')
    assert.equal(controls.positionStyle.value.top, '438px')
    controls.stopDrag(pointer(2000, 2000, { pointerId: 2 }))
    assert.equal(controls.dragging.value, true)
    controls.stopDrag(pointer(2000, 2000))
    assert.equal(captured, null)
    controls.moveDrag(pointer(600, 150))
    assert.equal(controls.positionStyle.value.top, '438px', 'position remains after pointer release')
    container.clientWidth = 600
    container.clientHeight = 400
    resize([], {} as ResizeObserver)
    assert.equal(controls.positionStyle.value.left, '348px')
    assert.equal(controls.positionStyle.value.top, '138px')
    preview.offsetHeight = 300
    resize([], {} as ResizeObserver)
    assert.equal(controls.positionStyle.value.top, '88px', 'growing status/error content stays visible')
    controls.startDrag(pointer(400, 100))
    element.value = null
    await nextTick()
    assert.equal(disconnected, true)
    assert.equal(captured, null)
    assert.equal(controls.dragging.value, false)
    element.value = preview as unknown as HTMLElement
    await nextTick()
    controls.startDrag(pointer(400, 100))
  } finally {
    app.unmount()
    globalThis.ResizeObserver = originalObserver
  }
  assert.equal(captured, null)
})

test('floating windows drag at the app zoom level, retain their right anchor, and stay inside the viewport', async () => {
  const originalObserver = globalThis.ResizeObserver
  const originalWindow = globalThis.window
  const listeners = new Set<() => void>()
  const viewport = {
    innerWidth: 1440, innerHeight: 1000,
    addEventListener: (_: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) => listeners.delete(listener),
  }
  globalThis.window = viewport as unknown as Window & typeof globalThis
  let resize!: ResizeObserverCallback
  globalThis.ResizeObserver = class {
    constructor(callback: ResizeObserverCallback) { resize = callback }
    observe() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
  const renderer = createRenderer<any, any>({
    patchProp() {}, insert() {}, remove() {}, setText() {}, setElementText() {},
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    parentNode: () => null, nextSibling: () => null,
  })
  const element = shallowRef<HTMLElement | null>(null)
  let controls!: ReturnType<typeof useFloatingPreviewDrag>
  let scale = 1
  const preview = {
    offsetParent: null, offsetWidth: 440, offsetHeight: 720,
    get offsetLeft() { return viewport.innerWidth / scale - this.offsetWidth - parseFloat(controls.positionStyle.value.right || '12') },
    get offsetTop() { return parseFloat(controls.positionStyle.value.top || '268') },
    getBoundingClientRect() { return { width: this.offsetWidth * scale } },
  }
  let captured: number | null = null
  const handle = {
    setPointerCapture(id: number) { captured = id },
    hasPointerCapture(id: number) { return captured === id },
    releasePointerCapture() { captured = null },
  }
  const pointer = (clientX: number, clientY: number) => ({
    clientX, clientY, pointerId: 1, isPrimary: true, button: 0,
    currentTarget: handle, preventDefault() {},
  } as unknown as PointerEvent)
  const app = renderer.createApp({ setup() { controls = useFloatingPreviewDrag(element, 'viewport'); return () => null } })
  app.mount({})
  try {
    element.value = preview as unknown as HTMLElement
    await nextTick()
    assert.deepEqual(controls.positionStyle.value, {}, 'the initial bottom-right position is preserved')
    controls.startDrag(pointer(1000, 280))
    controls.moveDrag(pointer(800, 180))
    controls.stopDrag()
    assert.deepEqual(controls.positionStyle.value, { left: 'auto', right: '212px', top: '168px', bottom: 'auto' })
    preview.offsetWidth = 500
    resize([], {} as ResizeObserver)
    assert.equal(controls.positionStyle.value.right, '212px', 'resizing from the left keeps the right edge anchored')

    element.value = null
    await nextTick()
    assert.equal(listeners.size, 0)
    element.value = preview as unknown as HTMLElement
    await nextTick()
    assert.equal(controls.positionStyle.value.right, '212px', 'reopening keeps the chosen position')

    scale = 1.25
    preview.offsetHeight = 600
    controls.startDrag(pointer(800, 200))
    controls.moveDrag(pointer(675, 75))
    controls.stopDrag()
    assert.equal(controls.positionStyle.value.right, '312px')
    assert.equal(controls.positionStyle.value.top, '68px', '125 screen pixels move 100 CSS pixels at 125% zoom')
    controls.startDrag(pointer(800, 200))
    controls.moveDrag(pointer(5000, -5000))
    assert.equal(controls.positionStyle.value.right, '12px')
    assert.equal(controls.positionStyle.value.top, '12px')
    controls.moveDrag(pointer(-5000, 5000))
    assert.equal(controls.positionStyle.value.right, '640px')
    assert.equal(controls.positionStyle.value.top, '188px')
    controls.stopDrag()
    viewport.innerWidth = 700
    viewport.innerHeight = 800
    listeners.forEach(listener => listener())
    assert.equal(controls.positionStyle.value.right, '48px')
    assert.equal(controls.positionStyle.value.top, '28px')
    controls.startDrag(pointer(100, 100))
  } finally {
    app.unmount()
    globalThis.ResizeObserver = originalObserver
    globalThis.window = originalWindow
  }
  assert.equal(captured, null)
  assert.equal(listeners.size, 0)
})
