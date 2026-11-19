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
  return { handler, messagesList, isReplying, currentAssistantMessageId, turns, fullContent }
}

const task = (status = 'running', run_id = 'child-run-1') => ({
  id: 'child', name: 'research', agent_id: 'agent', session_id: 'session', run_id, status,
  progress: { phase: 'tool', tool_name: 'search', iteration: 1 }, prompt_id: '',
})

test('collaboration snapshots reject duplicate/late revisions without reviving terminal runs', () => {
  const { handler, messagesList, isReplying } = setup()
  isReplying.value = true
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  const send = (revision: number, status: string, runId = 'child-run-1') => handler.processStreamChunk({
    response_type: 'collaboration_status', id: 'r1',
    data: { group_id: 'group', revision, tasks: [task(status, runId)] },
  })
  send(3, 'waiting')
  const waiting = messagesList[0].collaborationStatus
  send(2, 'running')
  send(3, 'running')
  assert.equal(messagesList[0].collaborationStatus, waiting)
  send(4, 'succeeded')
  send(5, 'running')
  assert.equal(messagesList[0].collaborationStatus.tasks[0].status, 'succeeded')
  assert.equal(messagesList[0].collaborationStatus.revision, 5)
  send(6, 'queued', 'child-run-2')
  assert.equal(messagesList[0].collaborationStatus.tasks[0].status, 'queued')
  assert.equal(messagesList[0].collaborationStatus.tasks[0].run_id, 'child-run-2')
  assert.equal(isReplying.value, true)
  assert.equal(messagesList[0].content, '')
  assert.equal(messagesList[0].is_completed, false)
})

test('delayed collaboration updates bind to their parent request, not the latest active turn', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r2', assistant_message_id: 'a2' })
  handler.processStreamChunk({ response_type: 'collaboration_status', id: 'r1',
    data: { group_id: 'g1', revision: 20, tasks: [task('succeeded')] } })
  handler.processStreamChunk({ response_type: 'collaboration_status', id: 'r2',
    data: { group_id: 'g2', revision: 1, tasks: [task('waiting')] } })
  handler.processStreamChunk({ response_type: 'collaboration_status', id: 'unknown-request',
    data: { group_id: 'g1', revision: 30, tasks: [task()] } })
  assert.equal(messagesList.length, 2)
  assert.equal(messagesList[0].collaborationStatus.group_id, 'g1')
  assert.equal(messagesList[0].collaborationStatus.tasks[0].status, 'succeeded')
  assert.equal(messagesList[1].collaborationStatus.group_id, 'g2')
  assert.equal(messagesList[1].collaborationStatus.revision, 1)
  assert.equal(messagesList[1].collaborationStatus.tasks[0].status, 'waiting')
})

test('steering does not reset collaboration revision or terminal state', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  const send = (revision: number, status: string) => handler.processStreamChunk({
    response_type: 'collaboration_status', id: 'r1',
    data: { group_id: 'g1', revision, tasks: [task(status)] },
  })
  send(5, 'failed')
  handler.processStreamChunk({ response_type: 'user_message_injected', id: 'r1',
    data: { steer_id: 's1', user_message_id: 'u1', content: 'continue' } })
  send(4, 'running')
  assert.equal(messagesList[2].collaborationStatus.revision, 5)
  assert.equal(messagesList[0].collaborationStatus, undefined)
  send(6, 'running')
  const updated = messagesList.at(-1)!
  assert.equal(updated.collaborationStatus.tasks[0].status, 'failed')
  assert.equal(messagesList.filter(m => m.collaborationStatus).length, 1)
})

test('history hydrates collaboration status once on the last steer segment', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([{
    id: 'a1', request_id: 'r1', role: 'assistant', content: 'Finished', is_completed: true,
    collaboration_status: { group_id: 'g1', revision: 9, tasks: [task('succeeded')] },
    agent_steps: [{ iteration: 0, progress: 'Checking', progress_event_id: 'p1' },
      { iteration: 1, user_messages_before: ['u1'] }],
  }, { id: 'u1', request_id: 'r1', role: 'user', content: 'Continue', created_at: '2026-10-17T01:01:00Z' }])
  assert.equal(messagesList.length, 3)
  assert.equal(messagesList[0].collaborationStatus, undefined)
  assert.equal(messagesList[2].collaborationStatus.revision, 9)
  assert.equal(messagesList.filter(m => m.collaborationStatus).length, 1)
  handler.processStreamChunk({ response_type: 'collaboration_status', id: 'r1',
    data: { group_id: 'g1', revision: 8, tasks: [task('running')] } })
  assert.equal(messagesList[2].collaborationStatus.tasks[0].status, 'succeeded')
})

