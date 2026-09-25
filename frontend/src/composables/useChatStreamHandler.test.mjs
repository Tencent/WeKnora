import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { resetSteerTurnForReplay } from '../utils/steerStreamFork.ts'

const source = readFileSync(new URL('./useChatStreamHandler.ts', import.meta.url), 'utf8')

test('command output updates only its pending tool and cannot replace a final result', () => {
  const start = source.indexOf("case 'command_output': {")
  const block = source.slice(start, source.indexOf("case 'tool_result':", start))
  const command = { type: 'tool_call', tool_name: 'shell_exec', tool_call_id: 'a', pending: true }
  const other = { type: 'tool_call', tool_name: 'shell_exec', tool_call_id: 'b', pending: true }
  const message = { agentEventStream: [command, other] }
  const process = vm.runInNewContext(ts.transpile(`(dataPayload) => { switch ('command_output') { ${block} } }`), { message })
  process({ tool_call_id: 'a', output: 'Reading CSV', done: false })
  assert.equal(command.command_output.output, 'Reading CSV')
  assert.equal(command.pending, true)
  assert.equal(other.command_output, undefined)
  process({ tool_call_id: 'missing', output: 'unmatched' })
  assert.equal(message.agentEventStream.length, 2)
  process({ tool_call_id: 'a', output: 'Finished', done: true })
  process({ tool_call_id: 'a', output: 'late chunk', done: false })
  assert.equal(command.command_output.output, 'Finished')
  command.pending = false
  command.output = 'Final tool result'
  process({ tool_call_id: 'a', output: 'more late output', done: false })
  assert.equal(command.output, 'Final tool result')
})

test('replaying agent_query binds the first segment and preserves distinct row IDs', () => {
  const messagesList = [
    { id: 'a', role: 'assistant', request_id: 'r', steerForked: true, is_completed: true },
    { id: 'u', role: 'user', request_id: 'r' },
    { id: 'a:steer:1', assistant_message_id: 'a', role: 'assistant', request_id: 'r', is_completed: false },
  ]
  const start = source.indexOf("if (data.response_type === 'agent_query')")
  const block = source.slice(start, source.indexOf('const isAgentOnlyResponse', start))
  const replaySegments = new Map()
  const process = vm.runInNewContext(ts.transpile(`(data) => { ${block} }`), {
    messagesList, replaySegments, resetSteerTurnForReplay,
    currentAssistantMessageId: { value: 'a' },
    getTrailingIncompleteAssistant: () => [...messagesList].reverse().find(m => m.role === 'assistant' && !m.is_completed),
    findLastMessage: fn => [...messagesList].reverse().find(fn),
    log() {}, ensureAgentMessageShell() {}, bindServerTurnTimestamps() {}, onAgentQuery() {},
  })
  process({ response_type: 'agent_query', id: 'r', assistant_message_id: 'a' })
  assert.deepEqual(messagesList.map(m => m.id), ['a', 'u', 'a:steer:1'])
  assert.equal(replaySegments.get('r'), messagesList[0])
})

test('failed tool results keep stdout/output instead of replacing it with the short error', () => {
  assert.match(source, /toolCallEvent\.output = dataPayload\.output \|\| data\.content/)
  assert.doesNotMatch(
    source,
    /toolCallEvent\.output = success\s*\?[\s\S]*dataPayload\.error/,
  )
})

test('later tool_call events merge arguments onto the same pending card', () => {
  assert.match(source, /function mergeToolCallArguments/)
  assert.match(source, /toolCallEvent\.arguments = mergeToolCallArguments\(toolCallEvent\.arguments, incomingArguments\)/)
})

test('agent chunks bind only to the in-flight assistant after a queued user message', () => {
  const chunkStart = source.indexOf('const handleAgentChunk = ')
  const chunkEnd = source.indexOf('const processStreamChunk = ', chunkStart)
  const chunk = source.slice(chunkStart, chunkEnd)
  assert.notEqual(chunkStart, -1)
  assert.notEqual(chunkEnd, -1)
  assert.match(chunk, /resolveActiveAssistantMessage\(data\)/)
  assert.doesNotMatch(chunk, /item\.request_id === dataId \|\| item\.id === dataId/)
})

test('incomplete assistant is found even when a later user message is the list tail', () => {
  const fnStart = source.indexOf('const getTrailingIncompleteAssistant = ')
  const fnEnd = source.indexOf('const markAssistantStopped = ', fnStart)
  const fn = source.slice(fnStart, fnEnd)
  assert.notEqual(fnStart, -1)
  assert.notEqual(fnEnd, -1)
  assert.match(fn, /item\?\.role === 'assistant'/)
  assert.doesNotMatch(fn, /const last = messagesList\[messagesList\.length - 1\]/)
})

