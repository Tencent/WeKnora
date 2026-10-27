import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { parse } from '@vue/compiler-sfc'
import { computed, reactive, ref } from 'vue'
import { persistedAssistantId } from '../../../utils/steerStreamFork.ts'

const source = readFileSync(new URL('./AgentStreamDisplay.vue', import.meta.url), 'utf8')
const child = parse(readFileSync(new URL('./ChildAgentStream.vue', import.meta.url), 'utf8')).descriptor
const start = source.indexOf('const showArtifactDrawer = ')
const end = source.indexOf('\nconst artifactRefContext = ', start)
assert.ok(start >= 0 && end > start)
const code = ts.transpile(source.slice(start, end))

function setup(overrides = {}, withPanel = true) {
  const calls = []
  const props = reactive({
    sessionId: 'child-session',
    session: { id: 'child-message', artifacts: [
      { file_name: 'deleted.md', deleted_at: '2026-10-18' },
      { file_name: 'slides.pptx' },
      { file_name: 'notes.md' },
    ] },
    ...overrides,
  })
  const state = vm.runInNewContext(`${code}; ({
    openArtifactDrawer, showArtifactDrawer, artifactPreviewIndex, useArtifactDrawer,
    sessionIdForArtifacts, messageIdForArtifacts, liveArtifacts,
  })`, {
    props, computed, ref,
    useChatSandboxPanel: () => withPanel ? {
      toggleArtifacts: id => calls.push(['toggle', id]),
      open: (tab, focus) => calls.push(['open', tab, focus.messageId, focus.previewIndex]),
    } : null,
    persistedAssistantId,
    useArtifactArriveMotion: () => ({}),
    isCollectingSkillArtifacts: () => false,
  })
  return { ...state, props, calls }
}

test('child viewer explicitly routes artifacts locally while retaining the child session', () => {
  const stream = child.template.ast.children[0].children[0].children
    .find(node => node.tag === 'template').children.find(node => node.tag === 'AgentStreamDisplay')
  assert.ok(stream.props.some(prop => prop.type === 6 && prop.name === 'artifacts-in-drawer'))
  assert.ok(stream.props.some(prop => prop.arg?.content === 'session-id' && prop.exp?.content === 'task.sessionId'))
  // Rendering and click routing must agree; otherwise clicks only toggle invisible state.
  const drawer = parse(source).descriptor.template.ast.children.find(node => node.tag === 'ChatArtifactsDrawer')
  for (const [attribute, expression] of [
    ['session-id', 'sessionIdForArtifacts'], ['message-id', 'messageIdForArtifacts'],
    ['artifacts', 'liveArtifacts'], ['preview-index', 'artifactPreviewIndex'],
  ]) {
    assert.ok(drawer.props.some(prop => prop.arg?.content === attribute && prop.exp?.content === expression))
  }
  const condition = drawer.props.find(prop => prop.name === 'if').exp.content
  const ctx = setup({ artifactsInDrawer: true })
  assert.equal(vm.runInNewContext(condition, {
    hasArtifacts: true, embeddedMode: false, useArtifactDrawer: ctx.useArtifactDrawer.value,
    sessionIdForArtifacts: ctx.sessionIdForArtifacts.value,
    messageIdForArtifacts: ctx.messageIdForArtifacts.value,
  }), 'child-message')
})

test('child inline cards and folder use the same local drawer, preserving original file indexes', () => {
  const ctx = setup({ artifactsInDrawer: true })
  for (const index of [1, 2]) {
    ctx.openArtifactDrawer(index)
    assert.equal(ctx.showArtifactDrawer.value, true)
    assert.equal(ctx.artifactPreviewIndex.value, index)
    assert.equal(ctx.liveArtifacts.value.find(file => file.index === index).file_name,
      index === 1 ? 'slides.pptx' : 'notes.md')
    assert.equal(ctx.sessionIdForArtifacts.value, 'child-session')
    assert.equal(ctx.messageIdForArtifacts.value, 'child-message')
    ctx.showArtifactDrawer.value = false
  }
  ctx.openArtifactDrawer()
  assert.equal(ctx.showArtifactDrawer.value, true)
  assert.equal(ctx.artifactPreviewIndex.value, null)
  assert.deepEqual(ctx.calls, [], 'must not send child message IDs to the parent panel')
})

test('main conversation retains panel routing; embedded and standalone viewers have a visible fallback', () => {
  const main = setup({ sessionId: 'main-session', session: { id: 'main-message', artifacts: [{}] } })
  main.openArtifactDrawer()
  main.openArtifactDrawer(0)
  assert.equal(main.useArtifactDrawer.value, false)
  assert.equal(main.showArtifactDrawer.value, false)
  assert.deepEqual(main.calls, [['toggle', 'main-message'], ['open', 'artifacts', 'main-message', 0]])
  for (const ctx of [setup({ embeddedMode: true }), setup({}, false)]) {
    ctx.openArtifactDrawer(1)
    assert.equal(ctx.useArtifactDrawer.value, true)
    assert.equal(ctx.showArtifactDrawer.value, true)
    assert.deepEqual(ctx.calls, [])
  }
})

test('messages without live artifacts do not open either surface', () => {
  const ctx = setup({ artifactsInDrawer: true, session: { id: 'empty', artifacts: [] } })
  ctx.openArtifactDrawer()
  assert.equal(ctx.showArtifactDrawer.value, false)
  assert.deepEqual(ctx.calls, [])
})
