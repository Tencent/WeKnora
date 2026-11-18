import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'

const source = readFileSync(new URL('./useChatStreamHandler.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

const modules: Record<string, unknown> = {
  vue,
  'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
}
const exports: { pendingPromptEvents?: (prompt: unknown) => any[] } = {}
vm.runInNewContext(compiled, {
  exports,
  console,
  require: (id: string) => {
    if (id in modules) return modules[id]
    return new Proxy({}, { get: () => () => {} })
  },
})
const pendingPromptEvents = exports.pendingPromptEvents!

test('budget pause history restores an answerable card', () => {
  const [card] = pendingPromptEvents({
    id: 'budget-1',
    kind: 'budget_pause',
    status: 'suspended',
    created_at: '2026-10-30T08:00:00Z',
    expires_at: '2026-10-31T08:00:00Z',
    request: {
      reason: 'token_budget_exhausted',
      limit: { max_total_tokens: 1000 },
      used: { total_tokens: 1000 },
      suggested_extension: { max_total_tokens: 500 },
    },
  })
  assert.equal(card.type, 'agent_prompt_required')
  assert.equal(card.kind, 'budget_pause')
  assert.equal(card.pending_id, 'budget-1')
  assert.equal(card.resolved, false)
  assert.equal(card.request.limit.max_total_tokens, 1000)
})

test('resolved budget pause keeps its decision in history', () => {
  const [card] = pendingPromptEvents({
    id: 'budget-2',
    kind: 'budget_pause',
    status: 'done',
    response: { action: 'extend', extension: { max_total_tokens: 500 } },
    request: { reason: 'token_budget_exhausted' },
  })
  assert.equal(card.resolved, true)
  assert.equal(card.answered, true)
  assert.equal(card.response.action, 'extend')
})