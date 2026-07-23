import assert from 'node:assert/strict'
import test from 'node:test'
import {
  KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH,
  KNOWLEDGE_FOLDER_SIDEBAR_STORAGE_KEY,
  clampKnowledgeFolderSidebarWidth,
  createKnowledgeFolderSidebarResizeController,
  getKnowledgeFolderSidebarMaxWidth,
  parseKnowledgeFolderSidebarWidth,
} from './useResizableSidebar.ts'

class FakeTarget {
  listeners = new Map<string, Set<(event: any) => void>>()

  addEventListener(type: string, listener: (event: any) => void) {
    const listeners = this.listeners.get(type) || new Set()
    listeners.add(listener)
    this.listeners.set(type, listeners)
  }

  removeEventListener(type: string, listener: (event: any) => void) {
    this.listeners.get(type)?.delete(listener)
  }

  dispatch(type: string, event: any = {}) {
    for (const listener of [...(this.listeners.get(type) || [])]) listener(event)
  }

  count(type: string) {
    return this.listeners.get(type)?.size || 0
  }
}

function setup(viewport = 1440, stored: string | null = null) {
  const target = new FakeTarget()
  const values = new Map<string, string>()
  if (stored !== null) values.set(KNOWLEDGE_FOLDER_SIDEBAR_STORAGE_KEY, stored)
  const storage = {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) },
  }
  let viewportWidth = viewport
  const widths: number[] = []
  const desktop: boolean[] = []
  const dragging: boolean[] = []
  const selectionDisabled: boolean[] = []
  const controller = createKnowledgeFolderSidebarResizeController({
    target,
    storage,
    getViewportWidth: () => viewportWidth,
    onWidthChange: width => widths.push(width),
    onDesktopChange: value => desktop.push(value),
    onDraggingChange: value => dragging.push(value),
    setTextSelectionDisabled: value => selectionDisabled.push(value),
  })
  return {
    target,
    values,
    widths,
    desktop,
    dragging,
    selectionDisabled,
    controller,
    setViewport: (value: number) => { viewportWidth = value },
  }
}

test('width helpers preserve the 224px default and validate persisted values', () => {
  assert.equal(parseKnowledgeFolderSidebarWidth(null, 1440), KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH)
  assert.equal(parseKnowledgeFolderSidebarWidth('360', 1440), 360)
  assert.equal(parseKnowledgeFolderSidebarWidth('NaN', 1440), KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH)
  assert.equal(parseKnowledgeFolderSidebarWidth('12', 1440), KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH)
  assert.equal(parseKnowledgeFolderSidebarWidth('900', 1440), KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH)
  assert.equal(getKnowledgeFolderSidebarMaxWidth(1000), 500)
  assert.equal(clampKnowledgeFolderSidebarWidth(100, 1000), 224)
  assert.equal(clampKnowledgeFolderSidebarWidth(900, 1000), 500)
})

test('global mouse listeners resize in both directions, clamp, persist, and stop on mouseup', () => {
  const env = setup(1200)
  env.controller.mount()
  assert.equal(env.widths.at(-1), 224)
  assert.equal(env.target.count('resize'), 1)

  assert.equal(env.controller.start({ clientX: 100, button: 0 }), true)
  assert.equal(env.target.count('mousemove'), 1)
  assert.equal(env.target.count('mouseup'), 1)
  env.target.dispatch('mousemove', { clientX: 420 })
  assert.equal(env.widths.at(-1), 544)
  env.target.dispatch('mousemove', { clientX: -500 })
  assert.equal(env.widths.at(-1), 224)
  env.target.dispatch('mouseup')
  assert.equal(env.controller.isDragging(), false)
  assert.equal(env.target.count('mousemove'), 0)
  assert.equal(env.values.get(KNOWLEDGE_FOLDER_SIDEBAR_STORAGE_KEY), '224')
  assert.deepEqual(env.selectionDisabled, [true, false])

  const count = env.widths.length
  env.target.dispatch('mousemove', { clientX: 900 })
  assert.equal(env.widths.length, count)
})

test('restored width is viewport-clamped and resize reacts to a narrower viewport', () => {
  const env = setup(1400, '560')
  env.controller.mount()
  assert.equal(env.widths.at(-1), 560)
  env.setViewport(900)
  env.target.dispatch('resize')
  assert.equal(env.controller.isDesktop(), false)
  assert.equal(env.widths.at(-1), 450)
  assert.equal(env.controller.start({ clientX: 0, button: 0 }), false)
})

test('keyboard-sized adjustments, reset, and destroy update storage and clean every listener', () => {
  const env = setup(1440, '320')
  env.controller.mount()
  env.controller.adjust(8)
  assert.equal(env.widths.at(-1), 328)
  assert.equal(env.values.get(KNOWLEDGE_FOLDER_SIDEBAR_STORAGE_KEY), '328')
  env.controller.adjust(40)
  assert.equal(env.widths.at(-1), 368)
  env.controller.reset()
  assert.equal(env.widths.at(-1), 224)

  env.controller.start({ clientX: 0, button: 0 })
  env.controller.destroy()
  assert.equal(env.target.count('resize'), 0)
  assert.equal(env.target.count('mousemove'), 0)
  assert.equal(env.target.count('mouseup'), 0)
  assert.equal(env.selectionDisabled.at(-1), false)
})
