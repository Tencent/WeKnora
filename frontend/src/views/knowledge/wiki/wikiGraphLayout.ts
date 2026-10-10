// Geometry is independent of SVG so dense and disconnected graphs can be
// exercised without a browser. Coordinates are world units, not screen pixels.
export interface GraphPoint { x: number; y: number }
export interface GraphLink { source: string; target: string }
export interface LayoutInput { slug: string; link_count: number; page_type?: string }
export interface GraphBounds { left: number; top: number; right: number; bottom: number }
export interface LayoutPlacement extends GraphPoint {
  center: GraphPoint
  stationary?: boolean
  degree?: number
}
export interface GraphCluster extends GraphPoint, GraphBounds {
  slugs: string[]
  representative: string
  anchors: string[]
}
export interface GraphSection extends GraphBounds {
  kind: 'unlinked' | 'offscreen'
  count: number
}

export function graphNodeRadius(linkCount: number): number {
  return Math.max(8, Math.min(24, 8 + Math.log(Math.max(0, linkCount) + 1) * 4))
}

/** Pick structural landmarks from different neighborhoods, not semantic titles.
 * A single hub or clique may need fewer labels than the size budget allows.
 */
export function selectGraphAnchors(
  slugs: string[], adjacency: ReadonlyMap<string, ReadonlySet<string>>,
  pages: ReadonlyMap<string, Pick<LayoutInput, 'page_type'>> = new Map(),
): string[] {
  if (!slugs.length) return []
  const members = new Set(slugs)
  const neighbors = new Map(slugs.map(slug => [slug, [...(adjacency.get(slug) ?? [])].filter(n => members.has(n) && n !== slug)]))
  const ranked = [...slugs].sort((a, b) => neighbors.get(b)!.length - neighbors.get(a)!.length || (a < b ? -1 : a > b ? 1 : 0))
  const budget = slugs.length < 16 ? 1 : slugs.length < 50 ? 2 : slugs.length < 100 ? 3 : slugs.length < 200 ? 4 : 5
  const selected: string[] = []
  const covered = new Set<string>()
  const nearest = new Map(slugs.map(slug => [slug, Infinity]))
  while (selected.length < budget) {
    let best: string | undefined, bestScore = -1, bestDistance = -1
    for (const slug of ranked) {
      if (selected.includes(slug)) continue
      const neighborhood = [slug, ...neighbors.get(slug)!]
      const gain = neighborhood.filter(n => !covered.has(n)).length
      if (!gain) continue
      // Overall prominence matters even when this subject shares neighbors
      // with an earlier landmark. Marginal coverage alone favors peripheral
      // configuration pages over a well-connected product/platform.
      const novelty = gain / neighborhood.length
      const separation = nearest.get(slug)!
      const distance = Math.min(3, separation)
      const entityPreference = pages.get(slug)?.page_type === 'entity' ? 1.15 : 1
      const score = neighborhood.length * (0.4 + 0.6 * Math.sqrt(novelty))
        * (1 + 0.15 * distance) * entityPreference
      if (score > bestScore || (score === bestScore && separation > bestDistance)) {
        best = slug; bestScore = score; bestDistance = separation
      }
    }
    if (!best) break
    selected.push(best)
    for (const slug of [best, ...neighbors.get(best)!]) covered.add(slug)
    // Distances to the nearest selected landmark discourage picking all
    // labels from the same dense part of the connected component.
    const queue = [best]
    const distances = new Map([[best, 0]])
    for (let i = 0; i < queue.length; i++) {
      const slug = queue[i]!, distance = distances.get(slug)!
      nearest.set(slug, Math.min(nearest.get(slug)!, distance))
      for (const neighbor of neighbors.get(slug)!) {
        if (distances.has(neighbor)) continue
        distances.set(neighbor, distance + 1); queue.push(neighbor)
      }
    }
  }
  return selected
}

/** Pack connected components separately; singletons get two explicit shelves.
 * A missing visible edge is not evidence that the page has no links in the KB.
 */
