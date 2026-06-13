import { kbFileTypeVerification } from '@/utils'

export const UPLOAD_VIDEO_EXTENSIONS = ['mp4', 'mov', 'avi', 'mkv', 'webm', 'wmv', 'flv']

export function getUploadFileExt(file: File): string {
  const dot = file.name.lastIndexOf('.')
  if (dot < 0) return ''
  return file.name.substring(dot + 1).toLowerCase()
}

export function getUploadFileKey(file: File): string {
  const path = (file as File & { webkitRelativePath?: string }).webkitRelativePath || ''
  return `${path || file.name}\0${file.size}`
}

export interface FilterUploadFilesOptions {
  supportedFileTypes?: Set<string> | string[]
  fromFolder?: boolean
  multiFile?: boolean
}

export interface FilterUploadFilesResult {
  validFiles: File[]
  skippedCount: number
  videoFilteredCount: number
  hiddenFileCount: number
}

export function filterUploadFiles(
  files: FileList | File[],
  options: FilterUploadFilesOptions = {},
): FilterUploadFilesResult {
  const list = Array.from(files)
  const dynamicTypes = options.supportedFileTypes
    ? options.supportedFileTypes instanceof Set
      ? options.supportedFileTypes
      : new Set(options.supportedFileTypes)
    : undefined

  const validFiles: File[] = []
  let skippedCount = 0
  let videoFilteredCount = 0
  let hiddenFileCount = 0
  const multiFile = options.multiFile ?? list.length > 1

  for (const file of list) {
    if (options.fromFolder) {
      const relativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name
      if (relativePath.split('/').some(part => part.startsWith('.'))) {
        hiddenFileCount++
        continue
      }
    }

    const fileExt = getUploadFileExt(file)
    if (UPLOAD_VIDEO_EXTENSIONS.includes(fileExt)) {
      videoFilteredCount++
      continue
    }

    if (kbFileTypeVerification(file, multiFile, dynamicTypes)) {
      skippedCount++
      continue
    }

    validFiles.push(file)
  }

  return { validFiles, skippedCount, videoFilteredCount, hiddenFileCount }
}
