/**
 * Canonical audit / KB-activity action strings and their i18n message roots.
 *
 * Action values mirror `internal/types/audit_log.go` (dot-namespaced, e.g.
 * `kb.created`, `rbac.member_added`). Locale bags under each root must expose
 * those strings as **flat object keys** (not nested paths) because vue-i18n's
 * `t('root.kb.created')` would traverse nested objects, while the runtime
 * value from the API is the literal string `kb.created`.
 *
 * Keep this list in sync when adding new AuditAction constants in Go.
 */
export const AUDIT_ACTION_I18N_ROOTS = {
  tenantMember: 'tenantMember.audit.action',
  systemGlobal: 'system.globalSettings.audit.action',
  kbActivity: 'knowledgeEditor.activity.actions',
} as const

/** Workspace membership / invitation audit events (tenantMember drawer). */
export const TENANT_MEMBER_AUDIT_ACTIONS = [
  'rbac.member_added',
  'rbac.member_removed',
  'rbac.member_role_changed',
  'rbac.member_left',
  'rbac.access_denied',
  'rbac.invitation_sent',
  'rbac.invitation_accepted',
  'rbac.invitation_declined',
  'rbac.invitation_revoked',
  'rbac.invitation_expired',
] as const

/** Platform control-plane audit events (system settings → audit tab). */
export const SYSTEM_GLOBAL_AUDIT_ACTIONS = [
  'system.setting_changed',
  'system.admin_promoted',
  'system.api_key_created',
  'system.api_key_revoked',
  'system.admin_revoked',
  'system.user_password_reset',
  'system.queue_task_retried',
  'system.queue_task_deleted',
  'system.queue_task_run_now',
  'system.queue_task_cancelled',
  'system.queue_archived_purged',
] as const

/** Knowledge-base activity feed (KB settings → activity). */
export const KB_ACTIVITY_ACTIONS = [
  'kb.created',
  'kb.updated',
  'kb.deleted',
  'kb.duplicated',
  'kb.clone_started',
  'kb.clone_completed',
  'kb.clone_failed',
  'knowledge.created',
  'knowledge.updated',
  'knowledge.deleted',
  'knowledge.batch_deleted',
  'knowledge.reparse_started',
  'knowledge.parse_canceled',
  'knowledge.move_started',
  'knowledge.move_completed',
  'knowledge.move_failed',
  'tag.created',
  'tag.updated',
  'tag.deleted',
  'datasource.created',
  'datasource.updated',
  'datasource.deleted',
  'datasource.sync_started',
  'datasource.sync_completed',
  'datasource.sync_failed',
  'datasource.paused',
  'datasource.resumed',
  'kb.share_added',
  'kb.share_permission_changed',
  'kb.share_removed',
  'wiki.content_changed',
  'faq.import_started',
  'faq.import_completed',
  'faq.import_failed',
] as const

/** KB activity outcome column / filter values (`AuditOutcome` subset used in activity UI). */
export const KB_ACTIVITY_OUTCOMES = [
  'accepted',
  'success',
  'failed',
  'partial',
  'canceled',
  'denied',
] as const

/** Enum-like detail payload values shown in the activity task drawer. */
export const KB_ACTIVITY_DETAIL_VALUES = [
  'user',
  'manual',
  'schedule',
  'system',
  'pending',
  'completed',
  'partial',
  'canceled',
  'failed',
  'enqueue',
  'reuse_vectors',
  'reparse',
  'viewer',
  'editor',
  'admin',
  'append',
  'replace',
] as const

export const KB_ACTIVITY_I18N_ROOTS = {
  outcomes: 'knowledgeEditor.activity.outcomes',
  detailValues: 'knowledgeEditor.activity.detailValues',
} as const

/** Locale object paths whose children use flat keys containing dots. */
export const FLAT_KEY_BAG_PATHS = [
  AUDIT_ACTION_I18N_ROOTS.tenantMember,
  AUDIT_ACTION_I18N_ROOTS.systemGlobal,
  AUDIT_ACTION_I18N_ROOTS.kbActivity,
] as const

export type FlatKeyBagPath = (typeof FLAT_KEY_BAG_PATHS)[number]

export const REGISTERED_AUDIT_ACTION_ENTRIES: ReadonlyArray<{
  root: FlatKeyBagPath
  actions: readonly string[]
}> = [
  { root: AUDIT_ACTION_I18N_ROOTS.tenantMember, actions: TENANT_MEMBER_AUDIT_ACTIONS },
  { root: AUDIT_ACTION_I18N_ROOTS.systemGlobal, actions: SYSTEM_GLOBAL_AUDIT_ACTIONS },
  { root: AUDIT_ACTION_I18N_ROOTS.kbActivity, actions: KB_ACTIVITY_ACTIONS },
]
