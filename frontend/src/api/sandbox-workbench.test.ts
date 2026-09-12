import assert from 'node:assert/strict'
import test from 'node:test'
import { createWorkbenchApi, type WorkbenchTransport } from './sandbox-workbench'

test('workbench requests use the finalized session-scoped contract and cancellation signal', async () => {
  const scope = new AbortController()
  const calls: { method: string; url: string; body: unknown; options: unknown }[] = []
  const transport = Object.fromEntries(['get', 'post', 'patch', 'del', 'postUpload'].map(method => [
    method,
    async (url: string, body?: unknown, options?: unknown, uploadOptions?: unknown) => {
      calls.push({ method, url, body: method === 'get' ? undefined : body, options: method === 'get' ? body : uploadOptions || options })
      if (url.endsWith('/audit')) return { success: true, data: [], next_cursor: 0 }
      return { success: true, data: { value: method } }
    },
  ])) as unknown as WorkbenchTransport
  const api = createWorkbenchApi('session /?', transport, scope.signal)
  assert.deepEqual(await api.status(), { value: 'get' })
  await api.bind('config-1')
  await api.ticket()
  await api.files('folder & name')
  const file = new File(['hello'], 'local.txt')
  await api.upload('reports/renamed.txt', file)
  await api.mkdir('reports')
  await api.rename('reports/old.txt', 'reports/new.txt')
  await api.remove('reports/new.txt')
  await api.audit()
  await api.download('reports/file & name.txt')
  const base = '/api/v1/sessions/session%20%2F%3F/sandbox'
  assert.deepEqual(calls.map(({ method, url }) => [method, url]), [
    ['get', `${base}/workbench`], ['post', `${base}/workbench`], ['post', `${base}/command-ticket`],
    ['get', `${base}/files?path=folder+%26+name`], ['postUpload', `${base}/files`],
    ['post', `${base}/directories`], ['patch', `${base}/files`],
    ['del', `${base}/files?path=reports%2Fnew.txt`], ['get', `${base}/audit`],
    ['get', `${base}/files/download?path=reports%2Ffile+%26+name.txt`],
  ])
  assert.deepEqual(calls[1].body, { config_id: 'config-1' })
  assert.deepEqual(calls[2].body, {})
  const form = calls[4].body as FormData
  assert.equal(form.get('path'), 'reports/renamed.txt')
  assert.equal((form.get('file') as File).name, 'local.txt')
  assert.deepEqual(calls[5].body, { path: 'reports' })
  assert.deepEqual(calls[6].body, { path: 'reports/old.txt', new_path: 'reports/new.txt' })
  assert.equal(calls[7].body, undefined)
  calls.forEach(call => assert.equal((call.options as { signal: AbortSignal }).signal, scope.signal))
  assert.deepEqual(calls[9].options, { signal: scope.signal, responseType: 'blob' })
})

test('a late response from an aborted scope never becomes a successful result', async () => {
  const scope = new AbortController()
  let resolve!: (value: unknown) => void
  const transport = { get: () => new Promise(done => { resolve = done }) } as unknown as WorkbenchTransport
  const result = createWorkbenchApi('s', transport, scope.signal).status()
  scope.abort()
  resolve({ success: true, data: { available: true } })
  await assert.rejects(result, { name: 'AbortError' })
})

test('failed envelopes are surfaced and never converted into empty success results', async () => {
  const denied = { success: false, error: { code: 'policy_denied', message: 'Denied' } }
  const transport = { post: async () => denied, get: async () => denied } as unknown as WorkbenchTransport
  const api = createWorkbenchApi('s', transport)
  await assert.rejects(api.bind('cfg'), error => error === denied)
  await assert.rejects(api.mkdir('dir'), error => error === denied)
  await assert.rejects(api.audit(), error => error === denied)
})

test('audit pages preserve the server cursor and share the cancellation scope', async () => {
  const controller = new AbortController()
  const calls: string[] = []
  const transport = { get: async (url: string, options: { signal: AbortSignal }) => {
    calls.push(url)
    assert.equal(options.signal, controller.signal)
    return { success: true, data: [{ id: 9 }], next_cursor: 9 }
  } } as unknown as WorkbenchTransport
  const api = createWorkbenchApi('session', transport, controller.signal)
  assert.deepEqual(await api.audit(10), { data: [{ id: 9 }], next_cursor: 9 })
  assert.equal(calls[0], '/api/v1/sessions/session/sandbox/audit?after_id=10')
  controller.abort()
  await assert.rejects(api.audit(9), { name: 'AbortError' })
})
