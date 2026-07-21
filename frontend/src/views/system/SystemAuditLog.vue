<template>
  <div class="system-audit-log">
    <header class="section-header audit-page-header">
      <div class="audit-page-header__title">
        <h2>{{ t('system.globalSettings.audit.tabLabel') }}</h2>
        <p class="section-description">{{ t('system.globalSettings.audit.description') }}</p>
      </div>
      <button
        type="button"
        class="rq-refresh"
        :disabled="auditLoading"
        :title="t('system.globalSettings.audit.refresh')"
        :aria-label="t('system.globalSettings.audit.refresh')"
        @click="reloadAuditLog"
      >
        <t-icon
          :name="auditLoading ? 'loading' : 'refresh'"
          :class="{ 'rq-refresh-spin': auditLoading }"
        />
      </button>
    </header>

    <div class="audit-page-body">
      <div v-if="auditError" class="audit-page-branch audit-page-branch--error">
        <t-alert theme="error" :message="auditError">
          <template #operation>
            <t-button size="small" @click="reloadAuditLog">
              {{ t('system.globalSettings.audit.retry') }}
            </t-button>
          </template>
        </t-alert>
      </div>

      <div
        v-else-if="!auditLoading && auditEntries.length === 0"
        class="audit-page-branch audit-page-branch--empty"
      >
        <t-empty :description="t('system.globalSettings.audit.empty')" />
      </div>

      <div v-else class="audit-scroll-area narrow-scrollbar audit-page-branch" ref="auditScrollRoot">
        <div class="data-table-shell audit-table-shell">
          <t-table
            row-key="id"
            :data="auditEntries"
            :columns="auditColumns"
            size="medium"
            hover
            @row-click="openAuditDetail"
          >
            <template #created_at="{ row }">
              <div class="audit-time">
                <span class="audit-time-date">{{ formatAuditDatePart(row.created_at) }}</span>
                <span class="audit-time-clock">{{ formatAuditTimePart(row.created_at) }}</span>
              </div>
            </template>
            <template #actor="{ row }">
              <div class="audit-actor">
                <span class="audit-actor-name">
                  {{ row.actor_user_id ? auditActorLabel(row.actor_user_id) :
                    t('system.globalSettings.audit.systemActor') }}
                </span>
                <span v-if="row.actor_role" class="audit-actor-role">
                  {{ auditActorRoleLabel(row.actor_role) }}
                </span>
              </div>
            </template>
            <template #action="{ row }">
              <t-tag :theme="auditActionTheme(row.action)" size="small" variant="light-outline">
                {{ formatAuditAction(row.action) }}
              </t-tag>
            </template>
            <template #target="{ row }">
              <div class="audit-target">
                <span v-if="auditTargetKey(row)" class="audit-target-key">{{ auditTargetKey(row) }}</span>
                <span v-if="auditTargetDiff(row)" class="audit-target-diff">{{ auditTargetDiff(row) }}</span>
                <span v-else-if="!auditTargetKey(row)" class="audit-target-empty">—</span>
              </div>
            </template>
            <template #outcome="{ row }">
              <t-tag :theme="auditOutcomeTheme(row.outcome)" size="small" variant="light">
                {{ t('system.globalSettings.audit.outcome.' + row.outcome) }}
              </t-tag>
            </template>
          </t-table>
        </div>

        <div ref="auditLoadSentinelEl" class="audit-load-sentinel" aria-hidden="true" />

        <div v-if="auditLoading && auditEntries.length > 0" class="audit-loading-more">
          <t-loading size="small" />
          <span>{{ t('system.globalSettings.audit.loading') }}</span>
        </div>

        <p v-if="!auditHasMore && auditEntries.length > 0 && !auditLoading" class="audit-end-hint">
          {{ t('system.globalSettings.audit.end') }}
        </p>
      </div>
    </div>

    <SettingDrawer
      v-model:visible="auditDetailVisible"
      class="audit-detail-drawer"
      :title="auditDetailTitle"
      :description="auditDetailDescription"
      icon="file-paste"
      width="640px"
      :min-width="480"
      :max-width="960"
      storage-key="setting-drawer:width:system-audit-detail"
      hide-footer
    >
      <template v-if="selectedAuditEntry">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">
            {{ t('system.globalSettings.audit.drawer.sectionSummary') }}
          </h4>
          <dl class="audit-detail-fields">
            <div
              v-for="field in auditSummaryFields(selectedAuditEntry)"
              :key="field.key"
              class="audit-detail-field"
            >
              <dt>{{ field.label }}</dt>
              <dd :title="field.value">{{ field.value }}</dd>
            </div>
          </dl>
        </section>

        <section v-if="auditIdentifierFields(selectedAuditEntry).length > 0" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">
            {{ t('system.globalSettings.audit.drawer.sectionIdentifiers') }}
          </h4>
          <dl class="audit-detail-fields">
            <div
              v-for="field in auditIdentifierFields(selectedAuditEntry)"
              :key="field.key"
              class="audit-detail-field"
            >
              <dt>{{ field.label }}</dt>
              <dd class="mono" :title="field.value">{{ field.value }}</dd>
            </div>
          </dl>
        </section>

        <section v-if="auditRequestFields(selectedAuditEntry).length > 0" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">
            {{ t('system.globalSettings.audit.drawer.sectionRequest') }}
          </h4>
          <dl class="audit-detail-fields">
            <div
              v-for="field in auditRequestFields(selectedAuditEntry)"
              :key="field.key"
              class="audit-detail-field"
            >
              <dt>{{ field.label }}</dt>
              <dd class="mono" :title="field.value">{{ field.value }}</dd>
            </div>
          </dl>
        </section>

        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">
            {{ t('system.globalSettings.audit.expanded.details') }}
          </h4>
          <pre class="audit-detail-json mono">{{ auditDetailsJSON(selectedAuditEntry) }}</pre>
        </section>
      </template>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  listSystemAuditLog,
  type AuditAction,
  type AuditLog,
  type AuditOutcome,
} from '@/api/system'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { useAuthStore } from '@/stores/auth'

