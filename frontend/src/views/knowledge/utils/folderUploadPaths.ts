export function getUploadRelativePath(file: File): string {
  return (file as File & { webkitRelativePath?: string }).webkitRelativePath || ''
}

export function getUploadFileDisplayName(file: File): string {
  return getUploadRelativePath(file) || file.name
}

export function getUploadDirectorySegments(file: File): string[] {
  const relativePath = getUploadRelativePath(file)
  if (!relativePath) return []

  const parts = relativePath.split('/').filter(Boolean)
  if (parts.length < 2) return []
  return parts.slice(0, -1)
}

export function getLegacyFolderUploadFileName(file: File): string | undefined {
  const relativePath = getUploadRelativePath(file)
  if (!relativePath) return undefined

  const pathParts = relativePath.split('/')
  if (pathParts.length <= 2) return undefined
  const subPath = pathParts.slice(1, -1).join('/')
  return `${subPath}/${file.name}`
}

export function countUploadDirectories(files: File[]): number {
  const paths = new Set<string>()

  for (const file of files) {
    const segments = getUploadDirectorySegments(file)
    for (let depth = 1; depth <= segments.length; depth++) {
      paths.add(JSON.stringify(segments.slice(0, depth)))
    }
  }

  return paths.size
}

export type UploadDirectoryTreeRow =
  | {
      kind: 'folder'
      key: string
      name: string
      path: string
      depth: number
    }
  | {
      kind: 'file'
      key: string
      name: string
      path: string
      depth: number
      file: File
      fileIndex: number
    }

interface UploadDirectoryFolder {
  name: string
  path: string
  folders: Map<string, UploadDirectoryFolder>
  files: Array<Extract<UploadDirectoryTreeRow, { kind: 'file' }>>
}

export function buildUploadDirectoryTreeRows(files: File[]): UploadDirectoryTreeRow[] {
  const root: UploadDirectoryFolder = {
    name: '',
    path: '',
    folders: new Map(),
    files: [],
  }

  files.forEach((file, fileIndex) => {
    const pathSegments = getUploadRelativePath(file).split('/').filter(Boolean)
    if (pathSegments.length < 2) return

    let parent = root
    const folderPath: string[] = []
    for (const folderName of pathSegments.slice(0, -1)) {
      folderPath.push(folderName)
      let folder = parent.folders.get(folderName)
      if (!folder) {
        folder = {
          name: folderName,
          path: folderPath.join('/'),
          folders: new Map(),
          files: [],
        }
        parent.folders.set(folderName, folder)
      }
      parent = folder
    }

    const path = pathSegments.join('/')
    parent.files.push({
      kind: 'file',
      key: `file:${path}:${fileIndex}`,
      name: pathSegments.at(-1) || file.name,
      path,
      depth: 0,
      file,
      fileIndex,
    })
  })

  const rows: UploadDirectoryTreeRow[] = []
  const appendFolder = (folder: UploadDirectoryFolder, depth: number) => {
    rows.push({
      kind: 'folder',
      key: `folder:${folder.path}`,
      name: folder.name,
      path: folder.path,
      depth,
    })
    for (const childFolder of folder.folders.values()) {
      appendFolder(childFolder, depth + 1)
    }
    for (const file of folder.files) {
      rows.push({ ...file, depth: depth + 1 })
    }
  }

  for (const folder of root.folders.values()) {
    appendFolder(folder, 0)
  }
  return rows
}

export function getUtf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

function truncateUtf8ToBytes(value: string, maxBytes: number): string {
  const encoder = new TextEncoder()
  let bytes = 0
  let result = ''

  for (const character of value) {
    const characterBytes = encoder.encode(character).length
    if (bytes + characterBytes > maxBytes) break
    result += character
    bytes += characterBytes
  }

  return result
}

export function makeUploadFolderCopyName(
  name: string,
  copyIndex: number,
  maxBytes = 255,
): string {
  const suffix = ` (${Math.max(1, copyIndex)})`
  const baseByteLimit = Math.max(1, maxBytes - getUtf8ByteLength(suffix))
  const base = truncateUtf8ToBytes(name.trim(), baseByteLimit).trimEnd()
  return `${base}${suffix}`
}
