import assert from 'node:assert/strict'
import test from 'node:test'
import {
  selectGraphAnchors, buildGraphLayout, graphNodeRadius, stepGraphLayout, visibleGraphLabels, initialGraphViewport, graphPointBounds, graphClusterOutline,
  type ForceNode, type GraphLink, type LayoutInput,
} from './wikiGraphLayout.ts'

function simulate(inputs: LayoutInput[], edges: GraphLink[], prior = new Map<string, { x: number; y: number }>()) {
  const layout = buildGraphLayout(inputs, edges, 1200, 800, prior)
  const nodes: ForceNode[] = inputs.map(n => ({
    slug: n.slug, linkCount: n.link_count, ...layout.placements.get(n.slug)!,
    vx: 0, vy: 0, pinned: false,
  }))
  const retained = new Set(prior.keys())
  for (let alpha = 1; alpha >= 0.02; alpha *= 0.985) stepGraphLayout(nodes, edges, layout.placements, alpha, retained)
  return { nodes, layout }
}

test('disconnected groups stay apart and isolated pages distinguish missing visible links', () => {
  const inputs = [
    { slug: 'a', link_count: 1 }, { slug: 'b', link_count: 1 },
    { slug: 'c', link_count: 1 }, { slug: 'd', link_count: 1 },
    { slug: 'orphan', link_count: 0 }, { slug: 'outside', link_count: 3 },
  ]
  const edges = [{ source: 'a', target: 'b' }, { source: 'd', target: 'c' }, { source: 'outside', target: 'not-loaded' }]
  const { nodes, layout } = simulate(inputs, edges)
  assert.deepEqual(layout.sections.map(s => [s.kind, s.count]), [['offscreen', 1], ['unlinked', 1]])
  const a = nodes.find(n => n.slug === 'a')!, b = nodes.find(n => n.slug === 'b')!
  const c = nodes.find(n => n.slug === 'c')!, d = nodes.find(n => n.slug === 'd')!
  const within = Math.max(Math.hypot(a.x - b.x, a.y - b.y), Math.hypot(c.x - d.x, c.y - d.y))
  for (const first of [a, b]) for (const second of [c, d]) {
    assert.ok(Math.hypot(first.x - second.x, first.y - second.y) > within)
  }
  for (const section of layout.sections) {
    const node = nodes.find(n => n.slug === (section.kind === 'unlinked' ? 'orphan' : 'outside'))!
    assert.ok(node.x > section.left && node.x < section.right && node.y > section.top && node.y < section.bottom)
  }
})

test('initial positions are stable across API ordering and reverse links', () => {
  const inputs = Array.from({ length: 20 }, (_, i) => ({ slug: `p${i}`, link_count: 2 }))
  const edges = inputs.slice(1).map(n => ({ source: 'p0', target: n.slug }))
  const a = buildGraphLayout(inputs, edges, 900, 600)
  const b = buildGraphLayout([...inputs].reverse(), [...edges].reverse().map(e => ({ source: e.target, target: e.source })), 900, 600)
  for (const n of inputs) assert.deepEqual(a.placements.get(n.slug), b.placements.get(n.slug))
})

test('many small components pack without overlap or forming rows', t => {
  const inputs = Array.from({ length: 500 }, (_, i) => ({ slug: `pair${i}`, link_count: 1 }))
  const edges = inputs.flatMap((n, i) => i % 2 ? [{ source: `pair${i - 1}`, target: n.slug }] : [])
  const start = performance.now()
  const { clusters } = buildGraphLayout(inputs, edges, 1400, 900)
  t.diagnostic(`250 components packed in ${Math.round(performance.now() - start)} ms`)
  assert.equal(clusters.length, 250)
  assert.ok(new Set(clusters.map(c => Math.round(c.y))).size > 100)
  for (let i = 0; i < clusters.length; i++) for (let j = i + 1; j < clusters.length; j++) {
    const a = clusters[i]!, b = clusters[j]!
    assert.ok(a.right <= b.left || a.left >= b.right || a.bottom <= b.top || a.top >= b.bottom)
  }
})

