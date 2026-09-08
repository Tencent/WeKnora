import { shouldRejectKnowledgeFileType } from '../../../utils/fileTypeVerification'

export const STORE_ONLY_FILE_EXTENSIONS = ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'yaml', 'yml', 'json', 'xml', 'pdf', 'txt', 'csv']

export type FolderFileMode = 'defer-processing' | 'store-only' | null

export const FOLDER_FINALIZE_BATCH_SIZE = 1000

// Parser engines arrive asynchronously. While their list is empty, known
// default document types (notably Markdown) must remain parseable rather than
// being permanently classified as store-only attachments.
export function resolveFolderFileMode(filename: string, supportedFileTypes?: Set<string> | string[]): FolderFileMode {
  const types = supportedFileTypes
    ? supportedFileTypes instanceof Set ? supportedFileTypes : new Set(supportedFileTypes)
    : undefined
  const extension = filename.includes('.') ? filename.substring(filename.lastIndexOf('.') + 1).toLowerCase() : ''
  if (types && types.size > 0) {
    if (types.has(extension)) return 'defer-processing'
  } else if (!shouldRejectKnowledgeFileType(filename)) {
    return 'defer-processing'
  }
  return STORE_ONLY_FILE_EXTENSIONS.includes(extension) ? 'store-only' : null
}

export function chunkFolderFinalizeKnowledgeIDs(knowledgeIDs: string[]): string[][] {
  const batches: string[][] = []
  for (let offset = 0; offset < knowledgeIDs.length; offset += FOLDER_FINALIZE_BATCH_SIZE) {
    batches.push(knowledgeIDs.slice(offset, offset + FOLDER_FINALIZE_BATCH_SIZE))
  }
  return batches
}
