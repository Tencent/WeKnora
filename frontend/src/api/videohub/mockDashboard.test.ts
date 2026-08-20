import assert from 'node:assert/strict'
import test from 'node:test'
import { fetchDashboard } from './dashboard'
import { getMockDashboard, mockClusters } from './mockDashboard'

test('dashboard mock returns four KPI values, seven trend points and five clusters', () => {
  const payload = getMockDashboard({ range: '7d' })
  assert.equal(payload.trend.length, 7)
  assert.equal(payload.clusters.length, 5)
  assert.equal(payload.kpi.total_questions, 1234)
  assert.equal(mockClusters.length, 5)
})

test('dashboard ranges produce semantically different trend windows and KPI totals', () => {
  const seven = getMockDashboard({ range: '7d' })
  const thirty = getMockDashboard({ range: '30d' })
  const ninety = getMockDashboard({ range: '90d' })
  assert.equal(thirty.trend.length, 30)
  assert.equal(ninety.trend.length, 90)
  assert.ok(seven.kpi.total_questions < thirty.kpi.total_questions)
  assert.ok(thirty.kpi.total_questions < ninety.kpi.total_questions)
  assert.notEqual(seven.clusters[0].count, thirty.clusters[0].count)
  assert.notEqual(thirty.clusters[0].count, ninety.clusters[0].count)
  for (const payload of [seven, thirty, ninety]) {
    assert.ok(payload.clusters.every(cluster => cluster.related_video_count === cluster.videos.length))
  }
})

test('custom dashboard range uses inclusive calendar days and enforces the 90-day boundary', async () => {
  const allowed = await fetchDashboard({ range: 'custom', from: '2026-05-23', to: '2026-08-20' })
  assert.equal(allowed.trend.length, 90)
  assert.equal(allowed.trend[0].date, '2026-05-23')
  assert.equal(allowed.trend.at(-1)?.date, '2026-08-20')
  await assert.rejects(fetchDashboard({ range: 'custom', from: '2026-05-22', to: '2026-08-20' }), /最长 90 天/)
  await assert.rejects(fetchDashboard({ range: 'custom', from: '2026-08-20', to: '2026-08-19' }), /开始日期/)
})

test('dashboard mock includes a deleted-video edge case', () => {
  assert.ok(mockClusters.some(cluster => cluster.videos.some(video => video.deleted)))
})
