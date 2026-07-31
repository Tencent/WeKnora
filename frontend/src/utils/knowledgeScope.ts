import type {
  FolderScopeRequest,
  FolderScopeSelection,
  KnowledgeScopeBuildInput,
  KnowledgeScopeProjection,
  KnowledgeScopeRequest,
  KnowledgeScopeTagSelection,
  TagScopeRequest,
} from '@/types/knowledgeScope'

export function folderScopeSelectionKey(
  selection: Pick<FolderScopeSelection, 'knowledgeBaseId' | 'folderId'>,
): string {
  return `${selection.knowledgeBaseId}:${selection.folderId}`
}

export function cloneKnowledgeScopeRequest(
  request: KnowledgeScopeRequest,
): KnowledgeScopeRequest {
  const cloned: KnowledgeScopeRequest = { ...request }
  if (request.knowledge_base_ids) {
    cloned.knowledge_base_ids = [...request.knowledge_base_ids]
  }
  if (request.knowledge_ids) {
    cloned.knowledge_ids = [...request.knowledge_ids]
  }
  if (request.tag_scopes) {
    cloned.tag_scopes = request.tag_scopes.map(scope => ({
      ...scope,
      tag_ids: [...scope.tag_ids],
    }))
  }
  if (request.folder_scopes) {
    cloned.folder_scopes = request.folder_scopes.map(scope => ({
      ...scope,
      folder_ids: [...scope.folder_ids],
    }))
  }
  return cloned
}

function uniqueNonEmpty(values: readonly string[]): string[] {
  const result: string[] = []
  const seen = new Set<string>()
  for (const value of values) {
    if (typeof value !== 'string') continue
    const normalized = value.trim()
    if (!normalized || seen.has(normalized)) continue
    seen.add(normalized)
    result.push(normalized)
  }
  return result
}

function normalizeSelection(selection: FolderScopeSelection): FolderScopeSelection | null {
  const knowledgeBaseId = selection.knowledgeBaseId.trim()
  const folderId = selection.folderId.trim()
  if (!knowledgeBaseId || !folderId) return null

  const folderPath = selection.folderPath
    .map(segment => segment.trim())
    .filter(Boolean)
  const folderName = selection.folderName.trim()
    || folderPath[folderPath.length - 1]
    || folderId
  return {
    knowledgeBaseId,
    knowledgeBaseName: selection.knowledgeBaseName.trim() || knowledgeBaseId,
    folderId,
    folderName,
    folderPath: folderPath.length > 0 ? folderPath : [folderName],
    ancestorFolderIds: uniqueNonEmpty(selection.ancestorFolderIds)
      .filter(ancestorId => ancestorId !== folderId),
    includeDescendants: true,
  }
}

export function normalizeFolderScopeSelections(
  selections: readonly FolderScopeSelection[],
  selectedKnowledgeBaseIds: readonly string[] = [],
): FolderScopeSelection[] {
  const wholeKnowledgeBases = new Set(uniqueNonEmpty(selectedKnowledgeBaseIds))
  const deduplicated = new Map<string, FolderScopeSelection>()

  for (const rawSelection of selections) {
    const selection = normalizeSelection(rawSelection)
    if (!selection || wholeKnowledgeBases.has(selection.knowledgeBaseId)) continue
    const key = folderScopeSelectionKey(selection)
    if (!deduplicated.has(key)) deduplicated.set(key, selection)
  }

  const selectedKeys = new Set(deduplicated.keys())
  return [...deduplicated.values()].filter(selection => !selection.ancestorFolderIds.some(
    ancestorId => selectedKeys.has(folderScopeSelectionKey({
      knowledgeBaseId: selection.knowledgeBaseId,
      folderId: ancestorId,
    })),
  ))
}

function buildTagScopes(tags: readonly KnowledgeScopeTagSelection[]): TagScopeRequest[] {
  const grouped = new Map<string, Set<string>>()
  for (const tag of tags) {
    const knowledgeBaseId = tag.kbId.trim()
    const tagId = tag.id.trim()
    if (!knowledgeBaseId || !tagId) continue
    if (!grouped.has(knowledgeBaseId)) {
      grouped.set(knowledgeBaseId, new Set())
    }
    grouped.get(knowledgeBaseId)!.add(tagId)
  }
  return [...grouped.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([knowledge_base_id, tagIds]) => ({
      knowledge_base_id,
      tag_ids: [...tagIds].sort(),
    }))
}

function buildFolderScopes(
  selections: readonly FolderScopeSelection[],
  folderUnrestrictedKnowledgeBaseIds: readonly string[],
): FolderScopeRequest[] {
  const grouped = new Map<string, Set<string>>()
  for (const selection of selections) {
    if (!grouped.has(selection.knowledgeBaseId)) {
      grouped.set(selection.knowledgeBaseId, new Set())
    }
    grouped.get(selection.knowledgeBaseId)!.add(selection.folderId)
  }
  for (const knowledgeBaseId of folderUnrestrictedKnowledgeBaseIds) {
    if (!grouped.has(knowledgeBaseId)) {
      grouped.set(knowledgeBaseId, new Set(['']))
    }
  }
  return [...grouped.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([knowledge_base_id, folderIds]) => ({
      knowledge_base_id,
      folder_ids: [...folderIds].sort(),
      include_descendants: true,
    }))
}

