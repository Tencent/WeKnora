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
import * as thinkBlocks from '../utils/thinkBlocks.ts'
import * as hostApproval from '../views/chat/components/hostApproval.ts'
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
  '@/utils/thinkBlocks': thinkBlocks,
  '@/views/chat/components/hostApproval': hostApproval,
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

test('parallel child approvals reload on a running parent without suspending it', async () => {
  const { handler, messagesList } = setup()
  const base = awaitingRow() as Record<string, any>
  base.run_state = ''
  base.is_completed = false
  const prompt = base.pending_prompt
  delete base.pending_prompt
  base.delegated_prompts = [
    { ...prompt, id: 'child-1', delegated_task: 'research' },
    { ...prompt, id: 'child-2', delegated_task: 'writer' },
  ]
  await handler.handleMsgList([base])
  const m = messagesList[0]
  assert.equal(m.run_state, '')
  assert.equal(m.is_completed, false)
  assert.deepEqual(Array.from(cards(m), (p) => p.pending_id), ['child-1', 'child-2'])
  assert.equal(cards(m)[0].delegated_task, 'research')
  handler.processStreamChunk({ response_type: 'tool_approval_required', id: 'r1', data: {
    pending_id: 'child-1', suspended: true, suspendable: true,
  } })
  assert.equal(cards(m).length, 2)
})

test('forwarded live approval is parked while parent keeps streaming', () => {
  const { handler, messagesList, isReplying } = setup()
  isReplying.value = true
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  handler.processStreamChunk({ response_type: 'tool_approval_required', id: 'r1', data: {
    pending_id: 'child', suspended: true, suspendable: true, description: 'writer: change a page',
  } })
  assert.equal(cards(messagesList[0])[0].suspended, true)
  assert.equal(isReplying.value, true)
  assert.notEqual(messagesList[0].run_state, 'awaiting_input')
})

test('child approval resolutions after steering update the original segment and not another turn', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  for (const pending_id of ['child-a', 'child-b']) {
    handler.processStreamChunk({ response_type: 'tool_approval_required', id: 'r1', data: { pending_id } })
  }
  handler.processStreamChunk({ response_type: 'agent_prompt_required', id: 'r1', data: { pending_id: 'child-question', kind: 'ask_user' } })
  const original = messagesList[0]
  handler.processStreamChunk({ response_type: 'user_message_injected', id: 'r1', data: {
    steer_id: 's1', user_message_id: 'u1', content: 'cancel child-a only',
  } })
  assert.equal(original.steerForked, true)
  // Replaying a required event after a segment split must not clone its card.
  handler.processStreamChunk({ response_type: 'tool_approval_required', id: 'r1', data: { pending_id: 'child-a' } })
  const foreign = { id: 'another-turn', role: 'assistant', is_completed: true, agentEventStream: [
    { type: 'tool_approval_required', pending_id: 'child-a', resolved: false },
  ] }
  messagesList.unshift(foreign)
  handler.processStreamChunk({ response_type: 'tool_approval_resolved', id: 'r1', data: { pending_id: 'child-a', approved: false, canceled: true } })
  assert.equal(cards(original)[0].resolved, true)
  assert.equal(cards(original)[0].canceled, true)
  assert.equal(cards(original)[1].resolved, false)
  handler.processStreamChunk({ response_type: 'agent_prompt_resolved', id: 'r1', data: { pending_id: 'child-question', answered: true } })
  assert.equal(original.agentEventStream.find((e: any) => e.pending_id === 'child-question').resolved, true)
  handler.processStreamChunk({ response_type: 'tool_approval_resolved', id: 'r1', data: { pending_id: 'child-b', approved: true } })
  assert.equal(cards(original)[1].approved, true)
  assert.equal(messagesList.length, 4)
  assert.equal(cards(messagesList[3]).length, 0)
  assert.equal(cards(foreign)[0].resolved, false)
})

test('stopping a suspended turn closes its cards even when its display row is completed', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([awaitingRow()])
  handler.markInFlightAssistantStopped('a1')
  assert.equal(messagesList[0].run_state, '')
  assert.equal(cards(messagesList[0])[0].resolved, true)
  assert.equal(cards(messagesList[0])[0].canceled, true)
  assert.equal(messagesList[0].is_completed, true)
})


