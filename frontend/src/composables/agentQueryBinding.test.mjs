// The next turn's agent_query must not take over an earlier turn's unfinished row.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { resetSteerTurnForReplay } from '../utils/steerStreamFork.ts'

const source = readFileSync(new URL('./useChatStreamHandler.ts', import.meta.url), 'utf8')
const start = source.indexOf("if (data.response_type === 'agent_query')")
const block = source.slice(start, source.indexOf('const isAgentOnlyResponse', start))

function agentQuery(messagesList) {
  const currentAssistantMessageId = { value: '' }
  const process = vm.runInNewContext(ts.transpile(`(data) => { ${block} }`), {
    messagesList, replaySegments: new Map(), resetSteerTurnForReplay, currentAssistantMessageId,
    getTrailingIncompleteAssistant: () => [...messagesList].reverse().find(m => m.role === 'assistant' && !m.is_completed),
    findLastMessage: fn => [...messagesList].reverse().find(fn),
    isAgentStreamSession: () => true, loading: { value: true },
    emitMessageCreated() {}, scrollToBottom() {}, log() {},
    ensureAgentMessageShell() {}, bindServerTurnTimestamps() {}, onAgentQuery() {},
  })
  return { process, currentAssistantMessageId }
}

test('a follow-up agent_query does not take over the previous question\'s unfinished row', () => {
  // The second turn's stream was cut, leaving its row incomplete; the third turn starts.
  const q2 = { id: 'a2', assistant_message_id: 'a2', request_id: 'r2', role: 'assistant', content: 'I am an assistant', is_completed: false }
  const messagesList = [
    { id: 'u2', role: 'user', request_id: 'r2', content: 'Who are you?' }, q2,
    { id: 'u3', role: 'user', request_id: 'r3', content: 'What can you do?' },
  ]
  const { process, currentAssistantMessageId } = agentQuery(messagesList)
  process({ response_type: 'agent_query', id: 'r3', assistant_message_id: 'a3' })
  assert.equal(q2.request_id, 'r2', 'previous row was re-labelled with the new request')
  assert.equal(q2.id, 'a2')
  assert.equal(q2.content, 'I am an assistant')
  assert.deepEqual(messagesList.map(m => m.id), ['u2', 'a2', 'u3', 'a3'])
  assert.equal(messagesList[3].request_id, 'r3')
  assert.equal(currentAssistantMessageId.value, 'a3')
})

test('a placeholder row without a request id is still adopted by agent_query', () => {
  const placeholder = { id: 'tmp', role: 'assistant', content: '', is_completed: false }
  const messagesList = [{ id: 'u1', role: 'user', content: 'q' }, placeholder]
  const { process } = agentQuery(messagesList)
  process({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  assert.equal(placeholder.request_id, 'r1')
  assert.equal(placeholder.id, 'a1')
  assert.equal(messagesList.length, 2)
})
