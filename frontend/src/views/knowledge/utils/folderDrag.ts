import type { Folder } from '@/types/folder'
import { isFolderUUID } from './folderRoute'
import { buildFolderMoveDisabledIds } from './folderTree'

export const KNOWLEDGE_TREE_DRAG_MIME = 'application/x-weknora-knowledge-tree-item+json'

export type ActiveDrag =
  | {
      type: 'folder'
      folderId: string
      sourceParentId: string | null
    }
  | {
      type: 'knowledge'
      knowledgeId: string
      sourceFolderId: string | null
    }

type DragDataTransfer = Pick<DataTransfer, 'types' | 'getData'>

function isNullableUUID(value: unknown): value is string | null {
  return value === null || (typeof value === 'string' && isFolderUUID(value))
}

function hasOnlyKeys(value: Record<string, unknown>, expected: string[]): boolean {
  const actual = Object.keys(value).sort()
  return actual.length === expected.length && actual.every((key, index) => key === expected[index])
}

export function serializeActiveDrag(payload: ActiveDrag): string {
  return JSON.stringify(payload)
}

export function parseActiveDragPayload(raw: string): ActiveDrag | null {
  if (!raw || raw.length > 1024) return null
  try {
    const value: unknown = JSON.parse(raw)
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null
    const record = value as Record<string, unknown>

    if (record.type === 'folder') {
      if (!hasOnlyKeys(record, ['folderId', 'sourceParentId', 'type'])) return null
      if (typeof record.folderId !== 'string' || !isFolderUUID(record.folderId)) return null
      if (!isNullableUUID(record.sourceParentId)) return null
      return {
        type: 'folder',
        folderId: record.folderId,
        sourceParentId: record.sourceParentId,
      }
    }

    if (record.type === 'knowledge') {
      if (!hasOnlyKeys(record, ['knowledgeId', 'sourceFolderId', 'type'])) return null
      if (typeof record.knowledgeId !== 'string' || !isFolderUUID(record.knowledgeId)) return null
      if (!isNullableUUID(record.sourceFolderId)) return null
      return {
        type: 'knowledge',
        knowledgeId: record.knowledgeId,
        sourceFolderId: record.sourceFolderId,
      }
    }
  } catch {
    return null
  }
  return null
}

function sameDrag(left: ActiveDrag, right: ActiveDrag): boolean {
  if (left.type !== right.type) return false
  if (left.type === 'folder' && right.type === 'folder') {
    return left.folderId === right.folderId && left.sourceParentId === right.sourceParentId
  }
  return left.type === 'knowledge' && right.type === 'knowledge'
    && left.knowledgeId === right.knowledgeId
    && left.sourceFolderId === right.sourceFolderId
}

export function readInternalDragPayload(
  dataTransfer: DragDataTransfer | null,
  activeDrag: ActiveDrag | null,
): ActiveDrag | null {
  if (!dataTransfer || !activeDrag || !Array.from(dataTransfer.types).includes(KNOWLEDGE_TREE_DRAG_MIME)) {
    return null
  }
  const parsed = parseActiveDragPayload(dataTransfer.getData(KNOWLEDGE_TREE_DRAG_MIME))
  return parsed && sameDrag(parsed, activeDrag) ? parsed : null
}

function hasSafeFolderAncestry(
  folderId: string,
  byId: ReadonlyMap<string, Folder>,
): boolean {
  const visited = new Set<string>()
  let currentId: string | null = folderId

  while (currentId) {
    if (visited.has(currentId)) return false
    visited.add(currentId)
    const folder = byId.get(currentId)
    if (!folder) return false
    currentId = folder.parent_id
  }
  return true
}

export function isDragTargetAllowed(
  activeDrag: ActiveDrag | null,
  targetFolderId: string | null,
  folders: Folder[],
): boolean {
  if (!activeDrag) return false
  const byId = new Map(folders.map(folder => [folder.id, folder]))
  if (targetFolderId && !hasSafeFolderAncestry(targetFolderId, byId)) return false

  if (activeDrag.type === 'knowledge') {
    if (activeDrag.sourceFolderId === targetFolderId) return false
    if (activeDrag.sourceFolderId && !hasSafeFolderAncestry(activeDrag.sourceFolderId, byId)) {
      return false
    }
    return targetFolderId === null || byId.has(targetFolderId)
  }

  const source = byId.get(activeDrag.folderId)
  if (!source || source.parent_id !== activeDrag.sourceParentId) return false
  if (!hasSafeFolderAncestry(source.id, byId)) return false
  if (targetFolderId === null) return source.parent_id !== null
  return !buildFolderMoveDisabledIds(folders, source.id, source.parent_id).has(targetFolderId)
}
