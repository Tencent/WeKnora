import { ROOT_FOLDER_FILTER } from '../../types/knowledgeFolder'

export interface KnowledgeFolderRouteState {
  folderId: string
  searchTerm: string
}

export interface QueryContext {
  kbId: string
  folderId: string
  searchTerm: string
  filtersSignature: string
  generation: number
}

export type QueryGeneration = {
  next(input: Omit<QueryContext, 'generation'>): QueryContext
  current(): QueryContext | null
}

function toTrimmedString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/**
 * Parse URL route query into UI state. The `__root__` sentinel is an API-boundary
 * concern only; it is never surfaced into UI folder state, so a stale/edited URL
 * carrying it resolves to the root folder instead of leaking the sentinel.
 */
export function parseKnowledgeFolderRouteQuery(query: Record<string, unknown>): KnowledgeFolderRouteState {
  const folderId = toTrimmedString(query.folder_id)
  if (folderId === ROOT_FOLDER_FILTER) {
    return { folderId: '', searchTerm: toTrimmedString(query.q) }
  }
  return { folderId, searchTerm: toTrimmedString(query.q) }
}

/**
 * Format UI state into URL route query. Root is expressed by *omitting*
 * folder_id (not by emitting `__root__`), and empty fragments are omitted so
 * the URL stays clean and root stays implicit.
 */
export function formatKnowledgeFolderRouteQuery(
  folderId: string,
  searchTerm: string,
): Record<string, string> {
  const result: Record<string, string> = {}
  const folder = toTrimmedString(folderId)
  const term = toTrimmedString(searchTerm)
  if (folder && folder !== ROOT_FOLDER_FILTER) result.folder_id = folder
  if (term) result.q = term
  return result
}

/**
 * Deterministic signature of the non-folder/non-search filters, so a result
 * commit can be compared by value even though object key order may differ.
 */
export function stableFiltersSignature(value: unknown): string {
  return JSON.stringify(sortKeysDeep(value))
}

function sortKeysDeep(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortKeysDeep)
  if (value && typeof value === 'object') {
    return Object.keys(value as Record<string, unknown>)
      .sort()
      .reduce<Record<string, unknown>>((acc, key) => {
        acc[key] = sortKeysDeep((value as Record<string, unknown>)[key])
        return acc
      }, {})
  }
  return value
}

/**
 * Monotonic generation counter for query contexts. Each navigation/refresh
 * bumps the generation; only results matching the *current* generation may
 * be committed, so stale async results from a previous folder/search are dropped.
 */
export function createQueryGeneration(): QueryGeneration {
  let current: QueryContext | null = null
  return {
    next(input: Omit<QueryContext, 'generation'>): QueryContext {
      current = { ...input, generation: (current?.generation ?? 0) + 1 }
      return current
    },
    current(): QueryContext | null {
      return current
    },
  }
}

/**
 * Decide whether a candidate result may be committed: only if it is the exact
 * current context object of *this* generator. A stale candidate from an older
 * generation, or a candidate from a different generator instance, is rejected.
 */
export function shouldCommitQueryResult(current: QueryContext | null, candidate: QueryContext): boolean {
  return current !== null && candidate === current
}
