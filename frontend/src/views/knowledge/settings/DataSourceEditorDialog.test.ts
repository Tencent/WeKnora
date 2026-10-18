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
}: {
  configured?: boolean
  create?: boolean
  type?: string
  settings?: Record<string, unknown>
  resources?: any[]
  dataSource?: any
} = {}) {
  const calls: Array<{ method: string; args: any[] }> = []
  const warnings: string[] = []
  const errors: string[] = []
  let listingError: any = null
  let storedToken = configured ? 'expired-token' : ''
  const api = {
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
    async createDataSource(data: any) {
      calls.push({ method: 'createDataSource', args: [JSON.parse(JSON.stringify(data))] })
      return { data: { id: 'source-temp' } }
    },
    // Lazy picker: the first call lists roots, later calls list one level.
    async listResources(id: string, parentId?: string) {
      calls.push({ method: 'listResources', args: [id, parentId] })
      if (listingError) throw listingError
      return { data: resources.filter(r => (parentId ? r.parent_id === parentId : !r.parent_id)) }
    },
    async resolveResourceAncestors(id: string, ids: string[]) {
      calls.push({ method: 'resolveResourceAncestors', args: [id, ids] })
      return { data: { ancestors: [] } }
    },
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
      if (name === 'tdesign-vue-next') return { MessagePlugin: { warning(msg: string) { warnings.push(msg) }, success() {}, error(msg: string) { errors.push(msg) } } }
      if (name === '@/api/datasource') return api
      // The dialog imports its pure helpers directly; compile them through the
      // same tsx loader instead of stubbing the behaviour under test.
      if (name === './datasourceErrorText') return require('./datasourceErrorText.ts')
      if (name === './resourceList') return require('./resourceList.ts')
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
    vm,
    calls,
    warnings,
    errors,
    replace,
    // Makes the next listing fail, as a 403 after a credential change would.
    failListing(error: any) { listingError = error },
    storedToken: () => storedToken,
    close: () => app.unmount(),
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

// The body a DingTalk 403 carries when the caller's egress IP is not whitelisted.
// The listing used to surface it verbatim in the toast.
const dingtalkListingError = {
  status: 403,
  message: [
    'list DingTalk workspaces: invalid credentials: DingTalk API status=403',
    'code=Forbidden.AccessDenied.IpNotInWhiteList',
    'message=访问ip不在白名单之中，request ip=198.51.100.7 appKey(dingabc123xyz)',
  ].join(' '),
}

// Opens a new non-Drive data source on the picker step, with `resources` listed.
async function pickerFixture(listed: any[]) {
  const f = await fixture({ create: true, resources: listed })
  f.vm.selectType(f.vm.connectorDefs.find((def: any) => def.type === 'yuque'))
  f.vm.form.config.credentials = { api_token: 'rotated-token' }
  f.vm.testResult = 'success'
  await f.vm.nextStep()
  await flush()
  return f
}

test('a failed listing clears the stale tree and exposes the error state', async () => {
  const f = await pickerFixture([
    { external_id: 'space-1', name: 'Space', type: 'book', has_children: false },
  ])
  try {
    assert.equal(f.vm.step, 2)
    assert.deepEqual((f.vm.resources as any[]).map(r => r.external_id), ['space-1'])

    f.failListing(dingtalkListingError)
    await f.vm.loadResources()

    // The rows from the previous listing are gone: a 403 must not leave a tree
    // that looks current (that was the reported bug).
    assert.deepEqual([...f.vm.resources], [])
    assert.equal(f.vm.resourceLoadError.kind, 'ip-whitelist')
    assert.equal(f.vm.resourceLoadError.messageKey, 'datasource.loadError.ipNotInWhitelist')
    assert.match(f.vm.resourceLoadError.faqUrl, /open\.dingtalk\.com/)
    // The toast and the detail never carry the IP or the app key.
    assert.equal(f.errors.at(-1), 'datasource.loadError.ipNotInWhitelist')
    assert.doesNotMatch(f.vm.resourceLoadError.detail, /198\.51\.100\.7|dingabc123xyz/)
  } finally { f.close() }
})

test('a successful retry after a failure restores the tree and clears the error', async () => {
  const f = await pickerFixture([
    { external_id: 'space-1', name: 'Space', type: 'book', has_children: false },
  ])
  try {
    f.failListing(dingtalkListingError)
    await f.vm.loadResources()
    assert.deepEqual([...f.vm.resources], [])

    // The retry button must be able to recover without reopening the dialog.
    f.failListing(null)
    await f.vm.loadResources()

    assert.equal(f.vm.resourceLoadError, null)
    assert.deepEqual((f.vm.resources as any[]).map(r => r.external_id), ['space-1'])
  } finally { f.close() }
})

test('duplicate external_ids render as one row and keep the richer copy', async () => {
  const f = await pickerFixture([
    { external_id: 'base-1', name: '', type: '', has_children: false },
    { external_id: 'base-1', name: '项目多维表', type: 'book', has_children: true },
    { external_id: 'doc-1', name: 'Doc', type: 'document', has_children: false },
  ])
  try {
    assert.deepEqual((f.vm.resources as any[]).map(r => r.external_id), ['base-1', 'doc-1'])
    assert.equal(f.vm.resources[0].name, '项目多维表')
    assert.equal(f.vm.resources[0].type, 'book')
    assert.equal(f.vm.visibleTree.length, 2)
  } finally { f.close() }
})

test('a node listed under two parents is rendered once', async () => {
  const f = await fixture()
  try {
    f.vm.resources = [
      { external_id: 'parent-a', name: 'A', type: '', has_children: true },
      { external_id: 'parent-b', name: 'B', type: '', has_children: true },
      { external_id: 'shared', name: 'Shared', type: '', parent_id: 'parent-a' },
      { external_id: 'shared', name: 'Shared', type: '', parent_id: 'parent-b' },
    ]
    f.vm.expandedResourceIds = new Set(['parent-a', 'parent-b'])
    await nextTick()
    assert.equal(
      f.vm.visibleTree.filter((row: any) => row.resource.external_id === 'shared').length,
      1,
    )
  } finally { f.close() }
})
