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
import type { useChatStreamHandler as StreamHandler } from './useChatStreamHandler.ts'

// Regression tests for #3099: inline <think> blocks must be split out of the
// chat stream by parseThinkBlocks — every block, not just the first pair —
// both while streaming and when restoring history.
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
    handler, messagesList,
    send: (content: string) => handler.processStreamChunk({
      id: 'r1', response_type: 'answer', content, done: false, data: {},
    }),
  }
}

test('streaming splits every think block and never leaks tags into the card (#3099)', () => {
  const { send, messagesList } = setup()
  const last = () => messagesList.at(-1)!

  send('<think>first round')
  assert.equal(last().thinking, true)
  assert.equal(last().showThink, true)
  assert.equal(last().content, '')
  assert.equal(last().thinkContent, 'first round')

  send('</think>intermediate answer<think>second')
  assert.equal(last().thinking, true)
  assert.equal(last().content, 'intermediate answer',
    'an answer between two think blocks stays in the chat stream')
  assert.equal(last().thinkContent, 'first round\nsecond')

  send(' round</think>final answer')
  assert.equal(last().thinking, false)
  assert.equal(last().showThink, true)
  assert.equal(last().thinkContent, 'first round\nsecond round')
  assert.ok(!last().thinkContent.includes('<'), 'no tag markup in the thinking card')
  assert.equal(last().content, 'intermediate answerfinal answer')
})

test('plain answers stream unchanged', () => {
  const { send, messagesList } = setup()
  send('just')
  send(' text')
  const m = messagesList.at(-1)!
  assert.equal(m.content, 'just text')
  assert.ok(!m.showThink)
  assert.ok(!m.thinking)
})

test('a stray close tag without an open tag stays in the answer text', () => {
  const { send, messagesList } = setup()
  send('mentions </think> inline')
  const m = messagesList.at(-1)!
  assert.equal(m.content, 'mentions </think> inline')
  assert.ok(!m.showThink)
  assert.ok(!m.thinking)
})

test('history restore splits multi-block think content (#3099)', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([{
    id: 'm1', role: 'assistant', is_completed: true,
    content: '<think>round one</think>first answer<think>round two</think>second answer',
  }])
  const m = messagesList[0]
  assert.equal(m.thinkContent, 'round one\nround two')
  assert.equal(m.content, 'first answersecond answer')
  assert.equal(m.showThink, true)
  assert.equal(m.thinking, false)
})

test('history restore keeps an unterminated think block thinking', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([{
    id: 'm1', role: 'assistant',
    content: '<think>still reasoning',
  }])
  const m = messagesList[0]
  assert.equal(m.thinking, true)
  assert.equal(m.showThink, true)
  assert.equal(m.thinkContent, 'still reasoning')
  assert.equal(m.content, '')
})

test('history restore clears stale think state for plain content', async () => {
  const { handler, messagesList } = setup()
  await handler.handleMsgList([{
    id: 'm1', role: 'assistant', is_completed: true, content: 'plain answer',
    thinkContent: 'stale', showThink: true, thinking: true,
  }])
  const m = messagesList[0]
  assert.equal(m.content, 'plain answer')
  assert.equal(m.thinkContent, '')
  assert.equal(m.showThink, false)
  assert.equal(m.thinking, false)
})
