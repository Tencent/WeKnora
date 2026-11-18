import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import * as vue from 'vue'
import ts from 'typescript'

test('the editor publishes existing local agents and retracts on create, close and unmount', async () => {
  const source = readFileSync(new URL('./AgentList.vue', import.meta.url), 'utf8')
  const publisher = source.match(/useAssistantPageContext\(\(\) => \{[\s\S]*?\n\}\)/)?.[0]
  assert.ok(publisher)
  const composable = readFileSync(new URL('../../composables/useAssistantPageContext.ts', import.meta.url), 'utf8')
  let context: unknown = null
  let unmount!: () => void
  const exports: any = {}
  runInNewContext(ts.transpileModule(composable, {
    compilerOptions: { module: ts.ModuleKind.CommonJS },
  }).outputText, { exports, require: (name: string) => name === 'vue'
    ? { ...vue, onBeforeUnmount: (fn: () => void) => { unmount = fn } }
    : { useAssistantStore: () => ({
      publishPageContext: (_: symbol, value: unknown) => { context = value },
      retractPageContext: () => { context = null },
    }) },
  })
  const agent = { id: 'local', tenant_id: 1, name: 'Local' }
  const state = {
    editorVisible: vue.ref(false), editorMode: vue.ref('edit'),
    editingAgent: vue.ref<any>(agent), agents: vue.ref([agent]),
    useAssistantPageContext: exports.useAssistantPageContext,
  }
  const scope = vue.effectScope()
  scope.run(() => runInNewContext(publisher, state))
  try {
    assert.equal(context, null)
    state.editorVisible.value = true
    await vue.nextTick()
    assert.equal(JSON.stringify(context), JSON.stringify({ page: 'agent_editor', agentId: 'local', agentName: 'Local' }))
    state.editorVisible.value = false
    await vue.nextTick()
    assert.equal(context, null)
    state.editorVisible.value = true
    state.editorMode.value = 'create'
    await vue.nextTick()
    assert.equal(context, null)
    state.editorMode.value = 'edit'
    state.editingAgent.value = { ...agent, tenant_id: 2 }
    await vue.nextTick()
    assert.equal(context, null, 'shared agents, including builtin IDs, must not attach')
    state.editingAgent.value = agent
    await vue.nextTick()
    assert.ok(context)
    unmount()
    assert.equal(context, null)
  } finally { scope.stop() }
})
