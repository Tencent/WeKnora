import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

let pendingPromptEvents: (prompt: unknown) => Record<string, any>[]

try {
  const mod = await import('./useChatStreamHandler.ts')
  pendingPromptEvents = mod.pendingPromptEvents
} catch {
  const source = readFileSync(new URL('./useChatStreamHandler.ts', import.meta.url), 'utf8')
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const exports: Record<string, any> = {}
  vm.runInNewContext(compiled, {
    exports,
    console,
    require: () => ({}),
  })
  pendingPromptEvents = exports.pendingPromptEvents
}

test('a pending secure form is restored as its card, without any value', () => {
  const [card] = pendingPromptEvents({
    id: 'p1', kind: 'secure_form', status: 'suspended', tool_call_id: 'tc1',
    request: { title: '登录', kind: 'login', site: 'a.com' }, created_at: '2026-10-28T08:00:00Z',
  })
  assert.equal(card.kind, 'secure_form')
  assert.equal(card.resolved, false)
  assert.equal(card.request.site, 'a.com')
  assert.equal('response' in card, false)
})

test('an answered secure form shows as submitted', () => {
  const [card] = pendingPromptEvents({
    id: 'p1', kind: 'secure_form', status: 'done', tool_call_id: 'tc1',
    request: { title: '登录', kind: 'login', site: 'a.com' }, response: { handle: 'cred_1', fields: ['password'] },
  })
  assert.equal(card.resolved, true)
  assert.equal(card.answered, true)
})
