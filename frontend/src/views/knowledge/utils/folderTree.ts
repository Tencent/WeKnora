import type { Folder, FolderTreeNode, FolderTreeRow } from '@/types/folder'

export interface FolderTreeResult {
  roots: FolderTreeNode[]
  byId: Map<string, Folder>
  nodeById: Map<string, FolderTreeNode>
  orphanIds: Set<string>
  cyclicIds: Set<string>
}

function findCyclicFolderIds(folders: Folder[], byId: Map<string, Folder>): Set<string> {
  const done = new Set<string>()
  const cyclic = new Set<string>()

  for (const folder of folders) {
    if (done.has(folder.id)) continue

    const path: string[] = []
    const pathIndex = new Map<string, number>()
    let currentId: string | null = folder.id

    while (currentId && byId.has(currentId) && !done.has(currentId)) {
      const cycleStart = pathIndex.get(currentId)
      if (cycleStart !== undefined) {
        for (let index = cycleStart; index < path.length; index += 1) {
          cyclic.add(path[index])
        }
        break
      }
      pathIndex.set(currentId, path.length)
      path.push(currentId)
      currentId = byId.get(currentId)?.parent_id ?? null
    }

    path.forEach(id => done.add(id))
  }
  return cyclic
}

export function buildFolderTree(folders: Folder[]): FolderTreeResult {
  const byId = new Map<string, Folder>()
  const nodeById = new Map<string, FolderTreeNode>()

  for (const folder of folders) {
    if (byId.has(folder.id)) continue
    byId.set(folder.id, folder)
    nodeById.set(folder.id, { folder, children: [] })
  }

  const cyclicIds = findCyclicFolderIds(folders, byId)
  const orphanIds = new Set<string>()
  const roots: FolderTreeNode[] = []

  for (const folder of folders) {
    const node = nodeById.get(folder.id)
    if (!node || node.folder !== folder) continue

    const parent = folder.parent_id ? nodeById.get(folder.parent_id) : undefined
    if (!folder.parent_id) {
      roots.push(node)
    } else if (!parent) {
      orphanIds.add(folder.id)
      roots.push(node)
    } else if (cyclicIds.has(folder.id)) {
      roots.push(node)
    } else {
      parent.children.push(node)
    }
  }

  return { roots, byId, nodeById, orphanIds, cyclicIds }
}

export function flattenVisibleFolderTree(
  roots: FolderTreeNode[],
  expandedIds: ReadonlySet<string>,
): FolderTreeRow[] {
  const rows: FolderTreeRow[] = []
  const visited = new Set<string>()
  const stack = roots
    .slice()
    .reverse()
    .map(node => ({ node, depth: 0 }))

  while (stack.length > 0) {
    const entry = stack.pop()
    if (!entry || visited.has(entry.node.folder.id)) continue
    visited.add(entry.node.folder.id)
    rows.push({
      folder: entry.node.folder,
      depth: entry.depth,
      hasChildren: entry.node.children.length > 0,
    })

    if (!expandedIds.has(entry.node.folder.id)) continue
    for (let index = entry.node.children.length - 1; index >= 0; index -= 1) {
      stack.push({ node: entry.node.children[index], depth: entry.depth + 1 })
    }
  }
  return rows
}

export function buildFolderBreadcrumb(
  folderId: string | null,
  byId: ReadonlyMap<string, Folder>,
): Folder[] {
  if (!folderId) return []

  const reversed: Folder[] = []
  const visited = new Set<string>()
  let currentId: string | null = folderId

  while (currentId && !visited.has(currentId)) {
    visited.add(currentId)
    const folder = byId.get(currentId)
    if (!folder) break
    reversed.push(folder)
    currentId = folder.parent_id
  }
  return reversed.reverse()
}

export function resolveFolderDeleteDestination(
  folder: Folder,
  byId: ReadonlyMap<string, Folder>,
): string | null {
  return folder.parent_id && byId.has(folder.parent_id) ? folder.parent_id : null
}

export function collectFolderSubtreeIds(folders: Folder[], folderId: string): Set<string> {
  const childIdsByParent = new Map<string, string[]>()
  for (const folder of folders) {
    if (!folder.parent_id) continue
    const childIds = childIdsByParent.get(folder.parent_id) || []
    childIds.push(folder.id)
    childIdsByParent.set(folder.parent_id, childIds)
  }

  const visited = new Set<string>()
  const stack = [folderId]
  while (stack.length > 0) {
    const currentId = stack.pop()
    if (!currentId || visited.has(currentId)) continue
    visited.add(currentId)
    const childIds = childIdsByParent.get(currentId) || []
    for (const childId of childIds) {
      if (!visited.has(childId)) stack.push(childId)
    }
  }
  return visited
}

export function buildFolderMoveDisabledIds(
  folders: Folder[],
  folderId: string,
  currentParentId: string | null,
): Set<string> {
  const disabled = collectFolderSubtreeIds(folders, folderId)
  if (currentParentId) disabled.add(currentParentId)
  return disabled
}

export function isFolderMoveTargetDisabled(
  folderId: string | null,
  disabledFolderIds: ReadonlySet<string>,
  rootDisabled: boolean,
): boolean {
  return folderId === null ? rootDisabled : disabledFolderIds.has(folderId)
}

export interface KnowledgePathItem {
  id: string
  folder_id?: string | null
  file_name?: string
  display_name?: string
  title?: string
}

function getKnowledgeDisplayName(knowledge: KnowledgePathItem): string {
  return knowledge.file_name || knowledge.display_name || knowledge.title || ''
}

export function buildKnowledgeDisplayPaths(
  knowledges: readonly KnowledgePathItem[],
  folderById: ReadonlyMap<string, Folder>,
  rootLabel: string,
): Map<string, string> {
  const paths = new Map<string, string>()

  for (const knowledge of knowledges) {
    const fileName = getKnowledgeDisplayName(knowledge)
    if (!fileName) continue

    if (knowledge.folder_id === null) {
      paths.set(knowledge.id, `${rootLabel} / ${fileName}`)
      continue
    }
    if (!knowledge.folder_id || !folderById.has(knowledge.folder_id)) {
      paths.set(knowledge.id, fileName)
      continue
    }

    const folders = buildFolderBreadcrumb(knowledge.folder_id, folderById)
    const names = folders.map(folder => folder.name).filter(Boolean)
    paths.set(knowledge.id, [...names, fileName].join(' / ') || fileName)
  }

  return paths
}
