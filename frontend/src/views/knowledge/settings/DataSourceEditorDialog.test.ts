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
const filename = fileURLToPath(new URL('./DataSourceEditorDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'datasource-editor-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

// Lets un-awaited API calls (e.g. loadResources fired by nextStep) settle.
const flush = async () => {
  await new Promise(resolve => setImmediate(resolve))
  await nextTick()
}

async function fixture({
  configured = true,
  create = false,
  type = 'gitlab',
  settings = { projects: [{ project_id: '123', paths: [] }] },
  resources = [],
  dataSource = null,
  detailDataSource = null,
}: {
  configured?: boolean
  create?: boolean
  type?: string
  settings?: Record<string, unknown>
  resources?: any[]
  dataSource?: any
  detailDataSource?: any
} = {}) {
  const calls: Array<{ method: string; args: any[] }> = []
  const warnings: string[] = []
  let storedToken = configured ? 'expired-token' : ''
  const api = {
    async validateCredentials(type: string, credentials: Record<string, string>) {
      calls.push({ method: 'validateCredentials', args: [type, { ...credentials }] })
      if (type !== 'confluence' && credentials.access_token !== 'rotated-token') throw new Error('gitlab API /user: status 401')
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
    async createDataSource(data: any) {
      calls.push({ method: 'createDataSource', args: [JSON.parse(JSON.stringify(data))] })
      return { data: { id: 'source-temp' } }
    },
    // Lazy picker: the first call lists roots, later calls list one level.
    async listResources(id: string, parentId?: string) {
      calls.push({ method: 'listResources', args: [id, parentId] })
      return { data: resources.filter(r => (parentId ? r.parent_id === parentId : !r.parent_id)) }
    },
    async resolveResourceAncestors(id: string, ids: string[]) {
      calls.push({ method: 'resolveResourceAncestors', args: [id, ids] })
      const byId = new Map(resources.map((r: any) => [r.external_id, r]))
      const ancestors: string[] = []
      const seen = new Set<string>()
      for (const rid of ids) {
        for (let cur = byId.get(rid)?.parent_id; cur; cur = byId.get(cur)?.parent_id) {
          if (!seen.has(cur)) {
            seen.add(cur)
            ancestors.push(cur)
          }
        }
      }
      return { data: { ancestors } }
    },
    ...(detailDataSource ? {
      async getDataSource(id: string) {
        calls.push({ method: 'getDataSource', args: [id] })
        return detailDataSource
      },
    } : {}),
  }
  const props = reactive({
    visible: false, kbId: 'kb-one',
    dataSource: create ? null : dataSource || {
      id: 'source-one', name: 'GitLab', type,
      credentials: { credentials: { configured } },
      config: { resource_ids: [], settings },
      sync_schedule: '0 0 */6 * * *', sync_mode: 'incremental',
      conflict_strategy: 'overwrite', sync_deletions: true,
    },
  })
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { warning(msg: string) { warnings.push(msg) }, success() {}, error() {} } }
      if (name === '@/api/datasource') return api
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
  return { vm, calls, warnings, replace, storedToken: () => storedToken, close: () => app.unmount() }
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
    f.vm.selectType(f.vm.connectorDefs.find((def: any) => def.type === 'gitlab'))
    await f.replace()
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'success')
    assert.deepEqual(f.calls.map(call => call.method), ['validateCredentials'])
  } finally { f.close() }
})

const seafileRepoA = '0f1e2d3c-4b5a-4968-8776-655443322110'
const seafileRepoB = 'ffffffff-0000-4000-8000-000000000001'

// A lazy Seafile tree: two libraries, one with a folder that holds a file.
const seafileResources = [
  { external_id: `${seafileRepoA}:/`, name: 'Docs', type: 'library', has_children: true },
  { external_id: `${seafileRepoB}:/`, name: 'Design', type: 'library', has_children: true },
  { external_id: `${seafileRepoA}:/guides`, name: 'guides', type: 'directory', parent_id: `${seafileRepoA}:/`, has_children: true },
  { external_id: `${seafileRepoA}:/guides/a.pdf`, name: 'a.pdf', type: 'file', parent_id: `${seafileRepoA}:/guides`, has_children: false },
]

