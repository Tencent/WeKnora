export function buildFolderMovePayload(parentId: string | null) {
  return { parent_id: parentId }
}

export function buildKnowledgeFolderMovePayload(folderId: string | null) {
  return { folder_id: folderId }
}

export interface BatchMoveKnowledgePayload {
  kb_id: string
  ids: string[]
  folder_id: string | null
}

export function buildBatchMoveKnowledgePayload(
  kbId: string,
  ids: Iterable<string>,
  folderId: string | null,
): BatchMoveKnowledgePayload {
  return {
    kb_id: kbId,
    ids: [...new Set(ids)],
    folder_id: folderId,
  }
}
