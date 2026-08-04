import type { KnowledgeFolderNode } from '@/api/knowledge-base/index'

/**
 * Pure helpers behind the knowledge base folder sidebar. The document page keeps
 * folder state as a single value with three meanings, so it is worth naming:
 * `null` browses every folder (the historical flat list), `''` is the knowledge
 * base root, and any other string is a folder path such as "docs/spec".
 */
export type FolderSelection = string | null

/** Minimal shape of the browser File objects the upload flow deals with. */
export type UploadFileLike = {
  name: string
  webkitRelativePath?: string
}

/** One rendered row of the flattened tree. */
export type FolderRow = {
  node: KnowledgeFolderNode
  depth: number
}

/**
 * A browser only sets webkitRelativePath when the file came from a directory
 * picker or a dropped directory, which is exactly what distinguishes a folder
 * upload from picking individual files.
 */
export function isFolderUpload(file: UploadFileLike): boolean {
  return !!file.webkitRelativePath
}

/**
 * Build the path-qualified `fileName` form field that the backend splits into
 * folder_path + file_name. Two sources are combined: the folder currently open
 * in the sidebar (so uploads land where the user is looking) and the browser's
 * webkitRelativePath, whose picked directory becomes the top-level tree node.
 *
 * Returns undefined for a plain single-file upload at the knowledge base root so
 * that request stays byte-identical to the pre-folder behaviour.
 */
export function buildUploadFileName(
  file: UploadFileLike,
  currentFolder: FolderSelection,
): string | undefined {
  const segments: string[] = []
  if (currentFolder) segments.push(currentFolder)
  if (file.webkitRelativePath) {
    segments.push(...file.webkitRelativePath.split('/').filter(Boolean).slice(0, -1))
  }
  if (segments.length === 0) return undefined
  return `${segments.join('/')}/${file.name}`
}

/** Breadcrumb entries for a folder path, from the top level down to the leaf. */
export function folderBreadcrumbs(path: FolderSelection): Array<{ name: string; path: string }> {
  if (!path) return []
  const segments = path.split('/')
  return segments.map((name, index) => ({
    name,
    path: segments.slice(0, index + 1).join('/'),
  }))
}

/**
 * The path itself plus every ancestor, used to keep the selected folder
 * reachable by expanding the rows above it.
 */
export function folderAncestorPaths(path: FolderSelection): string[] {
  if (!path) return []
  const segments = path.split('/')
  return segments.map((_, index) => segments.slice(0, index + 1).join('/'))
}

/** Depth-first search for a folder path in the tree. */
export function folderPathExists(folders: KnowledgeFolderNode[], path: string): boolean {
  return folders.some((node) => node.path === path || folderPathExists(node.children || [], path))
}

/** Flatten the tree into the visible rows, honouring the expanded folder set. */
export function flattenFolderRows(
  folders: KnowledgeFolderNode[],
  expanded: Set<string>,
  depth = 0,
): FolderRow[] {
  const rows: FolderRow[] = []
  folders.forEach((node) => {
    rows.push({ node, depth })
    if (node.children?.length && expanded.has(node.path)) {
      rows.push(...flattenFolderRows(node.children, expanded, depth + 1))
    }
  })
  return rows
}