// Opens a new Seafile data source on the picker step with the roots listed.
async function seafileFixture(resources = seafileResources) {
  const f = await fixture({ create: true, resources })
  f.vm.selectType(f.vm.connectorDefs.find((def: any) => def.type === 'seafile'))
  f.vm.form.config.credentials = { base_url: 'https://seafile.example.com', api_token: 'rotated-token' }
  f.vm.testResult = 'success'
  await f.vm.nextStep()
  await flush()
  return f
}

test('Seafile picker lists libraries lazily and shows the library icon', async () => {
  const f = await seafileFixture()
  try {
    assert.equal(f.vm.step, 2)
    assert.deepEqual(f.calls.map(call => call.method), ['createDataSource', 'listResources'])
    assert.deepEqual([...f.vm.resources].map((r: any) => r.external_id), [`${seafileRepoA}:/`, `${seafileRepoB}:/`])
    assert.equal(f.vm.resourceIconName(f.vm.resources[0]), 'root-list')
    assert.equal(f.vm.resourceTypeLabel('library'), 'datasource.resourceType.library')
    await f.vm.ensureChildrenLoaded(`${seafileRepoA}:/`)
    await f.vm.ensureChildrenLoaded(`${seafileRepoA}:/guides`)
    assert.equal(f.vm.resourceIconName(f.vm.resources.find((r: any) => r.name === 'guides')), 'folder')
    assert.equal(f.vm.resourceIconName(f.vm.resources.find((r: any) => r.name === 'a.pdf')), 'file')
  } finally { f.close() }
})

test('Seafile selection stays within one library until it is cleared', async () => {
  const f = await seafileFixture()
  try {
    await f.vm.ensureChildrenLoaded(`${seafileRepoA}:/`)
    await f.vm.ensureChildrenLoaded(`${seafileRepoA}:/guides`)
    f.vm.toggleResource(`${seafileRepoA}:/guides/a.pdf`)
    f.vm.toggleResource(`${seafileRepoB}:/`)
    assert.deepEqual([...f.vm.selectedResourceIds], [`${seafileRepoA}:/guides/a.pdf`])
    assert.deepEqual(f.warnings, ['datasource.seafile.singleLibraryOnly'])
    // Clearing library A first lets the user pick library B before saving.
    f.vm.toggleResource(`${seafileRepoA}:/guides/a.pdf`)
    f.vm.toggleResource(`${seafileRepoB}:/`)
    assert.deepEqual([...f.vm.selectedResourceIds], [`${seafileRepoB}:/`])
    assert.equal(f.warnings.length, 1)
  } finally { f.close() }
})

test('Seafile requires a selection before the strategy step', async () => {
  const f = await seafileFixture()
  try {
    await f.vm.nextStep()
    assert.equal(f.vm.step, 2)
    assert.deepEqual(f.warnings, ['datasource.seafile.selectionRequired'])
    f.vm.toggleResource(`${seafileRepoA}:/`)
    await f.vm.nextStep()
    assert.equal(f.vm.step, 3)
  } finally { f.close() }
})

test('Nextcloud declares an app password and requires an explicit root selection', async () => {
  const f = await fixture({ create: true, resources: [
    { external_id: '/', name: 'Nextcloud', type: 'directory', has_children: true },
    { external_id: '/docs', name: 'docs', type: 'directory', parent_id: '/', has_children: true },
  ] })
  try {
    const def = f.vm.connectorDefs.find((def: any) => def.type === 'nextcloud')
    assert.ok(def?.available)
    assert.equal(def.fields.find((field: any) => field.key === 'app_password')?.secret, true)
    assert.deepEqual([...def.fields].map((field: any) => field.key), ['base_url', 'username', 'app_password'])
    f.vm.selectType(def)
    f.vm.form.config.credentials = {
      base_url: 'https://nextcloud.example.com/cloud', username: 'alice', app_password: 'app-password-for-tests',
    }
    f.vm.form.config.settings = { include_extensions: '.pdf, .md', exclude_extensions: '.html' }
    f.vm.step = 2
    await f.vm.loadResources()
    await f.vm.nextStep()
    assert.equal(f.vm.step, 2)
    assert.deepEqual(f.warnings, ['datasource.nextcloud.selectionRequired'])
    await f.vm.ensureChildrenLoaded('/')
    f.vm.toggleResource('/docs')
    await f.vm.nextStep()
    assert.equal(f.vm.step, 3)
    assert.deepEqual([...f.vm.selectedResourceIds], ['/docs'])
    assert.equal(f.vm.form.config.settings.include_extensions, '.pdf, .md')
    assert.equal(f.vm.form.config.credentials.include_extensions, undefined)
  } finally { f.close() }
})

