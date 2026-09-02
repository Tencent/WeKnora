import assert from 'node:assert/strict'
import test from 'node:test'
import { buildChatRequest, normalizeChatError } from './chatRequest'
import { appendSelectedTenantHeader } from '../../utils/tenantHeaders'
import {
  displayQuestionFromStoredContent,
  mergeLocalTurnWithStoredMessages,
  parseWeKnoraStreamChunk,
  shouldAbortStream,
} from './chatStream'

test('shows only the user question when a video-scoped prompt is loaded from WeKnora history', () => {
  const stored = [
    '用户正在视频详情页围绕《个人知识库，还得是Obsidian+AI Agent》提问。当前视频 ID：49068459-82f3-4acd-9dba-0adb821f7607。',
    '必须先使用 Wiki 搜索读取当前视频已审计通过的知识对象页面，优先引用页面中的可读正文和结构化字段。',
    '只有 Wiki 页面不存在、检索不到或无法回答时，才使用当前视频同一转写代次的句级分块作为证据回溯；不得把未审计页面当作事实来源。',
    '回答必须区分知识对象结论与转写原文证据；引用来源时保留 Wiki 页面或转写分块的真实来源。',
    '用户问题：总结这段视频的核心观点',
  ].join('\n')

  assert.equal(displayQuestionFromStoredContent(stored), '总结这段视频的核心观点')
})

test('keeps previous rounds and appends the current local round when stored history lags', () => {
  const merged = mergeLocalTurnWithStoredMessages(
    [
      { id: 'u1', sender: 'user', text: '第一问', timestamp: '10:00' },
      { id: 'a1', sender: 'assistant', text: '第一答', timestamp: '10:01' },
    ],
    { id: 'u2-local', sender: 'user', text: '第二问', timestamp: '10:02' },
    { id: 'a2-local', sender: 'assistant', text: '第二答', timestamp: '10:03' },
  )

  assert.deepEqual(merged.map(message => message.text), ['第一问', '第一答', '第二问', '第二答'])
})

test('appends the streamed assistant answer after the stored current user message', () => {
  const merged = mergeLocalTurnWithStoredMessages(
    [
      { id: 'u1', sender: 'user', text: '第一问', timestamp: '10:00' },
      { id: 'a1', sender: 'assistant', text: '第一答', timestamp: '10:01' },
      { id: 'u2-stored', sender: 'user', text: '第二问', timestamp: '10:02' },
    ],
    { id: 'u2-local', sender: 'user', text: '第二问', timestamp: '10:02' },
    { id: 'a2-local', sender: 'assistant', text: '第二答', timestamp: '10:03' },
  )

  assert.deepEqual(merged.map(message => message.id), ['u1', 'a1', 'u2-stored', 'a2-local'])
})

test('updates the stored current assistant with the streamed answer while preserving its identity', () => {
  const merged = mergeLocalTurnWithStoredMessages(
    [
      { id: 'u1', sender: 'user', text: '第一问', timestamp: '10:00' },
      { id: 'a1', sender: 'assistant', text: '第一答', timestamp: '10:01' },
      { id: 'u2-stored', sender: 'user', text: '第二问', timestamp: '10:02' },
      { id: 'a2-stored', sender: 'assistant', text: '正在整合知识', timestamp: '10:03' },
    ],
    { id: 'u2-local', sender: 'user', text: '第二问', timestamp: '10:02' },
    { id: 'a2-local', sender: 'assistant', text: '第二答', timestamp: '10:04' },
  )

  assert.equal(merged[3].id, 'a2-stored')
  assert.equal(merged[3].timestamp, '10:03')
  assert.equal(merged[3].text, '第二答')
})

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

test('does not abort the SSE stream when the agent query event itself is done', () => {
  assert.equal(
    shouldAbortStream(JSON.stringify({ response_type: 'agent_query', done: true })),
    false,
  )
  assert.equal(
    shouldAbortStream(JSON.stringify({ response_type: 'answer', content: '答案', done: true })),
    false,
  )
})

test('aborts the SSE stream only on a terminal completion marker', () => {
  assert.equal(
    shouldAbortStream(JSON.stringify({ response_type: 'complete', done: true })),
    true,
  )
  assert.equal(shouldAbortStream('[DONE]'), true)
})

test('treats SSE DONE marker as completion', () => {
  assert.deepEqual(parseWeKnoraStreamChunk('[DONE]'), {
    kind: 'complete',
    done: true,
  })
})

test('uses the configured resource tenant for Agent requests', () => {
  const request = buildChatRequest(
    {
      scope: 'global',
      agent_id: 'agent-1',
      knowledge_base_ids: ['kb-1'],
      knowledge_ids: [],
      tenant_id: '10000',
    },
    '请介绍一下视频内容',
    'jwt-token',
    '10003',
  )

  assert.equal(request.headers.Authorization, 'Bearer jwt-token')
  assert.equal(request.headers['X-Tenant-ID'], '10000')
  assert.equal(request.body.agent_id, 'agent-1')
  assert.equal(request.body.agent_enabled, true)
})

test('keeps transcript chunk scope on video Agent requests for Wiki source_refs fallback', () => {
  const request = buildChatRequest(
    {
      scope: 'video',
      agent_id: 'agent-1',
      knowledge_base_ids: ['kb-1'],
      knowledge_ids: ['chunk-1', 'chunk-2'],
      tenant_id: '10000',
    },
    '这个视频的核心方法是什么',
    'jwt-token',
  )

  assert.deepEqual(request.body.knowledge_ids, ['chunk-1', 'chunk-2'])
})

test('preserves an explicit tenant header instead of replacing it with the selected tenant', () => {
  const headers = { 'X-Tenant-ID': '10000' }
  appendSelectedTenantHeader(headers, '10003')
  assert.equal(headers['X-Tenant-ID'], '10000')

  const lowerCaseHeaders = { 'x-tenant-id': '10000' }
  appendSelectedTenantHeader(lowerCaseHeaders, '10003')
  assert.equal(lowerCaseHeaders['x-tenant-id'], '10000')

  const emptyHeaders: Record<string, string> = {}
  appendSelectedTenantHeader(emptyHeaders, '10003')
  assert.equal(emptyHeaders['X-Tenant-ID'], '10003')
})

test('turns a workspace permission failure into an actionable chat error', () => {
  assert.equal(
    normalizeChatError({ status: 403, message: 'forbidden' }).message,
    '当前账号没有访问视频知识库所属工作空间的权限，请联系管理员加入该工作空间',
  )
})
