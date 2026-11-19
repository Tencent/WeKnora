import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import test from 'node:test'
import ts from 'typescript'

// The embed leg of a card click, exercised without a browser: the function is
// sliced out of action.ts, transpiled and run against a stubbed post().
// Expected URL and header names below are literals on purpose — reading them
// from the module under test could never fail when they drift (a same-source
// constant pin is vacuous).
const source = readFileSync(new URL('./action.ts', import.meta.url), 'utf8')
const start = source.indexOf('export async function executeEmbedAgentAction')
assert.ok(start >= 0, 'executeEmbedAgentAction not found in action.ts')
const logic = source.slice(start).replace('export async function', 'async function')
assert.ok(logic.length > 200, 'slice too short — test would assert nothing')

const compiled = ts.transpileModule(logic, {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText

function harness() {
  const calls = []
  const sandbox = {
    post: async (url, body, opts) => { calls.push({ url, body, opts }); return { data: { status: 'started' } } },
  }
  vm.runInNewContext(compiled + '\nthis.run = executeEmbedAgentAction;', sandbox)
  return { calls, run: sandbox.run }
}

test('posts to the embed route with Embed auth, sig and visitor headers', async () => {
  const { calls, run } = harness()
  await run('ch-9', 'ems_tok', 'sess-9', 'sig-abc', 'visitor-uuid', { action_name: 'confirm' })
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, '/api/v1/embed/ch-9/sessions/sess-9/agent-actions/execute')
  assert.equal(calls[0].opts.headers.Authorization, 'Embed ems_tok')
  assert.equal(calls[0].opts.headers['X-Embed-Session'], 'sig-abc')
  assert.equal(calls[0].opts.headers['X-Embed-Visitor'], 'visitor-uuid')
  assert.deepEqual(calls[0].body, { action_name: 'confirm' })
})

test('omits the visitor header when empty, never the sig header', async () => {
  const { calls, run } = harness()
  await run('ch-9', 'ems_tok', 'sess-9', 'sig-abc', '', {})
  assert.ok(!('X-Embed-Visitor' in calls[0].opts.headers))
  assert.equal(calls[0].opts.headers['X-Embed-Session'], 'sig-abc')
})
