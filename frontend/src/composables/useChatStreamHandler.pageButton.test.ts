import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import * as finalContent from '../utils/finalArtifactContent.ts'
import * as history from '../utils/rag-pipeline-history.ts'
import * as timestamps from '../utils/messageTimestamp.ts'
import * as steering from '../utils/steerStreamFork.ts'
import * as hostApproval from '../views/chat/components/hostApproval.ts'
import * as thinkBlocks from '../utils/thinkBlocks.ts'
import { fixture } from '../test-utils/vueFixture.ts'

function setup(t: TestContext) {
  const f = fixture(t)
  const { useChatStreamHandler } = f.evaluate(readFileSync(new URL('./useChatStreamHandler.ts', import.meta.url), 'utf8'), {
    vue, 'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/utils/finalArtifactContent': finalContent,
    '@/utils/rag-pipeline-history': history,
    '@/utils/messageTimestamp': timestamps,
    '@/utils/steerStreamFork': steering,
    '@/utils/assistantDataChange': f.dataChange,
    '@/utils/thinkBlocks': thinkBlocks,
    '@/views/chat/components/hostApproval': hostApproval,
  })
  const messagesList = vue.reactive<any[]>([])
  const handler = useChatStreamHandler({
    messagesList, loading: vue.ref(false), isReplying: vue.ref(false),
    currentAssistantMessageId: vue.ref(''), fullContent: vue.ref(''),
    isAgentStreamSession: () => true, scrollToBottom() {},
  })
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  handler.processStreamChunk({ response_type: 'answer', id: 'r1', content: 'the report', data: { event_id: 'ans-1' } })
  const message = () => messagesList.find(m => m.role === 'assistant')
  const answer = () => message().agentEventStream.find((e: any) => e.event_id === 'ans-1')
  const call = (args: Record<string, unknown>, tool = 'open_page') => handler.processStreamChunk({
    response_type: 'tool_call', id: 'r1', data: { tool_call_id: 'page', tool_name: tool, arguments: args },
  })
  return { handler, message, answer, call }
}

test('a page button keeps the answer written before it', t => {
  const { message, answer, call } = setup(t)
  call({ page: 'agents' })
  assert.equal(answer().type, 'answer')
  assert.ok(!answer().superseded)
  assert.equal(message().content, 'the report')
})

test('viewing a page is more work, so the text before it was a preamble', t => {
  const { answer, call } = setup(t)
  call({ page: 'settings' }, 'view_page')
  assert.equal(answer().superseded, true)
})

test('a progress event retracts the answer when the turn goes on', t => {
  const { handler, message, answer, call } = setup(t)
  call({ page: 'agents' })
  handler.processStreamChunk({ response_type: 'progress', id: 'r1', content: 'the report', done: true, data: { event_id: 'ans-1' } })
  assert.equal(answer().type, 'progress')
  assert.equal(message().content, '')
})
