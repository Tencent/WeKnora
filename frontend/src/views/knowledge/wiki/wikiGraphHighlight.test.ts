import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'

const source = readFileSync(new URL('./WikiBrowser.vue', import.meta.url), 'utf8')
const start = source.indexOf('// ─── Hover Highlight ───')
const end = source.indexOf('// graphSearchOptions drives', start)
const positionStart = source.indexOf('function createGraphPositionWriter(')
const positionEnd = source.indexOf('// ─── Drag ───', positionStart)
const controller = ts.transpileModule(`${source.slice(start, end)}\n${source.slice(positionStart, positionEnd)}`, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText

class StyleFixture {
  opacity = ''
  writes = 0
  setProperty(name: string, value: string) { (this as any)[name] = value; this.writes++ }
  removeProperty(name: string) { delete (this as any)[name]; this.writes++ }
}

class ElementFixture {
  attributes = new Map<string, string>()
  style = new StyleFixture()
  classList = {
    classes: new Set<string>(),
    add(name: string) { this.classes.add(name) },
    remove(name: string) { this.classes.delete(name) },
    contains(name: string) { return this.classes.has(name) },
    toggle(name: string, force?: boolean) {
      const enabled = force ?? !this.classes.has(name)
      if (enabled) this.classes.add(name)
      else this.classes.delete(name)
      return enabled
    },
  }
  parentElement: ElementFixture | null = null
  writes = 0
  setAttribute(name: string, value: string) { this.attributes.set(name, value); this.writes++ }
  getAttribute(name: string) { return this.attributes.get(name) ?? null }
  removeAttribute(name: string) { this.attributes.delete(name); this.writes++ }
}

function graph(size = 80) {
  const nodes = Array.from({ length: size }, (_, i) => ({ slug: `n${i}`, type: 'concept', linkCount: 1, x: 0, y: 0 }))
  const graphRoot = new ElementFixture()
  const nodeGroup = new ElementFixture()
  nodeGroup.parentElement = graphRoot
  const nodeEls = nodes.map(node => ({
    node, g: (() => { const g = new ElementFixture(); g.parentElement = nodeGroup; return g })(),
    circle: new ElementFixture(), text: new ElementFixture(), activeRing: new ElementFixture(),
  }))
  const edgeGroup = new ElementFixture()
  edgeGroup.parentElement = graphRoot
  const edgeEls = nodes.flatMap((node, i) => [0, 1, 2].map(offset => {
    const line = new ElementFixture()
    line.parentElement = edgeGroup
    return { line, source: node.slug, target: `n${(i + offset + 1) % size}`, bidir: false }
  }))
  const adjacency = new Map<string, Set<string>>()
  for (const edge of edgeEls) {
    if (!adjacency.has(edge.source)) adjacency.set(edge.source, new Set())
    adjacency.get(edge.source)!.add(edge.target)
  }
  const state: any = {
    graphSelectedSlug: { value: null }, graphFocusedSlugs: new Set<string>(),
    graphHighlightState: null, graphGroupMembersDirty: false,
    graphNodeRadius: () => 8, nodeColorMap: { concept: '#0052d9' },
    graphEdgeColor: '#999', graphEdgeWidth: '1', graphEdgeOpacity: '0.3',
    showArrows: { value: true }, refreshGraphLabels() {},
  }
  const scope = { ...state, graphAdjacencyRef: adjacency }
  runInNewContext(`${controller}\nthis.applyHighlight = applyHighlight; this.clearHighlight = clearHighlight; this.createGraphPositionWriter = createGraphPositionWriter; this.setEdgePositions = setEdgePositions; this.graphEdgeMarkersVisible = true`, scope)
  return { state: scope, nodeEls, edgeEls, adjacency }
}

test('hover only rewrites the old and new neighbour sets', () => {
  const { state, nodeEls, edgeEls, adjacency } = graph()
  state.applyHighlight('n0', adjacency, nodeEls, edgeEls)
  for (const node of nodeEls) node.circle.writes = 0
  for (const edge of edgeEls) edge.line.writes = 0
  state.applyHighlight('n0', adjacency, nodeEls, edgeEls, 'n10')
  const touched = nodeEls.filter(node => node.circle.writes > 0).map(node => node.node.slug)
  assert.ok(touched.length < nodeEls.length / 2, `${touched.length} of ${nodeEls.length} nodes rewritten`)
  assert.ok(edgeEls.some(edge => edge.line.writes > 0))
  assert.ok(edgeEls.filter(edge => edge.line.writes > 0).length < edgeEls.length / 2)
})

test('unchanged hover state does not touch any SVG attributes', () => {
  const { state, nodeEls, edgeEls, adjacency } = graph()
  state.applyHighlight('n0', adjacency, nodeEls, edgeEls, 'n10')
  for (const node of nodeEls) { node.circle.writes = 0 }
  for (const edge of edgeEls) edge.line.writes = 0
  state.applyHighlight('n0', adjacency, nodeEls, edgeEls, 'n10')
  const circleWrites = nodeEls.reduce((sum, node) => sum + node.circle.writes, 0)
  const edgeWrites = edgeEls.reduce((sum, edge) => sum + edge.line.writes, 0)
  assert.equal(circleWrites, 0)
  assert.equal(edgeWrites, 0)
})

test('clearing a hover restores only its highlighted neighbourhood', () => {
  const { state, nodeEls, edgeEls, adjacency } = graph()
  state.applyHighlight('n0', adjacency, nodeEls, edgeEls, 'n10')
  for (const node of nodeEls) node.circle.writes = 0
  for (const edge of edgeEls) edge.line.writes = 0
  state.clearHighlight(nodeEls, edgeEls)
  const touchedNodes = nodeEls.filter(node => node.circle.writes > 0).length
  const touchedEdges = edgeEls.filter(edge => edge.line.writes > 0).length
  assert.ok(touchedNodes < nodeEls.length / 2, `${touchedNodes} of ${nodeEls.length} nodes restored`)
  assert.ok(touchedEdges < edgeEls.length / 2, `${touchedEdges} of ${edgeEls.length} edges restored`)
  assert.equal(state.graphHighlightState, null)
})

test('an edge-less graph still enters and leaves batched highlight state', () => {
  const { state, nodeEls } = graph(8)
  state.applyHighlight('n0', new Map(), nodeEls, [])
  assert.equal((nodeEls[0]!.g.parentElement!.parentElement as any).classList.contains('is-highlighting'), true)
  for (const node of nodeEls) node.circle.writes = 0
  state.clearHighlight(nodeEls, [])
  assert.equal((nodeEls[0]!.g.parentElement!.parentElement as any).classList.contains('is-highlighting'), false)
  assert.ok(nodeEls.reduce((sum, node) => sum + node.circle.writes, 0) < nodeEls.length)
})

test('selecting a node moves only the previous and next active rings', () => {
  const { state, nodeEls, adjacency } = graph()
  state.graphSelectedSlug.value = 'n0'
  state.applyHighlight('n0', adjacency, nodeEls, [])
  for (const node of nodeEls) node.activeRing.style.writes = 0
  state.graphSelectedSlug.value = 'n1'
  state.applyHighlight('n0', adjacency, nodeEls, [])
  const changed = nodeEls.filter(node => node.activeRing.style.writes > 0).map(node => node.node.slug)
  assert.deepEqual(changed.sort(), ['n0', 'n1'])
})


test('layout helper touches only moved nodes and their edges', () => {
  const { state, nodeEls, edgeEls } = graph(20)
  const nodeMap = new Map(nodeEls.map(({ node }) => [node.slug, node]))
  const writer = state.createGraphPositionWriter(nodeEls, edgeEls, nodeMap, state.graphNodeRadius)
  const update = (flush = false) => writer.write(flush)
  for (const node of nodeEls) Object.assign(node.node, { x: 1, y: 1 })
  update(true)
  for (const node of nodeEls) node.g.writes = 0
  for (const edge of edgeEls) edge.line.writes = 0
  update()
  assert.equal(nodeEls.reduce((sum, node) => sum + node.g.writes, 0), 0)
  assert.equal(edgeEls.reduce((sum, edge) => sum + edge.line.writes, 0), 0)
  nodeEls[0]!.node.x = 1.1
  update()
  assert.equal(nodeEls[0]!.g.writes, 0, 'sub-pixel drift should not redraw')
  nodeEls[0]!.node.x = 2
  update()
  const linked = edgeEls.filter(edge => edge.source === 'n0' || edge.target === 'n0')
  const unrelated = edgeEls.filter(edge => edge.source !== 'n0' && edge.target !== 'n0')
  assert.ok(linked.length > 0)
  assert.ok(linked.every(edge => edge.line.writes > 0))
  assert.ok(unrelated.every(edge => edge.line.writes === 0))
})
