import assert from 'node:assert/strict'
import test from 'node:test'

import { parseThinkBlocks } from './thinkBlocks.ts'

test('leaves plain answers untouched', () => {
  const raw = 'A normal answer with **markdown** and `inline code`.'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('splits a terminated block from the answer', () => {
  assert.deepEqual(parseThinkBlocks('<think>reasoning</think>The answer.'), {
    think: 'reasoning',
    answer: 'The answer.',
    found: true,
    open: false,
  })
})

test('splits every reasoning round and keeps the answer order', () => {
  assert.deepEqual(
    parseThinkBlocks('<think>first round</think>one\n<think>second round</think>two'),
    {
      think: 'first round\nsecond round',
      answer: 'one\ntwo',
      found: true,
      open: false,
    }
  )
})

test('keeps answer text before and after a block', () => {
  assert.deepEqual(parseThinkBlocks('before <think>reason</think> after'), {
    think: 'reason',
    answer: 'before  after',
    found: true,
    open: false,
  })
})

test('reports an unterminated block and keeps its prefix as answer', () => {
  assert.deepEqual(parseThinkBlocks('partial answer<think>still reasoning'), {
    think: 'still reasoning',
    answer: 'partial answer',
    found: true,
    open: true,
  })
})

test('treats a stray close tag as literal answer text', () => {
  const raw = 'The closing marker is </think> in this example.'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('accepts an empty terminated block without inventing reasoning', () => {
  assert.deepEqual(parseThinkBlocks('<think></think>answer'), {
    think: '',
    answer: 'answer',
    found: true,
    open: false,
  })
})

test('ignores markup inside a fenced code block', () => {
  const raw = 'Example:\n```xml\n<think>not reasoning</think>\n```\nDone.'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('ignores markup inside a tilde fence', () => {
  const raw = 'Example:\n~~~\n<think>literal</think>\n~~~\nDone.'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('ignores markup inside inline code spans', () => {
  const raw = 'Use `<think>` and `</think>` around reasoning.'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('parses a real block before a fenced example', () => {
  const raw = '<think>real reasoning</think>Example:\n```\n<think>literal</think>\n```'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: 'real reasoning',
    answer: 'Example:\n```\n<think>literal</think>\n```',
    found: true,
    open: false,
  })
})

test('parses a real block after a closed fenced example', () => {
  const raw = 'Example:\n```\n<think>literal</think>\n```\n<think>real reasoning</think>Done.'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: 'real reasoning',
    answer: 'Example:\n```\n<think>literal</think>\n```\nDone.',
    found: true,
    open: false,
  })
})

test('treats a backslash-escaped opener as literal text', () => {
  const raw = 'Write \\<think> to start reasoning.'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('keeps tags literal after an unterminated inline code span', () => {
  const raw = 'Broken `span <think>not reasoning</think>'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('keeps tags literal inside an unterminated fenced block', () => {
  const raw = 'Example:\n```\n<think>literal</think>\n'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('honours a longer backtick fence around shorter runs', () => {
  const raw = '````md\n```\n<think>literal</think>\n```\n````'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('handles CRLF fences', () => {
  const raw = 'Example:\r\n```\r\n<think>literal</think>\r\n```\r\nDone.'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('keeps a literal tag inside reasoning text', () => {
  const raw = '<think>Compare `<think>` with a real opener.</think>Answer.'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: 'Compare `<think>` with a real opener.',
    answer: 'Answer.',
    found: true,
    open: false,
  })
})

test('handles several inline code spans before a real block', () => {
  const raw = '`<think>` then `</think>` then <think>real</think>answer'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: 'real',
    answer: '`<think>` then `</think>` then answer',
    found: true,
    open: false,
  })
})

test('does not treat a malformed opener as a block', () => {
  const raw = 'Text with <thinking> and <think attr="x"> but no opener.'
  assert.deepEqual(parseThinkBlocks(raw), {
    think: '',
    answer: raw,
    found: false,
    open: false,
  })
})

test('normalizes whitespace between reasoning rounds', () => {
  assert.deepEqual(parseThinkBlocks('<think> one </think>\n<think> two </think>\nanswer'), {
    think: 'one\ntwo',
    answer: '\n\nanswer',
    found: true,
    open: false,
  })
})
