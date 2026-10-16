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

test('settings and chat breadcrumbs name the page and the open resource', () => {
  const assistant = vue.reactive({ pageContext: { page: 'settings_models' } as any, contextAttached: true })
  const exports: any = {}
  runInNewContext(ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS },
  }).outputText, {
    exports,
    require: (name: string) => ({
      vue, 'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
      '@/stores/assistant': { useAssistantStore: () => assistant },
    } as Record<string, unknown>)[name],
  })
  const state = exports.default.setup({}, { expose() {} })
  assert.equal(state.icon.value, 'control-platform')
  assert.deepEqual(Array.from(state.segments.value), ['assistant.context.pages.settings_models'])
  assistant.pageContext = { page: 'settings_models', modelId: 'm-1', modelName: 'Qwen' }
  assert.deepEqual(Array.from(state.segments.value), ['assistant.context.pages.settings_models', 'Qwen'])
  assistant.pageContext = { page: 'mcp_services', mcpServiceId: 'mcp-1' }
  assert.equal(state.icon.value, 'tools')
  assert.deepEqual(Array.from(state.segments.value), ['assistant.context.pages.mcp_services', 'mcp-1'])
  assistant.pageContext = { page: 'chat', chatSessionId: 's', messageId: 'msg' }
  assert.equal(state.icon.value, 'chat-message')
  assert.deepEqual(Array.from(state.segments.value), ['assistant.context.chatMessage'])
  assistant.pageContext = { page: 'knowledge_base', knowledgeBaseId: 'kb', knowledgeBaseName: 'KB' }
  assert.deepEqual(Array.from(state.segments.value), ['KB'], 'a knowledge base keeps its own breadcrumb')
})

test('a dialog open over the page shows under the page', () => {
  const assistant = vue.reactive({
    pageContext: { knowledgeBaseId: 'kb', knowledgeBaseName: 'KB', knowledgeTitle: 'Doc', knowledgeId: 'doc' } as any,
    pageView: 'Edit Markdown', contextAttached: true,
  })
  const exports: any = {}
  runInNewContext(ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS },
  }).outputText, {
    exports,
    require: (name: string) => ({
      vue, 'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
      '@/stores/assistant': { useAssistantStore: () => assistant },
    } as Record<string, unknown>)[name],
  })
  const state = exports.default.setup({}, { expose() {} })
  assert.equal(state.icon.value, 'edit-1')
  assert.deepEqual(Array.from(state.segments.value), ['KB', 'Edit Markdown'])
  assistant.pageContext = null
  assert.deepEqual(Array.from(state.segments.value), ['Edit Markdown'], 'a dialog alone is enough')
  assistant.pageView = ''
  assert.deepEqual(Array.from(state.segments.value), [])
})

test('a page that publishes nothing shows its route title', () => {
  const assistant = vue.reactive({
    pageContext: null as any, pageTitle: 'Toolbox / Skills', pageView: '', contextAttached: true,
  })
  const exports: any = {}
  runInNewContext(ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS },
  }).outputText, {
    exports,
    require: (name: string) => ({
      vue, 'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
      '@/stores/assistant': { useAssistantStore: () => assistant },
    } as Record<string, unknown>)[name],
  })
  const state = exports.default.setup({}, { expose() {} })
  assert.equal(state.icon.value, 'browse')
  assert.deepEqual(Array.from(state.segments.value), ['Toolbox', 'Skills'])
  assistant.pageTitle = 'Agents'
  assistant.pageView = 'Agents / Tools'
  assert.deepEqual(Array.from(state.segments.value), ['Agents', 'Tools'], 'the page is not repeated')
  assistant.pageTitle = ''
  assistant.pageView = 'Settings / Models'
  assert.deepEqual(Array.from(state.segments.value), ['Settings', 'Models'])
  assistant.pageContext = { agentId: 'a', agentName: 'Helper' }
  assistant.pageTitle = 'Agents'
  assistant.pageView = ''
  assert.deepEqual(Array.from(state.segments.value), ['Helper'], 'a published context wins over the title')
})
