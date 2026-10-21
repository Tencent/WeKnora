import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileTemplate } from '@vue/compiler-sfc'
import * as vue from 'vue'
import ts from 'typescript'

const source = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
const start = source.indexOf('<div class="input-container"')
const end = source.indexOf('</InputField>', start) + '</InputField>'.length
const compiled = compileTemplate({
  source: `${source.slice(start, end)}</div>`, filename: 'index.vue', id: 'automation-composer',
})
assert.deepEqual(compiled.errors, [])
const exports: any = {}
runInNewContext(ts.transpileModule(compiled.code, {
  compilerOptions: { module: ts.ModuleKind.CommonJS },
}).outputText, {
  exports, require: () => ({
    ...vue, resolveComponent: (name: string) => name, withCtx: (fn: unknown) => fn,
    withDirectives: (node: unknown) => node,
  }),
})
function nodes(node: any): any[] {
  if (!node || typeof node !== 'object') return []
  if (Array.isArray(node)) return node.flatMap(nodes)
  const children = node.children
  return [node, ...(Array.isArray(children) ? nodes(children)
    : children && typeof children === 'object'
      ? Object.values(children).flatMap(slot => typeof slot === 'function' ? nodes(slot()) : []) : [])]
}
function render(kind: string | null, embeddedMode = false) {
  const currentSession = vue.ref(kind === null ? null : { kind })
  const declaration = source.match(/const isAutomationSession = .*;/)![0]
  const isAutomationSession = runInNewContext(`${declaration}\nisAutomationSession`, { currentSession, computed: vue.computed })
  return nodes(exports.render({
    currentSession: currentSession.value, isAutomationSession: isAutomationSession.value,
    embeddedMode, userHasScrolledUp: false, onClickScrollToBottom() {}, t: (key: string) => key,
    focusComposerOnMount: false, isReplying: false, composerLocked: false, session_id: 'session',
    currentAssistantMessageId: '', modelId: '', defaultModelId: '', steerQueue: [],
    isAgentStreamSession: () => true, $slots: {},
  }, []))
}

test('automation conversations replace the composer with a notice but retain prompt responses', () => {
  const rendered = render('automation')
  assert.ok(rendered.some(node => node.props?.class === 'automation-session-notice'))
  assert.ok(rendered.some(node => node.type === 'PendingPromptDock'))
  assert.ok(!rendered.some(node => node.type === 'InputField'))
})

test('normal and embedded conversations keep their composer, while unknown sessions wait for metadata', () => {
  assert.ok(render('chat').some(node => node.type === 'InputField'))
  assert.ok(render(null, true).some(node => node.type === 'InputField'))
  assert.ok(!render(null).some(node => node.type === 'InputField'))
})

test('automation history does not offer fork or rewind actions that lead back to a composer', () => {
  const fork = source.slice(source.indexOf('function forkAffordanceOf('), source.indexOf('// One pass over the transcript'))
  const rewind = source.slice(source.indexOf('function canRewindMessage('), source.indexOf('const FORK_PREFILL_KEY'))
  for (const automation of [true, false]) {
    const actions = runInNewContext(`${fork}\n${rewind}\n({ forkAffordanceOf, canRewindMessage })`, {
      isAutomationSession: { value: automation }, messagesList: [],
      rewindableIds: { value: new Set(['message']) }, resolveForkAffordance: () => ({ canFork: true }),
    })
    assert.equal(actions.forkAffordanceOf('message').canFork, !automation)
    assert.equal(actions.canRewindMessage('message'), !automation)
  }
})
