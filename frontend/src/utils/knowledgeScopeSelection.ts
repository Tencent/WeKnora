import type { FolderScopeSelection } from '../types/knowledgeScope'
import {
  folderScopeSelectionKey,
  normalizeFolderScopeSelections,
} from './knowledgeScope'

export interface KnowledgeScopeDraft {
  knowledgeBaseIds: string[]
  folders: FolderScopeSelection[]
}

export interface FolderScopeChip {
  kind: 'folder-scope'
  key: string
  label: string
  title: string
  scope: FolderScopeSelection
}

export interface KnowledgeScopeDisplayState<T> {
  items: Array<T | FolderScopeChip>
  folderChips: FolderScopeChip[]
  count: number
}

export type LastVisibleKnowledgeScopeItem<TFolder, TItem> =
  | { kind: 'folder'; item: TFolder }
  | { kind: 'item'; item: TItem }
  | null

function uniqueNonEmptyIds(ids: readonly string[]): string[] {
  return [...new Set(ids.map(id => id.trim()).filter(Boolean))]
}

export function getUnresolvedKnowledgeScopeFileIds(
  folders: readonly FolderScopeSelection[],
  fileIds: readonly string[],
  fileKbMap: Readonly<Record<string, string | undefined>>,
): string[] {
  if (
    normalizeFolderScopeSelections(folders).length === 0
    || fileIds.length === 0
  ) return []
  return uniqueNonEmptyIds(fileIds).filter(id => !fileKbMap[id]?.trim())
}

export function getLastVisibleKnowledgeScopeItem<TFolder, TItem>(
  folderItems: readonly TFolder[],
  selectedItems: readonly TItem[],
): LastVisibleKnowledgeScopeItem<TFolder, TItem> {
  if (selectedItems.length > 0) {
    return {
      kind: 'item',
      item: selectedItems[selectedItems.length - 1],
    }
  }
  if (folderItems.length > 0) {
    return {
      kind: 'folder',
      item: folderItems[folderItems.length - 1],
    }
  }
  return null
}

export function createKnowledgeScopeDraft(
  knowledgeBaseIds: readonly string[],
  folders: readonly FolderScopeSelection[],
): KnowledgeScopeDraft {
  const normalizedKnowledgeBaseIds = uniqueNonEmptyIds(knowledgeBaseIds)
  return {
    knowledgeBaseIds: [...normalizedKnowledgeBaseIds],
    folders: normalizeFolderScopeSelections(
      folders,
      normalizedKnowledgeBaseIds,
    ),
  }
}

export function confirmKnowledgeScopeDraft(
  draft: KnowledgeScopeDraft,
): KnowledgeScopeDraft {
  return createKnowledgeScopeDraft(draft.knowledgeBaseIds, draft.folders)
}

export function removeFolderScopeSelection(
  folders: readonly FolderScopeSelection[],
  knowledgeBaseId: string,
  folderId: string,
): FolderScopeSelection[] {
  const normalizedFolders = normalizeFolderScopeSelections(folders)
  return normalizedFolders.filter(folder => (
    folder.knowledgeBaseId !== knowledgeBaseId
    || folder.folderId !== folderId
  ))
}

export function buildFolderScopeChips(
  folders: readonly FolderScopeSelection[],
): FolderScopeChip[] {
  return normalizeFolderScopeSelections(folders).map(scope => {
    const label = [
      scope.knowledgeBaseName || scope.knowledgeBaseId,
      ...scope.folderPath,
    ].filter(Boolean).join(' / ')
    return {
      kind: 'folder-scope',
      key: folderScopeSelectionKey(scope),
      label,
      title: label,
      scope,
    }
  })
}

export function buildKnowledgeScopeDisplayState<T>(
  selectedItems: readonly T[],
  folders: readonly FolderScopeSelection[],
): KnowledgeScopeDisplayState<T> {
  const folderChips = buildFolderScopeChips(folders)
  const items = [...folderChips, ...selectedItems]
  return {
    items,
    folderChips,
    count: items.length,
  }
}
