import assert from 'node:assert/strict'
import test from 'node:test'

import { isYouTubeUrl, parseImportUrls, summarizeImportUrls, youTubeLinkKind } from './youtube.ts'

test('classifies YouTube video links', () => {
  for (const url of [
    'https://www.youtube.com/watch?v=jNQXAC9IVRw',
    'https://www.youtube.com/watch?v=jNQXAC9IVRw&list=PL123456',
    'https://m.youtube.com/watch?v=jNQXAC9IVRw',
    'https://youtu.be/jNQXAC9IVRw?si=abc',
    'https://www.youtube.com/shorts/jNQXAC9IVRw',
    'https://www.youtube.com/live/jNQXAC9IVRw',
    'https://www.youtube-nocookie.com/embed/jNQXAC9IVRw',
  ]) {
    assert.equal(youTubeLinkKind(url), 'video', url)
  }
})

test('classifies YouTube playlist links', () => {
  assert.equal(youTubeLinkKind('https://www.youtube.com/playlist?list=PLRqwX-V7Uu6ZiZxtDDRCi6uhfTH4FilpH'), 'playlist')
  assert.equal(youTubeLinkKind('  https://music.youtube.com/playlist?list=OLAK5uy_abc '), 'playlist')
})

test('rejects non-YouTube and malformed links', () => {
  for (const url of [
    'https://www.youtube.com/playlist',
    'https://www.youtube.com/playlist?list=bad id',
    'https://www.youtube.com/watch?v=short',
    'https://www.youtube.com/@channel',
    'https://youtube.com.evil.test/watch?v=jNQXAC9IVRw',
    'https://example.com/watch?v=jNQXAC9IVRw',
    'not a url',
  ]) {
    assert.equal(isYouTubeUrl(url), false, url)
  }
})

test('parses pasted links separated by new lines, spaces and commas', () => {
  const text = `https://youtu.be/jNQXAC9IVRw
    https://www.youtube.com/playlist?list=PL123456, https://example.com/doc，https://youtu.be/jNQXAC9IVRw
  notalink ftp://example.com/file`
  assert.deepEqual(parseImportUrls(text), {
    urls: [
      'https://youtu.be/jNQXAC9IVRw',
      'https://www.youtube.com/playlist?list=PL123456',
      'https://example.com/doc',
    ],
    invalid: ['notalink', 'ftp://example.com/file'],
  })
  assert.deepEqual(parseImportUrls('   '), { urls: [], invalid: [] })
})

test('summarizes pasted links for the import preview', () => {
  const text = `https://example.com/article
https://youtu.be/jNQXAC9IVRw
https://www.youtube.com/watch?v=reiXAO1vl9I&list=PL123456
https://www.youtube.com/playlist?list=PL123456
https://www.youtube.com/watchv=reiXAO1vl9I
not-a-link`
  assert.deepEqual(summarizeImportUrls(text), { webPages: 2, youTubeVideos: 2, youTubePlaylists: 1, invalid: 1 })
  assert.deepEqual(summarizeImportUrls(''), { webPages: 0, youTubeVideos: 0, youTubePlaylists: 0, invalid: 0 })
})
