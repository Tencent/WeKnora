import type { KnowledgeFolder, KnowledgeFolderRow } from '@/types/knowledgeFolder'

export function flattenKnowledgeFolders(folders: KnowledgeFolder[]): KnowledgeFolderRow[] {
  const children = new Map<string | null, KnowledgeFolder[]>()
  for (const folder of folders) {
    const parentID = folder.parent_id || null
    const siblings = children.get(parentID) || []
    siblings.push(folder)
    children.set(parentID, siblings)
  }
  for (const siblings of children.values()) {
    siblings.sort((left, right) => left.name.localeCompare(right.name))
  }

  const result: KnowledgeFolderRow[] = []
  const visited = new Set<string>()
  const walk = (parentID: string | null, depth: number) => {
    for (const folder of children.get(parentID) || []) {
      if (visited.has(folder.id)) continue
      visited.add(folder.id)
      result.push({ folder, depth })
      walk(folder.id, depth + 1)
    }
  }
  walk(null, 0)

  // Foreign keys prevent this in normal operation, but keeping orphaned or
  // cyclic legacy rows visible makes them recoverable from the UI.
  for (const folder of [...folders].sort((left, right) => left.name.localeCompare(right.name))) {
    if (visited.has(folder.id)) continue
    visited.add(folder.id)
    result.push({ folder, depth: 0 })
    walk(folder.id, 1)
  }
  return result
}