test('empty and entirely unlinked graphs have finite, nonoverlapping positions', () => {
  assert.equal(buildGraphLayout([], [], 800, 600).placements.size, 0)
  const inputs = Array.from({ length: 500 }, (_, i) => ({ slug: `orphan${i}`, link_count: 0 }))
  const { nodes, layout } = simulate(inputs, [])
  assert.deepEqual(layout.sections.map(s => [s.kind, s.count]), [['unlinked', 500]])
  for (let i = 0; i < nodes.length; i++) for (let j = i + 1; j < nodes.length; j++) {
    assert.ok(Math.hypot(nodes[i]!.x - nodes[j]!.x, nodes[i]!.y - nodes[j]!.y) > 50)
  }
})

test('500 and 1500 node graphs settle with finite positions and node clearance', t => {
  for (const count of [500, 1500]) {
    const inputs = Array.from({ length: count }, (_, i) => ({ slug: `p${i}`, link_count: i % 50 === 0 ? 49 : 1 }))
    const edges = inputs.flatMap((n, i) => i % 50 ? [{ source: `p${i - i % 50}`, target: n.slug }] : [])
    const start = performance.now()
    const { nodes } = simulate(inputs, edges)
    t.diagnostic(`${count} nodes settled in ${Math.round(performance.now() - start)} ms (pure layout, no DOM)`)
    for (const n of nodes) {
      assert.ok(Number.isFinite(n.x) && Number.isFinite(n.y))
    }
    for (let i = 0; i < nodes.length; i++) for (let j = i + 1; j < nodes.length; j++) {
      const a = nodes[i]!, b = nodes[j]!
      assert.ok(Math.hypot(a.x - b.x, a.y - b.y) >= graphNodeRadius(a.linkCount) + graphNodeRadius(b.linkCount) + 15,
        `${count}: ${a.slug} overlaps ${b.slug}`)
    }
  }
})

test('incremental expansion holds existing nodes and settles new neighbors', () => {
  const prior = new Map([['a', { x: 111, y: 222 }], ['b', { x: 260, y: 222 }]])
  const inputs = Array.from({ length: 30 }, (_, i) => ({ slug: i === 0 ? 'a' : i === 1 ? 'b' : `new${i}`, link_count: 1 }))
  const edges = inputs.slice(1).map(n => ({ source: 'a', target: n.slug }))
  const { nodes } = simulate(inputs, edges, prior)
  for (const [slug, point] of prior) {
    const n = nodes.find(n => n.slug === slug)!
    assert.deepEqual({ x: n.x, y: n.y }, point)
  }
  for (const n of nodes.slice(2)) {
    assert.ok(Number.isFinite(n.x) && Number.isFinite(n.y))
    assert.ok(Math.hypot(n.x - 111, n.y - 222) > 32)
  }
})

test('a large hub with 1499 neighbors settles with node clearance', t => {
  const inputs = Array.from({ length: 1500 }, (_, i) => ({ slug: `hub${i}`, link_count: i ? 1 : 1499 }))
  const edges = inputs.slice(1).map(n => ({ source: 'hub0', target: n.slug }))
  const start = performance.now()
  const { nodes } = simulate(inputs, edges)
  t.diagnostic(`1500 nodes in one component settled in ${Math.round(performance.now() - start)} ms (no DOM)`)
  for (let i = 0; i < nodes.length; i++) for (let j = i + 1; j < nodes.length; j++) {
    const a = nodes[i]!, b = nodes[j]!
    assert.ok(Math.hypot(a.x - b.x, a.y - b.y) >= graphNodeRadius(a.linkCount) + graphNodeRadius(b.linkCount) + 14,
      `${a.slug} overlaps ${b.slug}`)
  }
})

