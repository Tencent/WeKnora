import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick, ref } from 'vue'
import * as pluginUtils from '../../../utils/pluginMarketplace'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./PluginInstallDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'plugin-install-dialog-test' }).content
  .replace(/__expose\([^;]*\);/g, '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

const flush = async () => {
  for (let i = 0; i < 5; i++) {
    await new Promise<void>(resolve => setImmediate(resolve))
    await nextTick()
  }
}

async function fixture(options: {
  preflight: Record<string, unknown>
  hostOnly?: boolean
  sandboxes?: Array<{ id: string; name: string; sandbox_type: string }>
  installError?: string
  installResult?: Record<string, unknown>
  admin?: boolean
}) {
  const installs: Array<{ mp: string; name: string; body: any }> = []
  const persisted: string[] = []
  const errors: string[] = []
  const toasts: Array<{ kind: string; message: string }> = []
  const emitted: unknown[] = []
  const installerModelId = ref('model-1')
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console: { error() {} },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string, params?: any) => params ? `${key}:${JSON.stringify(params)}` : key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: {
        success: (m: string) => toasts.push({ kind: 'success', message: m }),
        info: (m: string) => toasts.push({ kind: 'info', message: m }),
        warning: (m: string) => toasts.push({ kind: 'warning', message: m }),
        error: (m: string) => errors.push(m),
      } }
      if (name === '@/api/plugin') return {
        preflightPlugin: async () => ({ data: options.preflight }),
        installPlugin: async (mp: string, pluginName: string, body: any) => {
          if (options.installError) throw { message: options.installError }
          installs.push({ mp, name: pluginName, body })
          return { data: options.installResult || { install_id: 'i1', status: 'installing', async_installation: true } }
        },
      }
      if (name === '@/api/system') return {
        isNamedSandboxBackend: (type: string) => type === 'docker' || type === 'e2b',
        listSandboxConfigs: async () => ({ data: options.sandboxes || [] }),
      }
      if (name === '@/stores/auth') return {
        useAuthStore: () => ({ canAccessAllTenants: false, hasRole: () => options.admin !== false }),
      }
      if (name === '@/stores/deploymentCapabilities') return {
        useDeploymentCapabilitiesStore: () => ({
          isSupported: (key: string) => key === 'settings.sandbox.host' ? Boolean(options.hostOnly) : !options.hostOnly,
        }),
      }
      if (name === '@/stores/ui') return { useUIStore: () => ({ openSettings() {} }) }
      if (name === '@/composables/useSkillInstallerModel') return {
        useSkillInstallerModel: () => ({
          installerModelId, savingInstallerModel: ref(false), loadInstallerModel: async () => {},
          persistInstallerModel: async (id: string) => { persisted.push(id) }, onInstallerModelChange() {},
        }),
      }
      if (name === '@/utils/skillTarget') return require('../../../utils/skillTarget')
      if (name === '@/utils/pluginMarketplace') return pluginUtils
      return { default: {} }
    },
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp(component, {
    visible: true, marketplaceId: 'mp-1',
    plugin: { name: 'neon', skill_count: 2, mcp_count: 1, requires_sandbox: true, installed: false },
    onInstalled: (result: unknown) => emitted.push(result),
  })
  const vm: any = app.mount({})
  await flush()
  return { vm, installs, persisted, errors, toasts, emitted, installerModelId, close: () => app.unmount() }
}

const neonPreflight = {
  plugin_name: 'neon', skill_count: 2, mcp_count: 1, requires_sandbox: true,
  env_declarations: [
    { name: 'NEON_API_KEY', required: true, description: 'API key' },
    { name: 'NEON_REGION' },
  ],
}

