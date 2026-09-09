import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

const source = readFileSync(new URL('./useChatStreamHandler.ts', import.meta.url), 'utf8')

// Execute the real composable while replacing only Vue/app lifecycle imports.
function streamHarness(content = '') {
  const module = { exports: {} }
  vm.runInNewContext(ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText, {
    exports: module.exports,
    require: (name) => {
      if (name === 'vue') return { markRaw: (v) => v, nextTick: (fn) => Promise.resolve().then(fn) }
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key) => key }) }
      return new Proxy({}, { get: () => () => {} })
    },
    console,
  })
  const messages = [{ id: 'message', role: 'assistant', content, isAgentMode: true }]
  const ref = (value) => ({ value })
  const handler = module.exports.useChatStreamHandler({
    messagesList: messages, loading: ref(false), isReplying: ref(true),
    currentAssistantMessageId: ref('message'), fullContent: ref(content),
    isAgentStreamSession: () => true, scrollToBottom: () => {},
  })
  return {
    message: messages[0],
    send: (id, text, done = false) => handler.processStreamChunk({
      id: 'message', response_type: 'answer', content: text, done, data: { event_id: id },
    }),
  }
}

test('distinct answer events append each segment exactly once', () => {
  const { message, send } = streamHarness()
  send('a', 'first ')
  send('b', 'second ')
  send('c', 'third')
  send('c', '', true)
  assert.equal(message.content, 'first second third')
})

test('continued chunks share an event and preserve whitespace', () => {
  const { message, send } = streamHarness()
  send('a', 'first ')
  send('a', 'second')
  send('a', '', true)
  assert.equal(message.content, 'first second')
})

test('resume from persisted content seeds only the first live answer', () => {
  const { message, send } = streamHarness('saved ')
  send('a', 'continued ')
  send('b', 'tail')
  assert.equal(message.content, 'saved continued tail')
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