interface AuditDetailField {
  key: string
  label: string
  value: string
}

const authStore = useAuthStore()
const { t, tm, te, locale } = useI18n()

const auditEntries = ref<AuditLog[]>([])
const auditLoading = ref(false)
const auditError = ref('')
const auditCursor = ref<number>(0)
const auditHasMore = ref(true)
const AUDIT_PAGE_SIZE = 50

const auditScrollRoot = ref<HTMLElement | null>(null)
const auditLoadSentinelEl = ref<HTMLElement | null>(null)
let auditScrollObserver: IntersectionObserver | null = null

const auditDetailVisible = ref(false)
const selectedAuditEntry = ref<AuditLog | null>(null)

const auditColumns = computed(() => [
  { colKey: 'created_at', title: t('system.globalSettings.audit.columns.time'), width: 120 },
  { colKey: 'actor', title: t('system.globalSettings.audit.columns.actor'), width: 180 },
  { colKey: 'action', title: t('system.globalSettings.audit.columns.action'), width: 150 },
  {
    colKey: 'target',
    title: t('system.globalSettings.audit.columns.target'),
    minWidth: 240,
  },
  { colKey: 'outcome', title: t('system.globalSettings.audit.columns.outcome'), width: 80, align: 'center' as const },
])

function formatAuditDatePart(s: string | undefined): string {
  if (!s) return '-'
  try {
    return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).format(new Date(s))
  } catch {
    return s
  }
}

function formatAuditTimePart(s: string | undefined): string {
  if (!s) return ''
  try {
    return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }).format(new Date(s))
  } catch {
    return ''
  }
}

