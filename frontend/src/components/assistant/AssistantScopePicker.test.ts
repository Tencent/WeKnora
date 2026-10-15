import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import ts from 'typescript'

const source = readFileSync(new URL('./AssistantScopePicker.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'scope-picker-test' }).content

test('agent context disables KB scope without changing the stored choice', () => {
  const assistant = vue.reactive({
    scope: 'knowledge_base', contextAttached: true, pageContext: { agentId: 'a' } as any,
    setScope(value: string) { this.scope = value },
  })
  const exports: any = {}
  const dependencies: Record<string, unknown> = {
    vue, 'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/stores/assistant': { useAssistantStore: () => assistant },
    '@/api/assistant': {
      ASSISTANT_SCOPES: ['page', 'knowledge_base', 'workspace'],
      isAssistantScope: (value: unknown) => ['page', 'knowledge_base', 'workspace'].includes(value as string),
    },
  }
  runInNewContext(ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS },
  }).outputText, { exports, require: (name: string) => dependencies[name] })
  const state = exports.default.setup({}, { expose() {} })
  assert.equal(state.allowed('knowledge_base'), false)
  assert.equal(assistant.scope, 'knowledge_base')
  state.pick('page')
  assert.equal(assistant.scope, 'page')
  state.pick('knowledge_base')
  assert.equal(assistant.scope, 'page')
  state.pick('unknown')
  assert.equal(assistant.scope, 'page')
  assistant.pageContext = { knowledgeBaseId: 'kb' }
  state.pick('knowledge_base')
  assert.equal(assistant.scope, 'knowledge_base')
  assistant.pageContext = null
  assistant.contextAttached = false
  assert.equal(assistant.scope, 'knowledge_base')
  assert.match(source, /:disabled="!allowed\(scope\)"/)
  assert.match(source, /assistant.scope.agentNoKB/)
  assert.match(source, /!assistant.contextAttached && assistant.scope !== 'workspace'/)
})
