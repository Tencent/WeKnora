export type WikiSourceDocumentPreviewLike = {
  knowledge_id?: unknown
  knowledge_base_id?: unknown
  preview_enabled?: unknown
}

/**
 * Wiki document identity is not a preview capability on its own. The server
 * must explicitly authorize preview for the current tool result.
 */
export function isWikiSourceDocumentPreviewEnabled(
  source: WikiSourceDocumentPreviewLike | null | undefined,
): boolean {
  return (
    source?.preview_enabled === true &&
    Boolean(String(source?.knowledge_id || '').trim()) &&
    Boolean(String(source?.knowledge_base_id || '').trim())
  )
}
