export interface WikiDirectoryState {
  collapsed: Set<string>
  touched: Set<string>
}

function directoryPathKey(type: string, parts: string[]): string {
  return `${type}:${parts.join('/')}`
}

/**
 * Keep every directory in the current path expanded across a data refresh.
 * Marking the path as touched prevents default initialization from collapsing
 * it again when the refreshed folder and page batches arrive.
 */
export function expandWikiDirectoryPath(
  type: string,
  path: string[],
  collapsed: ReadonlySet<string>,
  touched: ReadonlySet<string>,
): WikiDirectoryState {
  const nextCollapsed = new Set(collapsed)
  const nextTouched = new Set(touched)

  for (let depth = 1; depth <= path.length; depth++) {
    const key = directoryPathKey(type, path.slice(0, depth))
    nextCollapsed.delete(key)
    nextTouched.add(key)
  }

  return { collapsed: nextCollapsed, touched: nextTouched }
}

/** Return expanded, user-touched paths in parent-first order for reloading. */
export function expandedWikiDirectoryPaths(
  type: string,
  collapsed: ReadonlySet<string>,
  touched: ReadonlySet<string>,
): string[][] {
  const prefix = `${type}:`
  return [...touched]
    .filter(key => key.startsWith(prefix) && !collapsed.has(key))
    .map(key => key.slice(prefix.length).split('/').filter(Boolean))
    .filter(path => path.length > 0)
    .sort((left, right) => left.length - right.length)
}