test('unchecking a Nextcloud root clears saved descendants missing from the lazy tree', async () => {
  const f = await fixture({ type: 'nextcloud', resources: [
    { external_id: '/', name: 'Nextcloud', type: 'directory', has_children: true },
  ], dataSource: {
    id: 'source-one', name: 'Nextcloud', type: 'nextcloud',
    credentials: { credentials: { configured: true } },
    config: { resource_ids: ['/docs/vanished.pdf'], settings: {} },
  } })
  try {
    f.vm.step = 2
    await f.vm.loadResources()
    f.vm.toggleResource('/')
    f.vm.toggleResource('/')
    assert.deepEqual([...f.vm.selectedResourceIds], [])
  } finally { f.close() }
})

test('a vanished saved Seafile selection is cleared by unchecking its library', async () => {
  const f = await fixture({ resources: seafileResources, dataSource: {
    id: 'source-one', name: 'Seafile', type: 'seafile',
    credentials: { credentials: { configured: true } },
    config: { resource_ids: [`${seafileRepoA}:/guides/vanished.pdf`], settings: {} },
    sync_schedule: '0 0 */6 * * *', sync_mode: 'incremental',
    conflict_strategy: 'overwrite', sync_deletions: true,
  } })
  try {
    await f.vm.nextStep()
    await flush()
    assert.equal(f.vm.step, 2)
    assert.deepEqual([...f.vm.selectedResourceIds], [`${seafileRepoA}:/guides/vanished.pdf`])
    // The stale path is invisible, so library B is refused until A is cleared.
    f.vm.toggleResource(`${seafileRepoB}:/`)
    assert.deepEqual(f.warnings, ['datasource.seafile.singleLibraryOnly'])
    // Checking then unchecking library A sweeps the stale path with it.
    f.vm.toggleResource(`${seafileRepoA}:/`)
    f.vm.toggleResource(`${seafileRepoA}:/`)
    assert.deepEqual([...f.vm.selectedResourceIds], [])
    f.vm.toggleResource(`${seafileRepoB}:/`)
    assert.deepEqual([...f.vm.selectedResourceIds], [`${seafileRepoB}:/`])
  } finally { f.close() }
})

test('the single-library guard leaves Feishu and Lark Drive selections alone', async () => {
  for (const type of ['feishu_drive', 'lark_drive']) {
    const f = await fixture({ create: true })
    try {
      f.vm.selectType(f.vm.connectorDefs.find((def: any) => def.type === type))
      // Drive IDs are "folderToken:fileToken"; roots under two folders must
      // both stay selectable.
      f.vm.resources = [
        { external_id: 'folderA:fileA', name: 'A', type: 'file', has_children: false },
        { external_id: 'folderB:fileB', name: 'B', type: 'file', has_children: false },
      ]
      await nextTick()
      f.vm.toggleResource('folderA:fileA')
      f.vm.toggleResource('folderB:fileB')
      assert.deepEqual([...f.vm.selectedResourceIds].sort(), ['folderA:fileA', 'folderB:fileB'], type)
      assert.deepEqual(f.warnings, [], type)
    } finally { f.close() }
  }
})

