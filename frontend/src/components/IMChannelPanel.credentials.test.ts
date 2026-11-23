import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { fixture, flush } from '../test-utils/vueFixture'
import { normalizeOptionalString } from '../utils/optionalString'

function panel(t: TestContext) {
  const updates: Record<string, unknown>[] = []
  const warnings: string[] = []
  const channel = {
    id: 'channel', agent_id: 'agent', name: 'Bot', platform: 'feishu', mode: 'websocket',
    output_mode: 'stream', enabled: true, credentials_configured: true,
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
      updateIMChannel: async (_id: string, data: Record<string, unknown>) => updates.push(data),
    },
  })
  return { vm, channel, updates, warnings }
}

for (const platform of ['feishu', 'wechat', 'yunzhijia', 'mattermost']) {
  test(`editing ${platform} metadata keeps its configured credentials`, async t => {
    const { vm, channel, updates, warnings } = panel(t)
    channel.platform = platform
    await vm.editChannel({ id: channel.id, agent_id: channel.agent_id })
    await flush()
    vm.formData.name = 'Updated name'
    await vm.handleSave()
    assert.equal(warnings.length, 0)
    assert.equal(updates.length, 1)
    assert.equal(updates[0].name, 'Updated name')
    assert.equal(Object.hasOwn(updates[0], 'credentials'), false)
  })
}

test('replacing Feishu credentials requires both fields and sends the complete group', async t => {
  const { vm, channel, updates, warnings } = panel(t)
  await vm.editChannel(channel)
  vm.toggleCredentialReplacement()
  vm.formData.credentials.app_id = 'new-id'
  await vm.handleSave()
  assert.equal(updates.length, 0)
  assert.equal(warnings.length, 1)
  vm.formData.credentials.app_secret = 'new-secret'
  await vm.handleSave()
  assert.deepEqual(JSON.parse(JSON.stringify(updates[0].credentials)), {
    app_id: 'new-id', app_secret: 'new-secret',
  })
})

test('cancelling a credential replacement leaves the group out of the update', async t => {
  const { vm, channel, updates } = panel(t)
  await vm.editChannel(channel)
  vm.toggleCredentialReplacement()
  vm.formData.credentials.app_id = 'discarded'
  vm.toggleCredentialReplacement()
  await vm.handleSave()
  assert.equal(Object.hasOwn(updates[0], 'credentials'), false)
})

test('an unbound WeChat channel still requires QR binding', async t => {
  const { vm, channel, updates, warnings } = panel(t)
  await vm.editChannel({ ...channel, platform: 'wechat', credentials_configured: false })
  await vm.handleSave()
  assert.equal(updates.length, 0)
  assert.deepEqual(warnings, ['agentEditor.im.wechatScanBind'])
})
