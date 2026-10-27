import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'
import * as mcpOAuthPopup from '../../../utils/mcpOAuthPopup'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./McpServiceDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'mcp-dialog-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

function fixture(generate: () => Promise<string> = async () => 'Query logs by module and time.', initialStep: 0 | 1 = 0, oauthApi: Record<string, unknown> = {}, authType = '') {
  const updates: Array<{ id: string; data: Record<string, unknown> }> = []
  const warnings: string[] = []
  const successes: string[] = []
  const errors: string[] = []
  const timers = new Map<number, () => Promise<void>>()
  const popups: Array<{ closed: boolean; close: () => void }> = []
  let nextTimer = 0
  const props = reactive({ visible: true, service: { id: 'one', name: 'Logs', description: 'Legacy usage', transport_type: 'sse', url: 'https://example.com/mcp', auth_config: { auth_type: authType } }, mode: 'edit', initialStep })
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key, locale: ref('zh-CN') }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { warning: (text: string) => warnings.push(text), success: (text: string) => successes.push(text), error: (text: string) => errors.push(text) } }
      if (name === '@/api/mcp-service') return {
        MCP_OAUTH_CALLBACK_PATH: '/callback',
        getMCPOAuthAuthorizationStatus: async () => ({ authorized: false, state: 'reauth_required' }),
        getMCPOAuthAuthorizeURL: async () => ({ authorizationUrl: 'https://auth.example/consent', authorizationAttempt: 'attempt' }),
        getMCPOAuthStatus: async () => false,
        revokeMCPOAuthToken: async () => {},
        generateMCPUsageInstructions: generate,
        updateMCPService: async (id: string, data: Record<string, unknown>) => {
          updates.push({ id, data })
          return { ...props.service, ...data }
        },
        ...oauthApi,
      }
      if (name === '@/utils/mcpOAuthPopup') return mcpOAuthPopup
      return { default: {} }
    },
    URL, console,
    window: {
      location: { origin: 'https://weknora.example' },
      setInterval: (callback: () => Promise<void>) => { timers.set(++nextTimer, callback); return nextTimer },
      clearInterval: (id: number) => timers.delete(id),
      open: () => {
        const popup = { closed: false, close() { this.closed = true } }
        popups.push(popup)
        return popup
      },
    },
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance }) })
  app.mount({})
  let mounted = true
  return { vm: instance.value, props, updates, warnings, successes, errors, timers, popups, close: () => { if (mounted) { mounted = false; app.unmount() } } }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

async function oauthFixture(api: Record<string, unknown> = {}) {
  const f = fixture(undefined, 0, api, 'oauth')
  await nextTick()
  return f
}

function poll(f: Awaited<ReturnType<typeof oauthFixture>>) {
  assert.equal(f.timers.size, 1)
  return Array.from(f.timers.values())[0]!()
}

test('tool shortcut opens the saved service directly without resaving its connection', () => {
  const f = fixture(undefined, 1)
  try {
    assert.equal(f.vm.step, 1)
    assert.equal(f.vm.currentService.id, 'one')
    assert.equal(f.updates.length, 0)
  } finally { f.close() }
})

test('usage instructions are required for final save and legacy description is editable there', async () => {
  const f = fixture()
  try {
    assert.equal(f.vm.formData.usage_instructions, 'Legacy usage')
    assert.equal('description' in f.vm.formData, false)
    f.vm.toolsSynced = true
    f.vm.formData.usage_instructions = '  \n  '
    await f.vm.handleSubmit()
    assert.equal(f.updates.length, 0)
    assert.deepEqual(f.warnings, ['mcpMetadata.instructionsRequired'])
    f.vm.formData.usage_instructions = '  Query logs  '
    await f.vm.handleSubmit()
    assert.equal(f.updates.length, 1)
    assert.deepEqual({ ...f.updates[0]?.data }, { usage_instructions: 'Query logs' })
  } finally { f.close() }
})

test('connection save omits usage instructions so new services can sync tools first', () => {
  const f = fixture()
  try {
    f.vm.formData.usage_instructions = ''
    const body = f.vm.buildPayload(true)
    assert.equal('usage_instructions' in body, false)
    assert.equal('description' in body, false)
    assert.equal(body.url, 'https://example.com/mcp')
  } finally { f.close() }
})