function auditActionTheme(
  action: AuditAction,
): 'success' | 'warning' | 'danger' | 'primary' | 'default' {
  switch (action) {
    case 'system.admin_promoted':
      return 'success'
    case 'system.admin_revoked':
    case 'system.setting_changed':
    case 'system.queue_task_retried':
    case 'system.queue_task_run_now':
      return 'warning'
    case 'system.user_password_reset':
    case 'system.queue_task_deleted':
    case 'system.queue_task_cancelled':
      return 'danger'
    case 'rbac.access_denied':
      return 'danger'
    default:
      return 'default'
  }
}

function auditOutcomeTheme(o: AuditOutcome): 'success' | 'danger' | 'default' {
  if (o === 'denied') return 'danger'
  if (o === 'success') return 'success'
  return 'default'
}

function formatAuditAction(action: AuditAction): string {
  const bag = tm('system.globalSettings.audit.action') as unknown
  if (bag !== null && typeof bag === 'object' && typeof (bag as Record<string, string>)[action] === 'string') {
    return (bag as Record<string, string>)[action]
  }
  return action
}

function auditActorLabel(userId: string): string {
  const me = authStore.user
  if (me && me.id === userId) {
    return me.username?.trim() || me.email?.trim() || userId.slice(0, 8)
  }
  return userId.slice(0, 8)
}

function auditActorRoleLabel(role: string): string {
  const key = `system.globalSettings.audit.actorRole.${role}`
  if (te(key)) return t(key)
  return role
}

function auditDetailsObject(row: AuditLog): Record<string, unknown> | null {
  if (row.details && typeof row.details === 'object') {
    return row.details as Record<string, unknown>
  }
  return null
}

function auditTargetKey(row: AuditLog): string {
  const details = auditDetailsObject(row)
  if (row.action === 'system.setting_changed') {
    if (row.target_type === 'tenant_storage_quota') {
      return t('system.globalSettings.audit.target.bulkQuota')
    }
    if (details && typeof details.key === 'string' && details.key) return details.key
    return row.target_id || row.target_type || ''
  }
  if (
    row.action === 'system.admin_promoted'
    || row.action === 'system.admin_revoked'
    || row.action === 'system.user_password_reset'
  ) {
    if (!details) return row.target_user_id ? row.target_user_id.slice(0, 8) : ''
    const name = typeof details.target_username === 'string' ? details.target_username : ''
    const mail = typeof details.target_email === 'string' ? details.target_email : ''
    if (name && mail) return `${name} (${mail})`
    return name || mail || (row.target_user_id ? row.target_user_id.slice(0, 8) : '')
  }
  if (
    row.action === 'system.queue_task_retried'
    || row.action === 'system.queue_task_run_now'
    || row.action === 'system.queue_task_cancelled'
    || row.action === 'system.queue_task_deleted'
  ) {
    const queue = details && typeof details.queue === 'string' ? details.queue : ''
    const taskID = details && typeof details.task_id === 'string' ? details.task_id : row.target_id
    return queue && taskID ? `${queue}:${taskID}` : taskID || queue
  }
  if (row.target_user_id) return row.target_user_id.slice(0, 8)
  if (row.target_id) {
    return row.target_type ? `${row.target_type}:${row.target_id}` : row.target_id
  }
  return ''
}

function auditTargetDiff(row: AuditLog): string {
  const details = auditDetailsObject(row)
  if (!details) return ''
  if (row.action === 'system.setting_changed') {
    if (row.target_type === 'tenant_storage_quota') {
      const affected = typeof details.affected === 'number' ? details.affected : null
      const gb = typeof details.quota_gb === 'number' ? details.quota_gb : null
      if (affected !== null && gb !== null) {
        return t('system.globalSettings.audit.target.bulkQuotaDiff', {
          count: String(affected),
          gb: String(gb),
        })
      }
      return ''
    }
    return formatSettingDiff(details)
  }
  if (row.action === 'system.admin_promoted' && typeof details.idempotent === 'boolean') {
    if (details.idempotent === true) {
      return t('system.globalSettings.audit.target.promoteIdempotent')
    }
    return ''
  }
  if (row.action === 'system.admin_revoked' && typeof details.changed === 'boolean') {
    if (details.changed === false) {
      return t('system.globalSettings.audit.target.revokeNoop')
    }
    return ''
  }
  if (row.action === 'rbac.access_denied' && typeof details.required_role === 'string') {
    return t('system.globalSettings.audit.target.requiredRole', { role: details.required_role })
  }
  return ''
}

