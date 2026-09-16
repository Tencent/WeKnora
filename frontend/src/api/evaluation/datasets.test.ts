import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { build, type Plugin } from 'esbuild'
import type * as DatasetAPI from './datasets'

const global = globalThis as any
const requests: Array<{ url: string; body: unknown }> = []
const mock: Plugin = {
  name: 'dataset-http-contract', setup(builder) {
    builder.onResolve({ filter: /^@\/utils\/request$/ }, args => ({ path: args.path, namespace: 'dataset-http' }))
    builder.onLoad({ filter: /.*/, namespace: 'dataset-http' }, () => ({ loader: 'js', contents: `
      export const get = async () => { throw new Error('unexpected read') }
      export const post = async (url, body) => globalThis.__datasetHttpPost(url, body)
    ` }))
  },
}
let modulePromise: Promise<typeof DatasetAPI> | undefined
function load() {
  modulePromise ??= (async () => {
    const result = await build({ entryPoints: [fileURLToPath(new URL('./datasets.ts', import.meta.url))], bundle: true, format: 'esm', platform: 'node', target: 'node22', write: false, logLevel: 'silent', plugins: [mock] })
    return import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0]!.text).toString('base64')}`) as Promise<typeof DatasetAPI>
  })()
  return modulePromise
}
const input = { dataset_id: 'd1', dataset_version_id: 'v1', knowledge_base_id: 'kb1', chat_id: 'chat1' }
test('POST evaluation unwraps the actual detail.task response before publishing the created task', async () => {
  const api = await load()
  const task = { id: 'task-1', dataset_id: 'd1', dataset_version_id: 'v1', status: 0, labels: [] }
  global.__datasetHttpPost = (url: string, body: unknown) => { requests.push({ url, body }); return { success: true, data: { task, params: { source_kb_id: 'kb1' }, provenance_complete: true } } }
  assert.deepEqual(await api.createEvaluation(input), task)
  assert.deepEqual(requests.at(-1), { url: '/api/v1/evaluation', body: input })
})
test('a missing task, missing identifier or failed envelope never becomes a successful creation', async () => {
  const api = await load()
  for (const response of [{ success: true, data: { id: 'misplaced' } }, { success: true, data: { task: {} } }, { success: true, data: { task: { id: '' } } }, { success: false, data: { task: { id: 'task' } } }]) {
    global.__datasetHttpPost = () => response
    await assert.rejects(api.createEvaluation(input), /incomplete|no task/i)
  }
})
test('an incomplete import response cannot report an imported dataset', async () => {
  const api = await load()
  global.__datasetHttpPost = () => ({ success: true, data: { dataset: { id: 'd1' }, replayed: false } })
  await assert.rejects(api.importEvaluationDataset({ request_id: 'request', name: 'Sample', description: '', content: { passages: [], questions: [], relevance: [] } }), /incomplete/)
})