export function buildGraphLayout(
  nodes: LayoutInput[], edges: GraphLink[], width: number, height: number,
  prior: ReadonlyMap<string, GraphPoint> = new Map(),
) {
  const nodeBySlug = new Map(nodes.map(n => [n.slug, n]))
  const adjacency = new Map(nodes.map(n => [n.slug, new Set<string>()]))
  for (const e of edges) {
    if (e.source === e.target || !nodeBySlug.has(e.source) || !nodeBySlug.has(e.target)) continue
    adjacency.get(e.source)!.add(e.target)
    adjacency.get(e.target)!.add(e.source)
  }
  const components: string[][] = []
  const unlinked: string[] = []
  const offscreen: string[] = []
  const visited = new Set<string>()
  for (const slug of [...nodeBySlug.keys()].sort()) {
    if (visited.has(slug)) continue
    visited.add(slug)
    if (!adjacency.get(slug)!.size) {
      ;(nodeBySlug.get(slug)!.link_count > 0 ? offscreen : unlinked).push(slug)
      continue
    }
    const component = [slug]
    for (let i = 0; i < component.length; i++) {
      for (const neighbor of adjacency.get(component[i]!)!) {
        if (visited.has(neighbor)) continue
        visited.add(neighbor)
        component.push(neighbor)
      }
    }
    components.push(component.sort())
  }
  components.sort((a, b) => b.length - a.length || (a[0]! < b[0]! ? -1 : 1))

  // Reserve space from each component's topology, not a circular boundary.
  // These envelopes only place groups initially; the simulation never clips
  // nodes to them or pulls individual nodes towards a common center.
  const aspect = Math.min(1.8, Math.max(0.8, width / Math.max(height, 1)))
  const stretch = Math.sqrt(aspect)
  const goldenAngle = Math.PI * (3 - Math.sqrt(5))
  const clusters: GraphCluster[] = []
  const seeds = new Map<string, GraphPoint>()
  const cellSize = 500
  const cells = new Map<string, GraphCluster[]>()
  const keysFor = (bounds: GraphBounds) => {
    const keys: string[] = []
    for (let gx = Math.floor(bounds.left / cellSize); gx <= Math.floor(bounds.right / cellSize); gx++) {
      for (let gy = Math.floor(bounds.top / cellSize); gy <= Math.floor(bounds.bottom / cellSize); gy++) keys.push(`${gx},${gy}`)
    }
    return keys
  }
  for (const slugs of components) {
    const representative = [...slugs].sort((a, b) => adjacency.get(b)!.size - adjacency.get(a)!.size || (a < b ? -1 : 1))[0]!
    // Seed a spanning tree from the best-connected page. Branches follow
    // their parent direction, so chains and trees are not born as disks.
    const local = new Map<string, GraphPoint & { heading: number }>()
    local.set(representative, { x: 0, y: 0, heading: 0 })
    const queue = [representative]
    for (let cursor = 0; cursor < queue.length; cursor++) {
      const parentSlug = queue[cursor]!
      const parent = local.get(parentSlug)!
      const children = [...adjacency.get(parentSlug)!].filter(slug => !local.has(slug)).sort()
      children.forEach((slug, i) => {
        const heading = parent.heading + (cursor === 0 ? i * goldenAngle : ((i + 0.5) / children.length - 0.5) * Math.PI * 0.8)
        const distance = 150 + (children.length > 6 ? Math.sqrt(i) * 30 : (i % 3) * 12)
        local.set(slug, { x: parent.x + Math.cos(heading) * distance, y: parent.y + Math.sin(heading) * distance, heading })
        queue.push(slug)
      })
    }
    const mean = { x: 0, y: 0 }
    for (const point of local.values()) { mean.x += point.x / local.size; mean.y += point.y / local.size }
    for (const [slug, point] of local) seeds.set(slug, { x: point.x - mean.x, y: point.y - mean.y })
    const extent = graphPointBounds(slugs.map(slug => seeds.get(slug)!))
    // Room for repulsion and the caption; this is spacing, not a force limit.
    const padding = 160
    let x = 0, y = 0, bounds: GraphBounds
    for (let attempt = 0; ; attempt++) {
      const distance = Math.sqrt(attempt) * 48
      x = Math.cos(attempt * goldenAngle) * distance * stretch
      y = Math.sin(attempt * goldenAngle) * distance / stretch
      bounds = { left: x + extent.left - padding, right: x + extent.right + padding,
        top: y + extent.top - padding, bottom: y + extent.bottom + padding }
      const neighbors = new Set(keysFor(bounds).flatMap(key => cells.get(key) ?? []))
      if ([...neighbors].every(c => bounds.right <= c.left || bounds.left >= c.right || bounds.bottom <= c.top || bounds.top >= c.bottom)) break
    }
    const cluster = { x, y, ...bounds, slugs, representative, anchors: selectGraphAnchors(slugs, adjacency, nodeBySlug) }
    clusters.push(cluster)
    for (const key of keysFor(bounds)) {
      const bucket = cells.get(key) ?? []
      bucket.push(cluster); cells.set(key, bucket)
    }
  }
  const left = Math.min(0, ...clusters.map(c => c.left))
  const top = Math.min(0, ...clusters.map(c => c.top))
  const placements = new Map<string, LayoutPlacement>()
  const sections: GraphSection[] = []
  let right = 0, bottom = 0
  for (const cluster of clusters) {
    cluster.x -= left; cluster.y -= top
    cluster.left -= left; cluster.right -= left; cluster.top -= top; cluster.bottom -= top
    const center = { x: cluster.x, y: cluster.y }
    for (const slug of cluster.slugs) {
      const point = seeds.get(slug)!
      placements.set(slug, { x: center.x + point.x, y: center.y + point.y,
        center, degree: adjacency.get(slug)!.size })
    }
    right = Math.max(right, cluster.right)
    bottom = Math.max(bottom, cluster.bottom)
  }
  const gap = 80
  // Independent pages stay on a readable grid rather than accumulating at a
  // shared force center. The shelves are not semantic clusters.
  for (const [kind, slugs] of [['offscreen', offscreen], ['unlinked', unlinked]] as const) {
    if (!slugs.length) continue
    const shelfWidth = right || Math.sqrt(slugs.length * 180 * 100 * aspect)
    const columns = Math.min(slugs.length, Math.max(3, Math.floor(shelfWidth / 180)))
    const top = bottom ? bottom + gap : 0
    const sectionWidth = columns * 180
    const sectionHeight = Math.ceil(slugs.length / columns) * 100 + 50
    const left = Math.max(0, (right - sectionWidth) / 2)
    slugs.forEach((slug, i) => {
      const point = { x: left + (i % columns) * 180 + 90, y: top + 70 + Math.floor(i / columns) * 100 }
      placements.set(slug, { ...point, center: point, stationary: true })
    })
    sections.push({ kind, count: slugs.length, left, top, right: left + sectionWidth, bottom: top + sectionHeight })
    bottom = top + sectionHeight
    right = Math.max(right, left + sectionWidth)
  }

  const dx = (width - right) / 2, dy = (height - bottom) / 2
  for (const placement of placements.values()) {
    placement.x += dx; placement.y += dy
    // Centers are shared by a component; translate them only once below.
  }
  for (const center of new Set([...placements.values()].map(p => p.center))) { center.x += dx; center.y += dy }
  for (const cluster of clusters) {
    cluster.x += dx; cluster.y += dy
    cluster.left += dx; cluster.right += dx; cluster.top += dy; cluster.bottom += dy
  }
  for (const section of sections) {
    section.left += dx; section.right += dx; section.top += dy; section.bottom += dy
  }

  // During expansion, keep the existing mental map. New nodes start near an
  // already visible neighbor; old nodes are held still by the simulation.
  if (prior.size) {
    for (const slugs of components) {
      const existing = slugs.flatMap(slug => prior.has(slug) ? [prior.get(slug)!] : [])
      if (!existing.length) continue
      const center = { x: existing.reduce((sum, p) => sum + p.x, 0) / existing.length, y: existing.reduce((sum, p) => sum + p.y, 0) / existing.length }
      slugs.forEach((slug, i) => {
        const neighbor = [...adjacency.get(slug)!].find(nb => prior.has(nb))
        const anchor = prior.get(slug) ?? (neighbor ? prior.get(neighbor)! : center)
        const angle = i * Math.PI * (3 - Math.sqrt(5))
        const point = prior.has(slug) ? anchor : { x: anchor.x + Math.cos(angle) * 90, y: anchor.y + Math.sin(angle) * 90 }
        placements.set(slug, { ...point, center, degree: adjacency.get(slug)!.size })
      })
    }
    for (const [slug, point] of prior) {
      const placement = placements.get(slug)
      if (placement) { placement.x = point.x; placement.y = point.y }
    }
  }
  return { placements, sections: prior.size ? [] : sections, clusters: prior.size ? [] : clusters }
}

