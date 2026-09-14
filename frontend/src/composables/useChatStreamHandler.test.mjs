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

test('agent answer.done does not mark the session idle', () => {
  const chunkStart = source.indexOf("case 'answer':")
  const chunkEnd = source.indexOf("case 'artifacts_pending'", chunkStart)
  assert.notEqual(chunkStart, -1)
  assert.notEqual(chunkEnd, -1)
  const chunk = source.slice(chunkStart, chunkEnd)
  assert.match(chunk, /if \(!isAgentStreamSession\(\)\)/)
  assert.ok(
    chunk.indexOf('isAgentStreamSession') < chunk.indexOf('isReplying.value = false'),
    'agent turns must wait for complete before clearing isReplying',
  )
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
