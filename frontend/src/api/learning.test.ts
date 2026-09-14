import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import type { LearningApi } from './learning'

let server: ViteDevServer
let api: LearningApi
let transport: { calls: { method: string; url: string; body?: unknown; config?: { signal?: AbortSignal } }[]; response: unknown }
before(async () => {
  server = await createServer({ configFile: false, appType: 'custom', server: { middlewareMode: true, hmr: false },
    optimizeDeps: { noDiscovery: true, entries: [] },
    resolve: { alias: { '@': fileURLToPath(new URL('../', import.meta.url)) } },
    plugins: [{ name: 'learning-api-transport', enforce: 'pre', load(id) {
      if (id.endsWith('/utils/request.ts')) return `
        export const transport = { calls: [], response: { success: true, data: { enabled: false, algorithm_version: 'v1' } } };
        export const get = (url, config) => { transport.calls.push({method:'GET', url, config}); return Promise.resolve(transport.response) };
        export const post = (url, body, config) => { transport.calls.push({method:'POST', url, body, config}); return Promise.resolve(transport.response) };
        export const put = (url, body, config) => { transport.calls.push({method:'PUT', url, body, config}); return Promise.resolve(transport.response) };
        export const del = (url, body) => { transport.calls.push({method:'DELETE', url, body}); return Promise.resolve(transport.response) };
      `
    } }],
  })
  api = (await server.ssrLoadModule('/src/api/learning.ts')).learningApi
  transport = (await server.ssrLoadModule('/src/utils/request.ts')).transport
})
after(async () => { await server?.close() })

test('learning adapter unwraps the frozen success envelope and preserves server failures', async () => {
  assert.deepEqual(await api.settings(), { enabled: false, algorithm_version: 'v1' })
  transport.response = { success: false, error: { code: 'learning_disabled' } }
  await assert.rejects(api.settings(), error => (error as any).error.code === 'learning_disabled')
  transport.response = { success: true, data: null }
  assert.equal(await api.recordView('page'), null)
})

test('HTTP methods, scoped URLs, overlay and human answer payload match the frozen contract', async () => {
  transport.calls.length = 0
  const signal = new AbortController().signal
  await api.setEnabled(true, signal)
  await api.overview('kb +/&', signal)
  await api.recommendations('kb', signal)
  await api.node('page/id', signal)
  await api.overlay('kb', ['concept/a', 'concept/b'], signal)
  await api.prepareQuiz('page', signal)
  await api.quiz('quiz/id', signal)
  await api.answer({ question_id: 'question', option_id: 'option', attempt_id: 'attempt' }, signal)
  const calls = transport.calls
  assert.deepEqual(calls.map(call => call.method), ['PUT', 'GET', 'GET', 'GET', 'POST', 'POST', 'GET', 'POST'])
  assert.deepEqual(calls[0].body, { enabled: true })
  assert.equal(new URL(calls[1].url, 'https://example.test').searchParams.get('knowledge_base_id'), 'kb +/&')
  assert.equal(calls[2].url, '/api/v1/learning/recommendations?knowledge_base_id=kb&limit=5')
  assert.equal(calls[3].url, '/api/v1/learning/nodes/page%2Fid')
  assert.deepEqual(calls[4].body, { knowledge_base_id: 'kb', slugs: ['concept/a', 'concept/b'] })
  assert.deepEqual(calls[5].body, { page_id: 'page' })
  assert.equal(calls[6].url, '/api/v1/learning/question-sets/quiz%2Fid')
  assert.deepEqual(calls[7].body, { question_id: 'question', option_id: 'option', attempt_id: 'attempt' })
  assert.ok(calls.every(call => call.config?.signal === signal))
})

test('privacy keeps the optional KB scope absent for all-data export and clear', async () => {
  transport.calls.length = 0
  await api.export(); await api.export('kb'); await api.clear(); await api.clear('kb')
  assert.deepEqual(transport.calls.map(call => [call.method, call.url]), [
    ['GET', '/api/v1/learning/export'], ['GET', '/api/v1/learning/export?knowledge_base_id=kb'],
    ['DELETE', '/api/v1/learning/profile'], ['DELETE', '/api/v1/learning/profile?knowledge_base_id=kb'],
  ])
})
