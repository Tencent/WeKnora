import { MAX_FOLDER_DEPTH, MAX_FOLDER_PATH_LENGTH, MAX_FOLDER_SEGMENT_LENGTH } from './folderTree'

/** Return a lossless destination in this KB; null means the drop must not write. */
export function folderDropDestination(from: string, parent: string, paths: readonly string[]): string | null {
  if (!from || !paths.includes(from) || (parent && !paths.includes(parent))) return null
  if (parent === from || parent.startsWith(`${from}/`)) return null
  const name = from.split('/').at(-1)!
  const to = parent ? `${parent}/${name}` : name
  if (to === from || paths.includes(to)) return null
  const bytes = new TextEncoder()
  // Validate every descendant before allowing the drop, not just the new parent.
  for (const path of paths.filter(path => path === from || path.startsWith(`${from}/`))) {
    const target = to + path.slice(from.length)
    const segments = target.split('/')
    if (segments.length > MAX_FOLDER_DEPTH || bytes.encode(target).length > MAX_FOLDER_PATH_LENGTH
      || segments.some(segment => bytes.encode(segment).length > MAX_FOLDER_SEGMENT_LENGTH)) return null
  }
  return to
}
