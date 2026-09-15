export const KB_PERMISSION_CAPABILITIES = ['retrieve', 'ingest', 'manage_kbs'] as const

export type KnowledgeBasePermissionCapability = (typeof KB_PERMISSION_CAPABILITIES)[number]

export type KnowledgeBasePermissionMap = Record<string, string[]>

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

export function normalizeKnowledgeBasePermissions(
  perms: KnowledgeBasePermissionMap | null | undefined,
): KnowledgeBasePermissionMap {
  if (!perms) return {}
  const out: KnowledgeBasePermissionMap = {}
  for (const [kbID, grants] of Object.entries(perms)) {
    const id = kbID.trim()
    if (!id) continue
    out[id] = [...(grants || [])]
  }
  return out
}

export function hasKBReadCeiling(capabilities: readonly string[]): boolean {
  return capabilities.includes('retrieve') || capabilities.includes('chat')
}

export function defaultKnowledgeBaseGrants(
  capabilities: readonly string[],
): KnowledgeBasePermissionCapability[] {
  const grants: KnowledgeBasePermissionCapability[] = []
  if (hasKBReadCeiling(capabilities)) grants.push('retrieve')
  if (capabilities.includes('ingest')) grants.push('ingest')
  if (capabilities.includes('manage_kbs')) grants.push('manage_kbs')
  return grants
}

export function knowledgeBaseGrantsForID(
  kbID: string,
  ids: readonly string[],
  perms: KnowledgeBasePermissionMap,
  capabilities: readonly string[],
): KnowledgeBasePermissionCapability[] {
  const defaults = defaultKnowledgeBaseGrants(capabilities)
  if (!ids.includes(kbID)) return []
  const stored = perms[kbID]
  if (!stored) return defaults
  return KB_PERMISSION_CAPABILITIES.filter((cap) => (
    defaults.includes(cap) && stored.includes(cap)
  ))
}

function sameGrantSet(
  a: readonly string[],
  b: readonly string[],
): boolean {
  if (a.length !== b.length) return false
  const other = new Set(b)
  return a.every((item) => other.has(item))
}

export function buildKnowledgeBasePermissionsPayload(
  ids: readonly string[],
  perms: KnowledgeBasePermissionMap,
  capabilities: readonly string[],
): KnowledgeBasePermissionMap {
  const defaults = defaultKnowledgeBaseGrants(capabilities)
  const out: KnowledgeBasePermissionMap = {}
  let customized = false
  for (const id of ids) {
    const grants = knowledgeBaseGrantsForID(id, ids, perms, capabilities)
    out[id] = [...grants]
    if (!sameGrantSet(grants, defaults)) customized = true
  }
  return customized ? out : {}
}

export function summarizeKnowledgeBasePermissions(
  ids: readonly string[],
  perms: KnowledgeBasePermissionMap | null | undefined,
  capabilities: readonly string[] = [],
): { total: number; editable: number; manageable: number } {
  const normalizedIDs = normalizeAPIKeyKnowledgeBaseIDs(ids)
  const normalizedPerms = normalizeKnowledgeBasePermissions(perms)
  let editable = 0
  let manageable = 0
  for (const id of normalizedIDs) {
    const grants = knowledgeBaseGrantsForID(id, normalizedIDs, normalizedPerms, capabilities)
    if (grants.includes('ingest')) editable += 1
    if (grants.includes('manage_kbs')) manageable += 1
  }
  return { total: normalizedIDs.length, editable, manageable }
}
