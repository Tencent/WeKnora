/**
 * Remembers the sort chosen in the knowledge base, agent and space lists so
 * the next visit opens with it instead of the default.
 *
 * The choice is a per-user UI preference stored in localStorage through
 * preferenceStorage (WeKnora_${userId}_resource_sort_${list}): it follows the
 * user across spaces in the same browser, and accounts sharing a browser keep
 * their own choice. Each list is remembered separately.
 */
import { loadPreference, savePreference } from '../composables/preferenceStorage'
import { DEFAULT_RESOURCE_SORT, RESOURCE_SORT_OPTIONS, type ResourceSortValue } from './resourceSorting'

export type ResourceSortList = 'knowledge_bases' | 'agents' | 'organizations'

function preferenceSuffix(list: ResourceSortList): string {
  return `resource_sort_${list}`
}

export function loadResourceSortPreference(list: ResourceSortList): ResourceSortValue {
  const stored = loadPreference(preferenceSuffix(list))
  const option = RESOURCE_SORT_OPTIONS.find(item => item.value === stored)
  return option ? option.value : DEFAULT_RESOURCE_SORT
}

export function saveResourceSortPreference(list: ResourceSortList, value: ResourceSortValue): void {
  savePreference(preferenceSuffix(list), value)
}
