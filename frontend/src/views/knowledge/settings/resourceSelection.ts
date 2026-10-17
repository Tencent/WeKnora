export interface SelectionResource {
  external_id: string
  parent_id?: string
}

export type ResourceCheckState = 'checked' | 'indeterminate' | 'unchecked'

export function buildResourceIndexes<T extends SelectionResource>(resources: T[]) {
  const children = new Map<string, T[]>()
  const parents = new Map<string, string>()
  for (const resource of resources) {
    if (!resource.parent_id) continue
    parents.set(resource.external_id, resource.parent_id)
    const siblings = children.get(resource.parent_id)
    if (siblings) siblings.push(resource)
    else children.set(resource.parent_id, [resource])
  }
  return { children, parents }
}

export function computeResourceCheckStates<T extends SelectionResource>(
  resources: T[],
  children: Map<string, T[]>,
  selectedResourceIds: string[],
): Map<string, ResourceCheckState> {
  const states = new Map<string, ResourceCheckState>()
  const cover = new Set(selectedResourceIds)

  function walk(node: T, ancestorChecked: boolean, visiting: Set<string>): boolean {
    if (visiting.has(node.external_id)) return false
    const nextVisiting = new Set(visiting).add(node.external_id)
    const selfChecked = ancestorChecked || cover.has(node.external_id)
    let descendantChecked = false
    for (const child of children.get(node.external_id) || []) {
      if (walk(child, selfChecked, nextVisiting)) descendantChecked = true
    }
    states.set(
      node.external_id,
      selfChecked ? 'checked' : descendantChecked ? 'indeterminate' : 'unchecked',
    )
    return selfChecked || descendantChecked
  }

  for (const resource of resources) {
    if (!resource.parent_id) walk(resource, false, new Set())
  }
  return states
}

export function toggleResourceSelection<T extends SelectionResource>(
  id: string,
  selectedResourceIds: string[],
  children: Map<string, T[]>,
  parents: Map<string, string>,
  state: ResourceCheckState,
): string[] {
  const cover = new Set(selectedResourceIds)

  const descendants = (root: string): string[] => {
    const result: string[] = []
    const seen = new Set<string>([root])
    const visit = (parent: string) => {
      for (const child of children.get(parent) || []) {
        if (seen.has(child.external_id)) continue
        seen.add(child.external_id)
        result.push(child.external_id)
        visit(child.external_id)
      }
    }
    visit(root)
    return result
  }

  if (state === 'unchecked') {
    for (let current: string | undefined = id; current; current = parents.get(current)) {
      if (cover.has(current)) return [...cover]
    }
    const nested = new Set(descendants(id))
    for (const selected of [...cover]) {
      if (nested.has(selected)) cover.delete(selected)
    }
    cover.add(id)
    return [...cover]
  }

  const chain = [id]
  const seen = new Set<string>(chain)
  for (let parent = parents.get(id); parent && !seen.has(parent); parent = parents.get(parent)) {
    chain.push(parent)
    seen.add(parent)
  }
  let selectedAncestorIndex = -1
  for (let index = chain.length - 1; index >= 0; index--) {
    if (cover.has(chain[index])) {
      selectedAncestorIndex = index
      break
    }
  }
  if (selectedAncestorIndex > 0) {
    cover.delete(chain[selectedAncestorIndex])
    for (let index = selectedAncestorIndex; index > 0; index--) {
      const parent = chain[index]
      const excludedPath = chain[index - 1]
      for (const sibling of children.get(parent) || []) {
        if (sibling.external_id !== excludedPath) cover.add(sibling.external_id)
      }
    }
  }
  cover.delete(id)
  const nested = new Set(descendants(id))
  for (const selected of [...cover]) {
    if (nested.has(selected)) cover.delete(selected)
  }
  return [...cover]
}
