import assert from 'node:assert/strict'
import test from 'node:test'
import { parseWeKnoraStreamChunk } from './chatStream'

test('parses WeKnora agent answer chunks that use type instead of response_type', () => {
  assert.deepEqual(parseWeKnoraStreamChunk(JSON.stringify({ type: 'answer', content: '结果' })), {
    kind: 'answer',
    content: '结果',
  })
})

test('parses native agent thinking and activity events for video assistant streaming', () => {
  assert.deepEqual(parseWeKnoraStreamChunk(JSON.stringify({ response_type: 'thinking', content: '先查字幕' })), {
    kind: 'thinking',
    content: '先查字幕',
  })
  assert.deepEqual(parseWeKnoraStreamChunk(JSON.stringify({ response_type: 'tool_call', data: { tool_name: 'search_knowledge' } })), {
    kind: 'activity',
    content: '正在调用 检索知识库',
  })
})

test('keeps error chunks red even when they also mark the stream done', () => {
  assert.deepEqual(parseWeKnoraStreamChunk(JSON.stringify({ response_type: 'error', content: 'agent unavailable', done: true })), {
    kind: 'error',
    content: 'agent unavailable',
    done: true,
  })
})

test('uses final_answer from complete event when the agent did not emit answer chunks', () => {
  assert.deepEqual(parseWeKnoraStreamChunk(JSON.stringify({ response_type: 'complete', data: { final_answer: '最终答案' }, done: true })), {
    kind: 'complete',
    content: '最终答案',
    done: true,
  })
})

test('marks done answer chunks as complete enough for video assistant unlock', () => {
  assert.deepEqual(parseWeKnoraStreamChunk(JSON.stringify({ response_type: 'answer', content: '可执行建议', done: true })), {
    kind: 'answer',
    content: '可执行建议',
    done: true,
  })
})

test('treats SSE DONE marker as completion', () => {
  assert.deepEqual(parseWeKnoraStreamChunk('[DONE]'), {
    kind: 'complete',
    done: true,
  })
})
