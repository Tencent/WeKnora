import assert from 'node:assert/strict'
import test from 'node:test'

import {
  EMBED_SESSION_MARKER_PREFIX,
  classifyDateBucket,
  configuredPlatforms,
  groupSessions,
  groupSessionsBySource,
  resolveSessionOrigin,
  shouldShowSessionSourceBadge,
} from './sessionGrouping.ts'

const sourceLabels = {
  web: 'Web',
  embedFallback: 'Embed',
  embedChannel: (id: string) => `Embed ${id}`,
  imPlatform: (p: string) => `IM ${p}`,
}

test('resolveSessionOrigin distinguishes web, IM, and embed sessions', () => {
  assert.deepEqual(resolveSessionOrigin({ id: '1' }), { kind: 'web' })
  assert.deepEqual(resolveSessionOrigin({ id: '2', im_platform: 'feishu' }), {
    kind: 'im',
    platform: 'feishu',
  })
  assert.deepEqual(
    resolveSessionOrigin({
      id: '3',
      description: `${EMBED_SESSION_MARKER_PREFIX}ch-1`,
    }),
    { kind: 'embed', channelId: 'ch-1' },
  )
})

test('configuredPlatforms returns distinct platform keys in first-seen order', () => {
  const channels = [
    { platform: 'feishu' },
    { platform: 'wecom' },
    { platform: 'feishu' },
    { platform: '' },
  ]
  assert.deepEqual(configuredPlatforms(channels), ['feishu', 'wecom'])
})

test('groupSessionsBySource orders web, configured IM, then embed channels', () => {
  const sessions = [
    { id: 'w', title: 'web chat' },
    { id: 'f', title: 'feishu', im_platform: 'feishu' },
    { id: 'e', title: 'embed', description: `${EMBED_SESSION_MARKER_PREFIX}ec-1` },
  ]
  const groups = groupSessionsBySource(sessions, sourceLabels, { 'ec-1': 'Help widget' }, ['feishu'], 'Pinned')
  assert.deepEqual(
    groups.map((g) => g.key),
    ['web', 'im:feishu', 'embed:ec-1'],
  )
  assert.equal(groups[2]?.label, 'Help widget')
})

test('groupSessions date mode keeps pinned sessions in their own bucket', () => {
  const now = new Date().toISOString()
  const groups = groupSessions(
    'date',
    [
      { id: 'p', is_pinned: true, updated_at: now },
      { id: 't', updated_at: now },
    ],
    {
      pinnedLabel: 'Pinned',
      bucketLabels: {
        pinned: 'Pinned',
        today: 'Today',
        yesterday: 'Yesterday',
        last7Days: '7d',
        last30Days: '30d',
        lastYear: 'Year',
        earlier: 'Earlier',
      },
      categorizeDate: () => 'today',
      sourceLabels,
      embedChannelNames: {},
      configuredImPlatforms: [],
    },
  )
  assert.deepEqual(
    groups.map((g) => g.key),
    ['pinned', 'today'],
  )
})

test('classifyDateBucket buckets recent sessions as today', () => {
  assert.equal(classifyDateBucket(new Date().toISOString()), 'today')
})

test('shouldShowSessionSourceBadge is off when channels use separate folders', () => {
  assert.equal(shouldShowSessionSourceBadge('date'), false)
  assert.equal(shouldShowSessionSourceBadge('none'), false)
})