test('AI generation needs synced tools and fills the editor without saving', async () => {
  let calls = 0
  const f = fixture(async () => { calls++; return 'Generated usage' })
  try {
    await f.vm.handleGenerateUsage()
    assert.equal(calls, 0)
    f.vm.toolsSynced = true
    await f.vm.handleGenerateUsage()
    assert.equal(calls, 1)
    assert.equal(f.vm.formData.usage_instructions, 'Generated usage')
    assert.equal(f.updates.length, 0)
    assert.equal(f.vm.generatingUsage, false)
  } finally { f.close() }
})

test('generation failure preserves existing instructions', async () => {
  const f = fixture(async () => { throw new Error('unavailable') })
  try {
    f.vm.toolsSynced = true
    await f.vm.handleGenerateUsage()
    assert.equal(f.vm.formData.usage_instructions, 'Legacy usage')
    assert.equal(f.vm.generatingUsage, false)
    assert.equal(f.updates.length, 0)
  } finally { f.close() }
})

test('closing or switching service prevents a late AI result from overwriting the editor', async () => {
  for (const action of ['close', 'switch']) {
    let resolve!: (value: string) => void
    const pending = new Promise<string>(done => { resolve = done })
    const f = fixture(() => pending)
    try {
      f.vm.toolsSynced = true
      const generation = f.vm.handleGenerateUsage()
      assert.equal(f.vm.generatingUsage, true)
      if (action === 'close') f.props.visible = false
      else f.props.service = { ...f.props.service, id: 'two', description: 'Other service' }
      await nextTick()
      resolve('Stale generated usage')
      await generation
      assert.equal(f.vm.formData.usage_instructions, action === 'close' ? 'Legacy usage' : 'Other service')
      assert.equal(f.vm.generatingUsage, false)
    } finally { f.close() }
  }
})

test('OAuth payload always carries the client ID and sends its secret only on create', () => {
  const f = fixture()
  try {
    f.vm.formData.auth_config.auth_type = 'oauth'
    f.vm.formData.auth_config.client_id = '  pre-registered  '
    f.vm.formData.auth_config.client_secret = 'shh'
    const created = f.vm.buildPayload(true)
    assert.equal(created.auth_config.client_id, 'pre-registered')
    assert.equal(created.auth_config.client_secret, 'shh')
    const edited = f.vm.buildPayload(false)
    assert.equal(edited.auth_config.client_id, 'pre-registered')
    assert.equal('client_secret' in edited.auth_config, false)

    f.vm.formData.auth_config.client_id = ''
    assert.equal(f.vm.buildPayload(false).auth_config.client_id, '', 'clearing returns to dynamic registration')
  } finally { f.close() }
})

test('OAuth client secret is edited through the credentials card', () => {
  const f = fixture()
  try {
    f.vm.formData.auth_config.auth_type = 'oauth'
    assert.deepEqual(Array.from(f.vm.credentialFields, (field: { key: string }) => field.key), ['client_secret'])
    f.vm.formData.auth_config.auth_type = 'api_key'
    assert.deepEqual(Array.from(f.vm.credentialFields, (field: { key: string }) => field.key), ['api_key'])
    f.vm.formData.auth_config.auth_type = ''
    assert.equal(f.vm.credentialFields.length, 0)
  } finally { f.close() }
})

test('API key payload never carries OAuth client fields', () => {
  const f = fixture()
  try {
    f.vm.formData.auth_config.auth_type = 'api_key'
    f.vm.formData.auth_config.client_id = 'left-over'
    f.vm.formData.auth_config.client_secret = 'left-over'
    const body = f.vm.buildPayload(true)
    assert.equal('client_id' in body.auth_config, false)
    assert.equal('client_secret' in body.auth_config, false)
  } finally { f.close() }
})

