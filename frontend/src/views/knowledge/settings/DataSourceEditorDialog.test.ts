import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'

const require = createRequire(import.meta.url)
// The dialog validates credentials with the real schema helpers.
const schemaModule = require('../../../components/schema-form/schema.ts')
const sourceModule = require('../../../components/schema-form/source.ts')
const filename = fileURLToPath(new URL('./DataSourceEditorDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'datasource-editor-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

async function fixture({
  configured = true,
  create = false,
  type = 'gitlab',
  settings = { projects: [{ project_id: '123', paths: [] }] },
}: {
  configured?: boolean
  create?: boolean
  type?: string
  settings?: Record<string, unknown>
} = {}) {
  const calls: Array<{ method: string; args: any[] }> = []
  let storedToken = configured ? 'expired-token' : ''
  const api = {
    // Same credential form the backend serves for GitLab.
    async getConnectorTypes() {
      return [{
        type: 'gitlab',
        config_schema: {
          type: 'object',
          required: ['base_url', 'access_token'],
          properties: {
            base_url: { type: 'string', title: 'GitLab URL', 'x-order': 1 },
            access_token: { type: 'string', title: 'Personal access token', 'x-secret': true, 'x-order': 2 },
          },
        },
      }, { type: 'yuque', config_schema: { type: 'object', properties: {} } }, {
        type: 'acme.jira/jira', plugin_id: 'acme.jira',
        config_schema: { type: 'object', properties: { site: { type: 'string' } } },
      }]
    },
    async validateCredentials(type: string, credentials: Record<string, string>) {
      calls.push({ method: 'validateCredentials', args: [type, { ...credentials }] })
      if (credentials.access_token !== 'rotated-token') throw new Error('gitlab API /user: status 401')
    },
    async validateConnection(id: string) {
      calls.push({ method: 'validateConnection', args: [id] })
      if (storedToken !== 'rotated-token') throw new Error('gitlab API /user: status 401')
    },
    async updateDataSource(id: string, data: any) {
      calls.push({ method: 'updateDataSource', args: [id, JSON.parse(JSON.stringify(data))] })
      // The main update endpoint deliberately preserves stored credentials.
    },
    async putDataSourceCredentials(id: string, credentials: Record<string, string>) {
      calls.push({ method: 'putDataSourceCredentials', args: [id, { ...credentials }] })
      storedToken = credentials.access_token
    },
  }
  const props = reactive({
    visible: false, kbId: 'kb-one',
    dataSource: create ? null : {
      id: 'source-one', name: 'GitLab', type,
      credentials: { credentials: { configured } },
      config: { resource_ids: [], settings },
      sync_schedule: '0 0 */6 * * *', sync_mode: 'incremental',
      conflict_strategy: 'overwrite', sync_deletions: true,
    },
  })
  const exports: any = {}
  let formBinding: any
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') {
        return { useI18n: () => ({ t: (key: string) => key, te: () => true, locale: { value: 'en-US' } }) }
      }
      if (name === './connectorLabels') {
        return {
          connectorName: (d: { type: string }) => `datasource.connector.${d.type}`,
          connectorDescription: (d: { type: string }) => `datasource.connectorDesc.${d.type}`,
        }
      }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { warning() {}, success() {}, error() {} } }
      if (name === '@/api/datasource') return api
      if (name === '@/components/schema-form/schema') return schemaModule
      if (name === '@/components/schema-form/source') {
        return { valueAt: sourceModule.valueAt, provideSchemaFormSource() {} }
      }
      if (name === '@/components/schema-form/pluginSource') {
        return { pluginFormSource: (b: unknown) => { formBinding = b; return {} } }
      }
      if (name === '@/components/schema-form/useSchemaText') {
        return { useSchemaText: () => (schema: { title?: string }) => schema.title ?? '' }
      }
      return { default: {} }
    },
    URL, console,
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
  props.visible = true
  await nextTick()
  const vm = instance.value
  async function replace(token = 'rotated-token') {
    vm.enterReplaceCredentials()
    vm.form.config.credentials = { base_url: 'https://gitlab.example.com', access_token: token }
    await nextTick()
  }
  return {
    vm, calls, replace, formBinding, storedToken: () => storedToken, close: () => app.unmount(),
  }
}

test('rotated GitLab credentials are tested without updating the saved data source', async () => {
  const f = await fixture()
  try {
    await f.replace()
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'success')
    assert.deepEqual(f.calls, [{ method: 'validateCredentials', args: ['gitlab', {
      base_url: 'https://gitlab.example.com', access_token: 'rotated-token',
    }] }])
    assert.equal(f.storedToken(), 'expired-token')
  } finally { f.close() }
})

test('Next tests the replacement and final save commits credentials before settings', async () => {
  const f = await fixture()
  try {
    await f.replace()
    await f.vm.nextStep()
    assert.equal(f.vm.step, 2)
    await f.vm.nextStep()
    assert.equal(f.vm.step, 3)
    await f.vm.handleSubmit()
    assert.deepEqual(f.calls.map(call => call.method), [
      'validateCredentials', 'putDataSourceCredentials', 'updateDataSource',
    ])
    assert.equal(f.storedToken(), 'rotated-token')
    assert.deepEqual(f.calls[2].args[1].config.credentials, {})
  } finally { f.close() }
})