test('collision separates coincident nodes while respecting a dragged pin', () => {
  const inputs = [{ slug: 'a', link_count: 10 }, { slug: 'b', link_count: 10 }]
  const layout = buildGraphLayout(inputs, [{ source: 'a', target: 'b' }], 800, 600)
  const nodes: ForceNode[] = inputs.map(n => ({ slug: n.slug, linkCount: n.link_count, x: 400, y: 300, vx: 0, vy: 0, pinned: n.slug === 'a' }))
  for (let i = 0; i < 10; i++) stepGraphLayout(nodes, [], layout.placements, 0.02)
  assert.equal(nodes[0]!.x, 400)
  assert.equal(nodes[0]!.y, 300)
  assert.ok(Math.hypot(nodes[0]!.x - nodes[1]!.x, nodes[0]!.y - nodes[1]!.y) >= graphNodeRadius(10) * 2 + 15)
})

test('overlapping labels favor important nodes, but focused labels always stay readable', () => {
  const box = { left: 0, right: 100, top: 0, bottom: 20 }
  const labels = [
    { ...box, slug: 'low', priority: 1 }, { ...box, slug: 'high', priority: 20 },
    { ...box, left: 200, right: 300, slug: 'separate', priority: 0 },
  ]
  assert.deepEqual([...visibleGraphLabels(labels)], ['high', 'separate'])
  assert.deepEqual([...visibleGraphLabels([...labels, { ...box, slug: 'selected', priority: 0, required: true }])], ['selected', 'separate'])
})


test('long chains keep their elongated topology and reserve space away from other groups', () => {
  const inputs = Array.from({ length: 40 }, (_, i) => ({ slug: `chain${String(i).padStart(2, '0')}`, link_count: 2 }))
  const edges = inputs.flatMap((n, i) => i % 20 ? [{ source: inputs[i - 1]!.slug, target: n.slug }] : [])
  const { nodes, layout } = simulate(inputs, edges)
  for (const cluster of layout.clusters) {
    const members = nodes.filter(n => cluster.slugs.includes(n.slug))
    const bounds = graphPointBounds(members)
    const mean = { x: members.reduce((s, n) => s + n.x, 0) / members.length, y: members.reduce((s, n) => s + n.y, 0) / members.length }
    const xx = members.reduce((s, n) => s + (n.x - mean.x) ** 2, 0)
    const yy = members.reduce((s, n) => s + (n.y - mean.y) ** 2, 0)
    const xy = members.reduce((s, n) => s + (n.x - mean.x) * (n.y - mean.y), 0)
    const delta = Math.hypot(xx - yy, 2 * xy)
    // Principal axes make this invariant to the chain's heading.
    assert.ok((xx + yy + delta) / Math.max(1, xx + yy - delta) > 9)
    assert.ok(Math.hypot(bounds.right - bounds.left, bounds.bottom - bounds.top) > 1500)
  }
  const first = nodes.slice(0, 20), second = nodes.slice(20)
  for (const a of first) for (const b of second) assert.ok(Math.hypot(a.x - b.x, a.y - b.y) > 250)
})

test('large canvases start readable around the focus instead of fitting every node', () => {
  const points = [{ x: -4000, y: -2000 }, { x: 5000, y: 3000 }]
  const focus = { x: 300, y: 400 }
  const view = initialGraphViewport(points, 1200, 800, focus)
  assert.equal(view.scale, 0.4)
  assert.equal(focus.x * view.scale + view.x, 600)
  assert.equal(focus.y * view.scale + view.y, 400)
  assert.ok(points[1]!.x * view.scale + view.x > 1200)
  assert.equal(initialGraphViewport([{ x: 0, y: 0 }, { x: 100, y: 100 }], 1200, 800).scale, 0.85)
  assert.ok(Object.values(initialGraphViewport([], 1200, 800)).every(Number.isFinite))
})


