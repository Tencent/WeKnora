export interface SourceFolderSelectionStore {
  addKnowledgeBase(knowledgeBaseID: string): void
  getFolderScopes(knowledgeBaseID: string): string[]
  toggleFolderScope(knowledgeBaseID: string, folderID: string): void
  clearFolderScopes(knowledgeBaseID: string): void
}

export type SourceFolderSelectionResult = 'disabled' | 'unchanged' | 'updated'

const normalizedID = (value: string): string => value.trim()

export function toggleSourceFolderScope(
  store: SourceFolderSelectionStore,
  knowledgeBaseID: string,
  folderID: string,
  disabled = false,
): SourceFolderSelectionResult {
  if (disabled) return 'disabled'
  const kbID = normalizedID(knowledgeBaseID)
  const selectedFolderID = normalizedID(folderID)
  if (!kbID || !selectedFolderID) return 'unchanged'

  store.addKnowledgeBase(kbID)
  store.toggleFolderScope(kbID, selectedFolderID)
  return 'updated'
}

export function selectEntireKnowledgeBaseScope(
  store: SourceFolderSelectionStore,
  knowledgeBaseID: string,
  disabled = false,
): SourceFolderSelectionResult {
  if (disabled) return 'disabled'
  const kbID = normalizedID(knowledgeBaseID)
  if (!kbID) return 'unchanged'

  store.addKnowledgeBase(kbID)
  if (store.getFolderScopes(kbID).length === 0) return 'unchanged'
  store.clearFolderScopes(kbID)
  return 'updated'
}
