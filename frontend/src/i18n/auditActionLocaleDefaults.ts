import {
  AUDIT_ACTION_I18N_ROOTS,
  KB_ACTIVITY_DETAIL_VALUES,
  KB_ACTIVITY_I18N_ROOTS,
  KB_ACTIVITY_OUTCOMES,
  REGISTERED_AUDIT_ACTION_ENTRIES,
} from './auditActionRegistry.ts'

/**
 * Canonical English labels for audit / KB-activity runtime keys.
 * Used when locale files or git history lack a registered key during prune/regenerate.
 */
const TENANT_MEMBER_AUDIT_ACTION_LABELS_EN: Record<string, string> = {
  'rbac.member_added': 'Member added',
  'rbac.member_removed': 'Member removed',
  'rbac.member_role_changed': 'Role changed',
  'rbac.member_left': 'Member left',
  'rbac.access_denied': 'Access denied',
  'rbac.invitation_sent': 'Invitation sent',
  'rbac.invitation_accepted': 'Invitation accepted',
  'rbac.invitation_declined': 'Invitation declined',
  'rbac.invitation_revoked': 'Invitation revoked',
  'rbac.invitation_expired': 'Invitation expired',
}

const SYSTEM_GLOBAL_AUDIT_ACTION_LABELS_EN: Record<string, string> = {
  'system.setting_changed': 'System setting changed',
  'system.admin_promoted': 'System admin granted',
  'system.api_key_created': 'Platform API key created',
  'system.api_key_revoked': 'Platform API key revoked',
  'system.admin_revoked': 'System admin revoked',
  'system.user_password_reset': 'User password reset',
  'system.user_created': 'User created',
  'system.queue_task_retried': 'Failed task run again',
  'system.queue_task_deleted': 'Failed task record cleared',
  'system.queue_task_run_now': 'Queue task run now',
  'system.queue_task_cancelled': 'Queue task cancelled',
  'system.queue_archived_purged': 'All failed tasks cleared',
}

const KB_ACTIVITY_ACTION_LABELS_EN: Record<string, string> = {
  'kb.created': 'Knowledge base created',
  'kb.updated': 'Knowledge base updated',
  'kb.deleted': 'Knowledge base deleted',
  'kb.duplicated': 'Knowledge base duplicated',
  'kb.clone_started': 'Clone started',
  'kb.clone_completed': 'Clone completed',
  'kb.clone_failed': 'Clone failed',
  'knowledge.created': 'Knowledge added',
  'knowledge.updated': 'Knowledge updated',
  'knowledge.deleted': 'Knowledge deleted',
  'knowledge.batch_deleted': 'Knowledge batch deleted',
  'knowledge.reparse_started': 'Reparse started',
  'knowledge.parse_canceled': 'Parsing canceled',
  'knowledge.move_started': 'Knowledge move started',
  'knowledge.move_completed': 'Knowledge move completed',
  'knowledge.move_failed': 'Knowledge move failed',
  'tag.created': 'Tag created',
  'tag.updated': 'Tag updated',
  'tag.deleted': 'Tag deleted',
  'datasource.created': 'Data source created',
  'datasource.updated': 'Data source updated',
  'datasource.deleted': 'Data source deleted',
  'datasource.sync_started': 'Data source sync started',
  'datasource.sync_completed': 'Data source sync completed',
  'datasource.sync_failed': 'Data source sync failed',
  'datasource.paused': 'Data source paused',
  'datasource.resumed': 'Data source resumed',
  'kb.share_added': 'Share added',
  'kb.share_permission_changed': 'Share permission changed',
  'kb.share_removed': 'Share removed',
  'wiki.content_changed': 'Wiki content changed',
  'faq.import_started': 'FAQ import started',
  'faq.import_completed': 'FAQ import completed',
  'faq.import_failed': 'FAQ import failed',
}

const KB_ACTIVITY_OUTCOME_LABELS_EN: Record<string, string> = {
  accepted: 'Accepted',
  success: 'Success',
  failed: 'Failed',
  partial: 'Partial',
  canceled: 'Canceled',
  denied: 'Denied',
}

const KB_ACTIVITY_DETAIL_VALUE_LABELS_EN: Record<string, string> = {
  user: 'User initiated',
  manual: 'Manual',
  schedule: 'Scheduled',
  system: 'System',
  pending: 'Pending',
  completed: 'Completed',
  partial: 'Partially completed',
  canceled: 'Canceled',
  failed: 'Failed',
  enqueue: 'Task submission',
  reuse_vectors: 'Reuse vectors',
  reparse: 'Reparse',
  viewer: 'Read-only',
  editor: 'Can edit',
  admin: 'Manage',
  append: 'Append',
  replace: 'Replace',
}

function flattenBag(root: string, labels: Record<string, string>): Record<string, string> {
  const flat: Record<string, string> = {}
  for (const [key, value] of Object.entries(labels)) {
    flat[`${root}.${key}`] = value
  }
  return flat
}

export const AUDIT_ACTION_LOCALE_DEFAULTS: Readonly<Record<string, string>> = {
  ...flattenBag(AUDIT_ACTION_I18N_ROOTS.tenantMember, TENANT_MEMBER_AUDIT_ACTION_LABELS_EN),
  ...flattenBag(AUDIT_ACTION_I18N_ROOTS.systemGlobal, SYSTEM_GLOBAL_AUDIT_ACTION_LABELS_EN),
  ...flattenBag(AUDIT_ACTION_I18N_ROOTS.kbActivity, KB_ACTIVITY_ACTION_LABELS_EN),
  ...flattenBag(KB_ACTIVITY_I18N_ROOTS.outcomes, KB_ACTIVITY_OUTCOME_LABELS_EN),
  ...flattenBag(KB_ACTIVITY_I18N_ROOTS.detailValues, KB_ACTIVITY_DETAIL_VALUE_LABELS_EN),
}

export function getAuditActionLocaleDefault(path: string): string | undefined {
  return AUDIT_ACTION_LOCALE_DEFAULTS[path]
}

/** Verify registry lists and baked-in English defaults stay aligned. */
export function findAuditActionDefaultRegistryMismatches(): string[] {
  const failures: string[] = []

  for (const { root, actions } of REGISTERED_AUDIT_ACTION_ENTRIES) {
    for (const action of actions) {
      const path = `${root}.${action}`
      if (!AUDIT_ACTION_LOCALE_DEFAULTS[path]) failures.push(`missing default for ${path}`)
    }
  }
  for (const outcome of KB_ACTIVITY_OUTCOMES) {
    const path = `${KB_ACTIVITY_I18N_ROOTS.outcomes}.${outcome}`
    if (!AUDIT_ACTION_LOCALE_DEFAULTS[path]) failures.push(`missing default for ${path}`)
  }
  for (const value of KB_ACTIVITY_DETAIL_VALUES) {
    const path = `${KB_ACTIVITY_I18N_ROOTS.detailValues}.${value}`
    if (!AUDIT_ACTION_LOCALE_DEFAULTS[path]) failures.push(`missing default for ${path}`)
  }

  return failures
}
