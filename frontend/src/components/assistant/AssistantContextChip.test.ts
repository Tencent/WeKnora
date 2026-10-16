import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import { RobotIcon } from 'tdesign-icons-vue-next'
import * as vue from 'vue'
import ts from 'typescript'

const source = readFileSync(new URL('./AssistantContextChip.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'context-chip-test' }).content

test('agent breadcrumb uses its name and an available agent icon', () => {
  const assistant = vue.reactive({
    pageContext: { agentId: 'agent-a', agentName: 'Agent A' } as any, contextAttached: true,
  })
  const dependencies: Record<string, unknown> = {
    vue, 'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/stores/assistant': { useAssistantStore: () => assistant },
  }
  const exports: any = {}
  runInNewContext(ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS },
  }).outputText, { exports, require: (name: string) => dependencies[name] })
  const state = exports.default.setup({}, { expose() {} })
  assert.ok(RobotIcon)
  assert.equal(state.icon.value, 'robot')
  assert.deepEqual(Array.from(state.segments.value), ['Agent A'])
  assistant.pageContext.agentName = ''
  assert.deepEqual(Array.from(state.segments.value), ['agent-a'])
  assistant.pageContext = { knowledgeBaseId: 'kb', knowledgeBaseName: 'KB', knowledgeTitle: 'Doc', knowledgeId: 'doc' }
  assert.equal(state.icon.value, 'file')
  assert.deepEqual(Array.from(state.segments.value), ['KB', 'Doc'])
})
