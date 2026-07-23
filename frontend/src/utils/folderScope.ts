import type { Folder } from '../types/folder'
import { buildFolderBreadcrumb } from '../views/knowledge/utils/folderTree'

export interface FolderScope {
  knowledge_base_id: string
  folder_ids: string[]
}

export type SelectedFolderScopes = Record<string, string[] | undefined>

export interface RawFolderScope {
  knowledge_base_id?: unknown
  folder_ids?: unknown
  folder_id?: unknown
}

export type ResolvedFolderScopeState =
  | { status: 'entire-kb' }
  | { status: 'loading'; folderIDs: string[] }
  | { status: 'load-error'; folderIDs: string[]; error?: string }
  | { status: 'valid'; folders: Folder[]; paths: string[] }
  | {
      status: 'invalid'
      folders: Folder[]
      paths: string[]
      invalidFolderIDs: string[]
    }

const normalizeId = (value: unknown): string => {
  return typeof value === 'string' ? value.trim() : ''
}

export function normalizeFolderIDs(value: unknown): string[] {
  const values = typeof value === 'string' ? [value] : (Array.isArray(value) ? value : [])
  return [...new Set(values.map(normalizeId).filter(Boolean))].sort()
}

function validKbSet(validKnowledgeBaseIDs?: readonly string[]): Set<string> | null {
  if (!validKnowledgeBaseIDs) return null
  return new Set(validKnowledgeBaseIDs.map(id => String(id).trim()).filter(Boolean))
}

export function normalizeSelectedFolderScopes(
  scopes: unknown,
  validKnowledgeBaseIDs?: readonly string[],
): SelectedFolderScopes {
  const allowed = validKbSet(validKnowledgeBaseIDs)
  const normalized: SelectedFolderScopes = {}
  if (!scopes || typeof scopes !== 'object' || Array.isArray(scopes)) {
    return normalized
  }

  for (const [knowledgeBaseID, rawFolderIDs] of Object.entries(scopes)) {
    const kbID = normalizeId(knowledgeBaseID)
    const folderIDs = normalizeFolderIDs(rawFolderIDs)
    if (!kbID || folderIDs.length === 0) continue
    if (allowed && !allowed.has(kbID)) continue
    normalized[kbID] = folderIDs
  }
  return normalized
}

export function pruneFolderScopes(
  scopes: unknown,
  validKnowledgeBaseIDs: readonly string[],
): SelectedFolderScopes {
  return normalizeSelectedFolderScopes(scopes, validKnowledgeBaseIDs)
}

export function restoreFolderScopes(
  scopes: readonly (RawFolderScope | null | undefined)[] | null | undefined,
  validKnowledgeBaseIDs?: readonly string[],
): SelectedFolderScopes {
  const allowed = validKbSet(validKnowledgeBaseIDs)
  const restored: SelectedFolderScopes = {}
  if (!Array.isArray(scopes)) return restored

  for (const scope of scopes) {
    if (!scope || typeof scope !== 'object') continue
    const knowledgeBaseID = normalizeId(scope.knowledge_base_id)
    if (!knowledgeBaseID || (allowed && !allowed.has(knowledgeBaseID))) continue
    const folderIDs = normalizeFolderIDs(scope.folder_ids)
    const legacyFolderID = normalizeId(scope.folder_id)
    if (legacyFolderID) folderIDs.push(legacyFolderID)
    const combined = normalizeFolderIDs([...(restored[knowledgeBaseID] || []), ...folderIDs])
    if (combined.length > 0) restored[knowledgeBaseID] = combined
  }
  return restored
}

export function buildFolderScopes(
  selectedKnowledgeBaseIDs: readonly string[],
  selectedFolderScopes: unknown,
): FolderScope[] {
  const normalized = normalizeSelectedFolderScopes(selectedFolderScopes, selectedKnowledgeBaseIDs)
  const scopes: FolderScope[] = []
  const seen = new Set<string>()

  for (const rawKnowledgeBaseID of selectedKnowledgeBaseIDs) {
    const knowledgeBaseID = normalizeId(rawKnowledgeBaseID)
    if (!knowledgeBaseID || seen.has(knowledgeBaseID)) continue
    seen.add(knowledgeBaseID)
    const folderIDs = normalized[knowledgeBaseID]
    if (!folderIDs?.length) continue
    scopes.push({
      knowledge_base_id: knowledgeBaseID,
      folder_ids: [...folderIDs],
    })
  }

  return scopes
}

export function resolveFolderScopeState(
  rawFolderIDs: unknown,
  folders: readonly Folder[] | null | undefined,
  loading: boolean,
  error?: unknown,
): ResolvedFolderScopeState {
  const folderIDs = normalizeFolderIDs(rawFolderIDs)
  if (folderIDs.length === 0) return { status: 'entire-kb' }
  if (loading) return { status: 'loading', folderIDs }
  if (error) {
    return {
      status: 'load-error',
      folderIDs,
      error: error instanceof Error ? error.message : String(error),
    }
  }
  if (!folders) return { status: 'loading', folderIDs }

  const byId = new Map(folders.map(folder => [folder.id, folder]))
  const selectedFolders: Folder[] = []
  const paths: string[] = []
  const invalidFolderIDs: string[] = []
  for (const folderID of folderIDs) {
    const folder = byId.get(folderID)
    if (!folder) {
      invalidFolderIDs.push(folderID)
      continue
    }
    selectedFolders.push(folder)
    const breadcrumb = buildFolderBreadcrumb(folderID, byId)
    paths.push(breadcrumb.map(item => item.name).filter(Boolean).join(' / ') || folder.name)
  }

  if (invalidFolderIDs.length > 0) {
    return {
      status: 'invalid',
      folders: selectedFolders,
      paths,
      invalidFolderIDs,
    }
  }
  return { status: 'valid', folders: selectedFolders, paths }
}
