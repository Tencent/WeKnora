import assert from 'node:assert/strict'
import test from 'node:test'

import { consumeApiPlaygroundSSE } from './apiPlaygroundSSE.ts'

const encoder = new TextEncoder()

function streamFromChunks(chunks: string[], keepOpen = false) {
  let index = 0
  let cancelled = false
  const stream = new ReadableStream<Uint8Array>({
    pull(controller) {
      if (index < chunks.length) {
        controller.enqueue(encoder.encode(chunks[index++]))
      } else if (!keepOpen) {
        controller.close()
      }
    },
    cancel() {
      cancelled = true
    },
  })
  return { stream, wasCancelled: () => cancelled }
}

test('fails on a terminal error without waiting for the connection to close', async () => {
  const fixture = streamFromChunks([
    'event: message\ndata: {"response_type":"error","content":"synthetic provider failure","done":true}\n\n',
  ], true)

  const result = await Promise.race([
    consumeApiPlaygroundSSE(fixture.stream),
    new Promise<never>((_, reject) => setTimeout(() => reject(new Error('timed out')), 200)),
  ])

  assert.equal(result.status, 'failed')
  assert.equal(result.error, 'synthetic provider failure')
  assert.equal(fixture.wasCancelled(), true)
})

test('accepts current, legacy, and sentinel completion events', async (t) => {
  const cases = [
    {
      name: 'complete event',
      sse: 'data: {"response_type":"answer","content":"current","done":false}\n\ndata: {"response_type":"complete","content":"","done":true}\n\n',
      answer: 'current',
    },
    {
      name: 'legacy done event',
      sse: 'data: {"response_type":"answer","content":"legacy","done":true}\n\n',
      answer: 'legacy',
    },
    {
      name: 'DONE sentinel',
      sse: 'data: {"response_type":"answer","content":"sentinel","done":false}\n\ndata: [DONE]\n\n',
      answer: 'sentinel',
    },
  ]

  for (const fixture of cases) {
    await t.test(fixture.name, async () => {
      const result = await consumeApiPlaygroundSSE(streamFromChunks([fixture.sse], true).stream)
      assert.equal(result.status, 'success')
      assert.equal(result.answer, fixture.answer)
    })
  }
})

test('does not treat another event type done marker as stream completion', async () => {
  const result = await consumeApiPlaygroundSSE(streamFromChunks([
    'data: {"response_type":"thinking","content":"thought","done":true}\n\n'
      + 'data: {"response_type":"answer","content":"answer","done":false}\n\n'
      + 'data: {"response_type":"complete","content":"","done":true}\n\n',
  ]).stream)

  assert.equal(result.status, 'success')
  assert.equal(result.answer, 'answer')
})

test('ignores frames after a terminal event', async () => {
  const fixture = streamFromChunks([
    'data: {"response_type":"answer","content":"before","done":false}\n\n'
      + 'data: {"response_type":"complete","content":"","done":true}\n\n',
    'data: {"response_type":"answer","content":"after","done":false}\n\n',
  ], true)
  const result = await consumeApiPlaygroundSSE(fixture.stream)

  assert.equal(result.status, 'success')
  assert.equal(result.answer, 'before')
  assert.equal(fixture.wasCancelled(), true)
})

test('reports EOF without a business terminal event as failure', async () => {
  const result = await consumeApiPlaygroundSSE(streamFromChunks([
    'data: {"response_type":"answer","content":"partial","done":false}\n\n',
  ]).stream)

  assert.equal(result.status, 'failed')
  assert.equal(result.reason, 'unexpected-eof')
  assert.equal(result.answer, 'partial')
})

test('keeps valid non-object JSON visible without crashing the parser', async () => {
  const result = await consumeApiPlaygroundSSE(streamFromChunks([
    'data: null\n\ndata: {"response_type":"complete","content":"","done":true}\n\n',
  ]).stream)

  assert.equal(result.status, 'success')
  assert.match(result.raw, /data: null/)
})

test('reassembles an SSE frame split across network chunks', async () => {
  const result = await consumeApiPlaygroundSSE(streamFromChunks([
    'event: message\ndata: {"response_type":"ans',
    'wer","content":"split","done":false}\n',
    '\ndata: {"response_type":"complete","content":"","done":tr',
    'ue}\n\n',
  ]).stream)

  assert.equal(result.status, 'success')
  assert.equal(result.answer, 'split')
})

test('supports CRLF frames and joins multiple data lines', async () => {
  const result = await consumeApiPlaygroundSSE(streamFromChunks([
    'event: message\r\ndata: {"response_type":"answer",\r\n'
      + 'data: "content":"multiline","done":false}\r\n\r\n'
      + 'data: {"response_type":"complete","content":"","done":true}\r\n\r\n',
  ]).stream)

  assert.equal(result.status, 'success')
  assert.equal(result.answer, 'multiline')
})
