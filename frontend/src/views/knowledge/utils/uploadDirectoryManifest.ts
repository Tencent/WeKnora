export interface UploadDirectoryManifestFile {
  file: File
  relativeFilePath: string
  relativeDirectoryPath: string
}

export interface UploadDirectoryManifest {
  directoryPaths: string[]
  files: UploadDirectoryManifestFile[]
}

const CONTROL_CHARACTER = /[\u0000-\u001f\u007f]/

function validatePath(path: string): string[] {
  if (!path || path.startsWith('/') || /^[A-Za-z]:/.test(path) || path.includes('\\') || CONTROL_CHARACTER.test(path)) {
    throw new Error(`Invalid upload path: ${JSON.stringify(path)}`)
  }
  const segments = path.split('/')
  if (segments.some((segment) => !segment || segment === '.' || segment === '..')) {
    throw new Error(`Invalid upload path: ${JSON.stringify(path)}`)
  }
  return segments
}

export function buildUploadDirectoryManifest(input: readonly File[]): UploadDirectoryManifest {
  const directoryPaths = new Set<string>()
  const relativeFilePaths = new Set<string>()
  const files: UploadDirectoryManifestFile[] = []

  for (const file of input) {
    const webkitRelativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath || ''
    const relativeFilePath = webkitRelativePath || file.name
    const segments = validatePath(relativeFilePath)
    if (segments[segments.length - 1] !== file.name) {
      throw new Error(`Invalid upload path: ${JSON.stringify(relativeFilePath)}`)
    }
    if (relativeFilePaths.has(relativeFilePath)) {
      throw new Error(`Conflicting duplicate upload file path: ${relativeFilePath}`)
    }
    if (directoryPaths.has(relativeFilePath)) {
      throw new Error(`Conflicting upload file and directory path: ${relativeFilePath}`)
    }
    relativeFilePaths.add(relativeFilePath)

    const directorySegments = webkitRelativePath ? segments.slice(0, -1) : []
    for (let depth = 1; depth <= directorySegments.length; depth++) {
      const directoryPath = directorySegments.slice(0, depth).join('/')
      if (relativeFilePaths.has(directoryPath)) {
        throw new Error(`Conflicting upload file and directory path: ${directoryPath}`)
      }
      directoryPaths.add(directoryPath)
    }
    files.push({
      file,
      relativeFilePath,
      relativeDirectoryPath: directorySegments.join('/'),
    })
  }

  return {
    directoryPaths: [...directoryPaths].sort((left, right) => {
      const depthDifference = left.split('/').length - right.split('/').length
      return depthDifference || (left < right ? -1 : left > right ? 1 : 0)
    }),
    files: files.sort((left, right) =>
      left.relativeFilePath < right.relativeFilePath
        ? -1
        : left.relativeFilePath > right.relativeFilePath ? 1 : 0,
    ),
  }
}
