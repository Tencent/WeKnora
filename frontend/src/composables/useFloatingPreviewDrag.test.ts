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
  const preview = { offsetParent: container, offsetWidth: 240, offsetHeight: 250, offsetLeft: 740, offsetTop: 434 }
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