test('closing and reopening another service discards an in-flight OAuth poll', async () => {
  const status = deferred<boolean>()
  const calls: string[] = []
  const f = await oauthFixture({ getMCPOAuthStatus: (id: string) => { calls.push(id); return status.promise } })
  try {
    await f.vm.startAuthorize('one')
    const pending = poll(f)
    f.props.visible = false
    await nextTick()
    assert.equal(f.timers.size, 0)
    assert.equal(f.vm.oauthAuthorizing, false)
    f.props.service = { ...f.props.service, id: 'two' }
    f.props.visible = true
    await nextTick()
    status.resolve(true)
    await pending
    assert.deepEqual(calls, ['one'])
    assert.equal(f.vm.currentService.id, 'two')
    assert.equal(f.vm.oauthAuthorized, false)
    assert.equal(f.vm.oauthTokenState, 'reauth_required')
    assert.deepEqual(f.successes, [])
    assert.equal(f.popups[0]?.closed, false)
  } finally { f.close() }
})

test('saving and rebinding the same service preserves its active OAuth attempt', async () => {
  const f = await oauthFixture({ getMCPOAuthStatus: async () => true })
  try {
    f.vm.formRef = { validate: async () => true }
    await f.vm.handleAuthorize()
    assert.equal(f.updates.length, 1)
    f.props.service = { ...f.props.service, name: 'Saved service' }
    await nextTick()
    assert.equal(f.vm.oauthAuthorizing, true)
    await poll(f)
    assert.equal(f.vm.oauthTokenState, 'authorized')
    assert.equal(f.timers.size, 0)
  } finally { f.close() }
})

test('switching services discards a late OAuth status refresh', async () => {
  const status = deferred<{ authorized: boolean; state: string }>()
  const f = await oauthFixture({ getMCPOAuthAuthorizationStatus: (id: string) => id === 'one' ? status.promise : Promise.resolve({ authorized: false, state: 'reauth_required' }) })
  try {
    assert.equal(f.vm.oauthChecking, true)
    f.props.service = { ...f.props.service, id: 'two' }
    await nextTick()
    status.resolve({ authorized: true, state: 'authorized' })
    await status.promise
    await nextTick()
    assert.equal(f.vm.currentService.id, 'two')
    assert.equal(f.vm.oauthAuthorized, false)
    assert.equal(f.vm.oauthTokenState, 'reauth_required')
    assert.equal(f.vm.oauthChecking, false)
  } finally { f.close() }
})

test('a new authorization replaces the old poll and ignores its late completion', async () => {
  const oldStatus = deferred<boolean>()
  let attempt = 0
  const calls: string[] = []
  const f = await oauthFixture({
    getMCPOAuthAuthorizeURL: async () => ({ authorizationUrl: 'https://auth.example/consent', authorizationAttempt: `attempt-${++attempt}` }),
    getMCPOAuthStatus: (_id: string, current: string) => { calls.push(current); return current === 'attempt-1' ? oldStatus.promise : Promise.resolve(true) },
  })
  try {
    await f.vm.startAuthorize('one')
    const pending = poll(f)
    await f.vm.startAuthorize('one')
    assert.equal(f.timers.size, 1)
    oldStatus.resolve(true)
    await pending
    assert.equal(f.vm.oauthAuthorized, false)
    assert.equal(f.vm.oauthAuthorizing, true)
    assert.deepEqual(f.successes, [])
    await poll(f)
    assert.deepEqual(calls, ['attempt-1', 'attempt-2'])
    assert.equal(f.vm.oauthAuthorized, true)
    assert.equal(f.vm.oauthTokenState, 'authorized')
    assert.equal(f.vm.oauthAuthorizing, false)
    assert.equal(f.timers.size, 0)
    assert.deepEqual(f.successes, ['mcpServiceDialog.toasts.authorized'])
    assert.equal(f.popups[1]?.closed, true)
  } finally { f.close() }
})

