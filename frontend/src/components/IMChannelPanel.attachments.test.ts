import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { fixture, flush } from '../test-utils/vueFixture'
import { normalizeOptionalString } from '../utils/optionalString'

function panel(t: TestContext, knowledgeBaseId = '') {
  const updates: Record<string, unknown>[] = []
  const warnings: string[] = []
  const channel = {
    id: 'channel', agent_id: 'agent', name: 'Bot', platform: 'feishu', mode: 'websocket',
    output_mode: 'stream', enabled: true, knowledge_base_id: knowledgeBaseId, credentials_configured: true, credentials: {},
  }
  const resources = {
    agents: [], rawKnowledgeBases: [], ensureAgents() {}, ensureKnowledgeBases() {},
  }
  const logos = Object.fromEntries(
    ['wecom', 'feishu', 'lark', 'slack', 'telegram', 'dingtalk', 'mattermost', 'wechat', 'qqbot', 'yunzhijia']
      .map(platform => [`@/assets/img/im/${platform}.${platform === 'qqbot' ? 'png' : 'svg'}`, platform]),
  )
  const { vm } = fixture(t).component(new URL('./IMChannelPanel.vue', import.meta.url), {
    ...logos,
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { warning: (key: string) => warnings.push(key), success() {} } },
    '@/utils/clipboard': {},
    '@/utils/optionalString': { normalizeOptionalString },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/chatResources': { useChatResourcesStore: () => resources },
    '@/api/agent': {
      listAllIMChannels: async () => ({ data: [] }),
      listIMChannels: async () => ({ data: [channel] }),
      createIMChannel: async (_id: string, data: Record<string, unknown>) => updates.push(data),
      updateIMChannel: async (_id: string, data: Record<string, unknown>) => updates.push(data),
    },
  })
  return { vm, channel, updates, warnings }
}

test('turning off saving clears the existing destination on update', async t => {
  const { vm, channel, updates } = panel(t, 'kb-a')
  await vm.editChannel(channel)
  assert.equal(vm.formData.save_attachments, true)
  vm.formData.save_attachments = false
  await vm.handleSave()
  assert.equal(updates[0].knowledge_base_id, '')
})

test('a new channel saves attachments only after a destination is selected', async t => {
  const { vm, updates, warnings } = panel(t)
  vm.openCreate()
  vm.formData.target_agent_id = 'agent'
  assert.equal(vm.formData.save_attachments, false)
  vm.formData.save_attachments = true
  vm.wizardStep = 2
  await vm.handleDrawerConfirm()
  assert.equal(vm.wizardStep, 2)
  await vm.handleSave()
  assert.equal(updates.length, 0)
  assert.ok(warnings.length > 0)
  vm.formData.knowledge_base_id = 'kb-b'
  await vm.handleDrawerConfirm()
  assert.equal(vm.wizardStep, 3)
  await vm.handleSave()
  assert.equal(updates[0].knowledge_base_id, 'kb-b')
})

test('opening a new form resets the preceding channel’s setting', async t => {
  const { vm, channel, updates } = panel(t, 'kb-a')
  await vm.editChannel(channel)
  await flush()
  vm.openCreate()
  vm.formData.target_agent_id = 'agent'
  assert.equal(vm.formData.save_attachments, false)
  await vm.handleSave()
  assert.equal(updates[0].knowledge_base_id, '')
})