test('global typing indicator stays hidden while an in-flight agent assistant exists', () => {
  const fnStart = source.indexOf('const shouldShowGlobalTypingIndicator = ')
  const fnEnd = source.indexOf('const restoreQuickAnswerFlags = ', fnStart)
  const fn = source.slice(fnStart, fnEnd)
  assert.notEqual(fnStart, -1)
  assert.notEqual(fnEnd, -1)
  assert.match(fn, /messages\.some\(/)
  assert.match(fn, /role === 'assistant'/)
  assert.match(fn, /isAgentMode/)
  assert.match(fn, /is_completed/)
})

// Splitting a turn around an injected message can leave one segment holding
// the answer text and no timeline events. Hiding that row loses the reply.
test('completed agent messages with content but no events still render', () => {
  const fnStart = source.indexOf('const shouldRenderAssistantMessage = ')
  const fnEnd = source.indexOf('const shouldShowGlobalTypingIndicator = ', fnStart)
  const fn = source.slice(fnStart, fnEnd)
  assert.notEqual(fnStart, -1)
  assert.notEqual(fnEnd, -1)
  assert.match(fn, /session\.content/)
})

test('injected user messages fork a continuation assistant below the bubble', () => {
  const chunkStart = source.indexOf("case 'user_message_injected'")
  const chunkEnd = source.indexOf("case 'complete'", chunkStart)
  const chunk = source.slice(chunkStart, chunkEnd)
  assert.notEqual(chunkStart, -1)
  assert.notEqual(chunkEnd, -1)
  assert.match(chunk, /forkAfterInjectedUser/)
  assert.match(chunk, /onUserMessageInjected/)
  assert.match(source, /expandSteerForksInHistory\(processed\)/)
  assert.match(source, /expandSteerForksInHistory\(\[\.\.\.messagesList\]\)/)
})

test('answer.done never marks the session idle before persisted completion', () => {
  const chunkStart = source.indexOf("case 'answer':")
  const chunkEnd = source.indexOf("case 'artifacts_pending'", chunkStart)
  assert.notEqual(chunkStart, -1)
  assert.notEqual(chunkEnd, -1)
  const chunk = source.slice(chunkStart, chunkEnd)
  assert.doesNotMatch(chunk, /isReplying.value = false/)
})

// continue-stream replays the event log from the start, so after a refresh
// this event arrives for a message history has already loaded. Synthesizing a
// bubble unconditionally puts the same message on screen twice.
test('a replayed injection reuses the persisted row instead of duplicating it', () => {
  const chunkStart = source.indexOf("case 'user_message_injected'")
  const chunkEnd = source.indexOf("case 'complete'", chunkStart)
  const chunk = source.slice(chunkStart, chunkEnd)
  assert.notEqual(chunkStart, -1)
  assert.notEqual(chunkEnd, -1)

  assert.match(chunk, /messagesList\.find\(/)
  assert.match(chunk, /item\.id === injectedId/)
  assert.match(chunk, /alreadyInList/)
  assert.match(chunk, /if \(!alreadyInList\) emitMessageCreated\(injectedUser\)/)
})

// Remote stop used to mark the assistant done and leave the overlay chips
// in place. The parent owns steerQueue, so the stream handler has to say
// the generation stopped.
test('agent and non-agent stop notify onGenerationStopped', () => {
  assert.match(source, /onGenerationStopped\?: \(\) => void/)

  const agent = source.indexOf("log('[Stop Event] Generation stopped')")
  assert.notEqual(agent, -1)
  assert.match(source.slice(agent, agent + 500), /onGenerationStopped\?\.\(\)/)

  const nonAgent = source.indexOf("log('[Stop Event] Non-agent generation stopped')")
  assert.notEqual(nonAgent, -1)
  assert.match(source.slice(nonAgent, nonAgent + 500), /onGenerationStopped\?\.\(\)/)
})

// A new answer round with no tool_call between rounds (retry / length-continuation /
// nudge / finalize) used to inherit the concatenated text of every prior round via
// the message.content seed, duplicating it on screen with no way to retract.
const runAnswerCase = () => {
  const chunkStart = source.indexOf("case 'answer':")
  const chunkEnd = source.indexOf("case 'artifacts_pending'", chunkStart)
  const chunk = source.slice(chunkStart, chunkEnd)
  const recompose = (message) => {
    let out = ''
    for (const e of message.agentEventStream || []) {
      if (e.type === 'answer' && !e.superseded && e.content) out += e.content
    }
    return out
  }
  const state = { fullContent: { value: '' }, doneCount: 0 }
  const process = vm.runInNewContext(
    ts.transpile(`(message, data, dataPayload) => { switch ('answer') { ${chunk} } }`),
    {
      ...state,
      recomposeAgentAnswer: recompose,
      log() {},
      onAgentAnswerDone() { state.doneCount++ },
      isAgentStreamSession: () => true,
      loading: { value: true },
      isReplying: { value: true },
    },
  )
  return { process, state }
}

test('a new answer round does not inherit text from live prior answer events', () => {
  const { process, state } = runAnswerCase()
  const message = { agentEventStream: [], _eventMap: new Map(), content: '' }

  // Round 1 streams to completion.
  process(message, { content: '第一点' }, { event_id: 'r1' })
  process(message, { content: '' }, { event_id: 'r1', done: true })

  // Round 2 starts with no tool_call in between (the issue's exact sequence).
  process(message, { content: '第二点' }, { event_id: 'r2' })
  process(message, { content: '' }, { event_id: 'r2', done: true })

  const r1 = message._eventMap.get('r1')
  const r2 = message._eventMap.get('r2')
  assert.equal(r1.content, '第一点')
  assert.equal(r2.content, '第二点')
  assert.equal(message.content, '第一点第二点')
  assert.equal(state.fullContent.value, '第一点第二点')
})

test('seeding still recovers a resumption whose text lives only in message.content', () => {
  const { process } = runAnswerCase()
  const message = {
    agentEventStream: [],
    _eventMap: new Map(),
    // Resume path: content was restored from history but no answer event exists.
    content: '已恢复的文本',
  }
  process(message, { content: ' 续写' }, { event_id: 'r' })
  assert.equal(message._eventMap.get('r').content, '已恢复的文本 续写')
  assert.equal(message.content, '已恢复的文本 续写')
})

test('a superseded prior round does not block or re-seed the new round', () => {
  const { process } = runAnswerCase()
  const prior = { type: 'answer', event_id: 'r1', content: '旧稿', done: true, superseded: true }
  const message = { agentEventStream: [prior], _eventMap: new Map(), content: '' }

  process(message, { content: '正式答案' }, { event_id: 'r2' })

  assert.equal(prior.content, '旧稿')
  assert.equal(message._eventMap.get('r2').content, '正式答案')
  assert.equal(message.content, '正式答案')
})

// A round the completion cap cut off marks its agent step. History is rebuilt
// from agent_steps and never replays the live answer events, so the rebuilt
// answer event has to carry the flag forward or a reloaded half-written answer
// looks finished.
const buildReconstruct = () => {
  const helperStart = source.indexOf('const agentStepsAreTruncated =')
  const start = source.indexOf('const reconstructEventStreamFromSteps = (')
  const end = source.indexOf('const handleMsgList = async (', start)
  const block = source.slice(helperStart, end)
  return vm.runInNewContext(
    ts.transpile(`(() => { ${block}; return reconstructEventStreamFromSteps })()`),
    { markRaw: (v) => v, steerStepEvents: () => [] },
  )
}

test('a truncated agent step marks the rebuilt answer event', () => {
  const reconstruct = buildReconstruct()
  const steps = [
    { iteration: 0, thought: 'looking it up', tool_calls: [{ id: 'c1', name: 'wiki_read_page' }] },
    { iteration: 1, thought: '', tool_calls: [], truncated: true },
  ]
  const events = reconstruct(steps, '1. First point. 2. Second po', true, false, 0, undefined)
  const answer = events.filter((e) => e.type === 'answer').at(-1)
  assert.equal(answer.content, '1. First point. 2. Second po')
  assert.equal(answer.truncated, true)
})

test('an ordinary finished turn is not marked truncated', () => {
  const reconstruct = buildReconstruct()
  const steps = [{ iteration: 0, thought: '', tool_calls: [] }]
  const events = reconstruct(steps, 'a complete answer', true, false, 0, undefined)
  const answer = events.filter((e) => e.type === 'answer').at(-1)
  assert.equal(answer.content, 'a complete answer')
  assert.equal(answer.truncated, undefined)
})

test('the live answer path carries truncated onto the event and the message', () => {
  const chunkStart = source.indexOf("case 'answer':")
  const chunk = source.slice(chunkStart, source.indexOf("case 'artifacts_pending'", chunkStart))
  const state = { fullContent: { value: '' } }
  const process = vm.runInNewContext(
    ts.transpile(`(message, data, dataPayload) => { switch ('answer') { ${chunk} } }`),
    {
      ...state,
      recomposeAgentAnswer: (m) =>
        (m.agentEventStream || []).filter((e) => e.type === 'answer' && !e.superseded).map((e) => e.content || '').join(''),
      log() {},
      onAgentAnswerDone() {},
      isAgentStreamSession: () => true,
      loading: { value: true },
      isReplying: { value: true },
    },
  )
  const message = { agentEventStream: [], _eventMap: new Map(), content: '' }
  process(message, { content: 'half an answer' }, { event_id: 'r1' })
  assert.equal(message.truncated, undefined)
  // The cap is only known at the close, so the Done marker carries it too.
  process(message, { content: '' }, { event_id: 'r1', done: true, truncated: true })
  assert.equal(message._eventMap.get('r1').truncated, true)
  assert.equal(message.truncated, true)
})
