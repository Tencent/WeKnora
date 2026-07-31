import type {
  KnowledgeFolderEnsurePathInput,
  KnowledgeFolderEnsurePathResult,
} from '@/types/knowledgeFolder'

export const KNOWLEDGE_FOLDER_ENSURE_PATHS_LIMITS = {
  paths: 200,
  totalSegments: 2000,
  uniqueNodes: 1000,
} as const

export interface KnowledgeFolderUploadFile {
  file: File
  clientKey?: string
}

export interface KnowledgeFolderUploadPlan {
  files: KnowledgeFolderUploadFile[]
  paths: KnowledgeFolderEnsurePathInput[]
}

export interface KnowledgeFolderUploadTarget {
  file: File
  folder_id: string
}

export function getKnowledgeFolderSegments(relativePath: string): string[] {
  const pathParts = relativePath.split('/').filter(Boolean)
  const [firstFolder, ...nestedFolders] = pathParts.slice(0, -1)
  if (!firstFolder) return []
  return nestedFolders.length === 0 ? [firstFolder] : nestedFolders
}

export function buildKnowledgeFolderUploadPlan(
  files: readonly File[],
): KnowledgeFolderUploadPlan {
  const paths: KnowledgeFolderEnsurePathInput[] = []
  const clientKeyByPath = new Map<string, string>()
  const plannedFiles: KnowledgeFolderUploadFile[] = []

  for (const file of files) {
    const segments = getKnowledgeFolderSegments(file.webkitRelativePath || '')
    if (segments.length === 0) {
      plannedFiles.push({ file })
      continue
    }

    const pathKey = JSON.stringify(segments)
    let clientKey = clientKeyByPath.get(pathKey)
    if (!clientKey) {
      clientKey = `p${paths.length}`
      clientKeyByPath.set(pathKey, clientKey)
      paths.push({ client_key: clientKey, segments })
    }
    plannedFiles.push({ file, clientKey })
  }

  return { files: plannedFiles, paths }
}

export function mapKnowledgeFolderUploadTargets(
  files: readonly KnowledgeFolderUploadFile[],
  ensuredFolders: readonly KnowledgeFolderEnsurePathResult[],
  fallbackFolderId = '',
): KnowledgeFolderUploadTarget[] {
  const folderIdByClientKey = new Map(
    ensuredFolders.map((item) => [item.client_key, item.folder_id]),
  )

  return files.map((item) => {
    if (!item.clientKey) {
      return { file: item.file, folder_id: fallbackFolderId }
    }
    const folderId = folderIdByClientKey.get(item.clientKey)
    if (folderId === undefined) {
      throw new Error(`missing folder mapping for client key ${item.clientKey}`)
    }
    return { file: item.file, folder_id: folderId }
  })
}

function getUniqueNodeKeys(segments: readonly string[]): string[] {
  const keys: string[] = []
  for (let index = 1; index <= segments.length; index++) {
    keys.push(JSON.stringify(segments.slice(0, index)))
  }
  return keys
}

export function chunkKnowledgeFolderEnsurePaths(
  paths: readonly KnowledgeFolderEnsurePathInput[],
): KnowledgeFolderEnsurePathInput[][] {
  const batches: KnowledgeFolderEnsurePathInput[][] = []
  let currentBatch: KnowledgeFolderEnsurePathInput[] = []
  let currentSegmentCount = 0
  let currentNodeKeys = new Set<string>()

  for (const path of paths) {
    const nodeKeys = getUniqueNodeKeys(path.segments)
    const uniqueNodeCount = new Set(nodeKeys).size
    if (
      path.segments.length > KNOWLEDGE_FOLDER_ENSURE_PATHS_LIMITS.totalSegments ||
      uniqueNodeCount > KNOWLEDGE_FOLDER_ENSURE_PATHS_LIMITS.uniqueNodes
    ) {
      throw new RangeError(
        `ensure path ${path.client_key} cannot fit in one ensure-paths batch`,
      )
    }

    const additionalNodeCount = nodeKeys.reduce(
      (count, key) => count + (currentNodeKeys.has(key) ? 0 : 1),
      0,
    )
    const exceedsBatchLimit =
      currentBatch.length >= KNOWLEDGE_FOLDER_ENSURE_PATHS_LIMITS.paths ||
      currentSegmentCount + path.segments.length >
        KNOWLEDGE_FOLDER_ENSURE_PATHS_LIMITS.totalSegments ||
      currentNodeKeys.size + additionalNodeCount >
        KNOWLEDGE_FOLDER_ENSURE_PATHS_LIMITS.uniqueNodes

    if (exceedsBatchLimit && currentBatch.length > 0) {
      batches.push(currentBatch)
      currentBatch = []
      currentSegmentCount = 0
      currentNodeKeys = new Set<string>()
    }

    currentBatch.push(path)
    currentSegmentCount += path.segments.length
    for (const key of nodeKeys) currentNodeKeys.add(key)
  }

  if (currentBatch.length > 0) batches.push(currentBatch)
  return batches
}
