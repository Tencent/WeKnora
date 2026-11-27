import type { AttachmentFile } from '@/components/AttachmentUpload.vue'

interface DraftRestorer {
  restoreDraft: (text: string, imageFiles: File[], attachmentFiles: AttachmentFile[]) => void
}

// A new-chat attachment is uploaded only after navigation creates the session.
// If that deferred upload is rejected, put the consumed draft back into the
// mounted composer and make every cleaned-up attachment visibly removable.
export function restoreRejectedFirstMessage(
  inputField: DraftRestorer | null | undefined,
  text: string,
  imageFiles: File[],
  attachmentFiles: AttachmentFile[],
  errorMessage: string,
) {
  for (const attachment of attachmentFiles) {
    delete attachment.documentId
    attachment.status = 'failed'
    attachment.progress = 0
    attachment.error = errorMessage
  }
  inputField?.restoreDraft(text, imageFiles, attachmentFiles)
}
