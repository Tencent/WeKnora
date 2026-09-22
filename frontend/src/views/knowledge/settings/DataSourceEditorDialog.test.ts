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

async function fixture({ configured = true, create = false, resources = [] as any[], dataSource = null as any } = {}) {
  const calls: Array<{ method: string; args: any[] }> = []
  const warnings: string[] = []
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
      id: 'source-one', name: 'GitLab', type: 'gitlab',
      credentials: { credentials: { configured } },
      config: { resource_ids: [], settings: { projects: [{ project_id: '123', paths: [] }] } },
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
