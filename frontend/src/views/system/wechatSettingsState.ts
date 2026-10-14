import type { WeChatAppSettings, WeChatAppUpdate, WeChatSettings } from '@/api/system/wechat'

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

export interface WeChatSettingsDraft {
  platform_id: string
  web: WeChatAppDraft
  mobile: WeChatAppDraft
}

export function weChatSettingsDirty(draft: WeChatSettingsDraft, saved: WeChatSettings): boolean {
  if (draft.platform_id.trim() !== saved.platform_id) return true
  return (['web', 'mobile'] as const).some(client => {
    const app = weChatAppUpdate(draft[client])
    const previous = saved[client]
    return app.enabled !== previous.enabled || app.app_id !== previous.app_id ||
      (app.callback_url || '') !== (previous.callback_url || '') || Object.hasOwn(app, 'app_secret')
  })
}

export function weChatSettingsErrors(
  draft: WeChatSettingsDraft,
  saved: WeChatSettings,
): Record<string, string> {
  const errors: Record<string, string> = {}
  for (const client of ['web', 'mobile'] as const) {
    const app = draft[client]
    const secret = !app.clearSecret && (app.secret.trim() !== '' || app.secret_configured)
    if (app.enabled && !app.app_id.trim()) errors[`${client}.app_id`] = 'appIdRequired'
    if (app.enabled && !secret) errors[`${client}.secret`] = 'secretRequired'
    else if (app.app_id.trim() !== saved[client].app_id && app.secret_configured &&
      !app.clearSecret && !app.secret.trim()) errors[`${client}.secret`] = 'secretReplacementRequired'
    else if (secret && !saved.encryption_ready) errors[`${client}.secret`] = 'encryptionHint'
    if (client === 'web' && app.enabled) {
      try {
        const url = new URL(app.callback_url?.trim() || '')
        if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash ||
          url.pathname !== '/api/v1/auth/wechat/callback') errors['web.callback_url'] = 'callbackInvalid'
      } catch { errors['web.callback_url'] = 'callbackInvalid' }
    }
  }
  return errors
}
