import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isWeChatQRCodeReady, weChatQRCodeURL } from './wechatQRCode'

const attempt = {
  app_id: 'wx-web',
  scope: 'snsapi_login',
  state: 'state-value',
  redirect_uri: 'https://weknora.example.com/api/v1/auth/wechat/callback',
  expires_at: '2026-10-14T13:00:00Z',
}

test('the embedded QR code uses the server-issued attempt and redirects the top window', () => {
  const url = new URL(weChatQRCodeURL(attempt, { lang: 'zh-CN', colorScheme: 'light' }))
  assert.equal(url.origin, 'https://open.weixin.qq.com')
  assert.equal(url.pathname, '/connect/qrconnect')
  assert.equal(url.searchParams.get('appid'), 'wx-web')
  assert.equal(url.searchParams.get('state'), 'state-value')
  assert.equal(url.searchParams.get('redirect_uri'), attempt.redirect_uri)
  assert.equal(url.searchParams.get('login_type'), 'jssdk')
  assert.equal(url.searchParams.get('self_redirect'), 'false')
  assert.equal(url.searchParams.get('lang'), null)
  assert.equal(url.searchParams.get('color_scheme'), 'light')
})

test('English UI and dark theme are passed to WeChat', () => {
  const url = new URL(weChatQRCodeURL(attempt, { lang: 'en-US', colorScheme: 'dark' }))
  assert.equal(url.searchParams.get('lang'), 'en')
  assert.equal(url.searchParams.get('color_scheme'), 'dark')
})

test('only status messages from the WeChat origin mark the QR code as ready', () => {
  const ready = JSON.stringify({ type: 'status', status: 'wxQRcodeReady' })
  assert.equal(isWeChatQRCodeReady({ origin: 'https://open.weixin.qq.com', data: ready }), true)
  assert.equal(isWeChatQRCodeReady({ origin: 'https://open.weixin.qq.com', data: JSON.parse(ready) }), true)
  assert.equal(isWeChatQRCodeReady({ origin: 'https://evil.example.com', data: ready }), false)
  assert.equal(isWeChatQRCodeReady({ origin: 'https://open.weixin.qq.com', data: null }), false)
  assert.equal(isWeChatQRCodeReady({ origin: 'https://open.weixin.qq.com', data: '{bad' }), false)
  assert.equal(isWeChatQRCodeReady({ origin: 'https://open.weixin.qq.com', data: { type: 'status' } }), false)
})