function buildCanonicalKnowledgeScope(
  input: KnowledgeScopeBuildInput,
  folderSelections: readonly FolderScopeSelection[],
  wholeKnowledgeBaseIds: readonly string[],
  folderUnrestrictedKnowledgeBaseIds: readonly string[],
): KnowledgeScopeRequest | undefined {
  if (folderSelections.length === 0) return undefined

  const knowledgeIds = uniqueNonEmpty(input.knowledgeIds).sort()
  const tagScopes = buildTagScopes(input.tags)
  return {
    ...(wholeKnowledgeBaseIds.length > 0
      ? { knowledge_base_ids: [...wholeKnowledgeBaseIds] }
      : {}),
    ...(knowledgeIds.length > 0 ? { knowledge_ids: knowledgeIds } : {}),
    ...(tagScopes.length > 0 ? { tag_scopes: tagScopes } : {}),
    folder_scopes: buildFolderScopes(
      folderSelections,
      folderUnrestrictedKnowledgeBaseIds,
    ),
  }
}

function knowledgeSelectionKnowledgeBaseIds(
  input: KnowledgeScopeBuildInput,
): string[] {
  const selectedKnowledgeIds = new Set(uniqueNonEmpty(input.knowledgeIds))
  return uniqueNonEmpty((input.knowledgeSelections || [])
    .filter(selection => selectedKnowledgeIds.has(selection.id.trim()))
    .map(selection => selection.kbId || ''))
}

export function buildKnowledgeScopeProjection(
  input: KnowledgeScopeBuildInput,
): KnowledgeScopeProjection {
  const wholeKnowledgeBaseIds = uniqueNonEmpty(input.knowledgeBaseIds).sort()
  const folderSelections = normalizeFolderScopeSelections(
    input.folderSelections,
    wholeKnowledgeBaseIds,
  )
  const folderKnowledgeBaseIds = uniqueNonEmpty(
    folderSelections.map(selection => selection.knowledgeBaseId),
  )
  const tagKnowledgeBaseIds = uniqueNonEmpty(
    input.tags
      .filter(selection => selection.id.trim() && selection.kbId.trim())
      .map(selection => selection.kbId),
  )
  const knowledgeTargetKnowledgeBaseIds =
    knowledgeSelectionKnowledgeBaseIds(input)
  const folderUnrestrictedKnowledgeBaseIds = uniqueNonEmpty([
    ...wholeKnowledgeBaseIds,
    ...tagKnowledgeBaseIds,
    ...knowledgeTargetKnowledgeBaseIds,
  ])
  const legacyKnowledgeBaseIds = uniqueNonEmpty([
    ...wholeKnowledgeBaseIds,
    ...folderKnowledgeBaseIds,
    ...tagKnowledgeBaseIds,
    ...knowledgeTargetKnowledgeBaseIds,
  ]).sort()
  const knowledgeScope = buildCanonicalKnowledgeScope(
    input,
    folderSelections,
    wholeKnowledgeBaseIds,
    folderUnrestrictedKnowledgeBaseIds,
  )

  return {
    knowledge_base_ids: legacyKnowledgeBaseIds,
    ...(knowledgeScope ? { knowledge_scope: knowledgeScope } : {}),
  }
}

export function buildKnowledgeScopeRequest(
  input: KnowledgeScopeBuildInput,
): KnowledgeScopeRequest | undefined {
  return buildKnowledgeScopeProjection(input).knowledge_scope
}

export function knowledgeBaseSelectionsFromRequest(
  request: KnowledgeScopeRequest | null | undefined,
  explicitKnowledgeBaseIds: readonly string[] = [],
): string[] {
  return uniqueNonEmpty([
    ...(request?.knowledge_base_ids || []),
    ...explicitKnowledgeBaseIds,
  ]).sort()
}

export function folderSelectionsFromRequest(
  request: Pick<KnowledgeScopeRequest, 'folder_scopes'> | null | undefined,
): FolderScopeSelection[] {
  if (!Array.isArray(request?.folder_scopes)) return []

  const selections: FolderScopeSelection[] = []
  for (const scope of request.folder_scopes) {
    if (!scope?.knowledge_base_id || !Array.isArray(scope.folder_ids)) continue
    for (const folderId of scope.folder_ids) {
      if (!folderId) continue
      selections.push({
        knowledgeBaseId: scope.knowledge_base_id,
        knowledgeBaseName: scope.knowledge_base_id,
        folderId,
        folderName: folderId,
        folderPath: [folderId],
        ancestorFolderIds: [],
        includeDescendants: true,
      })
    }
  }
  return normalizeFolderScopeSelections(selections)
}