test('late authorization URL responses are ignored after close, service switch, unmount or a new attempt', async () => {
  for (const scenario of ['close', 'switch', 'unmount', 'retry'].flatMap(action => [
    { action, fail: false }, { action, fail: true },
  ])) {
    const { action, fail } = scenario
    const authorization = deferred<{ authorizationUrl: string; authorizationAttempt: string }>()
    let calls = 0
    const f = await oauthFixture({ getMCPOAuthAuthorizeURL: () => ++calls === 1 ? authorization.promise : Promise.resolve({ authorizationUrl: 'https://auth.example/new', authorizationAttempt: 'new' }) })
    try {
      const pending = f.vm.startAuthorize('one')
      if (action === 'close') f.props.visible = false
      if (action === 'switch') f.props.service = { ...f.props.service, id: 'two' }
      if (action === 'unmount') f.close()
      if (action === 'retry') await f.vm.startAuthorize('one')
      await nextTick()
      if (fail) authorization.reject(new Error('old authorization failed'))
      else authorization.resolve({ authorizationUrl: 'https://auth.example/old', authorizationAttempt: 'old' })
      await pending
      assert.equal(f.popups.length, action === 'retry' ? 1 : 0, action)
      assert.equal(f.timers.size, action === 'retry' ? 1 : 0, action)
      assert.equal(f.vm.oauthAuthorizing, action === 'retry', action)
      assert.deepEqual(f.errors, [], action)
    } finally { f.close() }
  }
})

test('unmount clears the OAuth poll and ignores its in-flight result', async () => {
  const status = deferred<boolean>()
  const f = await oauthFixture({ getMCPOAuthStatus: () => status.promise })
  await f.vm.startAuthorize('one')
  const pending = poll(f)
  f.close()
  assert.equal(f.timers.size, 0)
  status.resolve(true)
  await pending
  assert.equal(f.vm.oauthAuthorized, false)
  assert.equal(f.vm.oauthAuthorizing, false)
  assert.deepEqual(f.successes, [])
})

test('OAuth success invalidates overlapping polls from the completed attempt', async () => {
  const lateStatus = deferred<boolean>()
  let calls = 0
  const f = await oauthFixture({ getMCPOAuthStatus: () => ++calls === 1 ? lateStatus.promise : Promise.resolve(true) })
  try {
    await f.vm.startAuthorize('one')
    const pending = poll(f)
    await poll(f)
    lateStatus.resolve(false)
    await pending
    assert.equal(f.vm.oauthAuthorized, true)
    assert.equal(f.vm.oauthTokenState, 'authorized')
    assert.equal(f.timers.size, 0)
    assert.deepEqual(f.successes, ['mcpServiceDialog.toasts.authorized'])
  } finally { f.close() }
})

test('transient poll errors cannot report an existing token as a new authorization', async () => {
  const f = await oauthFixture({ getMCPOAuthStatus: async () => { throw new Error('offline') } })
  try {
    f.vm.oauthAuthorized = true
    f.vm.oauthTokenState = 'authorized'
    await f.vm.startAuthorize('one')
    await poll(f)
    assert.equal(f.timers.size, 1)
    assert.equal(f.vm.oauthAuthorizing, true)
    assert.deepEqual(f.successes, [])
    f.popups[0]!.closed = true
    await poll(f)
    assert.equal(f.timers.size, 1, 'closing the popup does not cancel the poll')
    assert.equal(f.vm.oauthAuthorizing, true)
    assert.equal(f.vm.oauthAuthorized, true)
    assert.deepEqual(f.successes, [])
  } finally { f.close() }
})

test('revoking OAuth invalidates an old poll and ignores a revoke response for another service', async () => {
  const status = deferred<boolean>()
  const revoke = deferred<void>()
  const revoked: string[] = []
  const f = await oauthFixture({
    getMCPOAuthStatus: () => status.promise,
    getMCPOAuthAuthorizationStatus: async (id: string) => ({ authorized: id === 'two', state: id === 'two' ? 'authorized' : 'reauth_required' }),
    revokeMCPOAuthToken: (id: string) => { revoked.push(id); return revoke.promise },
  })
  try {
    await f.vm.startAuthorize('one')
    const pending = poll(f)
    const revoking = f.vm.handleRevokeOAuth()
    assert.equal(f.timers.size, 0)
    f.props.service = { ...f.props.service, id: 'two' }
    await nextTick()
    status.resolve(false)
    revoke.resolve()
    await Promise.all([pending, revoking])
    assert.deepEqual(revoked, ['one'])
    assert.equal(f.vm.oauthAuthorized, true)
    assert.equal(f.vm.oauthTokenState, 'authorized')
    assert.deepEqual(f.successes, [])
  } finally { f.close() }
})
