import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { fixture } from '../test-utils/vueFixture.ts'

function setup(t: TestContext, props: Record<string, unknown>, create = async () => ({ data: { id: 'session' } })) {
  const f = fixture(t)
  const createSessions = t.mock.fn(create)
  const push = t.mock.fn(async (_route: unknown) => undefined)
  const error = t.mock.fn()
  const add = t.mock.fn()
  const first = t.mock.fn()
  const component = f.component(new URL('./ResourceQuickUse.vue', import.meta.url), {
    'vue-router': { useRouter: () => ({ push }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { error } },
    '@/api/chat/index': { createSessions },
    '@/api/agent': { BUILTIN_QUICK_ANSWER_ID: 'builtin-quick-answer' },
    '@/stores/menu': { useMenuStore: () => ({ updataMenuChildren: add, changeIsFirstSession: first }) },
  }, { resourceName: 'Resource', ...props })
  return { ...component, createSessions, push, error, add }
}

test('knowledge base quick-use creates a fresh chat with an explicit KB and default agent', async t => {
  const f = setup(t, { knowledgeBaseId: 'kb-1' })
  await f.vm.startChat()
  assert.equal(f.createSessions.mock.callCount(), 1)
  assert.deepEqual(JSON.parse(JSON.stringify(f.push.mock.calls[0].arguments[0])), {
    path: '/platform/chat/session', query: { agent_id: 'builtin-quick-answer', knowledge_base_id: 'kb-1' },
  })
  assert.equal(f.add.mock.calls[0].arguments[0].id, 'session')
})

test('shared agent quick-use retains its source tenant without selecting a KB', async t => {
  const f = setup(t, { agentId: 'agent-1', sourceTenantId: 42 })
  await f.vm.startChat()
  assert.deepEqual(JSON.parse(JSON.stringify(f.push.mock.calls[0].arguments[0])), {
    path: '/platform/chat/session', query: { agent_id: 'agent-1', source_tenant_id: '42' },
  })
})

test('own agent quick-use does not carry a foreign tenant', async t => {
  const f = setup(t, { agentId: 'agent-1' })
  await f.vm.startChat()
  assert.deepEqual(JSON.parse(JSON.stringify(f.push.mock.calls[0].arguments[0])), {
    path: '/platform/chat/session', query: { agent_id: 'agent-1' },
  })
})

test('pending creation ignores duplicate clicks and resets loading after failure', async t => {
  let reject!: (reason: Error) => void
  const response = new Promise<{ data: { id: string } }>((_, fail) => { reject = fail })
  const f = setup(t, { agentId: 'agent-1' }, () => response)
  const pending = f.vm.startChat()
  await f.vm.startChat()
  assert.equal(f.createSessions.mock.callCount(), 1)
  assert.equal(f.vm.opening, true)
  reject(new Error('network unavailable'))
  await pending
  assert.equal(f.error.mock.callCount(), 1)
  assert.equal(f.push.mock.callCount(), 0)
  assert.equal(f.add.mock.callCount(), 0)
  assert.equal(f.vm.opening, false)
})

test('disabled or missing resources do not create a session', async t => {
  for (const props of [{ agentId: 'disabled-agent', disabled: true }, {}]) {
    await t.test(JSON.stringify(props), async t => {
      const f = setup(t, props)
      await f.vm.startChat()
      assert.equal(f.createSessions.mock.callCount(), 0)
    })
  }
})
