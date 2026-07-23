export interface FolderRouteState {
  folderId: string | null
  invalid: boolean
}

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function isFolderUUID(value: string): boolean {
  return UUID_PATTERN.test(value)
}

export function parseFolderRouteQuery(value: unknown): FolderRouteState {
  if (value === undefined || value === null || value === '') {
    return { folderId: null, invalid: false }
  }
  if (typeof value !== 'string' || !isFolderUUID(value)) {
    return { folderId: null, invalid: true }
  }
  return { folderId: value, invalid: false }
}

export function withFolderRouteQuery(
  query: Record<string, unknown>,
  folderId: string | null,
): Record<string, unknown> {
  const next = { ...query }
  delete next.knowledge_id
  if (folderId) {
    next.folder_id = folderId
  } else {
    delete next.folder_id
  }
  return next
}