test('progress snapshots replay idempotently and remain separate from reasoning and final answer', () => {
  const { handler, messagesList, fullContent } = setup()
  const query = { response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' }
  handler.processStreamChunk(query)
  const progress = { response_type: 'progress', id: 'r1', content: 'Searching sources', done: true,
    data: { event_id: 'progress-1', iteration: 0 } }
  handler.processStreamChunk(progress)
  handler.processStreamChunk(query)
  handler.processStreamChunk(progress)
  handler.processStreamChunk({ ...progress, content: 'Earlier partial text', done: false })
  handler.processStreamChunk({ ...progress, content: 'Checking the findings', data: { event_id: 'progress-2', iteration: 0 } })
  handler.processStreamChunk({ response_type: 'thinking', id: 'r1', content: 'Private reasoning', data: { event_id: 'reasoning-1' } })
  handler.processStreamChunk({ response_type: 'answer', id: 'r1', content: 'Final answer', data: { event_id: 'answer-1' } })
  const message = messagesList[0]
  const events = message.agentEventStream as Record<string, any>[]
  assert.equal(events.filter(e => e.type === 'progress').length, 2)
  assert.equal(events[0].content, 'Searching sources')
  assert.equal(events[1].content, 'Checking the findings')
  assert.equal(events.find(e => e.type === 'thinking')?.content, 'Private reasoning')
  assert.equal(message.content, 'Final answer')
  assert.ok(!fullContent.value.includes('Searching sources'))
})

test('progress replay after steering does not duplicate an earlier segment', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  const progress = { response_type: 'progress', id: 'r1', content: 'Working', done: true,
    data: { event_id: 'p1', iteration: 1 } }
  handler.processStreamChunk(progress)
  handler.processStreamChunk({ response_type: 'user_message_injected', id: 'r1',
    data: { steer_id: 's1', user_message_id: 'u1', content: 'more' } })
  handler.processStreamChunk(progress)
  handler.processStreamChunk({ ...progress, id: 'missing-request', data: { event_id: 'p2' } })
  const progressEvents = messagesList.flatMap(m => m.agentEventStream || []).filter(e => e.type === 'progress')
  assert.equal(progressEvents.length, 1)
  assert.equal(messagesList.length, 3)
})

test('progress reclassifies a provisional answer with the same ID and ignores its replay', () => {
  const { handler, messagesList, fullContent } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  const provisional = { response_type: 'answer', id: 'r1', content: 'I will search', done: true, data: { event_id: 'p1' } }
  handler.processStreamChunk(provisional)
  handler.processStreamChunk({ ...provisional, response_type: 'progress', data: { event_id: 'p1', iteration: 1 } })
  handler.processStreamChunk(provisional)
  assert.equal(messagesList[0].agentEventStream.length, 1)
  assert.equal(messagesList[0].agentEventStream[0].type, 'progress')
  assert.equal(messagesList[0].agentEventStream[0].content, 'I will search')
  assert.equal(messagesList[0].content, '')
  assert.equal(fullContent.value, '')
  handler.processStreamChunk({ ...provisional, content: 'The result', data: { event_id: 'final' } })
  assert.equal(messagesList[0].content, 'The result')
  assert.equal(fullContent.value, 'The result')
})