test('invalid replacement stays on the credentials step and can be corrected', async () => {
  const f = await fixture()
  try {
    await f.replace('invalid-token')
    await f.vm.nextStep()
    assert.equal(f.vm.step, 1)
    assert.equal(f.vm.testResult, 'error')
    assert.match(f.vm.testErrorMsg, /401/)
    assert.equal(f.storedToken(), 'expired-token')
    await f.replace()
    await f.vm.nextStep()
    assert.equal(f.vm.step, 2)
  } finally { f.close() }
})

test('testing unchanged credentials still validates the stored token', async () => {
  const f = await fixture()
  try {
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'error')
    assert.deepEqual(f.calls.map(call => call.method), ['updateDataSource', 'validateConnection'])
  } finally { f.close() }
})

test('an existing data source with no saved credentials tests the entered token', async () => {
  const f = await fixture({ configured: false })
  try {
    await f.replace()
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'success')
    assert.deepEqual(f.calls.map(call => call.method), ['validateCredentials'])
    assert.equal(f.storedToken(), '')
  } finally { f.close() }
})

test('new GitLab data sources continue to test credentials without persistence', async () => {
  const f = await fixture({ create: true })
  try {
    await f.vm.loadConnectorTypes()
    f.vm.selectType(f.vm.connectorTypes.find((def: any) => def.type === 'gitlab'))
    await f.replace()
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'success')
    assert.deepEqual(f.calls.map(call => call.method), ['validateCredentials'])
  } finally { f.close() }
})

test('a missing required credential blocks the connection test and marks the field', async () => {
  const f = await fixture({ create: true })
  try {
    await f.vm.loadConnectorTypes()
    f.vm.selectType(f.vm.connectorTypes.find((def: any) => def.type === 'gitlab'))
    f.vm.form.config.credentials = { base_url: 'https://gitlab.example.com' }
    await nextTick() // the user typed before clicking; edits clear old errors
    await f.vm.testConnection()
    assert.deepEqual(f.calls, [], 'nothing reaches the backend')
    assert.deepEqual(JSON.stringify(f.vm.credentialErrors), JSON.stringify([{ path: 'access_token', code: 'required' }]))
  } finally { f.close() }
})

test('a new Yuque data source adopts the TOC folder layout, but not the filter', async () => {
  const f = await fixture({ create: true })
  try {
    assert.equal(f.vm.form.config.settings.folder_mode, undefined)
    await f.vm.loadConnectorTypes()
    f.vm.selectType(f.vm.connectorTypes.find((def: any) => def.type === 'yuque'))
    assert.equal(f.vm.form.config.settings.folder_mode, 'toc')
    // The layout is a presentation choice; toc_only decides what may enter the
    // knowledge base, so a new source is deliberately left without it.
    assert.equal(f.vm.form.config.settings.toc_only, undefined)
    assert.equal(f.vm.yuqueTOCOnly, false)
  } finally { f.close() }
})

test('an existing Yuque data source reports its admission filter', async () => {
  const f = await fixture({ type: 'yuque', settings: { folder_mode: 'toc', toc_only: true } })
  try {
    assert.equal(f.vm.yuqueTOCOnly, true)
  } finally { f.close() }
})

test('an existing Yuque data source is never switched to the TOC layout', async () => {
  const f = await fixture({ type: 'yuque', settings: {} })
  try {
    // Opening a source created before this control existed must not inject
    // folder_mode: the connector default (flat) has to stand, otherwise editing
    // an unrelated field would silently re-file the whole knowledge base.
    assert.equal(f.vm.form.config.settings.folder_mode, undefined)
    assert.equal(f.vm.yuqueFolderMode, 'none')
  } finally { f.close() }
})

test('an existing Yuque data source on the TOC layout reports it', async () => {
  const f = await fixture({ type: 'yuque', settings: { folder_mode: 'toc' } })
  try {
    assert.equal(f.vm.yuqueFolderMode, 'toc')
  } finally { f.close() }
})

test('Cloud hierarchy limitation stays visible after an empty space expansion', async () => {
  const f = await fixture()
  try {
    f.vm.resources = [{
      external_id: 'space-1',
      name: 'Cloud space',
      type: 'space',
      has_children: true,
      metadata: { hierarchy_limitation: 'cloud_top_level_containers' },
    }]
    f.vm.expandedResourceIds = new Set(['space-1'])
    await nextTick()
    assert.equal(f.vm.visibleTree.some((row: any) => row.noticeAfter), true)

    f.vm.expandedResourceIds = new Set()
    f.vm.loadedChildrenIds = new Set(['space-1'])
    f.vm.resources = [{ ...f.vm.resources[0], has_children: false }]
    await nextTick()
    assert.equal(f.vm.visibleTree.some((row: any) => row.noticeAfter), true)
  } finally { f.close() }
})

test('plugin connector forms ask the plugin behind the connector type', async () => {
  const f = await fixture({ type: 'acme.jira/jira', settings: { project: 'p1' } })
  try {
    await new Promise((resolve) => setTimeout(resolve, 0))
    assert.deepEqual(JSON.parse(JSON.stringify(f.formBinding.target())), {
      pluginId: 'acme.jira', scope: 'instance', contribution: 'connectors/jira', instanceId: 'source-one',
    })
    f.vm.form.config.credentials = { site: 'acme' }
    assert.equal(f.formBinding.dependency('site'), 'acme')
    assert.equal(f.formBinding.dependency('project'), 'p1')
    assert.deepEqual(JSON.parse(JSON.stringify(f.formBinding.values())), {
      credentials: { site: 'acme' }, settings: { project: 'p1' }, resourceIds: [],
    })
    f.vm.form.type = 'gitlab'
    assert.equal(f.formBinding.target(), undefined)
  } finally { f.close() }
})