test('group outlines follow displaced nodes and handle empty or coincident input', () => {
  assert.equal(graphClusterOutline([]), '')
  for (const points of [[{ x: 0, y: 0 }], [{ x: 0, y: 0 }, { x: 0, y: 0 }], [{ x: 0, y: 0 }, { x: 200, y: 10 }, { x: 800, y: 400 }]]) {
    const path = graphClusterOutline(points)
    assert.ok(path.startsWith('M') && path.endsWith('Z'))
    assert.ok(!path.includes('NaN') && !path.includes('Infinity'))
    assert.equal(graphClusterOutline([...points].reverse()), path)
  }
  assert.notEqual(graphClusterOutline([{ x: 0, y: 0 }, { x: 200, y: 0 }]), graphClusterOutline([{ x: 0, y: 0 }, { x: 400, y: 0 }]))
})


test('landmarks cover separate dense neighborhoods instead of only the largest one', () => {
  const sizes = [40, 30, 20]
  const slugs = sizes.flatMap((size, group) => Array.from({ length: size }, (_, i) => `region${group}-${i}`))
  const adjacency = new Map(slugs.map(slug => [slug, new Set<string>()]))
  const link = (a: string, b: string) => { adjacency.get(a)!.add(b); adjacency.get(b)!.add(a) }
  sizes.forEach((size, group) => {
    for (let i = 0; i < size; i++) for (let j = i + 1; j < size; j++) link(`region${group}-${i}`, `region${group}-${j}`)
  })
  link('region0-0', 'region1-0'); link('region1-0', 'region2-0')
  const anchors = selectGraphAnchors(slugs, adjacency)
  assert.equal(anchors.length, 3)
  assert.equal(new Set(anchors.map(slug => slug.split('-')[0])).size, 3)
  const reversed = new Map([...adjacency].reverse().map(([slug, neighbors]) => [slug, new Set([...neighbors].reverse())]))
  assert.deepEqual(selectGraphAnchors([...slugs].reverse(), reversed), anchors)
})

test('one hub does not create artificial extra themes and external neighbors do not affect landmarks', () => {
  const slugs = Array.from({ length: 200 }, (_, i) => `page${i}`)
  const adjacency = new Map(slugs.map(slug => [slug, new Set<string>()]))
  for (const slug of slugs.slice(1)) { adjacency.get('page0')!.add(slug); adjacency.get(slug)!.add('page0') }
  adjacency.get('page199')!.add('outside-the-component')
  assert.deepEqual(selectGraphAnchors(slugs, adjacency), ['page0'])
  assert.deepEqual(selectGraphAnchors([], adjacency), [])
})

test('landmark budgets scale from small to large components with a maximum of five', () => {
  for (const [size, expected] of [[10, 1], [30, 2], [75, 3], [150, 4], [500, 5]]) {
    const slugs = Array.from({ length: size! }, (_, i) => `p${i}`)
    const adjacency = new Map(slugs.map((slug, i) => [slug, new Set([slugs[i - 1], slugs[i + 1]].filter((s): s is string => !!s))]))
    const anchors = selectGraphAnchors(slugs, adjacency)
    assert.equal(anchors.length, expected)
    assert.equal(new Set(anchors).size, expected)
    if (size! >= 30) {
      const indices = anchors.map(slug => Number(slug.slice(1)))
      assert.ok(Math.max(...indices) - Math.min(...indices) > size! * 0.7)
    }
    const inputs = slugs.map(slug => ({ slug, link_count: adjacency.get(slug)!.size }))
    const edges = slugs.slice(1).map((slug, i) => ({ source: slugs[i]!, target: slug }))
    assert.deepEqual(buildGraphLayout(inputs, edges, 1200, 800).clusters[0]!.anchors, anchors)
  }
})

