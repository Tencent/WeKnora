import type { KnowledgeFolderNode, KnowledgeFolderTree } from '@/api/knowledge-base/index'

/**
 * Pure helpers behind the knowledge base folder sidebar.
 *
 * A folder selection is just a path, where the empty string is the knowledge
 * base root. The root is a real node of the tree — every top-level folder is
 * its child — so there is no separate "all documents" pseudo-row: what the root
 * row lists is decided by the same recursive switch as any other folder.
 */
export const ROOT_FOLDER_PATH = ''

/** Minimal shape of the browser File objects the upload flow deals with. */
export type UploadFileLike = {
  name: string
  webkitRelativePath?: string
}

/**
 * One rendered row of the flattened tree. The root row carries the knowledge
 * base totals; the component supplies its label.
 */
export type FolderRow = {
  kind: 'root' | 'folder'
  path: string
  name: string
  depth: number
  /** Documents stored directly in this folder. */
  documentCount: number
  /** Documents in this folder plus every descendant folder. */
  totalCount: number
  hasChildren: boolean
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
 * folder_path + file_name. Two sources are combined: the destination folder
 * chosen for the batch and the browser's webkitRelativePath, whose picked
 * directory becomes a folder under that destination.
 *
 * Returns undefined for a plain single-file upload into the root so that request
 * stays byte-identical to the pre-folder behaviour.
 */
export function buildUploadFileName(
  file: UploadFileLike,
  targetFolder: string,
): string | undefined {
  const segments: string[] = []
  if (targetFolder) segments.push(targetFolder)
  if (file.webkitRelativePath) {
    segments.push(...file.webkitRelativePath.split('/').filter(Boolean).slice(0, -1))
  }
  if (segments.length === 0) return undefined
  return `${segments.join('/')}/${file.name}`
}

/**
 * Breadcrumb entries for a folder path, from the top level down to the leaf.
 * The root has no crumbs of its own — callers render it as the leading item.
 */
export function folderBreadcrumbs(path: string): Array<{ name: string; path: string }> {
  if (!path) return []
  const segments = path.split('/')
  return segments.map((name, index) => ({
    name,
    path: segments.slice(0, index + 1).join('/'),
  }))
}

/**
 * The root, the path itself and every folder in between, used to keep the
 * selected folder reachable by expanding the rows above it.
 */
export function folderAncestorPaths(path: string): string[] {
  const paths = [ROOT_FOLDER_PATH]
  if (!path) return paths
  const segments = path.split('/')
  segments.forEach((_, index) => paths.push(segments.slice(0, index + 1).join('/')))
  return paths
}

/** Depth-first search for a folder path in the tree. The root always exists. */
export function folderPathExists(folders: KnowledgeFolderNode[], path: string): boolean {
  if (path === ROOT_FOLDER_PATH) return true
  return folders.some((node) => node.path === path || folderPathExists(node.children || [], path))
}

function flattenFolders(
  folders: KnowledgeFolderNode[],
  expanded: Set<string>,
  depth: number,
): FolderRow[] {
  const rows: FolderRow[] = []
  folders.forEach((node) => {
    const hasChildren = !!node.children?.length
    rows.push({
      kind: 'folder',
      path: node.path,
      name: node.name,
      depth,
      documentCount: node.document_count,
      totalCount: node.total_count,
      hasChildren,
    })
    if (hasChildren && expanded.has(node.path)) {
      rows.push(...flattenFolders(node.children!, expanded, depth + 1))
    }
  })
  return rows
}

/**
 * Flatten the tree into the visible rows: the root first, then the folders
 * nested beneath it, honouring the expanded set at every level.
 */
export function buildFolderRows(
  tree: KnowledgeFolderTree | null,
  expanded: Set<string>,
): FolderRow[] {
  const folders = tree?.folders ?? []
  const root: FolderRow = {
    kind: 'root',
    path: ROOT_FOLDER_PATH,
    name: '',
    depth: 0,
    documentCount: tree?.root_document_count ?? 0,
    totalCount: tree?.total_document_count ?? 0,
    hasChildren: folders.length > 0,
  }
  if (!root.hasChildren || !expanded.has(ROOT_FOLDER_PATH)) return [root]
  return [root, ...flattenFolders(folders, expanded, 1)]
}

/**
 * Direct sub-folders of a folder, i.e. what the document list shows as folder
 * entries while browsing it. The root's children are the top-level folders.
 */
export function childFolders(
  tree: KnowledgeFolderTree | null,
  path: string,
): KnowledgeFolderNode[] {
  const folders = tree?.folders ?? []
  if (path === ROOT_FOLDER_PATH) return folders
  const find = (nodes: KnowledgeFolderNode[]): KnowledgeFolderNode | undefined => {
    for (const node of nodes) {
      if (node.path === path) return node
      const found = find(node.children || [])
      if (found) return found
    }
    return undefined
  }
  return find(folders)?.children ?? []
}

/**
 * Whether the document list is filtering rather than browsing.
 *
 * This is the single input that decides how a folder selection is read, and it
 * comes from what the user is already doing instead of from a mode switch they
 * have to understand:
 *
 * - Browsing (no filter active) lists the selected folder's own contents — its
 *   sub-folders as entries plus the documents directly inside it, the way a file
 *   manager does. A document uploaded on its own therefore sits at the top level
 *   next to the folders, which is exactly where it belongs.
 * - Filtering searches the selected folder and everything below it, returning a
 *   flat result set, because a search that stopped at one level would hide
 *   matches for no good reason.
 */
export function isFilteringDocuments(filters: {
  keyword?: string
  tagIds?: string[]
  fileType?: string
  parseStatus?: string
  source?: string
  timeRange?: string[]
}): boolean {
  return !!(
    filters.keyword?.trim() ||
    filters.tagIds?.length ||
    filters.fileType ||
    filters.parseStatus ||
    filters.source ||
    filters.timeRange?.filter(Boolean).length
  )
}

/**
 * Canonicalize a user-typed folder path the same way the server does, so the
 * move dialog can preview and compare the destination it is about to send.
 */
export function normalizeFolderPath(path: string): string {
  return path
    .replace(/\\/g, '/')
    .split('/')
    .map((segment) => segment.trim().replace(/[. ]+$/, ''))
    .filter((segment) => segment && segment !== '.' && segment !== '..')
    .join('/')
}

/** Destination path for a new sub-folder of `parent`. */
export function joinFolderPath(parent: string, name: string): string {
  return normalizeFolderPath(parent ? `${parent}/${name}` : name)
}

/**
 * Whether a folder can be renamed/moved to `to`: a folder cannot land inside its
 * own subtree, which would make that subtree unreachable.
 */
export function canMoveFolderTo(from: string, to: string): boolean {
  const source = normalizeFolderPath(from)
  const target = normalizeFolderPath(to)
  if (!source || !target) return false
  if (target === source) return false
  return !target.startsWith(`${source}/`)
}
