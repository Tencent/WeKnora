export interface OperationDownload { path: string; fileName: string }
/** Operation results may describe a resource, but can never choose a request URL. */
export function operationDownload(operation: string, data: unknown): OperationDownload | undefined {
  if (!data || typeof data !== 'object' || Array.isArray(data)) return
  const d = data as Record<string, unknown>
  const id = (x: unknown): x is string => typeof x === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(x)
  if (operation === 'faq.export' && id(d.knowledge_base_id) && ['json', 'csv'].includes(String(d.format))) {
    return {path: `/api/v1/knowledge-bases/${d.knowledge_base_id}/faq/entries/export?format=${d.format}`, fileName: `faq_export.${d.format}`}
  }
  if (operation === 'attachment.download' && id(d.session_id) && id(d.attachment_id)) {
    const name = typeof d.file_name === 'string' ? d.file_name.split(/[\\/]/).pop() || 'attachment' : 'attachment'
    return {path: `/api/v1/sessions/${d.session_id}/attachments/${d.attachment_id}/preview`, fileName: name.replace(/[\x00-\x1f]/g, '').slice(0, 255)}
  }
}
