import type { Resource } from '@/api/datasource'

// The picker renders one row per external_id. A connector that lists the same
// node twice — or two listing calls that overlap while a tree is expanded —
// therefore used to render the same row twice, with one copy outside the
// selection and one inside it. Deduplicating at ingestion keeps the tree (and
// Vue's `:key`) honest no matter what the source returns.
//
// Duplicate rows are merged rather than dropped: a first copy may be a bare
// placeholder while a later one carries the name/type the row needs, so the
// survivor keeps the first row's position and parent but fills in the facts the
// later copy has.

function isBlank(value: unknown): boolean {
  return value == null || (typeof value === 'string' && value.trim() === '')
}

function mergeResourceRows(first: Resource, later: Resource): Resource {
  return {
    ...first,
    name: isBlank(first.name) && !isBlank(later.name) ? later.name : first.name,
    type: isBlank(first.type) && !isBlank(later.type) ? later.type : first.type,
    description: isBlank(first.description) && !isBlank(later.description)
      ? later.description
      : first.description,
    url: isBlank(first.url) && !isBlank(later.url) ? later.url : first.url,
    // A row that already knows its parent must keep it: re-parenting a node
    // would move it in the tree, and the duplicate is the same node.
    parent_id: isBlank(first.parent_id) && !isBlank(later.parent_id)
      ? later.parent_id
      : first.parent_id,
    // `has_children` is a union, not a preference: one listing saying "this has
    // children" is enough to keep the expander reachable.
    has_children: first.has_children === true || later.has_children === true,
    metadata: first.metadata || later.metadata,
  }
}

/**
 * Collapses rows that share an external_id. Rows without an external_id are
 * kept as-is: without an id there is nothing to prove they are the same row,
 * and dropping them would hide content.
 */
export function dedupeResources(resources: readonly Resource[]): Resource[] {
  const result: Resource[] = []
  const indexById = new Map<string, number>()

  for (const resource of resources) {
    const id = resource?.external_id
    if (typeof id !== 'string' || id === '') {
      result.push(resource)
      continue
    }
    const existingIndex = indexById.get(id)
    if (existingIndex === undefined) {
      indexById.set(id, result.length)
      result.push(resource)
      continue
    }
    result[existingIndex] = mergeResourceRows(result[existingIndex], resource)
  }

  return result
}
