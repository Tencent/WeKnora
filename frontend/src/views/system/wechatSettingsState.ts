import type { WeChatAppSettings, WeChatAppUpdate } from '@/api/system/wechat'

export interface WeChatAppDraft extends WeChatAppSettings {
  secret: string
  clearSecret: boolean
}
export function weChatAppDraft(app: WeChatAppSettings): WeChatAppDraft {
  return { ...app, secret: '', clearSecret: false }
}
export function weChatAppUpdate(app: WeChatAppDraft): WeChatAppUpdate {
  return {
    enabled: app.enabled,
    app_id: app.app_id.trim(),
    callback_url: app.callback_url?.trim(),
    // Empty input preserves; explicit clearing is a separate user action.
    ...(app.clearSecret ? { app_secret: '' } : app.secret.trim() ? { app_secret: app.secret.trim() } : {}),
  }
}
