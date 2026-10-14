import assert from 'node:assert/strict'
import { test } from 'node:test'
import { weChatAppDraft, weChatAppUpdate } from './wechatSettingsState'

test('a loaded configured secret is omitted on save, not replaced by a placeholder', () => {
  const draft = weChatAppDraft({ enabled: true, app_id: 'web', secret_configured: true })
  assert.equal(Object.hasOwn(weChatAppUpdate(draft), 'app_secret'), false)
  draft.secret = '   '
  assert.equal(Object.hasOwn(weChatAppUpdate(draft), 'app_secret'), false)
})

test('only an explicit clear sends an empty secret; a replacement sends the typed value', () => {
  const draft = weChatAppDraft({ enabled: false, app_id: 'web', secret_configured: true })
  draft.secret = 'new-secret'
  assert.equal(weChatAppUpdate(draft).app_secret, 'new-secret')
  draft.clearSecret = true
  assert.equal(weChatAppUpdate(draft).app_secret, '')
})

test('reload or successful save drops in-memory credentials and clear intent', () => {
  const draft = weChatAppDraft({ enabled: false, app_id: 'web', secret_configured: false })
  assert.equal(draft.secret, '')
  assert.equal(draft.clearSecret, false)
})
