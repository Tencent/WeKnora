import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'

import { withApiBasePath } from './api-base.ts'
import {
  buildProtectedFileRequest,
  isProtectedFileProxyPath,
  isProviderFileURL,
  resolveProtectedFileAccess,
  setDefaultProtectedFileAccess,
  type ProtectedFileAccessContext,
} from './protectedFileAccess.ts'

const RESOURCE = 'resource://AbCdEfGhIjKlMnOpQrStUv'

/**
 * Compile a second copy of the module whose api-base reports the given
 * Vite BASE_URL, so the sub-path wiring is exercised without a Vite build.
 */
function buildProtectedFileModule(base: string): typeof import('./protectedFileAccess.ts') {
  const source = readFileSync(new URL('./protectedFileAccess.ts', import.meta.url), 'utf8')
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const state: Record<string, unknown> = {}
  runInNewContext(compiled, {
    exports: state,
    URLSearchParams,
    localStorage: { getItem: () => null },
    require: (id: string) => {
      if (id === './api-base') {
        return { withApiBasePath: (path: string) => withApiBasePath(path, base) }
      }
      throw new Error(`Unexpected import: ${id}`)
    },
  })
  return state as unknown as typeof import('./protectedFileAccess.ts')
}

test.afterEach(() => {
  setDefaultProtectedFileAccess(null)
})

test('tenant access routes through the tenant-scoped proxy', () => {
  const request = buildProtectedFileRequest(RESOURCE, { mode: 'tenant' })

  assert.equal(request?.url, `/files?file_path=${encodeURIComponent(RESOURCE)}`)
})

test('embed access routes through the channel-scoped proxy with the Embed header', () => {
  const request = buildProtectedFileRequest(RESOURCE, {
    mode: 'embed',
    channelId: 'ch-1',
    token: 'tok-1',
  })

  assert.equal(request?.url, `/api/v1/embed/ch-1/files?file_path=${encodeURIComponent(RESOURCE)}`)
  assert.equal(request?.headers.Authorization, 'Embed tok-1')
})

test('knowledge-base access routes through the KB-scoped proxy', () => {
  const request = buildProtectedFileRequest(RESOURCE, { mode: 'knowledgeBase', kbId: 'kb 1' })

  assert.equal(
    request?.url,
    `/api/v1/knowledge-bases/kb%201/files?file_path=${encodeURIComponent(RESOURCE)}`,
  )
})

test('message access routes through the session-message-scoped proxy', () => {
  const request = buildProtectedFileRequest(RESOURCE, {
    mode: 'message',
    sessionId: 'session 1',
    messageId: 'message/1',
  })

  assert.equal(
    request?.url,
    `/api/v1/sessions/session%201/messages/message%2F1/files?file_path=${encodeURIComponent(RESOURCE)}`,
  )
})

test('an embed context without a token yields no request instead of an unauthenticated one', () => {
  const request = buildProtectedFileRequest(RESOURCE, { mode: 'embed', channelId: 'ch-1', token: '' })

  assert.equal(request, null)
})

test('non-storage sources are not proxied', () => {
  assert.equal(buildProtectedFileRequest('https://example.com/a.png', { mode: 'tenant' }), null)
  assert.equal(isProviderFileURL('https://example.com/a.png'), false)
  assert.equal(isProviderFileURL('storage://backend-1/local://42/a.png'), true)
})

test('the registered embed plane wins over a component-level knowledge-base scope', () => {
  // An embed visitor holds no Bearer token, so the KB proxy would answer 401.
  setDefaultProtectedFileAccess({ mode: 'embed', channelId: 'ch-1', token: 'tok-1' })

  const access = resolveProtectedFileAccess({ mode: 'knowledgeBase', kbId: 'kb-1' })

  assert.deepEqual(access, { mode: 'embed', channelId: 'ch-1', token: 'tok-1' })
})

test('a component scope refines the default tenant plane', () => {
  const override: ProtectedFileAccessContext = { mode: 'knowledgeBase', kbId: 'kb-1' }

  assert.deepEqual(resolveProtectedFileAccess(override), override)
  assert.deepEqual(resolveProtectedFileAccess(), { mode: 'tenant' })
  assert.deepEqual(
    resolveProtectedFileAccess({ mode: 'knowledgeBase', kbId: '  ' }),
    { mode: 'tenant' },
  )
  assert.deepEqual(
    resolveProtectedFileAccess({ mode: 'message', sessionId: 'session-1', messageId: '  ' }),
    { mode: 'tenant' },
  )
})

test('sub-path deployments prefix every protected file proxy with the app base', () => {
  const mod = buildProtectedFileModule('/app/weknora')
  const query = `file_path=${encodeURIComponent(RESOURCE)}`

  assert.equal(
    mod.buildProtectedFileRequest(RESOURCE, { mode: 'tenant' })?.url,
    `/app/weknora/files?${query}`,
  )
  assert.equal(
    mod.buildProtectedFileRequest(RESOURCE, { mode: 'knowledgeBase', kbId: 'kb-1' })?.url,
    `/app/weknora/api/v1/knowledge-bases/kb-1/files?${query}`,
  )
  assert.equal(
    mod.buildProtectedFileRequest(RESOURCE, {
      mode: 'message',
      sessionId: 's-1',
      messageId: 'm-1',
    })?.url,
    `/app/weknora/api/v1/sessions/s-1/messages/m-1/files?${query}`,
  )
  assert.equal(
    mod.buildProtectedFileRequest(RESOURCE, { mode: 'embed', channelId: 'ch-1', token: 'tok-1' })?.url,
    `/app/weknora/api/v1/embed/ch-1/files?${query}`,
  )
})

test('all file proxies are recognized as protected proxy paths', () => {
  assert.equal(isProtectedFileProxyPath('/files'), true)
  assert.equal(isProtectedFileProxyPath('/api/v1/knowledge-bases/kb-1/files'), true)
  assert.equal(isProtectedFileProxyPath('/api/v1/sessions/session-1/messages/message-1/files'), true)
  assert.equal(isProtectedFileProxyPath('/api/v1/embed/ch-1/files'), true)
  assert.equal(isProtectedFileProxyPath('/api/v1/embed/ch-1/config'), false)
})
