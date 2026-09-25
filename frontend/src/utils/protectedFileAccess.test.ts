import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildProtectedFileRequest,
  isProtectedFileProxyPath,
  isProviderFileURL,
  resolveProtectedFileAccess,
  setDefaultProtectedFileAccess,
  type ProtectedFileAccessContext,
} from './protectedFileAccess.ts'

const RESOURCE = 'resource://AbCdEfGhIjKlMnOpQrStUv'

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

test('all file proxies are recognized as protected proxy paths', () => {
  assert.equal(isProtectedFileProxyPath('/files'), true)
  assert.equal(isProtectedFileProxyPath('/api/v1/knowledge-bases/kb-1/files'), true)
  assert.equal(isProtectedFileProxyPath('/api/v1/sessions/session-1/messages/message-1/files'), true)
  assert.equal(isProtectedFileProxyPath('/api/v1/embed/ch-1/files'), true)
  assert.equal(isProtectedFileProxyPath('/api/v1/embed/ch-1/config'), false)
})
