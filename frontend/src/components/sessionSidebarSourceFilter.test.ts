import assert from 'node:assert/strict'
import test from 'node:test'
import {
  DEFAULT_SESSION_BUCKET_KEY,
  buildSessionSourceOptions,
  findSessionBucketKey,
  shouldShowSessionSourceFilter,
} from './sessionSidebarSourceFilter.ts'

test('shouldShowSessionSourceFilter hides when no channel buckets', () => {
  assert.equal(shouldShowSessionSourceFilter(0), false)
  assert.equal(shouldShowSessionSourceFilter(1), true)
})

test('buildSessionSourceOptions puts web first then channels', () => {
  const options = buildSessionSourceOptions(
    'My chats',
    [
      { key: 'im:wechat', label: 'WeChat', platform: 'wechat' },
      { key: 'embed:abc', label: 'Widget' },
    ],
    (platform) => `logo:${platform}`,
  )
  assert.equal(options.length, 3)
  assert.equal(options[0].value, DEFAULT_SESSION_BUCKET_KEY)
  assert.equal(options[1].logo, 'logo:wechat')
  assert.equal(options[2].logo, undefined)
})

test('findSessionBucketKey locates session bucket', () => {
  const key = findSessionBucketKey(
    {
      web: { items: [{ id: 'a' }] },
      'im:wechat': { items: [{ id: 'b' }] },
    },
    'b',
  )
  assert.equal(key, 'im:wechat')
})
