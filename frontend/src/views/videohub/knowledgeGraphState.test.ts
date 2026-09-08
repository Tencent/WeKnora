import assert from 'node:assert/strict'
import test from 'node:test'
import { createRequestGate, graphContainsSelection, graphTypesForAttribute, isWikiPageId } from './knowledgeGraphState'

test('maps all five graph filters to backend knowledge types', () => {
  assert.deepEqual(graphTypesForAttribute('实体'), ['entity'])
  assert.deepEqual(graphTypesForAttribute('概念'), ['concept'])
  assert.deepEqual(graphTypesForAttribute('方法论'), ['methodology'])
  assert.deepEqual(graphTypesForAttribute('案例'), ['case'])
  assert.deepEqual(graphTypesForAttribute('洞察'), ['insight'])
  assert.equal(graphTypesForAttribute('all'), undefined)
})

test('accepts only UUID Wiki page identities', () => {
  assert.equal(isWikiPageId('22222222-2222-4222-8222-222222222222'), true)
  assert.equal(isWikiPageId('wiki:22222222-2222-4222-8222-222222222222'), false)
  assert.equal(isWikiPageId('concept/title'), false)
  assert.equal(isWikiPageId(''), false)
})

test('drops stale graph responses after a newer filter request starts', () => {
  const graphGate = createRequestGate()
  const firstRequest = graphGate.next()
  const latestRequest = graphGate.next()

  assert.equal(graphGate.isCurrent(firstRequest), false)
  assert.equal(graphGate.isCurrent(latestRequest), true)
})

test('detail retries do not invalidate an in-flight cross-video request', () => {
  const detailGate = createRequestGate()
  const crossVideoGate = createRequestGate()
  const crossVideoRequest = crossVideoGate.next()

  detailGate.next()

  assert.equal(crossVideoGate.isCurrent(crossVideoRequest), true)
})

test('invalidating a node request prevents its late response from being applied', () => {
  const detailGate = createRequestGate()
  const previousNodeRequest = detailGate.next()

  detailGate.invalidate()

  assert.equal(detailGate.isCurrent(previousNodeRequest), false)
})

test('detects when a selected graph node leaves the filtered result', () => {
  const payload = {
    nodes: [{ id: 'wiki:22222222-2222-4222-8222-222222222222' }],
    wiki_pages: [{ id: '22222222-2222-4222-8222-222222222222' }],
  }

  assert.equal(graphContainsSelection(payload, 'wiki:22222222-2222-4222-8222-222222222222'), true)
  assert.equal(graphContainsSelection(payload, 'wiki:33333333-3333-4333-8333-333333333333'), false)
})
