import assert from 'node:assert/strict'
import test from 'node:test'
import { isVideoInitiallyAvailable } from './videoMapping'

test('keeps an uploaded video playable after content parsing fails', () => {
  assert.equal(isVideoInitiallyAvailable({ status: 'failed', file_url: 'https://cdn.example.com/video.mp4' }), true)
  assert.equal(isVideoInitiallyAvailable({ status: 'failed', file_url: '' }), false)
})