test('install stays disabled until a sandbox is picked and required env is filled', async () => {
  const f = await fixture({
    preflight: neonPreflight,
    sandboxes: [
      { id: 'cfg-a', name: 'A', sandbox_type: 'docker' },
      { id: 'cfg-b', name: 'B', sandbox_type: 'e2b' },
      { id: 'cfg-x', name: 'X', sandbox_type: 'local' },
    ],
  })
  try {
    assert.deepEqual(f.vm.sandboxes.map((s: any) => s.id), ['cfg-a', 'cfg-b'])
    assert.equal(f.vm.canInstall, false)
    assert.match(f.vm.blockReason, /sandboxRequired/)

    f.vm.toggleSandbox('cfg-b', true)
    assert.equal(f.vm.canInstall, false)
    assert.match(f.vm.blockReason, /NEON_API_KEY/)

    f.vm.envValues.NEON_API_KEY = 'secret'
    assert.equal(f.vm.canInstall, true)

    f.installerModelId.value = ''
    assert.equal(f.vm.canInstall, false, 'skills need an installer model')
    f.installerModelId.value = 'model-1'

    await f.vm.install()
    assert.equal(f.installs.length, 1)
    assert.deepEqual(JSON.parse(JSON.stringify(f.installs[0]!.body)), {
      sandbox_config_ids: ['cfg-b'],
      env: { NEON_API_KEY: 'secret' },
    })
    assert.deepEqual(f.persisted, ['model-1'])
    assert.equal(f.emitted.length, 1)
  } finally { f.close() }
})

test('a workspace with no sandbox shows the empty state and cannot install', async () => {
  const f = await fixture({ preflight: neonPreflight, sandboxes: [] })
  try {
    assert.equal(f.vm.sandboxes.length, 0)
    f.vm.envValues.NEON_API_KEY = 'secret'
    assert.equal(f.vm.canInstall, false)
  } finally { f.close() }
})

test('Lite targets the fixed host sandbox without a picker', async () => {
  const f = await fixture({ preflight: neonPreflight, hostOnly: true })
  try {
    assert.deepEqual([...f.vm.targetIds], ['host'])
    f.vm.envValues.NEON_API_KEY = 'secret'
    assert.equal(f.vm.canInstall, true)
    await f.vm.install()
    assert.deepEqual([...f.installs[0]!.body.sandbox_config_ids], ['host'])
  } finally { f.close() }
})

test('an MCP install that still needs OAuth is not a success toast', async () => {
  const f = await fixture({
    preflight: { plugin_name: 'calcom', skill_count: 0, mcp_count: 1, requires_sandbox: false, env_declarations: [] },
    installResult: {
      install_id: 'i1', status: 'needs_oauth', async_installation: false,
      mcp_oauth_states: { 'acme-plugins__calcom': 'needs_oauth' },
    },
  })
  try {
    await f.vm.install()
    assert.deepEqual(f.toasts.map((toast) => toast.kind), ['info'])
    assert.match(f.toasts[0]!.message, /needsOAuth/)
    assert.equal(f.emitted.length, 1)
  } finally { f.close() }
})

test('a member installs a skill plugin without rewriting the installer model', async () => {
  const f = await fixture({
    admin: false,
    preflight: neonPreflight,
    sandboxes: [{ id: 'cfg-a', name: 'A', sandbox_type: 'docker' }],
  })
  try {
    f.vm.envValues.NEON_API_KEY = 'secret'
    f.installerModelId.value = ''
    assert.equal(f.vm.canInstall, true)
    await f.vm.install()
    assert.equal(f.installs.length, 1)
    assert.deepEqual(f.persisted, [])
  } finally { f.close() }
})

test('an MCP-only plugin installs without sandbox or installer model', async () => {
  const f = await fixture({
    preflight: { plugin_name: 'calcom', skill_count: 0, mcp_count: 1, requires_sandbox: false, env_declarations: [] },
  })
  try {
    f.installerModelId.value = ''
    assert.equal(f.vm.canInstall, true)
    await f.vm.install()
    assert.deepEqual(JSON.parse(JSON.stringify(f.installs[0]!.body)), { sandbox_config_ids: [], env: {} })
    assert.deepEqual(f.persisted, [])
  } finally { f.close() }
})

test('backend conflicts surface as an error and keep the dialog open', async () => {
  const f = await fixture({
    preflight: { plugin_name: 'calcom', skill_count: 0, mcp_count: 1, requires_sandbox: false, env_declarations: [] },
    installError: 'plugin_name_taken: 插件名 "calcom" 已由市场 "other" 安装',
  })
  try {
    await f.vm.install()
    assert.equal(f.errors.length, 1)
    assert.match(f.errors[0]!, /plugin_name_taken/)
    assert.equal(f.emitted.length, 0)
    assert.equal(f.vm.installing, false)
  } finally { f.close() }
})