const SETTING_DIFF_MAX_LEN = 80
function formatSettingDiff(details: Record<string, unknown>): string {
  const fmt = (v: unknown): string => {
    if (v === null || v === undefined) {
      return t('system.globalSettings.audit.target.valueNull')
    }
    if (typeof v === 'string') return v
    if (typeof v === 'number' || typeof v === 'boolean') return String(v)
    try {
      return JSON.stringify(v)
    } catch {
      return String(v)
    }
  }
  const truncate = (s: string): string =>
    s.length > SETTING_DIFF_MAX_LEN ? s.slice(0, SETTING_DIFF_MAX_LEN - 1) + '…' : s
  const oldStr = truncate(fmt(details.old_value))
  const newStr = truncate(fmt(details.new_value))
  if (oldStr === newStr) return ''
  return `${oldStr} → ${newStr}`
}

function formatAuditDateTime(s: string | undefined): string {
  if (!s) return '—'
  const date = formatAuditDatePart(s)
  const time = formatAuditTimePart(s)
  return time ? `${date} ${time}` : date
}

function auditActorDisplay(row: AuditLog): string {
  if (!row.actor_user_id) {
    return t('system.globalSettings.audit.systemActor')
  }
  const name = auditActorLabel(row.actor_user_id)
  return row.actor_role ? `${name} (${auditActorRoleLabel(row.actor_role)})` : name
}

function auditSummaryFields(row: AuditLog): AuditDetailField[] {
  const fields: AuditDetailField[] = [
    {
      key: 'time',
      label: t('system.globalSettings.audit.columns.time'),
      value: formatAuditDateTime(row.created_at),
    },
    {
      key: 'actor',
      label: t('system.globalSettings.audit.columns.actor'),
      value: auditActorDisplay(row),
    },
    {
      key: 'action',
      label: t('system.globalSettings.audit.columns.action'),
      value: formatAuditAction(row.action),
    },
    {
      key: 'outcome',
      label: t('system.globalSettings.audit.columns.outcome'),
      value: t('system.globalSettings.audit.outcome.' + row.outcome),
    },
  ]

  const targetKey = auditTargetKey(row)
  if (targetKey) {
    fields.push({
      key: 'target',
      label: t('system.globalSettings.audit.columns.target'),
      value: targetKey,
    })
  }

  const diff = auditTargetDiff(row)
  if (diff) {
    fields.push({
      key: 'targetDiff',
      label: t('system.globalSettings.audit.drawer.targetChange'),
      value: diff,
    })
  }

  return fields
}

function auditIdentifierFields(row: AuditLog): AuditDetailField[] {
  const fields: AuditDetailField[] = []
  if (row.actor_user_id) {
    fields.push({
      key: 'actorId',
      label: t('system.globalSettings.audit.expanded.actorId'),
      value: row.actor_user_id,
    })
  }
  if (row.target_user_id) {
    fields.push({
      key: 'targetUserId',
      label: t('system.globalSettings.audit.expanded.targetUserId'),
      value: row.target_user_id,
    })
  }
  if (row.target_type) {
    fields.push({
      key: 'targetType',
      label: t('system.globalSettings.audit.expanded.targetType'),
      value: row.target_type,
    })
  }
  if (row.target_id) {
    fields.push({
      key: 'targetId',
      label: t('system.globalSettings.audit.expanded.targetId'),
      value: row.target_id,
    })
  }
  return fields
}

