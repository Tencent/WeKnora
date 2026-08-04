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
 * The count a row should display, which must match what selecting it puts in
 * the document list — otherwise the sidebar and the list disagree.
 */
export function folderRowCount(row: FolderRow, recursive: boolean): number {
  return recursive ? row.totalCount : row.documentCount
}

/**
 * i18n key for the root row's label. The root is the only row whose contents
 * change meaning with the scope: recursively it is the whole knowledge base,
 * directly it is exactly the documents that are not inside any folder — which
 * single-file uploads are, and which would otherwise have no visible home in a
 * tree that can only show folders. Naming the row after what it currently lists
 * keeps one mechanism instead of adding a second pseudo-folder for them.
 */
export function rootRowLabelKey(recursive: boolean): string {
  return recursive
    ? 'knowledgeBase.folderTree.rootRow'
    : 'knowledgeBase.folderTree.rootRowDirect'
}

/** i18n key for the root row's tooltip, following the same rule as its label. */
export function rootRowTitleKey(recursive: boolean): string {
  return recursive
    ? 'knowledgeBase.folderTree.rootRowTip'
    : 'knowledgeBase.folderTree.rootRowDirectTip'
}
