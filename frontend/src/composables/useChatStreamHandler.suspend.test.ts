import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import * as finalContent from '../utils/finalArtifactContent.ts'
import * as history from '../utils/rag-pipeline-history.ts'
import * as timestamps from '../utils/messageTimestamp.ts'
import * as steering from '../utils/steerStreamFork.ts'
import * as dataChange from '../utils/assistantDataChange.ts'
import type { useChatStreamHandler as StreamHandler } from './useChatStreamHandler.ts'

const source = readFileSync(new URL('./useChatStreamHandler.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const modules: Record<string, unknown> = {
  vue, 'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
  '@/utils/finalArtifactContent': finalContent,
  '@/utils/rag-pipeline-history': history,
  '@/utils/messageTimestamp': timestamps,
  '@/utils/steerStreamFork': steering,
  '@/utils/assistantDataChange': dataChange,
}
const exports: { useChatStreamHandler?: typeof StreamHandler } = {}
vm.runInNewContext(compiled, {
  exports, console,
  require: (id: string) => {
    assert.ok(id in modules, `unexpected import ${id}`)
    return modules[id]
  },
})
const useChatStreamHandler = exports.useChatStreamHandler!

function setup() {
  const messagesList = vue.reactive<Record<string, any>[]>([])
  const loading = vue.ref(false), isReplying = vue.ref(false)
  const currentAssistantMessageId = vue.ref(''), fullContent = vue.ref('')
  const turns: unknown[] = []
  const handler = useChatStreamHandler({
    messagesList, loading, isReplying, currentAssistantMessageId, fullContent,
    isAgentStreamSession: () => true, scrollToBottom() {},
    onTurnComplete: (m) => turns.push(m),
    preserveIncompleteStreamReactive: true,
  })
  return { handler, messagesList, isReplying, currentAssistantMessageId, turns }
}

const awaitingRow = () => ({
  id: 'a1', request_id: 'r1', role: 'assistant', content: '', is_completed: false,
  run_state: 'awaiting_input', agent_duration_ms: 1200,
  agent_steps: [{ iteration: 0, thought: 'checking', tool_calls: [{ id: 'call-1', name: 'mcp_x', args: {} }] }],
  pending_prompt: {
    id: 'p1', kind: 'tool_approval', tool_call_id: 'call-1', created_at: '2026-10-13T08:00:00Z',
    expires_at: '2026-10-20T08:00:00Z',
    request: { service_name: 'svc', mcp_tool_name: 'delete', args: { id: 7 } },
  },
})

const cards = (m: Record<string, any>) =>
  (m.agentEventStream as Record<string, any>[]).filter((e) => e.type === 'tool_approval_required')

test('history renders a suspended message as settled with its pending card', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([awaitingRow()])
  const m = messagesList[0]
  assert.equal(m.is_completed, true)
  const stream = m.agentEventStream as Record<string, any>[]
  assert.ok(!stream.some((e) => e.type === 'stop' || e.type === 'agent_complete'))
  const [card] = cards(m)
  assert.equal(card.pending_id, 'p1')
  assert.equal(card.suspended, true)
  assert.equal(card.args_json, '{"id":7}')
  assert.equal(card.requested_at, Date.parse('2026-10-13T08:00:00Z') / 1000)
})

test('a live run that suspends parks its card and ends without completion', () => {
  const { handler, messagesList, isReplying, turns } = setup()
  isReplying.value = true
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  handler.processStreamChunk({
    response_type: 'tool_approval_required', id: 'r1',
    data: { pending_id: 'p1', tool_call_id: 'call-1', suspendable: true, hot_wait_seconds: 120 },
  })
  handler.processStreamChunk({
    response_type: 'run_suspended', id: 'r1', data: { pending_id: 'p1', expires_at: '2026-10-20T08:00:00Z' },
  })
  handler.processStreamChunk({ response_type: 'complete', id: 'r1', data: { total_duration_ms: 5 } })
  const m = messagesList[0]
  const [card] = cards(m)
  assert.equal(card.suspendable, true)
  assert.equal(card.hot_wait_seconds, 120)
  assert.equal(card.suspended, true)
  assert.equal(m.run_state, 'awaiting_input')
  assert.equal(isReplying.value, false)
  assert.ok(!(m.agentEventStream as Record<string, any>[]).some((e) => e.type === 'agent_complete'))
  assert.equal(turns.length, 1)
})

