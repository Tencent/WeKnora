/**
 * Remembers the sort chosen in the knowledge base, agent and space lists so
 * the next visit opens with it instead of the default.
 *
 * The choice is a per-user, per-workspace UI preference stored in
 * localStorage through preferenceStorage
 * (WeKnora_${userId}_t${tenantId}_resource_sort_${list}, the same user and
 * workspace scoping as recents in useResourcePins): switching workspace
 * restores that workspace's own choice, and accounts sharing a browser keep
 * their own choice. Each list is remembered separately. Until the workspace
 * is known nothing is read or written, so no choice leaks across workspaces.
 */
import { loadPreference, savePreference } from '../composables/preferenceStorage'
import { DEFAULT_RESOURCE_SORT, RESOURCE_SORT_OPTIONS, type ResourceSortValue } from './resourceSorting'

export type ResourceSortList = 'knowledge_bases' | 'agents' | 'organizations'

type TenantId = number | string | null | undefined

function preferenceSuffix(list: ResourceSortList, tenantId: TenantId): string | null {
  return tenantId ? `t${tenantId}_resource_sort_${list}` : null
}

export function loadResourceSortPreference(list: ResourceSortList, tenantId: TenantId): ResourceSortValue {
  const suffix = preferenceSuffix(list, tenantId)
  const stored = suffix ? loadPreference(suffix) : null
  const option = RESOURCE_SORT_OPTIONS.find(item => item.value === stored)
  return option ? option.value : DEFAULT_RESOURCE_SORT
}

export function saveResourceSortPreference(list: ResourceSortList, tenantId: TenantId, value: ResourceSortValue): void {
  const suffix = preferenceSuffix(list, tenantId)
  if (suffix) savePreference(suffix, value)
}
