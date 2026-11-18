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
  const handler = useChatStreamHandler({
    messagesList, loading, isReplying, currentAssistantMessageId, fullContent,
    isAgentStreamSession: () => true, scrollToBottom() {},
    preserveIncompleteStreamReactive: true,
  })
  return { handler, messagesList }
}

test('nested JavaScript progress updates the outer run_javascript card live', () => {
  const { handler, messagesList } = setup()
  handler.processStreamChunk({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' })
  handler.processStreamChunk({
    response_type: 'tool_call', id: 'r1',
    data: { tool_call_id: 'outer', tool_name: 'run_javascript', arguments: { code: 'return 1;' } },
  })
  handler.processStreamChunk({
    response_type: 'javascript_progress', id: 'r1',
    data: {
      tool_call_id: 'outer', nested_call_id: 'outer-js-1', tool_name: 'read_document',
      phase: 'start', index: 1,
    },
  })
  handler.processStreamChunk({
    response_type: 'javascript_progress', id: 'r1',
    data: {
      tool_call_id: 'outer', nested_call_id: 'outer-js-1', tool_name: 'read_document',
      phase: 'result', index: 1, success: true, duration_ms: 42, chars: 120,
    },
  })

  const message = messagesList.find(m => m.assistant_message_id === 'a1')!
  const outer = (message.agentEventStream as Record<string, any>[])
    .find(e => e.type === 'tool_call' && e.tool_call_id === 'outer')!
  assert.equal(outer.js_progress.length, 1)
  const live = outer.js_progress[0]
  assert.equal(live.nested_call_id, 'outer-js-1')
  assert.equal(live.tool_name, 'read_document')
  assert.equal(live.phase, 'result')
  assert.equal(live.success, true)
  assert.equal(live.duration_ms, 42)
  assert.equal(live.chars, 120)
})
