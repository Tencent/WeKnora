import assert from 'node:assert/strict'
import { test } from 'node:test'
import { weChatAppDraft, weChatAppUpdate, weChatSettingsDirty, weChatSettingsErrors } from './wechatSettingsState'

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

const saved = {
  source: 'database' as const, version: 1, platform_id: 'platform', encryption_ready: true,
  web: { enabled: true, app_id: 'web', secret_configured: true, callback_url: 'https://example.com/api/v1/auth/wechat/callback' },
  mobile: { enabled: false, app_id: '', secret_configured: false },
}
function loadedDraft() {
  return { platform_id: saved.platform_id, web: weChatAppDraft(saved.web), mobile: weChatAppDraft(saved.mobile) }
}

test('save remains idle for unchanged values but detects credentials and toggles', () => {
  const draft = loadedDraft()
  assert.equal(weChatSettingsDirty(draft, saved), false)
  draft.web.app_id = ' web '
  draft.web.secret = ' '
  assert.equal(weChatSettingsDirty(draft, saved), false)
  draft.web.secret = 'replacement'
  assert.equal(weChatSettingsDirty(draft, saved), true)
  draft.web.secret = ''
  draft.web.clearSecret = true
  assert.equal(weChatSettingsDirty(draft, saved), true)
  draft.web.clearSecret = false
  draft.web.enabled = false
  assert.equal(weChatSettingsDirty(draft, saved), true)
})

test('enabled apps require credentials; AppID changes cannot silently reuse a saved secret', () => {
  const draft = loadedDraft()
  assert.deepEqual(weChatSettingsErrors(draft, saved), {})
  draft.mobile.enabled = true
  assert.equal(weChatSettingsErrors(draft, saved)['mobile.app_id'], 'appIdRequired')
  assert.equal(weChatSettingsErrors(draft, saved)['mobile.secret'], 'secretRequired')
  draft.web.app_id = 'new-app'
  assert.equal(weChatSettingsErrors(draft, saved)['web.secret'], 'secretReplacementRequired')
  draft.web.secret = 'replacement'
  assert.equal(weChatSettingsErrors(draft, saved)['web.secret'], undefined)
  draft.web.clearSecret = true
  assert.equal(weChatSettingsErrors(draft, saved)['web.secret'], 'secretRequired')
  draft.web.enabled = false
  assert.equal(weChatSettingsErrors(draft, saved)['web.secret'], undefined)
})

test('callback validation rejects unsafe or unsupported URLs only for enabled website login', () => {
  const draft = loadedDraft()
  for (const callback of ['', 'http://example.com/api/v1/auth/wechat/callback',
    'https://example.com/other', 'https://example.com/api/v1/auth/wechat/callback?next=bad',
    'https://user:pass@example.com/api/v1/auth/wechat/callback', 'https://example.com/api/v1/auth/wechat/callback#fragment']) {
    draft.web.callback_url = callback
    assert.equal(weChatSettingsErrors(draft, saved)['web.callback_url'], 'callbackInvalid', callback)
  }
  draft.web.enabled = false
  assert.equal(weChatSettingsErrors(draft, saved)['web.callback_url'], undefined)
})

test('encryption is required for retained or new credentials, but clearing remains possible', () => {
  const draft = loadedDraft()
  const unavailable = { ...saved, encryption_ready: false }
  assert.equal(weChatSettingsErrors(draft, unavailable)['web.secret'], 'encryptionHint')
  draft.web.enabled = false
  draft.web.clearSecret = true
  assert.equal(weChatSettingsErrors(draft, unavailable)['web.secret'], undefined)
  draft.mobile.secret = 'new-secret'
  assert.equal(weChatSettingsErrors(draft, unavailable)['mobile.secret'], 'encryptionHint')
})