test('the main subject leads, and a separate configuration theme is named instead of a second name for the same theme', () => {
  const names = ['platform', 'enterprise', 'configuration',
    ...Array.from({ length: 12 }, (_, i) => `shared${i}`),
    ...Array.from({ length: 10 }, (_, i) => `platformDetail${i}`),
    ...Array.from({ length: 4 }, (_, i) => `enterpriseDetail${i}`),
    ...Array.from({ length: 6 }, (_, i) => `configurationDetail${i}`)]
  const adjacency = new Map(names.map(slug => [slug, new Set<string>()]))
  const edges: GraphLink[] = []
  const link = (a: string, b: string) => { adjacency.get(a)!.add(b); adjacency.get(b)!.add(a); edges.push({ source: a, target: b }) }
  for (let i = 0; i < 12; i++) { link('platform', `shared${i}`); link('enterprise', `shared${i}`) }
  for (let i = 0; i < 10; i++) link('platform', `platformDetail${i}`)
  for (let i = 0; i < 4; i++) link('enterprise', `enterpriseDetail${i}`)
  for (let i = 0; i < 6; i++) link('configuration', `configurationDetail${i}`)
  for (let i = 0; i < 3; i++) link('configuration', `shared${i}`)
  link('platform', 'enterprise'); link('enterprise', 'configuration')
  const nodes = names.map(slug => ({ slug, link_count: adjacency.get(slug)!.size,
    page_type: ['platform', 'enterprise'].includes(slug) ? 'entity' : 'concept' }))
  const layout = buildGraphLayout(nodes, edges, 1200, 800)
  // enterprise shares twelve neighbors with platform: one theme, one name.
  assert.deepEqual(layout.clusters[0]!.anchors, ['platform', 'configuration'])
  assert.deepEqual(buildGraphLayout([...nodes].reverse(), [...edges].reverse(), 1200, 800).clusters[0]!.anchors, layout.clusters[0]!.anchors)
})

test('a subject linked to most pages of every theme names the component alone', () => {
  const slugs = ['hub', ...Array.from({ length: 3 }, (_, g) => Array.from({ length: 10 }, (_, i) => `t${g}-${i}`)).flat()]
  const adjacency = new Map(slugs.map(slug => [slug, new Set<string>()]))
  const link = (a: string, b: string) => { adjacency.get(a)!.add(b); adjacency.get(b)!.add(a) }
  for (let g = 0; g < 3; g++) {
    for (let i = 0; i < 10; i++) for (let j = i + 1; j < 10; j++) link(`t${g}-${i}`, `t${g}-${j}`)
    // One document: its subject links to nearly every page in each section.
    for (let i = 0; i < 8; i++) link('hub', `t${g}-${i}`)
  }
  assert.deepEqual(selectGraphAnchors(slugs, adjacency), ['hub'])
})

test('a theme is not named again by a page whose title repeats an existing label', () => {
  const sizes = [30, 20]
  const slugs = sizes.flatMap((size, g) => Array.from({ length: size }, (_, i) => `r${g}-${i}`))
  const adjacency = new Map(slugs.map(slug => [slug, new Set<string>()]))
  const link = (a: string, b: string) => { adjacency.get(a)!.add(b); adjacency.get(b)!.add(a) }
  sizes.forEach((size, g) => {
    for (let i = 0; i < size; i++) for (let j = i + 1; j < size; j++) link(`r${g}-${i}`, `r${g}-${j}`)
  })
  link('r0-0', 'r1-0')
  const pages = new Map([
    ['r0-0', { page_type: 'concept', title: '第三代社保卡' }],
    ['r1-0', { page_type: 'entity', title: '北京市第三代社保卡' }],
    ['r1-1', { page_type: 'concept', title: '线上申领' }],
  ])
  assert.deepEqual(selectGraphAnchors(slugs, adjacency, pages), ['r0-0', 'r1-1'])
})

