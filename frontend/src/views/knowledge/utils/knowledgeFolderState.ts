import type { LocationQuery, LocationQueryRaw } from 'vue-router'

export type KnowledgeFolderSelection = string | undefined
export type KnowledgeFolderRouteQuery = LocationQuery | LocationQueryRaw

export function readKnowledgeFolderSelection(
  query: KnowledgeFolderRouteQuery,
): KnowledgeFolderSelection {
  const raw = query.folder_id
  if (typeof raw === 'string') return raw
  if (Array.isArray(raw)) {
    for (const value of raw) {
      if (typeof value === 'string') return value
    }
  }
  return undefined
}

export function writeKnowledgeFolderSelection(
  query: KnowledgeFolderRouteQuery,
  folderId: KnowledgeFolderSelection,
): LocationQueryRaw {
  const nextQuery: LocationQueryRaw = { ...query }
  if (folderId === undefined) {
    delete nextQuery.folder_id
  } else {
    nextQuery.folder_id = folderId
  }
  return nextQuery
}

export function resolveKnowledgeUploadFolderID(
  folderId: KnowledgeFolderSelection,
): string {
  return folderId ?? ''
}

export function appendKnowledgeFolderListQuery(
  searchParams: URLSearchParams,
  folderId: KnowledgeFolderSelection,
): URLSearchParams {
  if (folderId === undefined) {
    searchParams.delete('folder_id')
  } else {
    searchParams.set('folder_id', folderId)
  }
  return searchParams
}
