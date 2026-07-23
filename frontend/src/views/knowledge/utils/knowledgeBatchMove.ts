export interface FolderAwareKnowledge {
  id: string
  folder_id?: string | null
}

export interface BatchKnowledgeFolderSources {
  ids: string[]
  disabledFolderIds: Set<string>
  rootDisabled: boolean
  unresolvedIds: string[]
}

export function resolveBatchKnowledgeFolderSources(
  selectedIds: Iterable<string>,
  knowledges: readonly FolderAwareKnowledge[],
): BatchKnowledgeFolderSources {
  const ids = [...new Set(selectedIds)]
  const knowledgeById = new Map(knowledges.map(item => [item.id, item]))
  const disabledFolderIds = new Set<string>()
  const unresolvedIds: string[] = []
  let rootDisabled = false

  for (const id of ids) {
    const knowledge = knowledgeById.get(id)
    if (!knowledge || knowledge.folder_id === undefined) {
      unresolvedIds.push(id)
      continue
    }
    if (knowledge.folder_id === null) {
      rootDisabled = true
    } else {
      disabledFolderIds.add(knowledge.folder_id)
    }
  }

  return { ids, disabledFolderIds, rootDisabled, unresolvedIds }
}