test('history restores step progress once, independently of reasoning and the final answer', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([{
    id: 'a1', request_id: 'r1', role: 'assistant', content: 'Final answer', is_completed: true,
    agent_steps: [{ iteration: 0, reasoning_content: 'Private reasoning', thought: 'Checking sources',
      progress: 'Checking sources', progress_event_id: 'stable-progress-id', intermediate_answer: true,
      tool_calls: [{ id: 'call-1', name: 'search', args: {} }] }],
  }])
  const message = messagesList[0]
  const events = message.agentEventStream as Record<string, any>[]
  assert.equal(events.filter(e => e.content === 'Checking sources').length, 1)
  assert.equal(events.find(e => e.type === 'progress')?.content, 'Checking sources')
  assert.equal(events.find(e => e.type === 'thinking')?.content, 'Private reasoning')
  assert.equal(events.filter(e => e.type === 'answer').length, 1)
  assert.equal(message.content, 'Final answer')
  handler.processStreamChunk({ response_type: 'progress', id: 'r1', content: 'Checking sources', done: true,
    data: { event_id: 'stable-progress-id', iteration: 0 } })
  assert.equal(events.filter(e => e.type === 'progress').length, 1)
})

test('a result with an unmatched call ID cannot complete another pending call with the same name', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  for (const id of ['call-a', 'call-b']) handler.processStreamChunk({ response_type: 'tool_call', id: 'r1',
    data: { tool_call_id: id, tool_name: 'search', arguments: {} } })
  const result = (tool_call_id?: string) => handler.processStreamChunk({ response_type: 'tool_result', id: 'r1',
    data: { tool_call_id, tool_name: 'search', success: true, output: tool_call_id || 'legacy' } })
  result('missing')
  const calls = messagesList[0].agentEventStream as Record<string, any>[]
  assert.equal(calls[0].pending, true)
  assert.equal(calls[1].pending, true)
  result('call-b')
  result('call-b') // A replayed result must not consume the still-pending sibling.
  assert.equal(calls[0].pending, true)
  assert.equal(calls[1].pending, false)
  result()
  assert.equal(calls[0].pending, false)
  assert.equal(calls[0].output, 'legacy')
})


test('protocol answer replacement retracts only its event and never replays the prefix as progress', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  const answer = (event_id: string, content: string, replace = false) => handler.processStreamChunk({ response_type: 'answer', id: 'r1', content, data: { event_id, replace } })
  answer('prior', 'Earlier legitimate result.')
  answer('bad', 'refs\nprivate planning')
  answer('bad', '', true)
  answer('bad', '## 进展\nComplete report')
  const stream = messagesList[0].agentEventStream
  assert.equal(stream.find((e: any) => e.event_id === 'prior').content, 'Earlier legitimate result.')
  assert.equal(stream.find((e: any) => e.event_id === 'bad').content, '## 进展\nComplete report')
  assert.doesNotMatch(messagesList[0].content, /private planning|refs/)
  assert.equal(stream.some((e: any) => e.type === 'progress'), false)
})

test('protocol replacement clears matching progress across steer rows', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  handler.processStreamChunk({ response_type: 'progress', id: 'r1', content: 'private provisional prefix', done: true, data: { event_id: 'bad' } })
  handler.processStreamChunk({ response_type: 'user_message_injected', id: 'r1', data: { steer_id: 'st1', message_id: 'u1', content: 'Continue' } })
  handler.processStreamChunk({ response_type: 'answer', id: 'r1', content: '', data: { event_id: 'bad', replace: true } })
  handler.processStreamChunk({ response_type: 'answer', id: 'r1', content: 'Clean final answer', data: { event_id: 'bad' } })
  const events = messagesList.flatMap(m => m.agentEventStream || [])
  assert.equal(events.filter(e => e.event_id === 'bad').length, 1)
  assert.equal(events.find(e => e.event_id === 'bad').type, 'answer')
  assert.equal(events.find(e => e.event_id === 'bad').content, 'Clean final answer')
  assert.doesNotMatch(JSON.stringify(events), /private provisional prefix/)
})


test('corrected archived answer keeps complete reasoning in its own channel', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([{ id: 'a1', request_id: 'r1', role: 'assistant', content: '## 进展\nComplete report', is_completed: true,
    agent_steps: [{ iteration: 0, reasoning_content: 'refs\nprivate planning', thought: '## 进展\nComplete report', tool_calls: [] }] }])
  const stream = messagesList[0].agentEventStream
  assert.equal(stream.find((e: any) => e.type === 'thinking').content, 'refs\nprivate planning')
  assert.equal(stream.filter((e: any) => e.type === 'answer').length, 1)
  assert.equal(stream.find((e: any) => e.type === 'answer').content, '## 进展\nComplete report')
  assert.equal(stream.some((e: any) => e.type === 'progress'), false)
})

