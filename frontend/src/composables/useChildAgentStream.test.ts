import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { parse } from '@vue/compiler-sfc'
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

const component = parse(readFileSync(new URL('../views/chat/components/ChildAgentStream.vue', import.meta.url), 'utf8')).descriptor.scriptSetup!.content
const childCode = ts.transpileModule(component + '\nexports.state = { messages, refresh, viewport, onScroll, scrollToBottom, scrollFollow };', {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

async function flushUntil(predicate: () => boolean) {
  for (let n = 0; n < 20 && !predicate(); n++) {
    await new Promise(resolve => setImmediate(resolve))
  }
  assert.ok(predicate(), 'expected async subscription transition')
}

test('open suspended child resumes GET subscription and live timeline after parent approval', async () => {
  const props = vue.reactive({ task: { sessionId: 'child-session', status: 'waiting' }, active: true })
  const requests: any[] = []
  let deliver: (chunk: any) => void = () => {}
  let finish: () => void = () => {}
  let mounted: () => Promise<void> = async () => {}
  let unmounted: () => void = () => {}
  let stopped = false
  const stream = {
    isStreaming: vue.ref(false), error: vue.ref(''),
    onChunk: (fn: typeof deliver) => { deliver = fn },
    startStream: async (args: any) => {
      requests.push(args)
      stream.isStreaming.value = true
      await new Promise<void>(resolve => { finish = resolve })
      stream.isStreaming.value = false
    },
    stopStream: () => { stopped = true; finish() },
  }
  const childExports: any = {}
  const scope = vue.effectScope()
  scope.run(() => vm.runInNewContext(childCode, {
    exports: childExports, console, defineProps: () => props,
    setTimeout: () => 1, clearTimeout() {},
    require: (id: string) => {
      if (id === 'vue') return { ...vue, onMounted: (fn: typeof mounted) => { mounted = fn }, onUnmounted: (fn: typeof unmounted) => { unmounted = fn } }
      if (id === '@/composables/useChatScrollFollow') {
        const exported: any = {}
        const scrollCode = ts.transpileModule(readFileSync(new URL('./useChatScrollFollow.ts', import.meta.url), 'utf8'), {
          compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
        }).outputText
        vm.runInNewContext(scrollCode, { exports: exported, require: (name: string) => name === 'vue'
          ? { ...vue, provide() {}, onMounted() {}, onUnmounted() {} }
          : { useStickyBottomOnResize() {} } })
        return exported
      }
      if (id === '@/api/chat') return { getMessageList: async () => ({ data: [{
        id: 'child-answer', request_id: 'child-run', role: 'assistant', content: '',
        is_completed: false, run_state: 'awaiting_input', agent_duration_ms: 100,
        agent_steps: [{ iteration: 0, reasoning_content: 'before approval', tool_calls: [{ id: 'call-1', name: 'mcp_read', args: {} }] }],
        pending_prompt: { id: 'approval', kind: 'tool_approval', tool_call_id: 'call-1', request: {} },
      }] }) }
      if (id === '@/api/chat/streame') return { useStream: () => stream }
      if (id === '@/composables/useChatStreamHandler') return { useChatStreamHandler, isAwaitingInput: (m: any) => m?.run_state === 'awaiting_input' }
      if (id === '@/utils/collaborationDisplay') return { isCollaborationTaskActive: (s: string) => ['running', 'waiting'].includes(s) }
      assert.ok(id in modules, `unexpected component import ${id}`)
      return modules[id]
    },
  }))
  await mounted()
  assert.equal(requests.length, 0, 'approval wait should not keep replaying the suspended stream')
  assert.equal(childExports.state.messages[0].is_completed, true, 'history is settled for presentation')
  props.task.status = 'running'
  await flushUntil(() => requests.length === 1)
  assert.equal(requests[0].method, 'GET')
  assert.equal(requests[0].query, 'child-answer')
  const message = childExports.state.messages[0]
  assert.equal(message.is_completed, false)
  assert.ok(!message.agentEventStream.some((e: any) => e.tool_call_id === 'call-1' && e.type === 'tool_call'))
  const visible = vue.computed(() => message.agentEventStream.map((e: any) => `${e.type}:${e.content || ''}:${e.pending}`).join('|'))
  assert.doesNotMatch(visible.value, /after approval/)
  deliver({ response_type: 'thinking', id: 'child-run', content: 'after approval', done: false, data: { event_id: 't2' } })
  assert.match(visible.value, /after approval/)
  deliver({ response_type: 'tool_call', id: 'child-run', data: { tool_call_id: 'call-2', tool_name: 'mcp_read', arguments: {} } })
  assert.match(visible.value, /tool_call::true/)
  assert.equal(message.is_completed, false, 'timeline updates before completion')
  childExports.state.viewport.value = { scrollTop: 400, scrollHeight: 2000, clientHeight: 400 }
  childExports.state.scrollFollow.rememberPosition()
  childExports.state.viewport.value.scrollTop = 240
  childExports.state.onScroll()
  props.active = false
  await vue.nextTick()
  childExports.state.viewport.value.scrollTop = 0
  deliver({ response_type: 'thinking', id: 'child-run', content: 'hidden-tab-update', done: false, data: { event_id: 't3' } })
  childExports.state.scrollToBottom()
  await vue.nextTick()
  assert.match(visible.value, /hidden-tab-update/, 'hidden tabs still receive stream updates')
  assert.equal(childExports.state.viewport.value.scrollTop, 0, 'hidden streams must not move the viewport')
  assert.equal(stopped, false, 'switching tabs must not disconnect the stream')
  props.active = true
  await vue.nextTick()
  await vue.nextTick()
  assert.equal(childExports.state.viewport.value.scrollTop, 240, 'returning to a scrolled-up tab restores its position')
  unmounted()
  scope.stop()
  assert.equal(stopped, true)
  const before = visible.value
  deliver({ response_type: 'thinking', id: 'child-run', content: 'late', done: false, data: { event_id: 't2' } })
  assert.equal(visible.value, before, 'late stream frames cannot update a closed viewer')
})
