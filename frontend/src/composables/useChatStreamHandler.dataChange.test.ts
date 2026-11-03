import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import * as finalContent from '../utils/finalArtifactContent.ts'
import * as history from '../utils/rag-pipeline-history.ts'
import * as timestamps from '../utils/messageTimestamp.ts'
import * as steering from '../utils/steerStreamFork.ts'
import { change, fixture } from '../test-utils/vueFixture.ts'

function setup(t: TestContext) {
  const f = fixture(t)
  const listener = t.mock.fn()
  f.window.addEventListener(f.dataChange.ASSISTANT_DATA_CHANGE_EVENT, listener)
  const { useChatStreamHandler } = f.evaluate(readFileSync(new URL('./useChatStreamHandler.ts', import.meta.url), 'utf8'), {
    vue, 'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/utils/finalArtifactContent': finalContent,
    '@/utils/rag-pipeline-history': history,
    '@/utils/messageTimestamp': timestamps,
    '@/utils/steerStreamFork': steering,
    '@/utils/assistantDataChange': f.dataChange,
  })
  const handler = useChatStreamHandler({
    messagesList: vue.reactive([]), loading: vue.ref(false), isReplying: vue.ref(false),
    currentAssistantMessageId: vue.ref(''), fullContent: vue.ref(''),
    isAgentStreamSession: () => true, scrollToBottom() {},
  })
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  handler.processStreamChunk({ response_type: 'tool_call', id: 'r1', data: { tool_call_id: 'call', tool_name: 'run_operation' } })
  return { ...handler, listener }
}

test('emits the normalized marker from a successful live SSE tool result', t => {
  const handler = setup(t)
  handler.processStreamChunk({ response_type: 'tool_result', id: 'r1', data: {
    tool_call_id: 'call', tool_name: 'run_operation', success: true,
    data_change: change('agent.update', { agent_id: ['real-agent'] }),
  } })
  assert.equal(handler.listener.mock.callCount(), 1)
  assert.deepEqual(Array.from(handler.listener.mock.calls[0].arguments[0].detail.ids.agentIds), ['real-agent'])
})

test('emits the marker of a Wiki edit', t => {
  const handler = setup(t)
  handler.processStreamChunk({ response_type: 'tool_result', id: 'r1', data: {
    tool_call_id: 'call', tool_name: 'wiki_write_page', success: true,
    data_change: change('wiki.write_page', { knowledge_base_id: ['kb'] }),
  } })
  assert.equal(handler.listener.mock.callCount(), 1)
})

for (const operation of ['automation.create', 'automation.update', 'automation.pause', 'automation.run_now']) {
  test(`forwards ${operation} changes without requiring resource IDs`, t => {
    const handler = setup(t)
    handler.processStreamChunk({ response_type: 'tool_result', id: 'r1', data: {
      tool_call_id: 'call', tool_name: 'run_operation', success: true,
      data_change: { operation, area: 'automation' },
    } })
    assert.equal(handler.listener.mock.callCount(), 1)
    assert.equal(handler.listener.mock.calls[0].arguments[0].detail.operation, operation)
    assert.equal(handler.listener.mock.calls[0].arguments[0].detail.area, 'automation')
  })
}

for (const data of [
  { success: false, data_change: change('agent.delete') },
  { success: true },
  { success: true, data_change: { area: 'agent' } },
  { success: true, tool_name: 'external_tool', data_change: change('agent.delete') },
  { data_change: change('agent.delete') },
]) {
  test(`does not emit on failure, missing/malformed markers or other tools: ${JSON.stringify(data)}`, t => {
    const handler = setup(t)
    handler.processStreamChunk({ response_type: 'tool_result', id: 'r1', data: {
      tool_call_id: 'call', tool_name: 'run_operation', ...data,
    } })
    assert.equal(handler.listener.mock.callCount(), 0)
  })
}

test('does not emit when loading history even if a legacy stored result contains the marker', async t => {
  const handler = setup(t)
  await handler.handleMsgList([{
    id: 'old', role: 'assistant', content: '', is_completed: true,
    agent_steps: [{ iteration: 0, tool_calls: [{
      id: 'old-call', name: 'run_operation', args: {},
      result: { success: true, output: 'done', data: { data_change: change('agent.delete') } },
    }] }],
  }])
  assert.equal(handler.listener.mock.callCount(), 0)
})