export interface ForceNode extends GraphPoint {
  slug: string; vx: number; vy: number; linkCount: number; pinned: boolean
}

/** Local repulsion uses a 2D spatial grid; collision includes outer rings. */
export function stepGraphLayout(
  nodes: ForceNode[], edges: GraphLink[], placements: ReadonlyMap<string, LayoutPlacement>,
  alpha: number, retained: ReadonlySet<string> = new Set(),
) {
  const fixed = (n: ForceNode) => n.pinned || retained.has(n.slug) || !!placements.get(n.slug)?.stationary
  if (nodes.every(fixed)) return
  const reach = 300
  const buckets = new Map<string, ForceNode[]>()
  const nodeMap = new Map(nodes.map(n => [n.slug, n]))
  for (const n of nodes) {
    const cx = Math.floor(n.x / reach), cy = Math.floor(n.y / reach)
    for (let gx = cx - 1; gx <= cx + 1; gx++) for (let gy = cy - 1; gy <= cy + 1; gy++) {
      for (const other of buckets.get(`${gx},${gy}`) ?? []) {
        let dx = n.x - other.x, dy = n.y - other.y
        let distance = Math.hypot(dx, dy)
        if (distance > reach) continue
        if (distance < 0.001) { dx = 1; dy = 0; distance = 1 }
        const force = 12000 * alpha / Math.max(distance * distance, 100)
        const fx = dx / distance * force, fy = dy / distance * force
        if (!fixed(n)) { n.vx += fx; n.vy += fy }
        if (!fixed(other)) { other.vx -= fx; other.vy -= fy }
      }
    }
    const key = `${cx},${cy}`
    const bucket = buckets.get(key) ?? []
    bucket.push(n); buckets.set(key, bucket)
  }
  // A reciprocal Wiki link is one visual spring, not double attraction.
  const pairs = new Set<string>()
  for (const edge of edges) {
    const s = nodeMap.get(edge.source), t = nodeMap.get(edge.target)
    if (!s || !t || s === t) continue
    const key = JSON.stringify([s.slug, t.slug].sort())
    if (pairs.has(key)) continue
    pairs.add(key)
    const dx = t.x - s.x, dy = t.y - s.y, distance = Math.hypot(dx, dy) || 1
    // Dense pages should not pull a whole group into a solid ball. Normalize
    // attraction by the visible degrees rather than the KB-wide link count.
    const degree = Math.min(placements.get(s.slug)?.degree ?? 1, placements.get(t.slug)?.degree ?? 1)
    const force = (distance - 140) * 0.003 * alpha / Math.sqrt(Math.max(1, degree))
    if (!fixed(s)) { s.vx += dx / distance * force; s.vy += dy / distance * force }
    if (!fixed(t)) { t.vx -= dx / distance * force; t.vy -= dy / distance * force }
  }
  for (const n of nodes) {
    if (fixed(n)) continue
    n.vx *= 0.6
    n.vy *= 0.6
    const speed = Math.hypot(n.vx, n.vy)
    if (speed > 20) { n.vx *= 20 / speed; n.vy *= 20 / speed }
    n.x += n.vx; n.y += n.vy
  }
  // Resolve overlaps after integration, even when the force has cooled. Use
  // X pruning here since the collision radius is small (at most 64 units).
  for (let pass = 0; pass < 3; pass++) {
    const sorted = [...nodes].sort((a, b) => a.x - b.x)
    for (let i = 0; i < sorted.length; i++) {
      const a = sorted[i]!
      for (let j = i + 1; j < sorted.length; j++) {
        const b = sorted[j]!
        if (b.x - a.x > 64) break
        let dx = b.x - a.x, dy = b.y - a.y
        let distance = Math.hypot(dx, dy)
        const minimum = graphNodeRadius(a.linkCount) + graphNodeRadius(b.linkCount) + 16
        if (distance >= minimum || (fixed(a) && fixed(b))) continue
        if (distance < 0.001) { dx = 1; dy = 0; distance = 1 }
        const shift = (minimum - distance) / (fixed(a) || fixed(b) ? 1 : 2)
        if (!fixed(a)) { a.x -= dx / distance * shift; a.y -= dy / distance * shift }
        if (!fixed(b)) { b.x += dx / distance * shift; b.y += dy / distance * shift }
      }
    }
  }
  // Correct only whole-component drift. The same translation is applied to
  // every member; no node is attracted to a shared center or clipped to a
  // circle. Pinned/retained components keep their user's coordinates intact.
  const groups = new Map<GraphPoint, ForceNode[]>()
  for (const node of nodes) {
    const center = placements.get(node.slug)?.center
    if (!center) continue
    const group = groups.get(center) ?? []
    group.push(node); groups.set(center, group)
  }
  for (const [center, group] of groups) {
    if (group.some(fixed)) continue
    const dx = (center.x - group.reduce((sum, n) => sum + n.x, 0) / group.length) * 0.03 * alpha
    const dy = (center.y - group.reduce((sum, n) => sum + n.y, 0) / group.length) * 0.03 * alpha
    for (const n of group) { n.x += dx; n.y += dy }
  }
}