test('stop still clears cards after a stream watcher has already settled the row', () => {
  const { handler, messagesList } = setup()
  messagesList.push({ id: 'a1', role: 'assistant', is_completed: true, run_state: '',
    agentEventStream: [{type: 'tool_approval_required', pending_id: 'p1', suspended: true}, {type: 'tool_call', pending: true}] })
  handler.markInFlightAssistantStopped()
  assert.equal(messagesList[0].agentEventStream[1].pending, false)
  assert.equal(cards(messagesList[0])[0].resolved, true)
  assert.equal(cards(messagesList[0])[0].canceled, true)
})

test('rehydrated in-flight steps remain reactive while tools and thinking stream in', async () => {
  const { handler, messagesList, currentAssistantMessageId } = setup()
  const row = awaitingRow()
  row.run_state = ''
  await handler.handleMsgList([row])
  currentAssistantMessageId.value = 'a1'
  const m = messagesList[0]
  const observed = vue.computed(() => (m.agentEventStream as any[]).map(e =>
    `${e.type}:${e.content || ''}:${e.pending}:${e.success}`).join('|'))
  const before = observed.value
  assert.doesNotMatch(before, /agent_complete/, 'an elapsed duration must not settle a running timeline')
  handler.processStreamChunk({ response_type: 'thinking', id: 'r1', content: 'new thought',
    done: false, data: { event_id: 'thinking-2' } })
  assert.notEqual(observed.value, before, 'new timeline rows must invalidate the render')
  handler.processStreamChunk({ response_type: 'thinking', id: 'r1', content: ' continues',
    done: false, data: { event_id: 'thinking-2' } })
  assert.match(observed.value, /new thought continues/)
  handler.processStreamChunk({ response_type: 'tool_call', id: 'r1',
    data: { tool_call_id: 'call-2', tool_name: 'search', arguments: {} } })
  assert.match(observed.value, /tool_call::true/)
  handler.processStreamChunk({ response_type: 'tool_result', id: 'r1',
    data: { tool_call_id: 'call-2', tool_name: 'search', success: true, output: 'found' } })
  assert.match(observed.value, /tool_call::false:true/)
})


test('resolved approvals reload once in timeline order before completion and never become pending', async () => {
  const { handler, messagesList } = setup()
  const prompt = { id: 'approved', kind: 'tool_approval', status: 'done',
    tool_call_id: 'call-1', created_at: '2026-10-13T08:00:01Z',
    request: { service_name: 'KM', mcp_tool_name: 'hot-articles', args: { limit: 1 } },
    response: { approved: true, modified_args: { limit: 2 } } }
  await handler.handleMsgList([{ id: 'a', role: 'assistant', is_completed: true,
    content: 'final', agent_duration_ms: 5000,
    agent_steps: [{ iteration: 0, timestamp: '2026-10-13T08:00:00Z', thought: 'starting',
      tool_calls: [{ id: 'call-1', name: 'mcp_x', args: {}, result: { success: true } }] }],
    prompt_history: [prompt], delegated_prompts: [prompt,
      { ...prompt, id: 'rejected', delegated_task: 'child', response: { approved: false } },
      { ...prompt, id: 'canceled', delegated_task: 'child', status: 'canceled', response: {} }],
  }])
  const m = messagesList[0]
  assert.equal(cards(m).length, 3)
  assert.ok(cards(m).every(c => c.resolved && !c.suspended))
  assert.equal(cards(m)[0].approved, true)
  assert.equal(cards(m)[0].args_json, '{"limit":2}')
  assert.equal(cards(m)[1].approved, false)
  assert.equal(cards(m)[2].canceled, true)
  const events = m.agentEventStream as Record<string, any>[]
  assert.ok(events.findIndex(e => e.pending_id === 'approved') < events.findIndex(e => e.type === 'tool_call'))
  assert.ok(events.findIndex(e => e.pending_id === 'canceled') < events.findIndex(e => e.type === 'agent_complete'))
  assert.equal(events.at(-1)?.content, 'final')
})