test('a summary names the document it dominates, but a nearly as central entity still wins', () => {
  const pages = new Map([
    ['summary', { page_type: 'summary', title: '初中63组语文古诗词高频考点(2).pdf - Summary' }],
    ['subject', { page_type: 'entity', title: '范仲淹' }],
  ])
  const anchorsWhenSubjectLinks = (details: number) => {
    const slugs = ['summary', 'subject', ...Array.from({ length: 10 }, (_, i) => `detail${i}`)]
    const adjacency = new Map(slugs.map(slug => [slug, new Set<string>()]))
    const link = (a: string, b: string) => { adjacency.get(a)!.add(b); adjacency.get(b)!.add(a) }
    for (const slug of slugs.slice(1)) link('summary', slug)
    for (let i = 0; i < details; i++) link('subject', `detail${i}`)
    return selectGraphAnchors(slugs, adjacency, pages)
  }
  assert.deepEqual(anchorsWhenSubjectLinks(5), ['summary'])
  assert.deepEqual(anchorsWhenSubjectLinks(9), ['subject'])
})

test('a component of several documents also names the page they share', () => {
  const anchorsWithBridgeDetails = (details: number) => {
    const slugs: string[] = ['bridge']
    const adjacency = new Map<string, Set<string>>([['bridge', new Set()]])
    const link = (a: string, b: string) => {
      for (const s of [a, b]) if (!adjacency.has(s)) { adjacency.set(s, new Set()); slugs.push(s) }
      adjacency.get(a)!.add(b); adjacency.get(b)!.add(a)
    }
    const pages = new Map<string, { page_type: string; title: string }>([['bridge', { page_type: 'concept', title: '强化学习' }]])
    for (let d = 0; d < 3; d++) {
      pages.set(`doc${d}`, { page_type: 'summary', title: `论文${d}.pdf - Summary` })
      for (let i = 0; i < 8; i++) {
        link(`doc${d}`, `d${d}-${i}`)
        if (i > 0) link(`d${d}-${i}`, `d${d}-${i - 1}`)
      }
      link('bridge', `doc${d}`)
      for (let i = 0; i < details; i++) link('bridge', `d${d}-${i}`)
    }
    return selectGraphAnchors(slugs, adjacency, pages)
  }
  const named = anchorsWithBridgeDetails(1)
  assert.equal(named.at(-1), 'bridge')
  assert.ok(named.slice(0, -1).every(slug => slug.startsWith('doc')), named.join())
  // Shared by every document but barely linked: a detail, not what ties them.
  assert.ok(!anchorsWithBridgeDetails(0).includes('bridge'))
})

test('an identifier-like file name does not name a theme over a readable page', () => {
  const slugs = ['summary', 'subject', ...Array.from({ length: 10 }, (_, i) => `detail${i}`)]
  const adjacency = new Map(slugs.map(slug => [slug, new Set<string>()]))
  const link = (a: string, b: string) => { adjacency.get(a)!.add(b); adjacency.get(b)!.add(a) }
  for (const slug of slugs.slice(1)) link('summary', slug)
  for (let i = 0; i < 5; i++) link('subject', `detail${i}`)
  const pages = new Map([
    ['summary', { page_type: 'summary', title: '2412.18925v1.pdf - Summary' }],
    ['subject', { page_type: 'entity', title: '香港中文大学（深圳）' }],
  ])
  assert.deepEqual(selectGraphAnchors(slugs, adjacency, pages), ['subject'])
})

test('entity preference breaks a close ranking without displacing a substantially stronger concept', () => {
  const inputs = Array.from({ length: 12 }, (_, i) => ({ slug: `page${i}`, link_count: 0, page_type: i === 1 ? 'entity' : 'concept' }))
  const edges = inputs.slice(1).map(n => ({ source: 'page0', target: n.slug }))
  assert.deepEqual(buildGraphLayout(inputs, edges, 1200, 800).clusters[0]!.anchors, ['page0'])
  // Two equally connected pages: the entity is the more useful overview anchor.
  const pair = [{ slug: 'a-concept', link_count: 1, page_type: 'concept' }, { slug: 'z-entity', link_count: 1, page_type: 'entity' }]
  assert.deepEqual(buildGraphLayout(pair, [{ source: pair[0]!.slug, target: pair[1]!.slug }], 1200, 800).clusters[0]!.anchors, ['z-entity'])
})