test('protocol replacement of current progress reuses its event for later chunks', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  handler.processStreamChunk({ response_type: 'progress', id: 'r1', content: 'private prefix', done: true, data: { event_id: 'bad' } })
  handler.processStreamChunk({ response_type: 'answer', id: 'r1', content: '', data: { event_id: 'bad', replace: true } })
  handler.processStreamChunk({ response_type: 'answer', id: 'r1', content: 'Clean final', data: { event_id: 'bad' } })
  assert.equal(messagesList[0].agentEventStream.length, 1)
  assert.equal(messagesList[0].agentEventStream[0].content, 'Clean final')
  assert.equal(messagesList[0].content, 'Clean final')
})


test('ordinary stream reconnect rebuilds answer, reasoning and tools without duplication', () => {
  const { handler, messagesList } = setup()
  const replay = () => {
    handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
    handler.processStreamChunk({ response_type: 'thinking', id: 'r1', content: 'reasoning', data: { event_id: 'thought' } })
    handler.processStreamChunk({ response_type: 'tool_call', id: 'r1', data: { tool_call_id: 'call', tool_name: 'search', arguments: {} } })
    handler.processStreamChunk({ response_type: 'tool_result', id: 'r1', data: { tool_call_id: 'call', tool_name: 'search', success: true, output: 'found' } })
    handler.processStreamChunk({ response_type: 'answer', id: 'r1', content: 'first part', data: { event_id: 'answer' }, done: false })
  }
  replay()
  const row = messagesList[0]
  replay()
  replay()
  assert.equal(messagesList.length, 1)
  assert.equal(messagesList[0], row)
  assert.equal(row.content, 'first part')
  assert.equal(row.agentEventStream.filter((e: Record<string, any>) => e.type === 'thinking').length, 1)
  assert.equal(row.agentEventStream.find((e: Record<string, any>) => e.type === 'thinking').content, 'reasoning')
  assert.equal(row.agentEventStream.filter((e: Record<string, any>) => e.type === 'tool_call').length, 1)
  handler.processStreamChunk({ response_type: 'answer', id: 'r1', content: ' continued', data: { event_id: 'answer' }, done: false })
  assert.equal(row.content, 'first part continued')
})

import { createChildAgentTabs, findChildAgentTask } from './useChildAgentTabs'
import { collaborationTasks } from '../utils/collaborationDisplay'

test('open child tab follows steering, approval, completion and subsequent runs', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  const update = (revision: number, status: string, runId = 'child-run-1') => handler.processStreamChunk({
    response_type: 'collaboration_status', id: 'r1', data: { group_id: 'group', revision, tasks: [task(status, runId)] },
  })
  update(1, 'running')
  const original = messagesList[0]
  const child = collaborationTasks(original.collaborationStatus)[0]
  const tabs = createChildAgentTabs()
  tabs.open(child, () => findChildAgentTask(messagesList, child))
  handler.processStreamChunk({ response_type: 'user_message_injected', id: 'r1', data: { steer_id: 's1', user_message_id: 'u1', content: 'Current progress?' } })
  assert.equal(original.collaborationStatus, undefined)
  update(2, 'waiting')
  assert.equal(tabs.tabs.value[0].task.status, 'waiting')
  update(3, 'succeeded')
  assert.equal(tabs.tabs.value[0].task.status, 'succeeded')
  update(4, 'running', 'child-run-2')
  assert.equal(tabs.tabs.value[0].task.runId, 'child-run-2')
  assert.equal(tabs.tabs.value[0].task.status, 'running')
  // Another conversation with a matching display ID must not hijack the tab.
  messagesList.push({ collaborationStatus: { tasks: [{ ...task('failed'), session_id: 'other-session' }] } })
  assert.equal(tabs.tabs.value[0].task.status, 'running')
})