export interface GraphLabel extends GraphBounds { slug: string; priority: number; required?: boolean }
/** Greedy screen-space label placement. Focused labels are never suppressed. */
export function visibleGraphLabels(labels: GraphLabel[]): Set<string> {
  const visible = new Set<string>()
  const occupied = new Map<string, GraphLabel[]>()
  const cells = (label: GraphLabel) => {
    const keys: string[] = []
    for (let x = Math.floor(label.left / 160); x <= Math.floor(label.right / 160); x++) {
      for (let y = Math.floor(label.top / 32); y <= Math.floor(label.bottom / 32); y++) keys.push(`${x},${y}`)
    }
    return keys
  }
  for (const label of [...labels].sort((a, b) => Number(!!b.required) - Number(!!a.required) || b.priority - a.priority)) {
    const keys = cells(label)
    const overlaps = keys.some(key => (occupied.get(key) ?? []).some(other => label.left < other.right && label.right > other.left && label.top < other.bottom && label.bottom > other.top))
    if (overlaps && !label.required) continue
    visible.add(label.slug)
    for (const key of keys) {
      const bucket = occupied.get(key) ?? []
      bucket.push(label); occupied.set(key, bucket)
    }
  }
  return visible
}


export function graphPointBounds(points: GraphPoint[]): GraphBounds {
  if (!points.length) return { left: 0, top: 0, right: 0, bottom: 0 }
  return {
    left: Math.min(...points.map(p => p.x)), right: Math.max(...points.map(p => p.x)),
    top: Math.min(...points.map(p => p.y)), bottom: Math.max(...points.map(p => p.y)),
  }
}

