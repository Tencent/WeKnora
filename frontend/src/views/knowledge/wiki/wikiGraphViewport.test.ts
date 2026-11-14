import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'
import { graphClusterOutline, visibleGraphLabels } from './wikiGraphLayout.ts'

// Execute the component's real viewport controller with a deterministic frame
// clock. No Vue template or browser layout is needed to count SVG mutations.
const source = readFileSync(new URL('./WikiBrowser.vue', import.meta.url), 'utf8')
const start = source.indexOf('function setupPanZoom(')
const end = source.indexOf('// ─── Hover Highlight', start)
const controller = ts.transpileModule(source.slice(start, end), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText

class ElementFixture {
  attributes = new Map<string, string>()
  writes: string[] = []
  style: Record<string, string> = {}
  listeners = new Map<string, (event: any) => void>()
  classList = { toggle() {} }
  setAttribute(name: string, value: string) { this.attributes.set(name, value); this.writes.push(name) }
  getAttribute(name: string) { return this.attributes.get(name) ?? null }
  addEventListener(name: string, handler: (event: any) => void) { this.listeners.set(name, handler) }
  removeEventListener(name: string) { this.listeners.delete(name) }
  getBoundingClientRect() { return { left: 0, top: 0 } }
  closest() { return null }
}

function fixture(scale = 1) {
  const frames = new Map<number, FrameRequestCallback>()
  let nextFrame = 0, labelPasses = 0, outlines = 0, edgeUpdates = 0
  const svg = new ElementFixture(), root = new ElementFixture(), window = new ElementFixture()
  const nodes = [
    { slug: 'first', x: 0, y: 0, linkCount: 10, labelWidth: 80 },
    { slug: 'second', x: 500, y: 0, linkCount: 1, labelWidth: 80 },
    { slug: 'colliding', x: 0, y: 0, linkCount: 0, labelWidth: 80 },
  ]
  const nodeEls = nodes.map(node => ({ node, text: new ElementFixture() }))
  const state: any = {
    svg, root, window, Element: ElementFixture, performance: { now: () => 0 },
    requestAnimationFrame: (callback: FrameRequestCallback) => { frames.set(++nextFrame, callback); return nextFrame },
    cancelAnimationFrame: (id: number) => frames.delete(id),
    graphNodes: nodes, graphNodeElsRef: nodeEls, graphEdgeElsRef: [],
    graphSectionEls: [], graphContourEls: [], graphClusterEls: [],
    graphFocusedSlugs: new Set(), graphImportantSlugs: new Set(),
    graphSelectedSlug: { value: null }, graphHighlightSlug: { value: null },
    graphDrawerVisible: { value: false }, graphFocusGroup: { value: null },
    graphEdgeMarkersVisible: true, showArrows: { value: true },
    graphPanZoomRef: null, disposeGraphPanZoom() {}, refreshGraphLabels() {},
    clearHighlight() {}, exitGraphFocus() {},
    graphNodeRadius: () => 8,
    setEdgePositions: () => { edgeUpdates++ },
    visibleGraphLabels: (...args: Parameters<typeof visibleGraphLabels>) => { labelPasses++; return visibleGraphLabels(...args) },
    graphClusterOutline: (...args: Parameters<typeof graphClusterOutline>) => { outlines++; return graphClusterOutline(...args) },
  }
  runInNewContext(`${controller}\nsetupPanZoom(svg, root, { scale: ${scale}, x: 0, y: 0 })`, state)
  function flush(time = 16) {
    const callbacks = [...frames.values()]
    frames.clear()
    for (const callback of callbacks) callback(time)
  }
  flush()
  return {
    svg, root, window, nodes, nodeEls, state, frames, flush,
    passes: () => labelPasses, outlines: () => outlines, edgeUpdates: () => edgeUpdates,
    wheel(deltaY = -120) { svg.listeners.get('wheel')!({ preventDefault() {}, deltaY, clientX: 100, clientY: 50 }) },
  }
}

test('wheel bursts update the cursor-anchored transform immediately and lay out labels once per frame', () => {
  const f = fixture()
  const initialPasses = f.passes()
  for (let i = 0; i < 8; i++) f.wheel()
  const scale = 1.08 ** 8
  assert.ok(Math.abs(f.state.graphPanZoomRef.getScale() - scale) < 1e-10)
  const { x, y } = f.state.graphPanZoomRef.getTranslate()
  assert.ok(Math.abs(x - 100 * (1 - scale)) < 1e-10)
  assert.ok(Math.abs(y - 50 * (1 - scale)) < 1e-10)
  assert.match(f.root.getAttribute('transform')!, /scale\(/)
  assert.equal(f.passes(), initialPasses, 'wheel events must not synchronously lay out labels')
  assert.equal(f.frames.size, 1)
  f.flush()
  assert.equal(f.passes(), initialPasses + 1)
  assert.equal(f.frames.size, 0)
})

test('collision-rejected labels leave SVG layout and receive no font or position writes', () => {
  const f = fixture()
  const hidden = f.nodeEls[2]!.text
  assert.equal(hidden.style.display, 'none')
  assert.equal(hidden.getAttribute('font-size'), null)
  assert.equal(hidden.getAttribute('dy'), null)
  f.wheel()
  f.flush()
  assert.equal(hidden.style.display, 'none')
  assert.equal(hidden.getAttribute('font-size'), null)
  assert.equal(hidden.getAttribute('dy'), null)
  assert.equal(f.nodeEls[0]!.text.style.display, '')
  assert.equal(f.nodeEls[1]!.text.style.display, '')
})

test('pan-only transforms do not recompute labels, even during mouse dragging', () => {
  const f = fixture()
  const before = f.passes()
  f.state.graphPanZoomRef.setTranslate(10, 20)
  f.state.graphPanZoomRef.apply()
  f.svg.listeners.get('mousedown')!({ button: 0, target: f.svg, clientX: 0, clientY: 0 })
  f.window.listeners.get('mousemove')!({ clientX: 40, clientY: 50 })
  f.flush()
  assert.equal(f.passes(), before)
  assert.match(f.root.getAttribute('transform')!, /translate\(50,70\)/)
})

test('hover and selection reveal hidden labels with current zoom metrics, then hide them again', () => {
  const f = fixture(0.2)
  const text = f.nodeEls[1]!.text
  assert.equal(text.style.display, 'none')
  f.state.graphHighlightSlug.value = 'second'
  f.state.refreshGraphLabels()
  f.state.refreshGraphLabels()
  f.flush()
  assert.equal(text.style.display, '')
  assert.equal(text.getAttribute('font-size'), '60')
  assert.equal(text.getAttribute('dy'), '88')
  f.state.graphSelectedSlug.value = 'second'
  f.state.graphHighlightSlug.value = null
  f.state.refreshGraphLabels()
  f.flush()
  assert.equal(text.style.display, '')
  f.state.graphSelectedSlug.value = null
  f.state.refreshGraphLabels()
  f.flush()
  assert.equal(text.style.display, 'none')
})

test('contours coalesce zooms, retain their pan cache, and invalidate after node movement', () => {
  const f = fixture()
  f.state.graphContourEls.push({ path: new ElementFixture(), cluster: { slugs: ['first', 'second'], representative: 'first' } })
  f.state.refreshGraphLabels()
  f.flush()
  assert.equal(f.outlines(), 1)
  for (let i = 0; i < 5; i++) f.wheel()
  assert.equal(f.outlines(), 1)
  f.flush()
  assert.equal(f.outlines(), 2)
  f.state.graphPanZoomRef.setTranslate(100, 200)
  f.state.graphPanZoomRef.apply()
  f.flush()
  assert.equal(f.outlines(), 2)
  f.nodes[0]!.x += 10
  f.state.refreshGraphLabels()
  f.flush()
  assert.equal(f.outlines(), 3)
})

test('marker thresholds and arrow toggles still update edge endpoints', () => {
  const f = fixture(0.7)
  f.state.graphEdgeElsRef.push({ line: new ElementFixture(), source: 'first', target: 'second' })
  f.state.graphPanZoomRef.setScale(1)
  f.state.graphPanZoomRef.apply()
  f.flush()
  assert.equal(f.state.graphEdgeMarkersVisible, true)
  assert.equal(f.edgeUpdates(), 1)
  f.state.showArrows.value = false
  f.state.refreshGraphLabels()
  f.flush()
  assert.equal(f.state.graphEdgeMarkersVisible, false)
  assert.equal(f.edgeUpdates(), 2)
})

test('cluster captions leave layout when hidden and retain keyboard access when revealed', () => {
  const f = fixture(0.4)
  const group = new ElementFixture()
  const cluster = { slugs: ['first', 'second'], anchors: ['first'], representative: 'first' }
  f.state.graphClusterEls.push({ group, cluster, anchorSlug: 'first', labelWidth: 80, priority: 100000 })
  f.state.refreshGraphLabels()
  f.flush()
  assert.equal(group.style.display, '')
  assert.equal(group.getAttribute('tabindex'), '0')
  assert.equal(f.nodeEls[0]!.text.style.display, 'none', 'caption replaces its ordinary label')
  f.state.graphPanZoomRef.setScale(1.3)
  f.state.graphPanZoomRef.apply()
  f.flush()
  assert.equal(group.style.display, 'none')
  assert.equal(group.getAttribute('tabindex'), '-1')
  assert.equal(f.nodeEls[0]!.text.style.display, '')
  // Outside groups retain a focus-switching caption even at detail zoom.
  f.state.graphFocusedSlugs.add('second')
  f.state.refreshGraphLabels()
  f.flush()
  assert.equal(group.style.display, '')
  assert.equal(group.getAttribute('tabindex'), '0')
})

test('same-scale hover refreshes do not rewrite visible label geometry', () => {
  const f = fixture()
  const text = f.nodeEls[1]!.text
  text.writes.length = 0
  f.state.graphHighlightSlug.value = 'second'
  f.state.refreshGraphLabels()
  f.flush()
  assert.deepEqual(text.writes, [])
})

test('animated focus reaches its final transform and updates labels at the final scale', () => {
  const f = fixture()
  f.state.graphPanZoomRef.flyTo(200, 300, 2, 100)
  f.flush(50)
  f.flush(100)
  f.flush(116)
  assert.equal(f.root.getAttribute('transform'), 'translate(200,300) scale(2)')
  assert.equal(f.nodeEls[1]!.text.getAttribute('font-size'), '6')
  assert.equal(f.frames.size, 0)
})

test('disposing a viewport cancels queued label work and animated navigation', () => {
  const f = fixture()
  const before = f.passes()
  f.wheel()
  f.state.graphPanZoomRef.flyTo(100, 100, 2)
  assert.ok(f.frames.size > 0)
  f.state.disposeGraphPanZoom()
  f.flush()
  assert.equal(f.passes(), before)
  assert.equal(f.frames.size, 0)
  assert.equal(f.window.listeners.size, 0)
})
