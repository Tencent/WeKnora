import assert from 'node:assert/strict'
import test from 'node:test'

import {
  DINGTALK_IP_WHITELIST_FAQ,
  classifyDatasourceError,
  datasourceErrorText,
  redactDatasourceErrorText,
} from './datasourceErrorText.ts'

// The body DingTalk returns when the caller's egress IP is not in the app's
// whitelist. It is the exact shape that used to reach the toast, IP and app key
// included.
const DINGTALK_IP_ERROR = [
  'list DingTalk workspaces: invalid credentials: DingTalk API status=403',
  'code=Forbidden.AccessDenied.IpNotInWhiteList',
  'message=访问ip不在白名单之中，请参考FAQ: https://open.dingtalk.com/document/org-faq/app-faq,',
  'request ip=198.51.100.7 appKey(dingabc123xyz)',
].join(' ')

test('a DingTalk whitelist error becomes an actionable message with the FAQ', () => {
  const classified = classifyDatasourceError({ status: 403, message: DINGTALK_IP_ERROR })

  assert.equal(classified.kind, 'ip-whitelist')
  assert.equal(classified.messageKey, 'datasource.loadError.ipNotInWhitelist')
  assert.equal(classified.faqUrl, DINGTALK_IP_WHITELIST_FAQ)
})

test('the whitelist error never exposes the egress IP or the app key', () => {
  const classified = classifyDatasourceError({ message: DINGTALK_IP_ERROR })

  assert.doesNotMatch(classified.detail, /198\.51\.100\.7/)
  assert.doesNotMatch(classified.detail, /dingabc123xyz/)
  assert.match(classified.detail, /request ip=\[redacted\]/)
  assert.match(classified.detail, /appKey\(\[redacted\]\)/)
  // The rest of the diagnostic survives, so the detail is still useful.
  assert.match(classified.detail, /Forbidden\.AccessDenied\.IpNotInWhiteList/)
})

test('invalid credentials map to their own message even without a whitelist code', () => {
  const classified = classifyDatasourceError(
    'list DingTalk workspaces: invalid credentials: DingTalk API status=403',
  )

  assert.equal(classified.kind, 'invalid-credentials')
  assert.equal(classified.messageKey, 'datasource.loadError.invalidCredentials')
})

test('plain 403, 401 and transport failures keep distinct messages', () => {
  assert.equal(
    classifyDatasourceError({ message: 'DingTalk API status=403 code=Forbidden.AccessDenied' }).messageKey,
    'datasource.loadError.forbidden',
  )
  assert.equal(
    classifyDatasourceError({ status: 401, message: 'unauthorized' }).messageKey,
    'datasource.loadError.unauthorized',
  )
  assert.equal(
    classifyDatasourceError({ message: 'request timed out' }).messageKey,
    'datasource.loadError.network',
  )
})

test('an unrecognised error falls back to a generic message and keeps only redacted detail', () => {
  const classified = classifyDatasourceError(
    'something odd happened, request ip=10.0.0.7 appKey=dingsecret',
  )

  assert.equal(classified.kind, 'unknown')
  assert.equal(classified.messageKey, 'datasource.loadError.unknown')
  assert.doesNotMatch(classified.detail, /10\.0\.0\.7|dingsecret/)
})

test('datasourceErrorText reads every shape the HTTP layer can reject with', () => {
  assert.equal(datasourceErrorText('boom'), 'boom')
  assert.equal(datasourceErrorText(new Error('boom')), 'boom')
  assert.equal(datasourceErrorText({ message: 'boom' }), 'boom')
  assert.equal(datasourceErrorText({ data: { error: 'boom' } }), 'boom')
  assert.equal(datasourceErrorText({ error: { message: 'boom' } }), 'boom')
  assert.equal(datasourceErrorText(null), '')
})

test('redaction strips secrets but leaves the surrounding sentence alone', () => {
  const redacted = redactDatasourceErrorText(
    'status=403 client_secret=s3cr3t access_token=abc123 request ip=192.168.1.9',
  )

  assert.equal(
    redacted,
    'status=403 client_secret=[redacted] access_token=[redacted] request ip=[redacted]',
  )
})
