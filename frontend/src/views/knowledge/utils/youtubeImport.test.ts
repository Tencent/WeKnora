import assert from 'node:assert/strict'
import test from 'node:test'

import { parseYoutubeUrlsInput } from './youtubeImport'

test('splits one URL per line, trims whitespace, drops blank lines', () => {
  const result = parseYoutubeUrlsInput(
    '  https://www.youtube.com/watch?v=abc123  \n\nhttps://youtu.be/def456\n',
  )
  assert.deepEqual(result.urls, [
    'https://www.youtube.com/watch?v=abc123',
    'https://youtu.be/def456',
  ])
  assert.deepEqual(result.invalidLines, [])
})

test('flags lines that are not valid URLs', () => {
  const result = parseYoutubeUrlsInput('not a url\nhttps://www.youtube.com/watch?v=abc123')
  assert.deepEqual(result.invalidLines, ['not a url'])
  assert.deepEqual(result.urls, ['https://www.youtube.com/watch?v=abc123'])
})

test('flags non-YouTube URLs as invalid', () => {
  const result = parseYoutubeUrlsInput('https://example.com/watch?v=abc123')
  assert.deepEqual(result.invalidLines, ['https://example.com/watch?v=abc123'])
  assert.deepEqual(result.urls, [])
})

test('empty input yields no urls and no invalid lines', () => {
  const result = parseYoutubeUrlsInput('   \n  ')
  assert.deepEqual(result.urls, [])
  assert.deepEqual(result.invalidLines, [])
})
