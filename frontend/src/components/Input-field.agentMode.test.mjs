import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

const BUILTIN_QUICK_ANSWER_ID = 'builtin-quick-answer'
const BUILTIN_SMART_REASONING_ID = 'builtin-smart-reasoning'

// Run the real helper and the real sync/loadAgents/watch code from the SFC, so
// the test fails if either the config -> mode mapping or its wiring is removed.
const modeSource = readFileSync(new URL('../utils/agent-mode.ts', import.meta.url), 'utf8')
const fnStart = modeSource.indexOf('export function customAgentStreamMode')
const fnEnd = modeSource.indexOf('\n}', fnStart) + 2
assert.ok(fnStart > 0 && fnEnd > fnStart, 'customAgentStreamMode not found in agent-mode.ts')
const modeFn = ts.transpile(modeSource.slice(fnStart, fnEnd).replace('export ', ''))

const source = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const start = source.indexOf('const syncSelectedAgentModeFromConfig =')
const end = source.indexOf('// 默认选中的 builtin', start)
assert.ok(start > 0 && end > start, 'agent mode sync not found in Input-field.vue')
const loaderCode = ts.transpile(`${source.slice(start, end)}\n({ loadAgents })`)

// `configAvailable` mirrors the SFC fallback: until the agents snapshot is
// loaded, selectedAgent resolves to the quick-answer placeholder instead of the
// selected custom id.
function loadScenario({ agentId, mode, isAgentEnabled, embeddedMode = false, configAvailable = true }) {
  const calls = []
  const state = { isAgentEnabled }
  let watcher
  const settingsStore = {
    get isAgentEnabled() { return state.isAgentEnabled },
    toggleAgent(enabled) {
      state.isAgentEnabled = enabled
      calls.push(enabled)
    },
  }
  const selectedAgent = {
    get value() {
      if (!configAvailable) {
        return { id: BUILTIN_QUICK_ANSWER_ID, is_builtin: true, config: { agent_mode: 'quick-answer' } }
      }
      return { id: agentId, config: { agent_mode: mode } }
    },
  }
  const context = {
    BUILTIN_QUICK_ANSWER_ID,
    BUILTIN_SMART_REASONING_ID,
    props: { embeddedMode },
    settingsStore,
    selectedAgentId: { value: agentId },
    selectedAgent,
    currentAgentConfig: { value: configAvailable ? { agent_mode: mode } : {} },
    watch: (_getter, callback) => { watcher = callback },
    chatResources: { ensureAgents: async () => {} },
    ensureSelectedAgentNotDisabled: () => {},
    console,
  }
  const { loadAgents } = vm.runInNewContext(`${modeFn}\n${loaderCode}`, context)
  return {
    loadAgents,
    calls,
    state,
    fireWatcher: () => watcher?.(),
  }
}

test('refreshing the agents snapshot switches a custom agent to agent chat after an edit', async () => {
  const { loadAgents, calls, state } = loadScenario({
    agentId: 'agent-a',
    mode: 'smart-reasoning',
    isAgentEnabled: false,
  })

  await loadAgents()

  assert.deepEqual(calls, [true], 'a quick-answer -> smart-reasoning edit must enable the agent pipeline')
  assert.equal(state.isAgentEnabled, true)
})

test('refreshing the agents snapshot switches a custom agent back to knowledge chat', async () => {
  const { loadAgents, calls, state } = loadScenario({
    agentId: 'agent-a',
    mode: 'quick-answer',
    isAgentEnabled: true,
  })

  await loadAgents()

  assert.deepEqual(calls, [false], 'a smart-reasoning -> quick-answer edit must return to RAG')
  assert.equal(state.isAgentEnabled, false)
})

test('builtin modes keep being decided by their id', async () => {
  const quick = loadScenario({
    agentId: 'builtin-quick-answer',
    mode: 'smart-reasoning',
    isAgentEnabled: true,
  })
  await quick.loadAgents()
  assert.deepEqual(quick.calls, [], 'a stale isAgentEnabled must not flip the quick-answer builtin')

  const reasoning = loadScenario({
    agentId: 'builtin-smart-reasoning',
    mode: 'quick-answer',
    isAgentEnabled: false,
  })
  await reasoning.loadAgents()
  assert.deepEqual(reasoning.calls, [])
})

test('the placeholder agent is not used before the list has loaded', async () => {
  const { loadAgents, calls, state } = loadScenario({
    agentId: 'agent-a',
    mode: 'quick-answer',
    isAgentEnabled: true,
    configAvailable: false,
  })

  await loadAgents()

  assert.deepEqual(calls, [], 'the quick-answer placeholder must not turn the selected custom agent off')
  assert.equal(state.isAgentEnabled, true)
})

test('a session restore that rewrites isAgentEnabled is corrected again by the watcher', async () => {
  const { loadAgents, calls, state, fireWatcher } = loadScenario({
    agentId: 'agent-a',
    mode: 'smart-reasoning',
    isAgentEnabled: true,
  })

  await loadAgents()
  assert.deepEqual(calls, [])

  // Opening an older session replays its last_request_state, including the
  // stale agent_enabled value from before the agent was edited.
  state.isAgentEnabled = false
  fireWatcher()

  assert.deepEqual(calls, [true], 'the watcher must re-align after last_request_state overrides the mode')
  assert.equal(state.isAgentEnabled, true)
})

test('the watcher is idempotent once the mode matches', async () => {
  const { loadAgents, calls, fireWatcher } = loadScenario({
    agentId: 'agent-a',
    mode: 'smart-reasoning',
    isAgentEnabled: false,
  })

  await loadAgents()
  fireWatcher()
  fireWatcher()

  assert.deepEqual(calls, [true], 'a matching mode must not keep rewriting the store')
})

test('embedded hosts control the agent mode', async () => {
  const { loadAgents, calls, fireWatcher } = loadScenario({
    agentId: 'agent-a',
    mode: 'smart-reasoning',
    isAgentEnabled: false,
    embeddedMode: true,
  })

  await loadAgents()
  fireWatcher()

  assert.deepEqual(calls, [], 'the embedded composer must not rewrite the host store')
})
