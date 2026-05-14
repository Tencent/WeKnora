import { get } from '@/utils/request'

// AuditAction mirrors internal/types/audit_log.go's namespaced action
// enum. The dot prefix (`rbac.`) is deliberate — future PRs will add
// `kb.*` / `agent.*` namespaces without a schema change, and the
// backend already treats this column as an opaque string. Keep this
// list in sync with types/audit_log.go.
export type AuditAction =
  | 'rbac.member_added'
  | 'rbac.member_removed'
  | 'rbac.member_role_changed'
  | 'rbac.member_left'
  | 'rbac.access_denied'
  | string // forward-compat: future namespaces shouldn't break the type

export type AuditOutcome = 'success' | 'denied'

// AuditLog mirrors internal/types/audit_log.go. `details` is the JSONB
// blob — for role changes it carries `{"old_role":..., "new_role":...}`,
// for access_denied it carries `{"required_role":...}`. We keep it as
// an opaque record so future detail shapes don't need a frontend
// breaking change.
export interface AuditLog {
  id: number
  tenant_id: number
  actor_user_id: string
  actor_role: string
  action: AuditAction
  target_type: string
  target_id: string
  target_user_id: string
  request_path: string
  request_method: string
  outcome: AuditOutcome
  details: Record<string, unknown> | string | null
  created_at: string
}

export interface ListAuditLogResponse {
  success: boolean
  data?: AuditLog[]
  next_cursor?: number
  message?: string
}

export interface ListAuditLogParams {
  // Cursor: rows with id < after_id, newest first. Pass the
  // previous response's `next_cursor`. Omit on first page.
  after_id?: number
  // Page size, 1–100. Server defaults to 50 if omitted.
  limit?: number
  // Optional filters; backend matches on equality.
  action?: AuditAction
  outcome?: AuditOutcome
  actor?: string
}

/**
 * List the per-tenant audit log. Cursor-paginated by descending id.
 * Backend: GET /api/v1/tenants/:id/audit-log (Admin+).
 *
 * The first call should pass no cursor. Each subsequent page should
 * pass `after_id = previousResponse.next_cursor` until next_cursor
 * comes back as 0 (no older rows).
 */
export async function listAuditLog(
  tenantId: number,
  params: ListAuditLogParams = {},
): Promise<ListAuditLogResponse> {
  const qs = new URLSearchParams()
  if (params.after_id) qs.append('after_id', String(params.after_id))
  if (params.limit) qs.append('limit', String(params.limit))
  if (params.action) qs.append('action', params.action)
  if (params.outcome) qs.append('outcome', params.outcome)
  if (params.actor) qs.append('actor', params.actor)
  const tail = qs.toString()
  const url = `/api/v1/tenants/${tenantId}/audit-log${tail ? '?' + tail : ''}`
  return (await get(url)) as unknown as ListAuditLogResponse
}
