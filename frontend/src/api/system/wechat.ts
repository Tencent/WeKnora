import { get, put } from '@/utils/request'

export interface WeChatAppSettings {
  enabled: boolean
  app_id: string
  callback_url?: string
  secret_configured: boolean
}
export interface WeChatSettings {
  source: 'deployment' | 'database'
  version: number
  platform_id: string
  web: WeChatAppSettings
  mobile: WeChatAppSettings
  encryption_ready: boolean
}
export interface WeChatAppUpdate {
  enabled: boolean
  app_id: string
  callback_url?: string
  app_secret?: string
}
export interface WeChatSettingsUpdate {
  version: number
  platform_id: string
  web: WeChatAppUpdate
  mobile: WeChatAppUpdate
}
export async function getWeChatSettings(): Promise<WeChatSettings> {
  return await get('/api/v1/system/admin/auth/wechat') as unknown as WeChatSettings
}
export async function updateWeChatSettings(value: WeChatSettingsUpdate): Promise<WeChatSettings> {
  return await put('/api/v1/system/admin/auth/wechat', value) as unknown as WeChatSettings
}
