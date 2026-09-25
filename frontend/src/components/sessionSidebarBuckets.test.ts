import assert from 'node:assert/strict'
import test from 'node:test'

import {
  applyBucketCountProbe,
  bucketVisible,
  buildBucketDefinitions,
  createEmptyBucket,
  prependSessionToWebBucket,
  type BucketDefinition,
} from './sessionSidebarBuckets.ts'

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
