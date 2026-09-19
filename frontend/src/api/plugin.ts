import { get, post, put } from '@/utils/request'

// RescanReport mirrors the backend internal/plugin.RescanReport.
export interface RescanReport {
  added: string[]
  skipped: string[]
  changed: string[]
  errors?: string[]
}

// PluginListEntry mirrors the backend handler.PluginListEntry.
export interface PluginListEntry {
  id: string
  name: string
  version: string
  extension_type: string
  description?: string
  entrypoint: string
  trust_level: string
  state: string
  /** true when a trust-level change is saved but not yet applied (needs rescan) */
  pending: boolean
}

export type PluginTrustLevel = 'offline' | 'trusted' | 'isolated'

export function rescanPlugins(): Promise<RescanReport> {
  return post('/api/v1/plugins/rescan', {}).then((res: any) => {
    if (res && typeof res === 'object' && 'success' in res) {
      return (res.data as RescanReport) || { added: [], skipped: [], changed: [] }
    }
    return res as RescanReport
  })
}

export function listPlugins(): Promise<PluginListEntry[]> {
  return get('/api/v1/plugins').then((res: any) => {
    if (res && typeof res === 'object' && 'success' in res) {
      return (res.data as PluginListEntry[]) || []
    }
    return (res as PluginListEntry[]) || []
  })
}

export function setPluginTrust(pluginId: string, trustLevel: PluginTrustLevel): Promise<void> {
  return put('/api/v1/plugins/trust', { plugin_id: pluginId, trust_level: trustLevel }).then(() => {})
}

export function restartPlugin(pluginId: string): Promise<void> {
  return post('/api/v1/plugins/restart', { plugin_id: pluginId }).then(() => {})
}