/** Start at a readable scale. Fitting the entire graph is an explicit action. */
export function initialGraphViewport(points: GraphPoint[], width: number, height: number, focus?: GraphPoint) {
  const bounds = graphPointBounds(points)
  const fit = Math.min(Math.max(100, width - 120) / Math.max(100, bounds.right - bounds.left),
    Math.max(100, height - 120) / Math.max(100, bounds.bottom - bounds.top))
  const scale = Math.max(0.4, Math.min(0.85, fit))
  const center = fit < 0.4 && focus ? focus : { x: (bounds.left + bounds.right) / 2, y: (bounds.top + bounds.bottom) / 2 }
  return { scale, x: width / 2 - center.x * scale, y: height / 2 - center.y * scale }
}


/** Rounded envelope of the node disks with a stable screen-space gutter.
 * Circular joins stay tangent to the edges. Long edges bow gently outwards;
 * neither smoothing step cuts into the enclosed nodes.
 */
export function graphClusterOutline(points: (GraphPoint & { radius?: number })[], scale = 1): string {
  if (!points.length) return ''
  const zoom = Number.isFinite(scale) && scale > 0 ? scale : 1
  const padding = 18 / zoom
  const expanded = points.flatMap(p => p.radius && p.radius > 0
    ? Array.from({ length: 16 }, (_, i) => ({
      x: p.x + Math.cos(i * Math.PI / 8) * p.radius!,
      y: p.y + Math.sin(i * Math.PI / 8) * p.radius!,
    })) : [{ x: p.x, y: p.y }]).sort((a, b) => a.x - b.x || a.y - b.y)
  const unique = expanded.filter((p, i) => !i || p.x !== expanded[i - 1]!.x || p.y !== expanded[i - 1]!.y)
  if (unique.length === 1) {
    const p = unique[0]!
    return `M${p.x - padding},${p.y} A${padding},${padding} 0 1 1 ${p.x + padding},${p.y} A${padding},${padding} 0 1 1 ${p.x - padding},${p.y} Z`
  }
  const cross = (o: GraphPoint, a: GraphPoint, b: GraphPoint) => (a.x - o.x) * (b.y - o.y) - (a.y - o.y) * (b.x - o.x)
  const half = (list: GraphPoint[]) => {
    const hull: GraphPoint[] = []
    for (const point of list) {
      while (hull.length > 1 && cross(hull[hull.length - 2]!, hull[hull.length - 1]!, point) <= 0) hull.pop()
      hull.push(point)
    }
    hull.pop()
    return hull
  }
  const hull = [...half(unique), ...half([...unique].reverse())]
  const normals = hull.map((p, i) => {
    const next = hull[(i + 1) % hull.length]!
    const length = Math.hypot(next.x - p.x, next.y - p.y)
    return { x: (next.y - p.y) / length, y: (p.x - next.x) / length }
  })
  const start = { x: hull[0]!.x + normals.at(-1)!.x * padding, y: hull[0]!.y + normals.at(-1)!.y * padding }
  const path = [`M${start.x},${start.y}`]
  hull.forEach((p, i) => {
    const n = normals[i]!, next = hull[(i + 1) % hull.length]!
    const a = { x: p.x + n.x * padding, y: p.y + n.y * padding }
    const b = { x: next.x + n.x * padding, y: next.y + n.y * padding }
    path.push(`A${padding},${padding} 0 0 1 ${a.x},${a.y}`)
    const dx = b.x - a.x, dy = b.y - a.y
    if (Math.hypot(dx, dy) * zoom > 120) {
      const bow = 4 / zoom
      const mid = { x: (a.x + b.x) / 2 + n.x * bow, y: (a.y + b.y) / 2 + n.y * bow }
      path.push(`C${a.x + dx / 6},${a.y + dy / 6} ${mid.x - dx / 6},${mid.y - dy / 6} ${mid.x},${mid.y}`)
      path.push(`C${mid.x + dx / 6},${mid.y + dy / 6} ${b.x - dx / 6},${b.y - dy / 6} ${b.x},${b.y}`)
    } else path.push(`L${b.x},${b.y}`)
  })
  return path.join(' ') + ' Z'
}