for (const prepared of [false, true]) {
  test(`same-request approval resume and reconnect retain the timeline (prepared=${prepared})`, async () => {
    const { handler, messagesList } = setup()
    await handler.handleMsgList([awaitingRow()])
    const m = messagesList[0]
    if (prepared) handler.prepareResumedMessage(m, 'p1')
    const resume = () => {
      handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1',
        data: { resume_pending_id: 'p1', resume_tool_call_id: 'call-1' } })
      handler.processStreamChunk({ response_type: 'tool_approval_resolved', id: 'r1',
        data: { pending_id: 'p1', approved: true } })
      handler.processStreamChunk({ response_type: 'tool_call', id: 'r1',
        data: { tool_call_id: 'call-1', tool_name: 'mcp_x', arguments: {} } })
      handler.processStreamChunk({ response_type: 'tool_result', id: 'r1',
        data: { tool_call_id: 'call-1', tool_name: 'mcp_x', success: true, output: 'approved result' } })
      handler.processStreamChunk({ response_type: 'answer', id: 'r1', content: 'after approval',
        data: { event_id: 'resume-progress' }, done: false })
    }
    resume()
    resume()
    resume()
    assert.equal(messagesList.length, 1)
    const events = m.agentEventStream as Record<string, any>[]
    assert.equal(events.filter(e => e.content === 'checking').length, 1)
    assert.equal(events.filter(e => e.type === 'tool_call' && e.tool_call_id === 'call-1').length, 1)
    assert.equal(events.filter(e => e.type === 'answer' && e.content === 'after approval').length, 1)
    assert.equal(cards(m).length, 1)
    assert.equal(cards(m)[0].approved, true)
    assert.equal(m.content, 'after approval')

    // A second approval creates a new suffix, retaining the first one's history.
    handler.processStreamChunk({ response_type: 'tool_approval_required', id: 'r1',
      data: { pending_id: 'p2', tool_call_id: 'call-2' } })
    handler.prepareResumedMessage(m, 'p2')
    const secondResume = () => {
      handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1',
        data: { resume_pending_id: 'p2', resume_tool_call_id: 'call-2' } })
      handler.processStreamChunk({ response_type: 'tool_approval_resolved', id: 'r1',
        data: { pending_id: 'p2', approved: true } })
    }
    secondResume()
    secondResume()
    assert.equal(cards(m).length, 2)
    assert.ok(cards(m).every(e => e.approved))
    assert.equal(m.agentEventStream.filter((e: any) => e.content === 'checking').length, 1)
    assert.equal(m.agentEventStream.filter((e: any) => e.content === 'after approval').length, 1)
  })
}

test('approval suffix replay preserves steer boundaries before and after suspension', async () => {
  const { handler, messagesList } = setup()
  const query = { response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' }
  const steer = (id: string) => handler.processStreamChunk({ response_type: 'user_message_injected', id: 'r1',
    data: { steer_id: id, user_message_id: id, content: id } })
  const answer = (text: string) => handler.processStreamChunk({ response_type: 'answer', id: 'r1',
    data: { event_id: text }, content: text, done: false })
  handler.processStreamChunk(query)
  answer('before-steer')
  steer('u-before')
  answer('before-approval')
  const replay = () => {
    handler.processStreamChunk({ ...query, data: { resume_pending_id: 'p1', resume_tool_call_id: 'c1' } })
    answer('after-approval')
    steer('u-after')
    answer('after-steer')
  }
  replay()
  replay()
  const assistants = messagesList.filter(m => m.role === 'assistant')
  assert.equal(assistants.length, 3)
  const texts = assistants.map(m => m.agentEventStream.filter((e: any) => e.type === 'answer').map((e: any) => e.content))
  assert.deepEqual(JSON.parse(JSON.stringify(texts)), [['before-steer'], ['before-approval', 'after-approval'], ['after-steer']])
})


test('reloaded history containing a later checkpoint does not duplicate the resumed suffix', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([awaitingRow()])
  const m = messagesList[0]
  m.agentEventStream.push({ type: 'answer', content: 'persisted suffix', event_id: 'suffix', done: true })
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1',
    data: { resume_pending_id: 'p1', resume_tool_call_id: 'call-1' } })
  handler.processStreamChunk({ response_type: 'answer', id: 'r1', content: 'persisted suffix',
    data: { event_id: 'suffix' }, done: false })
  assert.equal(m.agentEventStream.filter((e: any) => e.content === 'checking').length, 1)
  assert.equal(m.agentEventStream.filter((e: any) => e.content === 'persisted suffix').length, 1)
  assert.equal(cards(m).length, 1)
})


test('approval resume keeps updates that arrived while approval was pending', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([awaitingRow()])
  const m = messagesList[0]
  handler.prepareResumedMessage(m, 'p1')
  m.agentEventStream.push({ type: 'answer', content: 'another child finished', event_id: 'child-progress', done: true })
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1',
    data: { resume_pending_id: 'p1', resume_tool_call_id: 'call-1' } })
  assert.equal(m.agentEventStream.filter((e: any) => e.content === 'another child finished').length, 1)
})
