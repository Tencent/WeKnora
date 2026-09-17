import assert from 'node:assert/strict'
import test from 'node:test'

import { isYouTubePlaylistUrl } from './youtube.ts'

test('recognises YouTube playlist pages', () => {
  assert.equal(isYouTubePlaylistUrl('https://www.youtube.com/playlist?list=PLRqwX-V7Uu6ZiZxtDDRCi6uhfTH4FilpH'), true)
  assert.equal(isYouTubePlaylistUrl('  https://m.youtube.com/playlist?list=PL123456&si=abc '), true)
  assert.equal(isYouTubePlaylistUrl('https://music.youtube.com/playlist?list=OLAK5uy_abc'), true)
})

test('treats video links, including watch links inside a playlist, as single videos', () => {
  assert.equal(isYouTubePlaylistUrl('https://www.youtube.com/watch?v=jNQXAC9IVRw&list=PL123456'), false)
  assert.equal(isYouTubePlaylistUrl('https://youtu.be/jNQXAC9IVRw'), false)
})

test('rejects non-playlist and non-YouTube links', () => {
  assert.equal(isYouTubePlaylistUrl('https://www.youtube.com/playlist'), false)
  assert.equal(isYouTubePlaylistUrl('https://www.youtube.com/playlist?list=bad id'), false)
  assert.equal(isYouTubePlaylistUrl('https://youtube.com.evil.test/playlist?list=PL123456'), false)
  assert.equal(isYouTubePlaylistUrl('https://example.com/playlist?list=PL123456'), false)
  assert.equal(isYouTubePlaylistUrl('not a url'), false)
})
