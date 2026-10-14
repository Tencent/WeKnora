import type { WeChatWebAttempt } from '@/api/auth'

export const WECHAT_ORIGIN = 'https://open.weixin.qq.com'

// Mirrors the iframe that WeChat's official wxLogin.js builds, without loading
// that third-party script. self_redirect=false makes WeChat navigate the top
// window to redirect_uri after the user confirms on their phone.
export function weChatQRCodeURL(
  attempt: WeChatWebAttempt,
  options: { lang: string; colorScheme: 'light' | 'dark' },
): string {
  const params = new URLSearchParams({
    appid: attempt.app_id,
    scope: attempt.scope,
    redirect_uri: attempt.redirect_uri,
    state: attempt.state,
    login_type: 'jssdk',
    self_redirect: 'false',
    stylelite: '1',
    color_scheme: options.colorScheme,
  })
  if (options.lang.toLowerCase().startsWith('en')) params.set('lang', 'en')
  return `${WECHAT_ORIGIN}/connect/qrconnect?${params.toString()}`
}

/** Reports whether a postMessage from the WeChat frame says the QR code is visible. */
export function isWeChatQRCodeReady(event: { origin: string; data: unknown }): boolean {
  if (event.origin !== WECHAT_ORIGIN) return false
  let message = event.data
  if (typeof message === 'string') {
    try {
      message = JSON.parse(message)
    } catch {
      return false
    }
  }
  const status = message as { type?: unknown; status?: unknown } | null
  return status?.type === 'status' && status.status === 'wxQRcodeReady'
}
