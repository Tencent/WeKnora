import { resolveFilePreviewExt, isKnownPreviewableFile } from './filePreview'

export type ChatAttachmentLike = {
  id?: string
  file_name?: string
  file_type?: string
}

export function resolveAttachmentFileType(fileName?: string, fileType?: string): string {
  return resolveFilePreviewExt(fileName, fileType)
}

export function isPreviewableAttachment(attachment: ChatAttachmentLike | null | undefined): boolean {
  if (!attachment?.id) return false
  return isKnownPreviewableFile(attachment.file_name, attachment.file_type)
}