test('a new Yuque data source adopts the TOC folder layout, but not the filter', async () => {
  const f = await fixture({ create: true })
  try {
    assert.equal(f.vm.form.config.settings.folder_mode, undefined)
    f.vm.selectType(f.vm.connectorDefs.find((def: any) => def.type === 'yuque'))
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

test('a DingTalk data source reports the uploaded-file switch, defaulting to off', async () => {
  const off = await fixture({ type: 'dingtalk', settings: {} })
  try {
    // The connector treats a missing key as false, so an existing source that
    // predates this control must render as off — and stay untouched.
    assert.equal(off.vm.dingtalkIncludeUploadedFiles, false)
    assert.equal(off.vm.form.config.settings.include_uploaded_files, undefined)
  } finally { off.close() }

  const on = await fixture({ type: 'dingtalk', settings: { include_uploaded_files: true } })
  try {
    assert.equal(on.vm.dingtalkIncludeUploadedFiles, true)
  } finally { on.close() }

  const explicitOff = await fixture({ type: 'dingtalk', settings: { include_uploaded_files: false } })
  try {
    assert.equal(explicitOff.vm.dingtalkIncludeUploadedFiles, false)
  } finally { explicitOff.close() }
})

test('toggling the DingTalk uploaded-file switch writes the settings key', async () => {
  const f = await fixture({ type: 'dingtalk', settings: {} })
  try {
    f.vm.dingtalkIncludeUploadedFiles = true
    assert.equal(f.vm.form.config.settings.include_uploaded_files, true)
    f.vm.dingtalkIncludeUploadedFiles = false
    assert.equal(f.vm.form.config.settings.include_uploaded_files, false)
    // The switch is scoped to DingTalk: no other connector's key is written.
    assert.equal(f.vm.form.config.settings.include_sheets, undefined)
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

for (const type of ['feishu', 'lark', 'feishu_drive', 'lark_drive']) {
  test(`${type} preserves inherited parse mode when saving unrelated settings`, async () => {
    const settings = { timezone: 'Asia/Shanghai', resync_required: true }
    const f = await fixture({ type, settings })
    try {
      assert.equal(f.vm.feishuParseMode, '')
      assert.equal(f.vm.form.config.settings.parse_mode, undefined)
      f.vm.form.name = 'Renamed source'
      await f.vm.handleSubmit()
      const saved = f.calls.find(call => call.method === 'updateDataSource')
      assert.ok(saved)
      assert.deepEqual(saved.args[1].config.settings, settings)
      assert.equal('parse_mode' in settings, false, 'opening the editor must not mutate props')
    } finally { f.close() }
  })
}

test('Feishu explicit mode changes are saved without mutating the source configuration', async () => {
  const settings = { parse_mode: 'export', timezone: 'Asia/Shanghai' }
  const f = await fixture({ type: 'feishu', settings })
  try {
    assert.equal(f.vm.feishuParseMode, 'export')
    f.vm.feishuParseMode = 'blocks'
    assert.equal(settings.parse_mode, 'export')
    assert.equal(f.vm.buildConfigPayload().settings.parse_mode, 'blocks')
    f.vm.feishuParseMode = ''
    assert.deepEqual(JSON.parse(JSON.stringify(f.vm.buildConfigPayload().settings)), { timezone: 'Asia/Shanghai' })
    assert.equal(settings.parse_mode, 'export')
  } finally { f.close() }
})

test('new Feishu sources keep the blocks default and do not leak it to another connector', async () => {
  const f = await fixture({ create: true })
  try {
    f.vm.selectType(f.vm.connectorDefs.find((def: any) => def.type === 'feishu'))
    assert.equal(f.vm.feishuParseMode, 'blocks')
    f.vm.selectType(f.vm.connectorDefs.find((def: any) => def.type === 'gitlab'))
    assert.equal(f.vm.form.config.settings.parse_mode, undefined)
  } finally { f.close() }
})


test('Confluence PAT permits connection testing and saving without Basic credentials', async () => {
  const f = await fixture({ create: true, configured: false, settings: {} })
  try {
    f.vm.selectType(f.vm.connectorDefs.find((def: any) => def.type === 'confluence'))
    f.vm.form.config.credentials = { edition: 'server', base_url: 'https://confluence.test/wiki', token: 'pat-value' }
    await nextTick()
    const fields = f.vm.displayedCredentialFields
    assert.equal(fields.find((field: any) => field.key === 'token').secret, true)
    assert.equal(fields.find((field: any) => field.key === 'username').optional, true)
    assert.equal(fields.find((field: any) => field.key === 'password').optional, true)
    assert.equal(fields.some((field: any) => field.key === 'api_token'), false)
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'success')
    assert.equal(f.calls.find(c => c.method === 'validateCredentials')?.args[1].token, 'pat-value')
    await f.vm.loadResources()
    const created = f.calls.find(c => c.method === 'createDataSource')?.args[0]
    assert.equal(created.config.credentials.token, 'pat-value')
    assert.equal(created.config.settings.token, undefined)
    assert.deepEqual(f.warnings, [])
  } finally { f.close() }
})

test('Confluence requires Basic credentials without a PAT and hides PAT in Cloud', async () => {
  const f = await fixture({ type: 'confluence', configured: false, settings: {} })
  try {
    f.vm.form.config.credentials = { edition: 'server', base_url: 'https://confluence.test/wiki', token: '  ' }
    await nextTick()
    assert.equal(f.vm.displayedCredentialFields.find((field: any) => field.key === 'username').optional, false)
    assert.equal(f.vm.displayedCredentialFields.find((field: any) => field.key === 'password').optional, false)
    await f.vm.testConnection()
    assert.equal(f.calls.some(c => c.method === 'validateCredentials'), false)
    f.vm.form.config.credentials.edition = 'cloud'
    f.vm.form.config.credentials.token = 'stale-server-token'
    await nextTick()
    assert.deepEqual(Array.from(f.vm.displayedCredentialFields, (field: any) => field.key), ['base_url', 'username', 'api_token'])
    assert.equal(f.vm.displayedCredentialFields.find((field: any) => field.key === 'username').optional, false)
  } finally { f.close() }
})


test('Confluence PAT replacement uses the credentials subresource and keeps the token out of settings', async () => {
  const f = await fixture({ type: 'confluence', settings: { edition: 'server', base_url: 'https://confluence.test/wiki' } })
  try {
    f.vm.enterReplaceCredentials()
    f.vm.form.config.credentials.token = 'replacement-pat'
    await nextTick()
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'success')
    await f.vm.handleSubmit()
    const replacement = f.calls.find(c => c.method === 'putDataSourceCredentials')
    assert.equal(replacement?.args[1].token, 'replacement-pat')
    const update = f.calls.find(c => c.method === 'updateDataSource')?.args[1]
    assert.deepEqual(update.config.credentials, {})
    assert.equal(update.config.settings.token, undefined)
  } finally { f.close() }
})

test('a deleted Feishu node selection stays visible in the picker so the user can uncheck and save', async () => {
  const spaceId = '7701598185524874545'
  const deletedId = `${spaceId}:Ut8Gw3GsbiNIAnkNp2kcaZTpnAe`
  const liveId = `${spaceId}:DocBNodeToken`
  const f = await fixture({
    type: 'feishu',
    resources: [
      { external_id: spaceId, name: '研发知识库', type: 'wiki_space', has_children: true },
      { external_id: liveId, name: '文档 B', type: 'wiki_node', parent_id: spaceId, has_children: false },
    ],
    dataSource: {
      id: 'ds-feishu-5633',
      name: 'Feishu Wiki',
      type: 'feishu',
      credentials: { credentials: { configured: true } },
      config: { resource_ids: [deletedId, liveId], settings: { parse_mode: 'blocks' } },
      sync_schedule: '0 0 */6 * * *',
      sync_mode: 'incremental',
      conflict_strategy: 'overwrite',
      sync_deletions: true,
      latest_sync_log: {
        id: 'log-2',
        status: 'success',
        result: {
          deleted_items: [{
            external_id: 'Ut8Gw3GsbiNIAnkNp2kcaZTpnAe',
            source_resource_id: deletedId,
            title: '文档 A',
          }],
        },
      },
    },
  })
  try {
    await f.vm.nextStep()
    await flush()
    assert.equal(f.vm.step, 2)
    assert.deepEqual(JSON.parse(JSON.stringify(f.vm.unavailableResources)), [
      { external_id: deletedId, label: '文档 A' },
    ])
    assert.equal(f.vm.resourceRowState(deletedId), 'checked')
    assert.equal(f.vm.resourceRowState(liveId), 'checked')
    assert.equal(f.vm.selectedResourceCount, 2)

    // Unchecking the deleted node removes it from selectedResourceIds while
    // keeping its row visible as unchecked until the drawer is saved.
    f.vm.toggleResource(deletedId)
    assert.equal(f.vm.resourceRowState(deletedId), 'unchecked')
    assert.equal(f.vm.selectedResourceCount, 1)
    assert.deepEqual([...f.vm.selectedResourceIds], [liveId])

    await f.vm.nextStep()
    await f.vm.handleSubmit()
    const saved = f.calls.find(c => c.method === 'updateDataSource')?.args[1]
    assert.deepEqual(saved.config.resource_ids, [liveId])
  } finally { f.close() }
})

test('selecting a whole Feishu space subsumes vanished node selections under that space', async () => {
  const spaceId = '7701598185524874545'
  const deletedId = `${spaceId}:Ut8Gw3GsbiNIAnkNp2kcaZTpnAe`
  const f = await fixture({
    type: 'feishu',
    resources: [
      { external_id: spaceId, name: '研发知识库', type: 'wiki_space', has_children: true },
    ],
    dataSource: {
      id: 'ds-feishu-space',
      name: 'Feishu Wiki',
      type: 'feishu',
      credentials: { credentials: { configured: true } },
      config: { resource_ids: [deletedId], settings: {} },
      sync_schedule: '0 0 */6 * * *',
      sync_mode: 'incremental',
      conflict_strategy: 'overwrite',
      sync_deletions: true,
    },
  })
  try {
    await f.vm.nextStep()
    await flush()
    f.vm.toggleResource(spaceId)
    assert.deepEqual([...f.vm.selectedResourceIds], [spaceId])
    assert.equal(f.vm.resourceRowState(deletedId), 'unchecked')
    assert.equal(f.vm.selectedResourceCount, 1)
    // Clicking the superseded deleted row while the space is selected must not re-add it.
    f.vm.toggleResource(deletedId)
    assert.deepEqual([...f.vm.selectedResourceIds], [spaceId])
    f.vm.toggleResource(spaceId)
    assert.deepEqual([...f.vm.selectedResourceIds], [])
    assert.equal(f.vm.selectedResourceCount, 0)
  } finally { f.close() }
})

test('unavailable parent wiki node resolves its own title rather than a deleted child title', async () => {
  const spaceId = '7701598185524874545'
  const folderId = `${spaceId}:folderToken`
  const f = await fixture({
    type: 'feishu',
    resources: [
      { external_id: spaceId, name: '研发知识库', type: 'wiki_space', has_children: true },
    ],
    dataSource: {
      id: 'ds-feishu-folder',
      name: 'Feishu Wiki',
      type: 'feishu',
      credentials: { credentials: { configured: true } },
      config: { resource_ids: [folderId], settings: {} },
      sync_schedule: '0 0 */6 * * *',
      sync_mode: 'incremental',
      conflict_strategy: 'overwrite',
      sync_deletions: true,
      latest_sync_log: {
        id: 'log-subtree',
        status: 'success',
        result: {
          deleted_items: [
            { external_id: 'a-child-token', source_resource_id: folderId, title: '子文档一' },
            { external_id: 'folderToken', source_resource_id: folderId, title: '父目录文档' },
          ],
        },
      },
    },
  })
  try {
    await f.vm.nextStep()
    await flush()
    assert.deepEqual(JSON.parse(JSON.stringify(f.vm.unavailableResources)), [
      { external_id: folderId, label: '父目录文档' },
    ])
  } finally { f.close() }
})

test('Confluence Cloud folder-limited spaces do not misclassify unexpanded live pages as unavailable', async () => {
  const f = await fixture({
    type: 'confluence',
    resources: [
      {
        external_id: '101',
        name: 'Engineering',
        type: 'space',
        has_children: true,
        metadata: { hierarchy_limitation: 'cloud_top_level_containers' },
      },
    ],
    dataSource: {
      id: 'ds-conf-cloud',
      name: 'Confluence Cloud',
      type: 'confluence',
      credentials: { credentials: { configured: true } },
      config: { resource_ids: ['page:101:999'], settings: { edition: 'cloud', base_url: 'https://team.atlassian.net/wiki' } },
      sync_schedule: '0 0 */6 * * *',
      sync_mode: 'incremental',
      conflict_strategy: 'overwrite',
      sync_deletions: true,
    },
  })
  try {
    await f.vm.nextStep()
    await flush()
    assert.deepEqual(JSON.parse(JSON.stringify(f.vm.unavailableResources)), [])
    assert.deepEqual([...f.vm.selectedResourceIds], ['page:101:999'])

    // Selecting the whole space subsumes hidden/vanished page:<spaceId>:<pageId>
    // selections instead of leaving stale page IDs alongside the space ID.
    f.vm.toggleResource('101')
    assert.deepEqual([...f.vm.selectedResourceIds], ['101'])
    assert.equal(f.vm.selectedResourceCount, 1)
    f.vm.toggleResource('101')
    assert.deepEqual([...f.vm.selectedResourceIds], [])
    assert.equal(f.vm.selectedResourceCount, 0)
  } finally { f.close() }
})

test('selecting a DingTalk workspace or folder subsumes vanished child selections', async () => {
  const wsId = 'ws-eng'
  const folderId = 'dingtalk:v1?node=folder-1&workspace=ws-eng'
  const vanishedDocId = 'dingtalk:v1?ancestor=folder-1&node=doc-deleted&workspace=ws-eng'
  const f = await fixture({
    type: 'dingtalk',
    resources: [
      { external_id: wsId, name: '研发空间', type: 'workspace', has_children: true },
      { external_id: folderId, name: '架构目录', type: 'folder', parent_id: wsId, has_children: true },
    ],
    dataSource: {
      id: 'ds-dingtalk-cover',
      name: 'DingTalk',
      type: 'dingtalk',
      credentials: { credentials: { configured: true } },
      config: { resource_ids: [vanishedDocId], settings: {} },
      sync_schedule: '0 0 */6 * * *',
      sync_mode: 'incremental',
      conflict_strategy: 'overwrite',
      sync_deletions: true,
    },
  })
  try {
    await f.vm.nextStep()
    await flush()
    await f.vm.ensureChildrenLoaded(wsId)
    f.vm.toggleResource(folderId)
    assert.deepEqual([...f.vm.selectedResourceIds], [folderId])
    assert.equal(f.vm.resourceRowState(wsId), 'indeterminate')
    f.vm.toggleResource(wsId)
    assert.deepEqual([...f.vm.selectedResourceIds], [])
    f.vm.toggleResource(wsId)
    assert.deepEqual([...f.vm.selectedResourceIds], [wsId])
  } finally { f.close() }
})

test('editing a Feishu Drive source does not overwrite saved sub-selections before save', async () => {
  const f = await fixture({
    type: 'feishu_drive',
    resources: [
      { external_id: 'root-folder', name: '研发云盘', type: 'drive_folder', has_children: true },
      { external_id: 'root-folder:sub', name: '架构子目录', type: 'folder', parent_id: 'root-folder', has_children: true },
      { external_id: 'root-folder:shortcut-1', name: '快捷方式规范', type: 'shortcut', parent_id: 'root-folder:sub', has_children: false },
    ],
    dataSource: {
      id: 'ds-drive-edit',
      name: 'Feishu Drive',
      type: 'feishu_drive',
      credentials: { credentials: { configured: true } },
      config: { resource_ids: ['root-folder:shortcut-1'], settings: { parse_mode: 'blocks' } },
      sync_schedule: '0 0 */6 * * *',
      sync_mode: 'incremental',
      conflict_strategy: 'overwrite',
      sync_deletions: true,
    },
  })
  try {
    await f.vm.nextStep()
    await flush()
    await flush()
    assert.equal(f.calls.some(c => c.method === 'updateDataSource'), false)
    assert.deepEqual([...f.vm.selectedResourceIds], ['root-folder:shortcut-1'])
  } finally { f.close() }
})

test('unavailable Feishu selection refreshes deleted item title when list snapshot was stale', async () => {
  const spaceId = '7701598185524874545'
  const deletedId = `${spaceId}:Ut8Gw3GsbiNIAnkNp2kcaZTpnAe`
  const f = await fixture({
    type: 'feishu',
    resources: [
      { external_id: spaceId, name: '研发知识库', type: 'wiki_space', has_children: true },
    ],
    dataSource: {
      id: 'ds-feishu-stale',
      name: 'Feishu Wiki',
      type: 'feishu',
      credentials: { credentials: { configured: true } },
      config: { resource_ids: [deletedId], settings: { parse_mode: 'blocks' } },
      sync_schedule: '0 0 */6 * * *',
      sync_mode: 'incremental',
      conflict_strategy: 'overwrite',
      sync_deletions: true,
    },
    detailDataSource: {
      id: 'ds-feishu-stale',
      last_sync_result: {
        deleted_items: [{
          external_id: 'Ut8Gw3GsbiNIAnkNp2kcaZTpnAe',
          source_resource_id: deletedId,
          title: '文档 A.md',
        }],
      },
    },
  })
  try {
    await f.vm.nextStep()
    await flush()
    await flush()
    assert.deepEqual(JSON.parse(JSON.stringify(f.vm.unavailableResources)), [
      { external_id: deletedId, label: '文档 A.md' },
    ])
    assert.ok(f.calls.some(c => c.method === 'getDataSource' && c.args[0] === 'ds-feishu-stale'))
  } finally { f.close() }
})
