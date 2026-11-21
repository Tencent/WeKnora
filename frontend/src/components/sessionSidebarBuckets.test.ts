import assert from 'node:assert/strict'
import test from 'node:test'

import {
  applyBucketCountProbe,
  bucketVisible,
  buildBucketDefinitions,
  createEmptyBucket,
  prependSessionToWebBucket,
  removeSessionFromBuckets,
  mergeBucketPage,
  isChannelBucket,
  type SidebarSessionBucket,
  type BucketDefinition,
} from './sessionSidebarBuckets.ts'
import {
  buildSessionSourceOptions,
  shouldShowSessionSourceFilter,
} from './sessionSidebarSourceFilter.ts'

const imDef: BucketDefinition = {
  key: 'im:feishu',
  apiSource: 'feishu',
  label: 'Feishu',
  kind: 'im',
  platform: 'feishu',
}

test('bucketVisible hides channel buckets until count is known', () => {
  const bucket = createEmptyBucket(imDef)
  assert.equal(bucketVisible(bucket), false)
})

test('bucketVisible hides channel buckets with zero sessions after probe', () => {
  const bucket = applyBucketCountProbe(createEmptyBucket(imDef), 0)
  assert.equal(bucketVisible(bucket), false)
})

test('bucketVisible shows channel buckets with sessions after probe', () => {
  const bucket = applyBucketCountProbe(createEmptyBucket(imDef), 3)
  assert.equal(bucketVisible(bucket), true)
})

test('prependSessionToWebBucket inserts new session at front and bumps total', () => {
  const webDef: BucketDefinition = {
    key: 'web',
    apiSource: 'web',
    label: 'Chats',
    kind: 'web',
  }
  const bucket = { ...createEmptyBucket(webDef), total: 2, items: [{ id: 'a' }] }
  const next = prependSessionToWebBucket(bucket, { id: 'b', title: 'New' })
  assert.deepEqual(next.items.map((s) => s.id), ['b', 'a'])
  assert.equal(next.total, 3)
})

test('prependSessionToWebBucket is idempotent for existing session', () => {
  const webDef: BucketDefinition = {
    key: 'web',
    apiSource: 'web',
    label: 'Chats',
    kind: 'web',
  }
  const bucket = { ...createEmptyBucket(webDef), total: 1, items: [{ id: 'a' }] }
  const next = prependSessionToWebBucket(bucket, { id: 'a', title: 'Same' })
  assert.equal(next, bucket)
})

test('buildBucketDefinitions hides channel buckets unless admin', () => {
  const defs = buildBucketDefinitions(
    ['feishu'],
    { ch1: 'Embed' },
    {
      web: 'Chats',
      imPlatform: (platform) => platform,
      embedChannel: (name) => name,
      api: 'API',
    },
  )
  assert.deepEqual(defs.map((def) => def.key), ['web'])

  const adminDefs = buildBucketDefinitions(
    ['feishu'],
    { ch1: 'Embed' },
    {
      web: 'Chats',
      imPlatform: (platform) => platform,
      embedChannel: (name) => name,
      api: 'API',
    },
    { includeAdminChannelBuckets: true },
  )
  assert.deepEqual(
    adminDefs.map((def) => def.key),
    ['im:feishu', 'embed:ch1', 'api', 'web'],
  )
})

const channelDefs: BucketDefinition[] = [
  { key: 'api', apiSource: 'api', label: 'API', kind: 'api' },
  imDef,
  { key: 'embed:widget', apiSource: 'embed:widget', label: 'Widget', kind: 'embed' },
]

// Follow the sidebar's deletion/count-probe -> visibility -> source-options path.
for (const def of channelDefs) {
  for (const removal of ['single', 'batch', 'count probe'] as const) {
    test(`${def.kind}: ${removal} keeps the empty active source available until switching to web`, () => {
      const web = prependSessionToWebBucket(
        createEmptyBucket({ key: 'web', apiSource: 'web', label: 'My chats', kind: 'web' }),
        { id: 'web-chat', title: 'Web conversation' },
      )
      const rows = removal === 'batch'
        ? [{ id: 'first' }, { id: 'last' }]
        : [{ id: 'last' }]
      let buckets: Record<string, SidebarSessionBucket> = {
        web,
        [def.key]: mergeBucketPage(
          applyBucketCountProbe(createEmptyBucket(def), rows.length),
          rows,
          rows.length,
          1,
        ),
      }
      if (removal === 'count probe') {
        buckets[def.key] = applyBucketCountProbe(createEmptyBucket(def), 0)
      } else {
        for (const row of rows) buckets = removeSessionFromBuckets(buckets, row.id)
      }
      assert.equal(buckets[def.key].total, 0)
      assert.deepEqual(buckets[def.key].items, [])
      assert.deepEqual(buckets.web.items.map((row) => row.id), ['web-chat'])

      const visibleChannels = Object.values(buckets).filter(
        (bucket) => isChannelBucket(bucket) && bucketVisible(bucket, def.key),
      )
      assert.equal(shouldShowSessionSourceFilter(visibleChannels.length), true)
      const options = buildSessionSourceOptions('My chats', visibleChannels, () => '')
      assert.deepEqual(options.map((option) => option.value), ['web', def.key])
      assert.equal(options.find((option) => option.value === def.key)?.label, def.label)

      // Once the user switches back, the empty channel disappears as before.
      assert.equal(bucketVisible(buckets[def.key], 'web'), false)
      assert.equal(bucketVisible(buckets.web, 'web'), true)
    })
  }
}

test('only the active channel stays visible while its count is unknown', () => {
  const bucket = createEmptyBucket(imDef)
  assert.equal(bucketVisible(bucket, imDef.key), true)
  assert.equal(bucketVisible(bucket, 'api'), false)
  assert.equal(bucketVisible(bucket, 'web'), false)
})

test('nonempty inactive channels remain visible', () => {
  const bucket = applyBucketCountProbe(createEmptyBucket(imDef), 1)
  assert.equal(bucketVisible(bucket, 'api'), true)
})