test('a resumed run streams into the suspended message', async () => {
  const { handler, messagesList, currentAssistantMessageId } = setup()
  await handler.handleMsgList([awaitingRow()])
  const m = messagesList[0]
  handler.prepareResumedMessage(m, 'p1')
  assert.equal(m.is_completed, false)
  assert.equal(m.run_state, '')
  assert.ok(!(m.agentEventStream as Record<string, any>[]).some(
    (e) => e.type === 'tool_call' && e.tool_call_id === 'call-1',
  ))
  currentAssistantMessageId.value = 'a1'
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r2', assistant_message_id: 'a1' })
  handler.processStreamChunk({ response_type: 'run_resumed', id: 'r2', data: { pending_id: 'p1' } })
  handler.processStreamChunk({
    response_type: 'tool_approval_resolved', id: 'r2', data: { pending_id: 'p1', approved: true },
  })
  handler.processStreamChunk({ response_type: 'complete', id: 'r2', data: { total_duration_ms: 9 } })
  assert.equal(messagesList.length, 1)
  const [card] = cards(m)
  assert.equal(card.resolved, true)
  assert.equal(card.approved, true)
  assert.equal(m.is_completed, true)
  assert.ok((m.agentEventStream as Record<string, any>[]).some((e) => e.type === 'agent_complete'))
})

test('a new message abandons the prompt a suspended run waits on', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([awaitingRow()])
  handler.prepareForNewOutgoingMessage()
  const m = messagesList[0]
  const [card] = cards(m)
  assert.equal(card.resolved, true)
  assert.equal(card.abandoned, true)
  assert.equal(m.run_state, '')
})

const prompts = (m: Record<string, any>) =>
  (m.agentEventStream as Record<string, any>[]).filter((e) => e.type === 'agent_prompt_required')

test('a question card follows the prompt events of a live run', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  handler.processStreamChunk({
    response_type: 'agent_prompt_required', id: 'r1',
    data: { pending_id: 'q1', kind: 'ask_user', tool_call_id: 'call-2', request: { question: 'Which?' } },
  })
  handler.processStreamChunk({
    response_type: 'agent_prompt_resolved', id: 'r1',
    data: { pending_id: 'q1', kind: 'ask_user', answered: true, response: { text: 'this one' } },
  })
  const [card] = prompts(messagesList[0])
  assert.equal(card.kind, 'ask_user')
  assert.equal(card.request.question, 'Which?')
  assert.equal(card.resolved, true)
  assert.equal(card.answered, true)
  assert.deepEqual(card.response, { text: 'this one' })
})

test('history shows a pending question and turns answered ones into cards', async () => {
  const { handler, messagesList } = setup()
  const row = awaitingRow()
  row.agent_steps = [{
    iteration: 0, thought: '',
    tool_calls: [
      { id: 'call-0', name: 'ask_user', args: {}, result: { success: true, output: 'x', data: {
        display_type: 'ask_user', question: { question: 'First?' }, answered: false,
        reason: 'the user declined to answer',
      } } },
      { id: 'call-1', name: 'ask_user', args: {} },
    ],
  }] as any
  row.pending_prompt = {
    id: 'q2', kind: 'ask_user', tool_call_id: 'call-1', created_at: '2026-10-13T08:00:00Z',
    expires_at: '2026-10-20T08:00:00Z', request: { question: 'Second?' },
  } as any
  await handler.handleMsgList([row])
  const m = messagesList[0]
  const [answered, pending] = prompts(m)
  assert.equal(answered.request.question, 'First?')
  assert.equal(answered.resolved, true)
  assert.equal(answered.answered, false)
  assert.equal(answered.resolve_reason, 'the user declined to answer')
  assert.ok(!(m.agentEventStream as Record<string, any>[]).some(
    (e) => e.type === 'tool_call' && e.tool_call_id === 'call-0',
  ))
  assert.equal(pending.pending_id, 'q2')
  assert.equal(pending.suspended, true)
  assert.equal(pending.request.question, 'Second?')
  handler.prepareForNewOutgoingMessage()
  assert.equal(prompts(m)[1].abandoned, true)
})

test('a prompt without display steps remains answerable after orphan recovery', async () => {
  const { handler, messagesList } = setup()
  const row = awaitingRow()
  row.agent_steps = []
  await handler.handleMsgList([row])
  const message = messagesList[0]
  assert.equal(message.isAgentMode, true)
  assert.equal(message.is_completed, true)
  assert.equal(cards(message).length, 1)
  assert.equal(cards(message)[0].pending_id, 'p1')
})
