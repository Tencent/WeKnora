import type {
  ChatAttachmentPreviewDrawerContext,
  ChatAttachmentPreviewTarget,
} from '@/composables/useChatAttachmentPreviewDrawer'

export type KnowledgeDocumentPreviewSource = {
  knowledgeId: string
  title: string
  fileType?: string
}

export function inferDocumentFileType(title: string, fileType?: string): string {
  const explicit = String(fileType || '').trim().toLowerCase()
  if (explicit) return explicit
  return String(title || '').match(/\.([^.]+)$/)?.[1]?.toLowerCase() || ''
}

export function buildKnowledgeDocumentPreviewTarget(
  source: KnowledgeDocumentPreviewSource,
): ChatAttachmentPreviewTarget | null {
  const knowledgeId = String(source.knowledgeId || '').trim()
  const fileName = String(source.title || '').trim()
  if (!knowledgeId || !fileName) return null
  return {
    knowledgeId,
    fileName,
    fileType: inferDocumentFileType(fileName, source.fileType),
  }
}

export function openKnowledgeDocumentPreview(
  drawer: ChatAttachmentPreviewDrawerContext | null,
  source: KnowledgeDocumentPreviewSource,
): boolean {
  const target = buildKnowledgeDocumentPreviewTarget(source)
  if (!drawer || !target) return false
  drawer.open(target)
  return true
}
