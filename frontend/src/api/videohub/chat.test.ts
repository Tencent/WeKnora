import assert from 'node:assert/strict'
import test from 'node:test'
import { buildChatRequest, normalizeChatError } from './chatRequest'
import { appendSelectedTenantHeader } from '../../utils/tenantHeaders'
import { displayQuestionFromStoredContent, mergeLocalTurnWithStoredMessages, parseWeKnoraStreamChunk } from './chatStream'

test('shows only the user question when a video-scoped prompt is loaded from WeKnora history', () => {
  const stored = [
    '用户正在视频详情页围绕《个人知识库，还得是Obsidian+AI Agent》提问。当前视频 ID：49068459-82f3-4acd-9dba-0adb821f7607。',
    '请优先检索并引用当前视频的转写内容；如果当前视频信息不足，必须允许使用同一知识库中的其他视频或全局知识补充。',
    '引用当前视频之外的内容时，必须明确标注来源视频，不要让用户误以为全部来自当前视频。',
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