function auditRequestFields(row: AuditLog): AuditDetailField[] {
  const fields: AuditDetailField[] = []
  if (row.request_method) {
    fields.push({
      key: 'method',
      label: t('system.globalSettings.audit.drawer.requestMethod'),
      value: row.request_method,
    })
  }
  if (row.request_path) {
    fields.push({
      key: 'path',
      label: t('system.globalSettings.audit.columns.path'),
      value: row.request_path,
    })
  }
  return fields
}

const auditDetailTitle = computed(() =>
  selectedAuditEntry.value ? formatAuditAction(selectedAuditEntry.value.action) : '',
)

const auditDetailDescription = computed(() =>
  selectedAuditEntry.value ? formatAuditDateTime(selectedAuditEntry.value.created_at) : '',
)

function openAuditDetail(context: { row: AuditLog }) {
  selectedAuditEntry.value = context.row
  auditDetailVisible.value = true
}

function auditDetailsJSON(row: AuditLog): string {
  if (row.details === null || row.details === undefined) return '{}'
  if (typeof row.details === 'string') return row.details
  try {
    return JSON.stringify(row.details, null, 2)
  } catch {
    return String(row.details)
  }
}

async function loadAuditLog(reset: boolean) {
  if (auditLoading.value) return
  if (!reset && !auditHasMore.value) return

  auditLoading.value = true
  auditError.value = ''
  try {
    const resp = await listSystemAuditLog({
      after_id: reset ? undefined : auditCursor.value || undefined,
      limit: AUDIT_PAGE_SIZE,
    })
    if (resp.success) {
      const rows = resp.data || []
      auditEntries.value = reset ? rows : [...auditEntries.value, ...rows]
      auditCursor.value = resp.next_cursor || 0
      auditHasMore.value = !!resp.next_cursor && rows.length > 0
    } else {
      auditError.value = resp.message || t('system.globalSettings.audit.errors.generic')
    }
  } catch (err: any) {
    const status = err?.status
    if (status === 403) {
      auditError.value = t('system.globalSettings.audit.forbidden')
    } else {
      auditError.value = err?.message || t('system.globalSettings.audit.errors.generic')
    }
  } finally {
    auditLoading.value = false
  }
}

function detachAuditInfiniteScroll() {
  auditScrollObserver?.disconnect()
  auditScrollObserver = null
}

function attachAuditInfiniteScroll() {
  detachAuditInfiniteScroll()
  const root = auditScrollRoot.value
  const sentinel = auditLoadSentinelEl.value
  if (!root || !sentinel) return

  auditScrollObserver = new IntersectionObserver(
    (entries) => {
      const hitBottom = entries.some((e) => e.isIntersecting)
      if (!hitBottom || !auditHasMore.value || auditLoading.value) return
      void loadAuditLog(false)
    },
    { root, rootMargin: '100px 0px', threshold: 0 },
  )
  auditScrollObserver.observe(sentinel)
}

function reloadAuditLog() {
  auditCursor.value = 0
  auditHasMore.value = true
  void loadAuditLog(true)
}

watch(
  () => [auditEntries.value.length, auditError.value],
  async () => {
    await nextTick()
    if (auditError.value) {
      detachAuditInfiniteScroll()
      return
    }
    attachAuditInfiniteScroll()
  },
  { flush: 'post' },
)

onMounted(async () => {
  await loadAuditLog(true)
  await nextTick()
  attachAuditInfiniteScroll()
})

onUnmounted(() => {
  detachAuditInfiniteScroll()
})
</script>

