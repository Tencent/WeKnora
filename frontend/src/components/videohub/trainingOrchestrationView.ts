export interface TrainingNetworkLayout {
  columns: number
  rows: number
  height: number
}

export interface TrainingNetworkPoint {
  x: number
  y: number
}

export function createTrainingNetworkLayout(count: number): TrainingNetworkLayout {
  const normalizedCount = Math.max(0, Math.floor(count))
  const columns = Math.min(3, Math.max(1, Math.ceil(Math.sqrt(normalizedCount))))
  const rows = Math.max(1, Math.ceil(normalizedCount / columns))
  return { columns, rows, height: Math.max(540, rows * 118 + 72) }
}

export function positionTrainingCluster(index: number, count: number, layout: TrainingNetworkLayout): TrainingNetworkPoint {
  const row = Math.floor(index / layout.columns)
  const column = index % layout.columns
  const rowSize = Math.min(layout.columns, count - row * layout.columns)
  return {
    x: ((column + 1) / (rowSize + 1)) * 100,
    y: ((row + 1) / (layout.rows + 1)) * 100,
  }
}

export function clipTrainingEdgeToNodes(source: TrainingNetworkPoint, target: TrainingNetworkPoint) {
  const deltaX = target.x - source.x
  const deltaY = target.y - source.y
  if (deltaX === 0 && deltaY === 0) return { x1: source.x, y1: source.y, x2: target.x, y2: target.y }

  const scale = 1 / Math.max(Math.abs(deltaX) / 98, Math.abs(deltaY) / 54)
  const offsetX = deltaX * scale
  const offsetY = deltaY * scale
  return {
    x1: source.x + offsetX,
    y1: source.y + offsetY,
    x2: target.x - offsetX,
    y2: target.y - offsetY,
  }
}

export function routeTrainingEdge(
  source: TrainingNetworkPoint,
  target: TrainingNetworkPoint,
  obstacles: TrainingNetworkPoint[],
  bounds: { width: number; height: number },
) {
  const endpoints = clipTrainingEdgeToNodes(source, target)
  const start = { x: endpoints.x1, y: endpoints.y1 }
  const end = { x: endpoints.x2, y: endpoints.y2 }
  const direct = [start, end]
  if (!obstacles.some(point => normalizedSegmentDistance(point, start, end) < 1.08)) {
    return { ...endpoints, path: pointsToPath(direct), routed: false }
  }

  const xLanes = routingLanes([source.x, target.x, ...obstacles.map(point => point.x)], bounds.width)
  const yLanes = routingLanes([source.y, target.y, ...obstacles.map(point => point.y)], bounds.height)
  const candidates = [
    ...xLanes.map(x => [start, { x, y: start.y }, { x, y: end.y }, end]),
    ...yLanes.map(y => [start, { x: start.x, y }, { x: end.x, y }, end]),
  ]
  const best = candidates.reduce((winner, candidate) => {
    const score = routeScore(candidate, obstacles)
    if (!winner || score > winner.score) return { points: candidate, score }
    return winner
  }, null as { points: TrainingNetworkPoint[]; score: number } | null)?.points || direct

  return { ...endpoints, path: pointsToPath(best), routed: true }
}

function routingLanes(values: number[], bound: number) {
  const unique = [...new Set(values.map(value => Math.round(value * 1000) / 1000))].sort((a, b) => a - b)
  const lanes = [24, bound - 24]
  for (let index = 1; index < unique.length; index += 1) lanes.push((unique[index - 1] + unique[index]) / 2)
  return [...new Set(lanes)].filter(value => value > 0 && value < bound)
}

function routeScore(points: TrainingNetworkPoint[], obstacles: TrainingNetworkPoint[]) {
  let clearance = Number.POSITIVE_INFINITY
  let length = 0
  for (let index = 1; index < points.length; index += 1) {
    const start = points[index - 1]
    const end = points[index]
    length += Math.hypot(end.x - start.x, end.y - start.y)
    for (const obstacle of obstacles) clearance = Math.min(clearance, normalizedSegmentDistance(obstacle, start, end))
  }
  return clearance - length / 100000
}

function normalizedSegmentDistance(point: TrainingNetworkPoint, start: TrainingNetworkPoint, end: TrainingNetworkPoint) {
  const pointX = point.x / 98
  const pointY = point.y / 54
  const startX = start.x / 98
  const startY = start.y / 54
  const endX = end.x / 98
  const endY = end.y / 54
  const deltaX = endX - startX
  const deltaY = endY - startY
  const squaredLength = deltaX * deltaX + deltaY * deltaY
  const ratio = squaredLength === 0
    ? 0
    : Math.max(0, Math.min(1, ((pointX - startX) * deltaX + (pointY - startY) * deltaY) / squaredLength))
  return Math.hypot(pointX - (startX + ratio * deltaX), pointY - (startY + ratio * deltaY))
}

function pointsToPath(points: TrainingNetworkPoint[]) {
  return points.map((point, index) => `${index === 0 ? 'M' : 'L'} ${point.x} ${point.y}`).join(' ')
}
