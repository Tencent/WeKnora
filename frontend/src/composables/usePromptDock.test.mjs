import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import { isSubmitKey, optionIndexForKey, optionKey } from '../utils/promptKeys.ts'

const source = readFileSync(new URL('./usePromptDock.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const modules = {
  vue,
  '@/composables/useChatStreamHandler': {
    isAwaitingInput: (m) => m?.run_state === 'awaiting_input',
  },
}
const exports = {}
vm.runInNewContext(compiled, {
  exports, Date,
  require: (id) => {
    assert.ok(id in modules, `unexpected import ${id}`)
    return modules[id]
  },
})
const { pendingPrompts, markPromptSubmitted, promptSubmission } = exports

// pendingPrompts runs in the vm realm; its arrays are rebuilt here to compare.
const ids = (list) => Array.from(list, (p) => p.pending_id)

const approval = (id, extra = {}) =>
  ({ type: 'tool_approval_required', pending_id: id, resolved: false, ...extra })

test('docks the open prompts of streaming and suspended runs, oldest first', () => {
  const prompts = pendingPrompts([
    { role: 'user', agentEventStream: [approval('user-row')] },
    { role: 'assistant', is_completed: true, run_state: 'awaiting_input', agentEventStream: [approval('a')] },
    {
      role: 'assistant', is_completed: false, agentEventStream: [
        { type: 'tool_call', pending_id: 'x' },
        { type: 'agent_prompt_required', kind: 'ask_user', pending_id: 'b', resolved: false },
        { type: 'mcp_oauth_required', pending_id: 'c', resolved: false },
      ],
    },
  ])
  assert.deepEqual(ids(prompts), ['a', 'b', 'c'])
})

test('leaves settled runs, resolved prompts and unknown kinds in the timeline', () => {
  const prompts = pendingPrompts([
    { role: 'assistant', is_completed: true, agentEventStream: [approval('settled')] },
    {
      role: 'assistant', is_completed: false, agentEventStream: [
        approval('resolved', { resolved: true }),
        { type: 'agent_prompt_required', kind: 'something_else', pending_id: 'other', resolved: false },
        approval(''),
      ],
    },
  ])
  assert.deepEqual(ids(prompts), [])
})

test('drops a suspended prompt once it expired', () => {
  const past = new Date(Date.now() - 60_000).toISOString()
  const future = new Date(Date.now() + 60_000).toISOString()
  const prompts = pendingPrompts([{
    role: 'assistant', is_completed: true, run_state: 'awaiting_input', agentEventStream: [
      approval('expired', { suspended: true, expires_at: past }),
      approval('live', { suspended: true, expires_at: future }),
    ],
  }])
  assert.deepEqual(ids(prompts), ['live'])
})

test('a prompt answered on this page leaves the dock before the stream confirms it', () => {
  const messages = vue.reactive([
    { role: 'assistant', is_completed: false, agentEventStream: [approval('answered-here'), approval('next')] },
  ])
  const docked = vue.computed(() => ids(pendingPrompts(messages)))
  assert.deepEqual(docked.value, ['answered-here', 'next'])

  markPromptSubmitted('answered-here', { approved: true })
  assert.deepEqual(docked.value, ['next'])
  assert.deepEqual(promptSubmission('answered-here'), { approved: true })
})

test('both chat views mount the dock above their composer and feed it their messages', () => {
  const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8')
  const chat = read('../views/chat/index.vue')
  assert.match(chat, /providePromptDock\(messagesList\)/)
  assert.match(chat, /<PendingPromptDock[^>]*\/>\s*<InputField/)

  const embed = read('../views/embed/EmbedChatCore.vue')
  assert.match(embed, /providePromptDock\(messagesList\)/)
  assert.match(embed, /<PendingPromptDock[\s\S]*?:embed-session-sig="sessionSig"[\s\S]*?\/>\s*<EmbedInputField/)
})

test('option keys are letters, and a letter key picks its option', () => {
  assert.equal(optionKey(0), 'A')
  assert.equal(optionKey(25), 'Z')
  assert.equal(optionKey(26), '')
  assert.equal(optionIndexForKey('b'), 1)
  assert.equal(optionIndexForKey('C'), 2)
  assert.equal(optionIndexForKey('1'), -1)
  assert.equal(optionIndexForKey('Enter'), -1)
})

test('Enter submits unless it inserts a line break or confirms an IME candidate', () => {
  const key = (init) => ({ key: 'Enter', shiftKey: false, isComposing: false, keyCode: 13, ...init })
  assert.equal(isSubmitKey(key({})), true)
  assert.equal(isSubmitKey(key({ shiftKey: true })), false)
  assert.equal(isSubmitKey(key({ isComposing: true })), false)
  assert.equal(isSubmitKey(key({ keyCode: 229 })), false)
  assert.equal(isSubmitKey(key({ key: 'a' })), false)
})

test('Enter on a focused control keeps its native action instead of submitting the prompt', () => {
  for (const control of ['reject', 'skip', 'args-toggle', 'dock-next']) {
    const key = {
      key: 'Enter', shiftKey: false, isComposing: false, keyCode: 13,
      target: { closest: (selector) => selector.includes('button') ? { control, getAttribute: () => null } : null },
    }
    assert.equal(isSubmitKey(key), false, control)
  }
})

test('Enter on the option just picked submits it rather than toggling it off', () => {
  const option = { getAttribute: (name) => ({ role: 'option', 'aria-selected': 'true' })[name] }
  const key = {
    key: 'Enter', shiftKey: false, isComposing: false, keyCode: 13,
    target: { closest: (selector) => (selector.includes('button') ? option : null) },
  }
  assert.equal(isSubmitKey(key), true)
})

test('the approval handler does not approve when Enter targets Reject', () => {
  const card = readFileSync(new URL('../views/chat/components/ToolApprovalCard.vue', import.meta.url), 'utf8')
  const handler = card.slice(card.indexOf('const handleKey ='), card.indexOf('defineExpose'))
  const decisions = []
  const compiled = ts.transpileModule(handler, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
  const handleKey = vm.runInNewContext(compiled + '\nhandleKey', {
    interactive: { value: true }, isJsonValid: { value: true }, isSubmitKey,
    submit: (decision) => decisions.push(decision),
  })
  const key = { key: 'Enter', shiftKey: false, isComposing: false, keyCode: 13,
    target: { tagName: 'BUTTON', closest: () => ({ textContent: 'Reject' }) } }
  assert.equal(handleKey(key), false)
  assert.deepEqual(decisions, [])
  assert.equal(handleKey({ ...key, target: { tagName: 'SECTION', closest: () => null } }), true)
  assert.deepEqual(decisions, ['approve'])
})


test('Enter on an unselected option selects it instead of submitting the previous answer', () => {
  const card = readFileSync(new URL('../views/chat/components/AskUserCard.vue', import.meta.url), 'utf8')
  const source = card.slice(card.indexOf('const handleKey ='), card.indexOf('defineExpose'))
  const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
  for (const initial of [[], ['A']]) {
    let selected = [...initial]
    const submitted = []
    const handleKey = vm.runInNewContext(compiled + '\nhandleKey', {
      interactive: { value: true }, isSubmitKey, isTextEntry: () => false,
      optionIndexForKey, options: { value: ['A', 'B'] },
      submit: () => submitted.push([...selected]), toggleOption: (value) => { selected = [value] },
    })
    const control = { getAttribute: (name) => ({ role: 'option', 'aria-selected': String(selected.includes('B')) })[name] }
    const key = { key: 'Enter', shiftKey: false, isComposing: false, keyCode: 13,
      target: { closest: (selector) => selector.includes('button') ? control : null } }
    // Returning false preserves the native button click; the dock must not
    // preventDefault or submit A while the user's focus is on B.
    assert.equal(handleKey(key), false)
    assert.deepEqual(submitted, [])
    selected = ['B'] // The button's native click selects B.
    assert.equal(handleKey(key), true)
    assert.deepEqual(submitted, [['B']])
  }
})
