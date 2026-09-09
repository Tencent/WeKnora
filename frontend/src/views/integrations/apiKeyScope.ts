import type { APIKeyKBPermission, APIKeyKBPermissions, TenantAPIKeyCapability } from '@/api/tenant'

export interface APIKeyKnowledgeBaseOption {
  id: string
  name: string
  source?: string
  shared: boolean
  maxPermission: APIKeyKBPermission
}

export const API_KEY_KB_PERMISSIONS: APIKeyKBPermission[] = ['read', 'write', 'manage']

export function cloneAPIKeyKBPermissions(value: APIKeyKBPermissions | null | undefined): APIKeyKBPermissions | null {
  return value == null ? null : { ...value }
}

export function canAssignKBPermission(
  permission: APIKeyKBPermission,
  option: APIKeyKnowledgeBaseOption | undefined,
  capabilities: readonly TenantAPIKeyCapability[],
): boolean {
  if (!option || API_KEY_KB_PERMISSIONS.indexOf(permission) > API_KEY_KB_PERMISSIONS.indexOf(option.maxPermission)) return false
  if (permission === 'manage') return capabilities.includes('manage_kbs')
  if (permission === 'write') return capabilities.some(cap => ['ingest', 'manage_kbs', 'manage_datasources'].includes(cap))
  return true
}

// Retain existing levels when the selection changes; new KBs start read-only.
export function selectAPIKeyKBs(ids: string[], current: APIKeyKBPermissions): APIKeyKBPermissions {
  return Object.fromEntries(ids.map(id => [id, current[id] || 'read']))
}

/**
 * 将 API 返回的知识库范围归一化为前端表单可安全使用的数组。
 *
 * @param ids API Key 的知识库 ID；完全授权的 Key 可能由服务端返回 null。
 * @returns 一份新的知识库 ID 数组；null 或 undefined 返回空数组，表示全部知识库。
 */
export function normalizeAPIKeyKnowledgeBaseIDs(
  ids: readonly string[] | null | undefined,
): string[] {
  return ids ? [...ids] : []
}
