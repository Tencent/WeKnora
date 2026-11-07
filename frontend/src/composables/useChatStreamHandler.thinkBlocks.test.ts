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

// Regression tests for #3132 / #3099: literal tag markup in code examples must
// not be mistaken for protocol markers, and every real reasoning block must be
// split out while streaming and when restoring history.
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
  const handler = useChatStreamHandler({
    messagesList, loading: vue.ref(false), isReplying: vue.ref(false),
    currentAssistantMessageId: vue.ref(''), fullContent: vue.ref(''),
    isAgentStreamSession: () => false, scrollToBottom() {},
  })
  return {
    handler,
    messagesList,
    send: (content: string) => handler.processStreamChunk({
      id: 'r1', response_type: 'answer', content, done: false, data: {},
    }),
  }
}

test('streaming keeps a real block separate from a code example', () => {
  const { send, messagesList } = setup()
  send('<think>real reasoning</think>Example:\n```\n<think>literal</think>\n```\nDone.')
  const message = messagesList.at(-1)!

  assert.equal(message.thinkContent, 'real reasoning')
  assert.equal(message.content, 'Example:\n```\n<think>literal</think>\n```\nDone.')
  assert.equal(message.showThink, true)
  assert.equal(message.thinking, false)
})

test('streaming splits multiple reasoning rounds', () => {
  const { send, messagesList } = setup()
  const last = () => messagesList.at(-1)!

  send('<think>first')
  assert.equal(last().thinking, true)
  assert.equal(last().showThink, true)
  assert.equal(last().content, '')
  assert.equal(last().thinkContent, 'first')

  send(' round</think>one<think>second')
  assert.equal(last().thinking, true)
  assert.equal(last().content, 'one')
  assert.equal(last().thinkContent, 'first round\nsecond')

  send(' round</think>two')
  assert.equal(last().thinking, false)
  assert.equal(last().showThink, true)
  assert.equal(last().thinkContent, 'first round\nsecond round')
  assert.equal(last().content, 'onetwo')
})

test('streaming keeps literal code tags in the answer', () => {
  const { send, messagesList } = setup()
  send('Use `<think>` and `</think>` to delimit reasoning.')
  const message = messagesList.at(-1)!

  assert.equal(message.content, 'Use `<think>` and `</think>` to delimit reasoning.')
  assert.equal(message.showThink, false)
  assert.ok(!message.thinking)
})

test('streaming keeps a stray close tag in the answer', () => {
  const { send, messagesList } = setup()
  send('The closing marker is </think>.')
  const message = messagesList.at(-1)!

  assert.equal(message.content, 'The closing marker is </think>.')
  assert.equal(message.showThink, false)
  assert.ok(!message.thinking)
})

test('history restore keeps a code example intact', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([{
    id: 'm1', role: 'assistant', is_completed: true,
    content: 'Example:\n```\n<think>literal</think>\n```\nDone.',
    thinkContent: 'stale', showThink: true, thinking: true,
  }])
  const message = messagesList[0]

  assert.equal(message.content, 'Example:\n```\n<think>literal</think>\n```\nDone.')
  assert.equal(message.thinkContent, '')
  assert.equal(message.showThink, false)
  assert.equal(message.thinking, false)
})

test('history restore splits a real block from a code example', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([{
    id: 'm1', role: 'assistant', is_completed: true,
    content: '<think>real reasoning</think>Example:\n```\n<think>literal</think>\n```\nDone.',
  }])
  const message = messagesList[0]

  assert.equal(message.thinkContent, 'real reasoning')
  assert.equal(message.content, 'Example:\n```\n<think>literal</think>\n```\nDone.')
  assert.equal(message.showThink, true)
  assert.equal(message.thinking, false)
})

test('history restore reports an unterminated block', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([{
    id: 'm1', role: 'assistant',
    content: 'partial answer<think>still reasoning',
  }])
  const message = messagesList[0]

  assert.equal(message.thinkContent, 'still reasoning')
  assert.equal(message.content, 'partial answer')
  assert.equal(message.showThink, true)
  assert.equal(message.thinking, true)
})
