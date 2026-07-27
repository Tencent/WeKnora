import type { FileSystemSelection, KnowledgeFolder } from '@/types/knowledgeFolder'

export interface FolderIndex {
  byId: Map<string, KnowledgeFolder>
  childrenByParent: Map<string, KnowledgeFolder[]>
  pathIds: Map<string, string[]>
  orphanIds: Set<string>
  invalidPaths: Set<string>
}

export interface FolderSearchResult {
  folder: KnowledgeFolder
  pathLabel: string
}

export function sortDirectFolders(items: KnowledgeFolder[]): KnowledgeFolder[] {
  return [...items].sort((a, b) =>
    a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' }),
  )
}

export function flattenFolders(tree: KnowledgeFolder[]): KnowledgeFolder[] {
  const output: KnowledgeFolder[] = []
  const visit = (items: KnowledgeFolder[], seen: Set<string>) => {
    for (const item of items) {
      if (seen.has(item.id)) continue
      seen.add(item.id)
      output.push(item)
      visit(item.children || [], seen)
    }
  }
  visit(tree, new Set())
  return output
}

export function buildFolderIndex(tree: KnowledgeFolder[]): FolderIndex {
  const byId = new Map<string, KnowledgeFolder>()
  const childrenByParent = new Map<string, KnowledgeFolder[]>()
  const orphanIds = new Set<string>()
  const invalidPaths = new Set<string>()

  // Flatten with a visited set so child-object cycles cannot recurse forever.
  for (const item of flattenFolders(tree)) {
    // Duplicate IDs: first occurrence wins, later duplicates ignored.
    if (byId.has(item.id)) continue
    byId.set(item.id, item)
    const siblings = childrenByParent.get(item.parent_id) || []
    siblings.push(item)
    childrenByParent.set(item.parent_id, siblings)
  }
  for (const [parent, children] of childrenByParent) {
    childrenByParent.set(parent, sortDirectFolders(children))
  }

  const pathIds = new Map<string, string[]>()
  const resolvePath = (startId: string): string[] => {
    const cached = pathIds.get(startId)
    if (cached) return cached
    // Iterative upward walk with a per-resolution visited set, so parent_id
    // cycles terminate instead of recursing forever. Path is built root-first
    // (reversed at the end); a repeated node stops the walk and marks invalid.
    const chain: string[] = []
    const seen = new Set<string>()
    let currentId: string | undefined = startId
    let cycled = false
    while (currentId) {
      if (seen.has(currentId)) {
        cycled = true
        break
      }
      seen.add(currentId)
      chain.push(currentId)
      const item = byId.get(currentId)
      if (!item) break
      if (!item.parent_id || !byId.has(item.parent_id)) {
        // Root or orphan: stop. Orphan (parent missing) is recorded separately.
        if (item.parent_id && !byId.has(item.parent_id)) orphanIds.add(currentId)
        break
      }
      currentId = item.parent_id
    }
    if (cycled) invalidPaths.add(startId)
    const value = chain.reverse()
    pathIds.set(startId, value)
    return value
  }
  for (const id of byId.keys()) resolvePath(id)

  return { byId, childrenByParent, pathIds, orphanIds, invalidPaths }
}

export function folderPathItems(index: FolderIndex, id: string): KnowledgeFolder[] {
  return (index.pathIds.get(id) || [])
    .map((pathId) => index.byId.get(pathId))
    .filter((item): item is KnowledgeFolder => Boolean(item))
}

export function folderPathLabel(index: FolderIndex, id: string, rootLabel: string): string {
  const names = folderPathItems(index, id).map((item) => item.name)
  return [rootLabel, ...names].join(' / ')
}

export function searchFolders(index: FolderIndex, query: string, rootLabel = '根目录'): FolderSearchResult[] {
  const normalized = query.trim().toLocaleLowerCase()
  if (!normalized) return []
  return [...index.byId.values()]
    .filter((folder) => folder.name.toLocaleLowerCase().includes(normalized))
    .sort((a, b) => folderPathLabel(index, a.id, rootLabel).localeCompare(folderPathLabel(index, b.id, rootLabel)))
    .map((folder) => ({ folder, pathLabel: folderPathLabel(index, folder.id, rootLabel) }))
}

export function countDirectChildren(index: FolderIndex, id: string): number {
  return index.childrenByParent.get(id)?.length || 0
}

export function descendantIds(index: FolderIndex, id: string): Set<string> {
  const result = new Set<string>()
  const visit = (current: string) => {
    if (result.has(current)) return
    result.add(current)
    for (const child of index.childrenByParent.get(current) || []) visit(child.id)
  }
  visit(id)
  return result
}

export function selectionCount(selection: FileSystemSelection): number {
  return selection.knowledgeIds.size + selection.folderIds.size
}

export function selectionKeys(selection: FileSystemSelection): string[] {
  // Sorted for deterministic payloads/debug output only. NOT used for Shift range;
  // rendered order comes from buildRenderedSelectionKeys.
  return [
    ...[...selection.folderIds].sort().map((id) => `folder:${id}`),
    ...[...selection.knowledgeIds].sort().map((id) => `knowledge:${id}`),
  ]
}

export function buildRenderedSelectionKeys(
  directFolders: KnowledgeFolder[],
  documents: Array<{ id: string }>,
): string[] {
  // Folders render before documents in the same grid/list body, in rendered folder
  // order (already sorted by the caller). Documents follow in their loaded array
  // order, so Shift range and select-loaded match what the user sees.
  const folderKeys = directFolders.map((folder) => `folder:${folder.id}`)
  const documentKeys = documents.map((doc) => `knowledge:${doc.id}`)
  return [...folderKeys, ...documentKeys]
}

export function selectionCapabilities(selection: FileSystemSelection) {
  const count = selectionCount(selection)
  return {
    canMove: count > 0,
    canDelete: count > 0,
    canReparse: count > 0,
  }
}

export function isMoveTargetDisabled(
  index: FolderIndex,
  selectedFolderIds: Set<string>,
  targetId: string,
  currentParentId: string,
): boolean {
  if (targetId === currentParentId) return true
  for (const sourceId of selectedFolderIds) {
    if (descendantIds(index, sourceId).has(targetId)) return true
  }
  return false
}

// A synthetic create-input row inserted into a flattened visible-node list
// when folder creation is active in the tree. The level mirrors what a real
// child of the parent would have, so the input row indents correctly.
export interface CreatePlaceholderNode {
  isPlaceholder: true
  level: number
}

export function insertCreatePlaceholder<T extends { id: string; level: number }>(
  nodes: T[],
  creatingParentId: string | null,
): Array<T | CreatePlaceholderNode> {
  if (creatingParentId === null) return nodes
  const parentIndex = nodes.findIndex((node) => node.id === creatingParentId)
  if (parentIndex < 0) return nodes
  const placeholder: CreatePlaceholderNode = {
    isPlaceholder: true,
    level: nodes[parentIndex].level + 1,
  }
  const result: Array<T | CreatePlaceholderNode> = nodes.slice()
  result.splice(parentIndex + 1, 0, placeholder)
  return result
}