<style lang="less" scoped>
.system-audit-log {
  width: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.section-header {
  margin-bottom: 20px;
}

.audit-page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.audit-page-header h2 {
  margin: 0 0 8px;
  font-size: 20px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.section-description {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 14px;
  line-height: 1.5;
}

.rq-refresh {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: color 0.2s cubic-bezier(0.16, 1, 0.3, 1), background 0.2s cubic-bezier(0.16, 1, 0.3, 1);

  :deep(.t-icon) {
    font-size: 12px;
  }

  &:hover:not(:disabled) {
    color: var(--td-brand-color);
    background: var(--td-bg-color-secondarycontainer);
  }

  &:active:not(:disabled) {
    background: var(--td-bg-color-secondarycontainer);
  }

  &:disabled {
    cursor: default;
    opacity: 0.7;
  }
}

.rq-refresh-spin {
  animation: rq-refresh-rotate 0.8s linear infinite;
}

@keyframes rq-refresh-rotate {
  from {
    transform: rotate(0deg);
  }

  to {
    transform: rotate(360deg);
  }
}

.audit-page-body {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.audit-page-branch {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.audit-page-branch--error {
  justify-content: flex-start;
}

.audit-page-branch--empty {
  justify-content: center;
  align-items: center;
  min-height: 280px;
}

.audit-scroll-area {
  flex: 1 1 auto;
  min-height: 0;
  max-height: calc(100vh - 260px);
  overflow-x: hidden;
  overflow-y: auto;
}

.audit-load-sentinel {
  height: 1px;
  width: 100%;
  pointer-events: none;
}

.audit-loading-more {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 12px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.audit-end-hint {
  text-align: center;
  font-size: 12px;
  color: var(--td-text-color-disabled);
  padding: 8px 0 14px;
  margin: 0;
}

.audit-time {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.3;

  .audit-time-date {
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }

  .audit-time-clock {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    font-variant-numeric: tabular-nums;
  }
}

.audit-actor {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.3;
  min-width: 0;

  .audit-actor-name {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .audit-actor-role {
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }
}

.audit-target {
  display: flex;
  flex-direction: column;
  gap: 4px;
  line-height: 1.35;
  min-width: 0;
  padding: 2px 0;

  .audit-target-key {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    word-break: break-all;
    font-family: var(--td-font-family-mono, monospace);
  }

  .audit-target-diff {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    font-family: var(--td-font-family-mono, monospace);
    word-break: break-all;
    line-height: 1.4;
  }

  .audit-target-empty {
    color: var(--td-text-color-placeholder);
  }
}

.audit-detail-fields {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 0;
}

.audit-detail-field {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr);
  gap: 12px;
  align-items: baseline;
  margin: 0;

  dt {
    margin: 0;
    color: var(--td-text-color-placeholder);
    font-size: 12px;
    line-height: 1.45;
    white-space: nowrap;
  }

  dd {
    margin: 0;
    color: var(--td-text-color-primary);
    font-size: 13px;
    line-height: 1.55;
    word-break: break-all;
  }
}

.audit-detail-json {
  margin: 0;
  padding: 12px 14px;
  font-size: 12px;
  line-height: 1.55;
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: min(420px, 50vh);
  overflow: auto;
}

.mono {
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Consolas, monospace);
}

.data-table-shell {
  overflow-x: auto;
  border-radius: 10px;
  border: 1px solid var(--td-component-stroke);
  background-color: var(--td-bg-color-container);

  &:deep(thead th) {
    font-weight: 600;
    font-size: 13px;
    background-color: var(--td-bg-color-secondarycontainer) !important;
  }

  &:deep(.t-table td),
  &:deep(.t-table th) {
    padding-top: 14px;
    padding-bottom: 14px;
    vertical-align: middle;
  }
}

.audit-table-shell {
  &:deep(thead th) {
    position: sticky;
    top: 0;
    z-index: 2;
    box-shadow: inset 0 -1px 0 var(--td-component-stroke);
  }

  &:deep(.t-table tbody tr) {
    cursor: pointer;
  }

  &:deep(.t-table tbody tr:hover > td) {
    background-color: var(--td-bg-color-container-hover);
  }
}
</style>
