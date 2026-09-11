import i18n from '@/i18n'

const categories: Record<string, string> = {
  outline_auth_failed: 'auth',
  outline_permission_denied: 'permission',
  outline_rate_limited: 'rate',
  outline_response_invalid: 'response',
  outline_format_unsupported: 'response',
  outline_request_failed: 'request',
  outline_scan_incomplete: 'request',
  outline_instance_changed: 'instance',
  outline_document_too_large: 'size',
  outline_cursor_invalid: 'cursor',
  outline_pending_upserts: 'pending',
  outline_deletion_unconfirmed: 'deletion',
  outline_deletion_capability_unavailable: 'deletion',
  outline_collection_inactive: 'scope',
  outline_collection_required: 'scope',
  outline_invalid_collection_id: 'scope',
  outline_redirect_rejected: 'request',
  datasource_sync_running: 'running',
  datasource_lease_lost: 'request',
}

export function localizeDatasourceError(message: unknown): string {
  if (typeof message !== 'string') return ''
  return message.replace(/outline_[a-z_]+|datasource_sync_running|datasource_lease_lost/g, (code) => {
    const category = categories[code]
    return category ? i18n.global.t(`datasource.outlineError.${category}`) : code
  })
}
