// A workspace's own remote plugins (/api/v1/tenant-plugins), when the
// platform lets workspaces register them.
import { del, get, post, put } from '@/utils/request'

import { packageEndpoints, type InstalledPlugin } from '@/api/system/plugins'

const BASE = '/api/v1/tenant-plugins'

export interface TenantPluginListing {
  /** Whether the platform lets workspaces register their own plugins. */
  allowed: boolean
  plugins: InstalledPlugin[]
}

export function listTenantPlugins() {
  return get<{ data: TenantPluginListing }>(BASE)
}

/** Reviewing and registering this workspace's own plugins. */
export const tenantPackages = packageEndpoints(BASE)

export function setTenantPluginRemoteUrl(id: string, url: string) {
  return put<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}/remote-url`, { url })
}

/** Issues a new signing secret; the response carries it in issuedSecret. */
export function rotateTenantPluginSecret(id: string) {
  return post<{ data: InstalledPlugin }>(`${BASE}/${encodeURIComponent(id)}/secret/rotate`, {})
}

export function uninstallTenantPlugin(id: string) {
  return del(`${BASE}/${encodeURIComponent(id)}`)
}
