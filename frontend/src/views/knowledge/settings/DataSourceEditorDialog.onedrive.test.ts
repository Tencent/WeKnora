import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const editor = readFileSync(new URL('./DataSourceEditorDialog.vue', import.meta.url), 'utf8')
const settings = readFileSync(new URL('./DataSourceSettings.vue', import.meta.url), 'utf8')
const api = readFileSync(new URL('../../../api/datasource/index.ts', import.meta.url), 'utf8')
const locales = [
  '../../../i18n/locales/zh-CN.ts',
  '../../../i18n/locales/en-US.ts',
  '../../../i18n/locales/ko-KR.ts',
  '../../../i18n/locales/ru-RU.ts',
].map(path => readFileSync(new URL(path, import.meta.url), 'utf8'))

test('OneDrive uses the OAuth lifecycle instead of credential fields', () => {
  assert.match(editor, /type: 'onedrive'/)
  assert.match(editor, /const isOAuthConnector = computed\(\(\) => form\.value\.type === 'onedrive'\)/)
  assert.match(editor, /getDataSourceOAuthAuthorizeURL\(dsId, replaceConnection\)/)
  assert.match(editor, /waitForOneDriveAuthorization/)
  assert.match(editor, /disconnectDataSourceOAuth/)
  assert.match(editor, /oauthReauthorizationRequired/)
  assert.match(editor, /oauthReplaceConfirm/)
  assert.match(editor, /oauthDisconnectConfirm/)
})

test('OneDrive API surface exposes only non-secret connection state', () => {
  assert.match(api, /authorize-url/)
  assert.match(api, /oauth\/status/)
  assert.match(api, /oauth\/token/)
  assert.match(api, /account_display_name\?: string/)
  assert.doesNotMatch(api, /access_token\??:/)
  assert.doesNotMatch(api, /refresh_token\??:/)
})

test('OneDrive connection states and actions are translated in every locale', () => {
  for (const locale of locales) {
    assert.match(locale, /oauthConnect:/)
    assert.match(locale, /oauthReconnect:/)
    assert.match(locale, /oauthReauthorizationRequired:/)
    assert.match(locale, /oauthReplaceConfirm:/)
    assert.match(locale, /oauthDisconnectConfirm:/)
    assert.match(locale, /connecting:/)
  }
  assert.match(settings, /&--connecting/)
  assert.match(settings, /reauthorization_required/)
})
