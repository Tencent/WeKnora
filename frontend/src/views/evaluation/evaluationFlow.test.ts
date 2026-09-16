import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import { build, type Plugin } from 'esbuild'
import { DATASET_TEMPLATE } from './datasetValidation'

const serviceLimits = { max_request_body_bytes: 65536, max_passages: 10, max_questions: 10, max_relevance: 100, max_passage_bytes: 1024, max_question_bytes: 256, max_name_chars: 255, max_id_chars: 128, max_grade: 2147483647 }
const dataset = { id: 'd1', name: 'Sample', description: '', scope: 'tenant', current_version_id: 'v1' }
const version = { id: 'v1', dataset_id: 'd1', version_number: 1, passage_count: 1, question_count: 1, relevance_count: 1 }
const kb = { id: 'kb1', name: 'Source', embedding_model_id: 'embed1', chunking_config: { chunk_size: 256, chunk_overlap: 32 } }
const saved = { dataset, version, replayed: false }
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: Error) => void; const promise = new Promise<T>((r, j) => { resolve = r; reject = j }); return { promise, resolve, reject } }
const flush = async () => { for (let i = 0; i < 8; i++) await Promise.resolve() }
const global = globalThis as any
function install(overrides: Record<string, (...args: any[]) => any> = {}) {
  global.__flowWatchers = []; global.__flowUnmount = []
  global.__flowApi = {
    listEvaluationDatasets: async () => [dataset],
    listDatasetVersions: async () => [version],
    getPublicDatasetCatalog: async () => ({ items: [], limits: serviceLimits }),
    getPublicDataset: async () => ({ id: 'public', name: 'Sample', description: '', content: DATASET_TEMPLATE, manifest: {} }),
    importEvaluationDataset: async () => saved,
    createDatasetVersion: async () => version,
    createEvaluation: async () => ({ id: 'task1', dataset_id: 'd1', dataset_version_id: 'v1', labels: [], status: 0 }),
    listModels: async () => [{ id: 'chat1', name: 'Chat', type: 'KnowledgeQA' }, { id: 'embed1', name: 'Embedding', type: 'Embedding' }],
    listKnowledgeBases: async () => ({ success: true, data: [kb] }),
    getKnowledgeBaseById: async () => ({ success: true, data: kb }),
    ...overrides,
  }
}
const dependencies: Plugin = {
  name: 'evaluation-flow-component-dependencies', setup(builder) {
    const modules: Record<string, string> = {
      vue: `
        export const defineComponent = value => value
        export const ref = value => ({ value })
        export const reactive = value => value
        export const computed = getter => ({ get value() { return getter() } })
        export const watch = (source, callback, options) => { globalThis.__flowWatchers.push({ source, callback }); if(options?.immediate) callback(source()) }
        export const onBeforeUnmount = callback => globalThis.__flowUnmount.push(callback)
      `,
      'vue-i18n': 'export const useI18n = () => ({ t: (key) => key })',
      'vue-router': 'export const RouterLink = {}',
      '@lucide/vue': ['ArrowRight','CheckCircle2','Database','Download','ExternalLink','FileJson','LoaderCircle','Plus','ShieldCheck','Upload','Coins','FlaskConical','Play','X'].map(name => `export const ${name} = {}`).join('\n'),
      '@/api/evaluation/datasets': ['listEvaluationDatasets','listDatasetVersions','getPublicDatasetCatalog','getPublicDataset','importEvaluationDataset','createDatasetVersion','createEvaluation'].map(name => `export const ${name} = (...args) => globalThis.__flowApi.${name}(...args)`).join('\n'),
      '@/api/knowledge-base': ['listKnowledgeBases','getKnowledgeBaseById'].map(name => `export const ${name} = (...args) => globalThis.__flowApi.${name}(...args)`).join('\n'),
      '@/api/model': 'export const listModels = (...args) => globalThis.__flowApi.listModels(...args)',
    }
    builder.onResolve({ filter: /^(vue|vue-i18n|vue-router|@lucide\/vue|@\/api\/(evaluation\/datasets|knowledge-base|model))$/ }, args => ({ path: args.path, namespace: 'flow-mock' }))
    builder.onLoad({ filter: /.*/, namespace: 'flow-mock' }, args => ({ contents: modules[args.path], loader: 'js' }))
    builder.onResolve({ filter: /SettingDrawer\.vue$/ }, args => ({ path: args.path, namespace: 'flow-child' }))
    builder.onLoad({ filter: /.*/, namespace: 'flow-child' }, () => ({ contents: 'export default {}', loader: 'js' }))
  },
}
const components = new Map<string, Promise<any>>()
async function component(name: string) {
  if (!components.has(name)) components.set(name, (async () => {
    const filename = fileURLToPath(new URL(`./${name}.vue`, import.meta.url))
    const source = await readFile(filename, 'utf8')
    const compiled = compileScript(parse(source, { filename }).descriptor, { id: 'flow-test' })
    const result = await build({ bundle: true, format: 'esm', platform: 'node', target: 'node22', write: false, logLevel: 'silent', plugins: [dependencies], stdin: { contents: compiled.content, resolveDir: dirname(filename), sourcefile: filename + '.ts', loader: 'ts' }, alias: { '@': fileURLToPath(new URL('../../', import.meta.url)) } })
    return (await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0]!.text).toString('base64')}`)).default
  })())
  return components.get(name)!
}
async function setup(name: string, props: Record<string, any>) {
  const events: any[][] = []
  const bindings = (await component(name)).setup(props, { expose() {}, emit: (...event: any[]) => events.push(event) })
  await flush()
  return { bindings, events }
}
function switchTenant(props: Record<string, any>, key: string) {
  props.tenantKey = key
  for (const watcher of global.__flowWatchers) if (typeof watcher.source === 'function' && watcher.source() === key) watcher.callback(key)
}
test('preview never posts; confirmed import publishes version and refreshes the workspace library once', async () => {
  let posts = 0, lists = 0
  install({ importEvaluationDataset: async () => { posts++; return saved }, listEvaluationDatasets: async () => { lists++; return [dataset] } })
  const { bindings: b, events } = await setup('EvaluationDatasetsDrawer', { visible: true, tenantKey: 'tenant1', canManage: true })
  await b.previewPublic({ id: 'public', license: 'CC-BY-SA-4.0' })
  assert.equal(posts, 0); assert.equal(b.content.value.questions.length, 1)
  await b.submitImport(); await flush()
  assert.equal(posts, 1); assert.equal(lists, 2); assert.equal(b.imported.value.version.id, 'v1')
  assert.equal(events.filter(event => event[0] === 'imported').length, 1)
})
test('component double submit is blocked and failed imports preserve the UUID for retry', async () => {
  const failure = deferred<any>(); const requests: any[] = []
  install({ importEvaluationDataset: (request: any) => { requests.push(structuredClone(request)); return requests.length === 1 ? failure.promise : Promise.resolve({ ...saved, replayed: true }) } })
  const { bindings: b } = await setup('EvaluationDatasetsDrawer', { visible: true, tenantKey: 'tenant1', canManage: true })
  await b.previewPublic({ id: 'public', license: 'CC-BY-SA-4.0' })
  const pending = b.submitImport(); await b.submitImport(); assert.equal(requests.length, 1)
  failure.reject(new Error('connection lost')); await pending
  assert.equal(b.error.value, 'connection lost'); assert.equal(b.content.value.questions.length, 1)
  await b.submitImport(); assert.deepEqual(requests[1], requests[0]); assert.equal(b.imported.value.replayed, true)
})
test('a tenant switch during import discards success and clears prior tenant draft and versions', async () => {
  const delayed = deferred<any>()
  install({ importEvaluationDataset: () => delayed.promise })
  const props = { visible: true, tenantKey: 'tenant1', canManage: true }
  const { bindings: b, events } = await setup('EvaluationDatasetsDrawer', props)
  await b.previewPublic({ id: 'public', license: 'CC-BY-SA-4.0' })
  const pending = b.submitImport(); switchTenant(props, 'tenant2'); delayed.resolve(saved); await pending
  assert.equal(b.imported.value, undefined); assert.equal(b.content.value, undefined)
  assert.equal(events.filter(event => event[0] === 'imported').length, 0)
})
test('a source selection race applies only the latest public package', async () => {
  const delayed = deferred<any>()
  install({ getPublicDataset: (id: string) => id === 'slow' ? delayed.promise : Promise.resolve({ name: 'Fast', description: '', content: DATASET_TEMPLATE }) })
  const { bindings: b } = await setup('EvaluationDatasetsDrawer', { visible: true, tenantKey: 'tenant1', canManage: true })
  const pending = b.previewPublic({ id: 'slow' }); await b.previewPublic({ id: 'fast' })
  delayed.resolve({ name: 'Slow', description: '', content: DATASET_TEMPLATE }); await pending
  assert.equal(b.name.value, 'Fast')
})
test('evaluation creation requires explicit cost confirmation and uses loaded version and knowledge configuration', async () => {
  const requests: any[] = []
  install({ createEvaluation: async (request: any) => { requests.push(request); return { id: 'task1' } } })
  const { bindings: b, events } = await setup('EvaluationCreateDrawer', { visible: true, tenantKey: 'tenant1', datasetId: 'd1', versionId: 'v1' })
  await b.selectDataset('v1'); b.draft.knowledgeBaseId = 'kb1'; b.draft.chatId = 'chat1'; await b.loadKnowledgeBase()
  assert.equal(b.selectedKB.value.embedding_model_id, 'embed1')
  await b.submit(); assert.equal(requests.length, 0)
  b.confirmed.value = true; await b.submit()
  assert.deepEqual(requests[0], { dataset_id: 'd1', dataset_version_id: 'v1', knowledge_base_id: 'kb1', chat_id: 'chat1' })
  assert.equal(events.find(e => e[0] === 'created')?.[1].id, 'task1')
})
test('uncertain task creation does not permit a blind duplicate retry', async () => {
  let posts = 0
  install({ createEvaluation: async () => { posts++; throw new Error('connection lost') } })
  const { bindings: b } = await setup('EvaluationCreateDrawer', { visible: true, tenantKey: 'tenant1', datasetId: 'd1', versionId: 'v1' })
  await b.selectDataset('v1'); b.draft.knowledgeBaseId = 'kb1'; b.draft.chatId = 'chat1'; await b.loadKnowledgeBase(); b.confirmed.value = true
  await b.submit(); b.confirmed.value = true; await b.submit()
  assert.equal(b.uncertain.value, true); assert.equal(posts, 1)
})
test('task creation success after a tenant switch cannot open another workspace task', async () => {
  const delayed = deferred<any>()
  install({ createEvaluation: () => delayed.promise })
  const props = { visible: true, tenantKey: 'tenant1', datasetId: 'd1', versionId: 'v1' }
  const { bindings: b, events } = await setup('EvaluationCreateDrawer', props)
  await b.selectDataset('v1'); b.draft.knowledgeBaseId = 'kb1'; b.draft.chatId = 'chat1'; await b.loadKnowledgeBase(); b.confirmed.value = true
  const pending = b.submit(); switchTenant(props, 'tenant2'); delayed.resolve({ id: 'old-task' }); await pending
  assert.equal(events.filter(e => e[0] === 'created').length, 0)
})

test('both evaluation drawers close when idle and preserve the active submission when close is requested', async () => {
  for (const name of ['EvaluationDatasetsDrawer', 'EvaluationCreateDrawer']) {
    install()
    const { bindings: b, events } = await setup(name, { visible: true, tenantKey: 'tenant1', canManage: true })
    b.submitting.value = true
    b.close(false)
    assert.equal(events.filter(e => e[0] === 'update:visible').length, 0, `${name} must stay open while submitting`)
    b.submitting.value = false
    b.close(false)
    assert.deepEqual(events.filter(e => e[0] === 'update:visible'), [['update:visible', false]])
  }
})
