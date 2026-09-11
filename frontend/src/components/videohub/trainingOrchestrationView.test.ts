import assert from 'node:assert/strict'
import test from 'node:test'
import { clipTrainingEdgeToNodes, createTrainingNetworkLayout, positionTrainingCluster, routeTrainingEdge } from './trainingOrchestrationView'

test('lays out the maximum 20 topic clusters without overlapping fixed node boxes', () => {
  const count = 20
  const canvasWidth = 720
  const nodeWidth = 148
  const nodeHeight = 92
  const layout = createTrainingNetworkLayout(count)
  const points = Array.from({ length: count }, (_, index) => positionTrainingCluster(index, count, layout))
    .map(point => ({ x: point.x * canvasWidth / 100, y: point.y * layout.height / 100 }))

  for (let left = 0; left < points.length; left += 1) {
    for (let right = left + 1; right < points.length; right += 1) {
      const horizontalOverlap = Math.abs(points[left].x - points[right].x) < nodeWidth
      const verticalOverlap = Math.abs(points[left].y - points[right].y) < nodeHeight
      assert.equal(horizontalOverlap && verticalOverlap, false, `clusters ${left} and ${right} overlap`)
    }
  }
})

test('clips a directed edge before the target node so its arrow remains visible', () => {
  const edge = clipTrainingEdgeToNodes({ x: 225, y: 270 }, { x: 450, y: 270 })
  assert.deepEqual(edge, { x1: 323, y1: 270, x2: 352, y2: 270 })
  assert.ok(edge.x2 < 450 - 92.5, 'target endpoint must stay outside a 148px node on the 720px canvas')
})

test('routes a long same-column relation around an intermediate topic cluster', () => {
  const route = routeTrainingEdge(
    { x: 225, y: 112.25 },
    { x: 225, y: 336.75 },
    [{ x: 225, y: 224.5 }],
    { width: 900, height: 898 },
  )
  assert.equal(route.routed, true)
  assert.match(route.path, /^M .+ L .+ L .+ L /)
  assert.doesNotMatch(route.path, /L 225 224\.5/)
})
