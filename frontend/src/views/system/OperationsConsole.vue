<template>
  <div class="operations-console">
    <header class="section-header ops-header">
      <div>
        <h2>{{ t('operations.title') }}</h2>
        <p class="section-description">{{ t('operations.description') }}</p>
      </div>
      <div class="ops-header-actions">
        <span v-if="updatedAt" class="ops-updated-at">
          <t-icon name="time" />{{ t('operations.updatedAt', { value: updatedAt }) }}
        </span>
        <button
          type="button"
          class="ops-icon-button"
          :disabled="loading"
          :title="t('operations.refresh')"
          :aria-label="t('operations.refresh')"
          @click="reload(true)"
        >
          <t-icon :name="loading ? 'loading' : 'refresh'" :class="{ 'ops-spin': loading }" />
        </button>
      </div>
    </header>

    <div v-if="loading && !status" class="ops-loading" aria-live="polite">
      <t-skeleton v-for="item in 4" :key="item" animation="gradient" :row-col="[{ width: '32%', height: '16px' }, { width: '72%', height: '28px' }]" />
    </div>

    <div v-else-if="error && !status" class="ops-error" role="alert">
      <t-alert theme="error" :message="error">
        <template #operation><t-button size="small" @click="reload(true)">{{ t('operations.retry') }}</t-button></template>
      </t-alert>
    </div>

    <template v-else-if="status">
      <t-alert v-if="error" theme="warning" :message="error" class="ops-stale-alert">
        <template #operation><t-button size="small" @click="reload(true)">{{ t('operations.retry') }}</t-button></template>
      </t-alert>

      <section class="ops-overview" :class="`ops-overview--${overallTone}`">
        <div class="ops-overview-state">
          <t-icon :name="overallTone === 'success' ? 'check-circle' : 'error-circle'" size="28px" />
          <div>
            <strong>{{ overallLabel }}</strong>
            <p>{{ overallDescription }}</p>
          </div>
        </div>
        <dl class="ops-overview-metrics">
          <div><dt>{{ t('operations.overview.uptime') }}</dt><dd>{{ formatUptime(status.uptime_seconds) }}</dd></div>
          <div><dt>{{ t('operations.overview.database') }}</dt><dd>{{ databaseDriverLabel }}</dd></div>
          <div><dt>{{ t('operations.overview.migration') }}</dt><dd>{{ migrationLabel }}</dd></div>
        </dl>
      </section>

      <section class="ops-section">
        <div class="ops-section-heading">
          <div><h3>{{ t('operations.dependencies.title') }}</h3><p>{{ t('operations.dependencies.description') }}</p></div>
        </div>
        <div class="ops-dependency-grid">
          <div v-for="dependency in dependencyItems" :key="dependency.key" class="ops-dependency">
            <div class="ops-dependency-label"><t-icon :name="dependency.icon" /><span>{{ dependency.label }}</span></div>
            <t-tag :theme="dependency.theme" size="small" variant="light">{{ dependency.state }}</t-tag>
          </div>
        </div>
      </section>

      <section class="ops-section ops-technical-grid">
        <div class="ops-technical-block">
          <h3>{{ t('operations.database.title') }}</h3>
          <dl class="ops-value-list">
            <div><dt>{{ t('operations.database.connections') }}</dt><dd>{{ status.database.in_use_connections }} / {{ status.database.open_connections }}</dd></div>
            <div><dt>{{ t('operations.database.waits') }}</dt><dd>{{ formatNumber(status.database.wait_count) }}</dd></div>
            <div><dt>{{ t('operations.database.schema') }}</dt><dd>{{ migrationLabel }}</dd></div>
          </dl>
        </div>
        <div class="ops-technical-block">
          <h3>{{ t('operations.storage.title') }}</h3>
          <dl class="ops-value-list">
            <div><dt>{{ t('operations.storage.fileLog') }}</dt><dd>{{ status.file_log.enabled ? t('operations.enabled') : t('operations.disabled') }}</dd></div>
            <div><dt>{{ t('operations.storage.logSize') }}</dt><dd>{{ status.file_log.enabled ? formatBytes(status.file_log.size_bytes) : '-' }}</dd></div>
            <div><dt>{{ t('operations.storage.freeSpace') }}</dt><dd :class="`ops-value--${status.file_log.disk_state}`">{{ fileLogSpaceLabel }}</dd></div>
          </dl>
        </div>
      </section>

      <section class="ops-section">
        <div class="ops-section-heading ops-section-heading--actions">
          <div><h3>{{ t('operations.backups.title') }}</h3><p>{{ t('operations.backups.description') }}</p></div>
          <t-button theme="primary" :disabled="!canCreateManualBackup" @click="backupDialogVisible = true">
            <template #icon><t-icon name="save" /></template>{{ t('operations.backups.create') }}
          </t-button>
        </div>
        <p v-if="!canCreateManualBackup" class="ops-action-hint">{{ manualBackupUnavailableReason }}</p>
        <div class="ops-backup-grid">
          <div><span>{{ t('operations.backups.schedule') }}</span><strong>{{ scheduleLabel }}</strong></div>
          <div><span>{{ t('operations.backups.lastSuccess') }}</span><strong>{{ formatDate(status.backup.last_success_at) }}</strong></div>
          <div :class="{ 'ops-backup-cell--warning': hasBackupFailure }"><span>{{ t('operations.backups.lastFailure') }}</span><strong>{{ formatBackupFailure }}</strong></div>
          <div><span>{{ t('operations.backups.retention') }}</span><strong>{{ retentionLabel }}</strong></div>
        </div>
      </section>
    </template>

    <t-dialog
      v-model:visible="backupDialogVisible"
      :header="t('operations.backups.dialogTitle')"
      :confirm-btn="null"
      :cancel-btn="t('operations.cancel')"
      width="520px"
      :close-on-overlay-click="!backupSubmitting"
      @close="resetBackupDialog"
    >
      <p class="ops-dialog-description">{{ t('operations.backups.dialogDescription') }}</p>
      <t-textarea
        v-model="backupReason"
        :maxlength="512"
        :autosize="{ minRows: 3, maxRows: 6 }"
        :placeholder="t('operations.backups.reasonPlaceholder')"
        :disabled="backupSubmitting"
      />
      <div class="ops-dialog-meta">{{ backupReasonLength }} / 512</div>
      <t-alert v-if="backupError" theme="error" :message="backupError" class="ops-dialog-alert" />
      <template #footer>
        <t-button variant="outline" :disabled="backupSubmitting" @click="backupDialogVisible = false">{{ t('operations.cancel') }}</t-button>
        <t-popconfirm
          :content="t('operations.backups.confirm')"
          theme="warning"
          @confirm="createBackup"
        >
          <t-button theme="primary" :loading="backupSubmitting" :disabled="!validBackupReason || backupSubmitting">
            <template #icon><t-icon name="save" /></template>{{ t('operations.backups.confirmAction') }}
          </t-button>
        </t-popconfirm>
      </template>
    </t-dialog>

    <t-dialog
      v-model:visible="backupResultVisible"
      :header="t('operations.backups.resultTitle')"
      :confirm-btn="t('operations.close')"
      :cancel-btn="null"
      width="560px"
    >
      <t-alert theme="success" :message="t('operations.backups.success')" />
      <dl v-if="backupResult" class="ops-result-list">
        <div><dt>{{ t('operations.backups.backupId') }}</dt><dd class="ops-mono">{{ backupResult.backup_id }}</dd></div>
        <div><dt>{{ t('operations.backups.archive') }}</dt><dd>{{ backupResult.archive_file }}</dd></div>
        <div><dt>{{ t('operations.backups.manifest') }}</dt><dd>{{ backupResult.manifest_file }}</dd></div>
        <div><dt>{{ t('operations.backups.size') }}</dt><dd>{{ formatBytes(backupResult.size_bytes) }}</dd></div>
        <div><dt>{{ t('operations.backups.migration') }}</dt><dd>{{ backupResult.migration_known ? `v${backupResult.migration_version}` : t('operations.unknown') }}</dd></div>
      </dl>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  createManualBackup,
  getOperationsStatus,
  type ManualBackupResult,
  type OperationsStatus,
} from '@/api/operations'

const { t, locale } = useI18n()
const status = ref<OperationsStatus | null>(null)
const loading = ref(false)
const error = ref('')
const updatedAt = ref('')
const backupDialogVisible = ref(false)
const backupResultVisible = ref(false)
const backupReason = ref('')
const backupSubmitting = ref(false)
const backupError = ref('')
const backupResult = ref<ManualBackupResult | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

const overallTone = computed(() => status.value?.status === 'ready' ? 'success' : 'danger')
const overallLabel = computed(() => overallTone.value === 'success' ? t('operations.overview.ready') : t('operations.overview.notReady'))
const overallDescription = computed(() => overallTone.value === 'success' ? t('operations.overview.readyDescription') : t('operations.overview.notReadyDescription'))
const databaseDriverLabel = computed(() => status.value ? t(`operations.database.drivers.${status.value.database.driver}`) : t('operations.unknown'))
const migrationLabel = computed(() => {
  if (!status.value?.migration.known) return t('operations.unknown')
  if (status.value.migration.dirty) return t('operations.database.migrationDirty', { version: status.value.migration.version })
  return t('operations.database.migrationClean', { version: status.value.migration.version })
})
const fileLogSpaceLabel = computed(() => {
  const fileLog = status.value?.file_log
  if (!fileLog?.enabled) return t('operations.disabled')
  return `${formatBytes(fileLog.disk_free_bytes)} · ${t(`operations.storage.states.${fileLog.disk_state}`)}`
})
const canCreateManualBackup = computed(() => status.value?.database.driver === 'mysql' && status.value.dependencies.database === 'ok' && !status.value.migration.dirty)
const manualBackupUnavailableReason = computed(() => {
  if (!status.value) return ''
  if (status.value.database.driver !== 'mysql') return t('operations.backups.mysqlOnly')
  if (status.value.dependencies.database !== 'ok') return t('operations.backups.databaseUnavailable')
  if (status.value.migration.dirty) return t('operations.backups.migrationDirty')
  return ''
})
const validBackupReason = computed(() => {
  const value = backupReason.value.trim()
  return value.length > 0 && Array.from(value).length <= 512
})
const backupReasonLength = computed(() => Array.from(backupReason.value).length)
const scheduleLabel = computed(() => {
  if (!status.value?.backup.scheduled) return t('operations.backups.notScheduled')
  return status.value.backup.schedule || t('operations.backups.scheduled')
})
const retentionLabel = computed(() => {
  if (!status.value) return '-'
  return status.value.backup.retention_days > 0
    ? t('operations.backups.retentionValue', { days: status.value.backup.retention_days })
    : t('operations.backups.retentionDisabled')
})
const hasBackupFailure = computed(() => Boolean(status.value?.backup.last_failure_at || status.value?.backup.last_retention_failure_at || status.value?.backup.configuration_error))
const formatBackupFailure = computed(() => {
  if (!status.value) return '-'
  if (status.value.backup.configuration_error) return t('operations.backups.configurationError')
  if (status.value.backup.last_retention_failure_at) return t('operations.backups.retentionFailure')
  if (status.value.backup.last_failure_at) return status.value.backup.last_failure_kind || t('operations.backups.failed')
  return t('operations.backups.none')
})
const dependencyItems = computed(() => {
  if (!status.value) return []
  const labels: Record<string, string> = {
    database: t('operations.dependencies.database'),
    redis: t('operations.dependencies.redis'),
  }
  return Object.entries(status.value.dependencies).map(([key, value]) => ({
    key,
    label: labels[key] || key,
    state: t(`operations.dependencies.states.${value}`),
    theme: value === 'ok' ? 'success' : value === 'disabled' ? 'default' : 'danger',
    icon: key === 'database' ? 'data-base' : 'server',
  }))
})

function formatNumber(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value || 0)
}

function formatBytes(value: number): string {
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const exponent = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  const amount = value / Math.pow(1024, exponent)
  return `${amount >= 10 || exponent === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[exponent]}`
}

function formatUptime(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds || 0))
  const days = Math.floor(total / 86400)
  const hours = Math.floor((total % 86400) / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  if (days > 0) return t('operations.overview.uptimeDays', { days, hours })
  if (hours > 0) return t('operations.overview.uptimeHours', { hours, minutes })
  return t('operations.overview.uptimeMinutes', { minutes })
}

function validDate(value?: string): Date | null {
  if (!value || value.startsWith('0001-')) return null
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

function formatDate(value?: string): string {
  const date = validDate(value)
  return date ? date.toLocaleString(locale.value) : t('operations.backups.none')
}

function resetBackupDialog() {
  if (backupSubmitting.value) return
  backupReason.value = ''
  backupError.value = ''
}

async function createBackup() {
  if (!validBackupReason.value || backupSubmitting.value) return
  backupSubmitting.value = true
  backupError.value = ''
  try {
    backupResult.value = await createManualBackup(backupReason.value.trim())
    backupDialogVisible.value = false
    backupResultVisible.value = true
    MessagePlugin.success(t('operations.backups.success'))
    await reload(false)
  } catch (requestError: any) {
    backupError.value = requestError?.message || t('operations.backups.requestFailed')
  } finally {
    backupSubmitting.value = false
  }
}

async function reload(showSpinner: boolean) {
  if (loading.value) return
  loading.value = showSpinner
  try {
    status.value = await getOperationsStatus()
    error.value = ''
    updatedAt.value = new Date().toLocaleTimeString(locale.value)
  } catch (requestError: any) {
    error.value = requestError?.message || t('operations.loadFailed')
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void reload(true)
  pollTimer = setInterval(() => { void reload(false) }, 30000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<style lang="less" scoped>
.ops-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; }
.ops-header-actions { display: flex; align-items: center; gap: 10px; padding-top: 4px; }
.ops-updated-at { color: var(--td-text-color-secondary); font-size: 12px; display: inline-flex; gap: 4px; align-items: center; white-space: nowrap; }
.ops-icon-button { width: 32px; height: 32px; border: 1px solid var(--td-component-border); border-radius: 5px; background: var(--td-bg-color-container); color: var(--td-text-color-secondary); display: inline-grid; place-items: center; cursor: pointer; }
.ops-icon-button:hover:not(:disabled) { color: var(--td-brand-color); border-color: var(--td-brand-color); }
.ops-icon-button:disabled { cursor: not-allowed; opacity: .55; }
.ops-spin { animation: ops-spin .85s linear infinite; }
.ops-loading { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; padding-top: 16px; }
.ops-error, .ops-stale-alert { margin-top: 20px; }
.ops-overview { margin-top: 20px; padding: 20px; border: 1px solid var(--td-component-border); border-left-width: 4px; border-radius: 6px; display: flex; justify-content: space-between; gap: 24px; background: var(--td-bg-color-container); }
.ops-overview--success { border-left-color: var(--td-success-color); }
.ops-overview--danger { border-left-color: var(--td-error-color); }
.ops-overview-state { display: flex; align-items: flex-start; gap: 12px; }
.ops-overview--success .ops-overview-state > :first-child { color: var(--td-success-color); }
.ops-overview--danger .ops-overview-state > :first-child { color: var(--td-error-color); }
.ops-overview-state strong { display: block; color: var(--td-text-color-primary); font-size: 16px; }
.ops-overview-state p, .ops-section-heading p { margin: 5px 0 0; color: var(--td-text-color-secondary); font-size: 13px; line-height: 1.55; }
.ops-overview-metrics { display: grid; grid-template-columns: repeat(3, minmax(104px, 1fr)); margin: 0; gap: 18px; }
.ops-overview-metrics div { border-left: 1px solid var(--td-component-border); padding-left: 18px; }
.ops-overview-metrics dt, .ops-backup-grid span, .ops-value-list dt { color: var(--td-text-color-secondary); font-size: 12px; }
.ops-overview-metrics dd { margin: 5px 0 0; color: var(--td-text-color-primary); font-weight: 600; }
.ops-section { margin-top: 30px; border-top: 1px solid var(--td-component-border); padding-top: 22px; }
.ops-section-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 18px; }
.ops-section-heading h3, .ops-technical-block h3 { margin: 0; color: var(--td-text-color-primary); font-size: 15px; }
.ops-section-heading--actions > :last-child { flex: 0 0 auto; }
.ops-dependency-grid, .ops-backup-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); margin-top: 15px; border: 1px solid var(--td-component-border); border-radius: 6px; overflow: hidden; }
.ops-dependency { min-height: 58px; padding: 0 16px; display: flex; align-items: center; justify-content: space-between; gap: 12px; border-bottom: 1px solid var(--td-component-border); }
.ops-dependency:nth-last-child(-n + 2) { border-bottom: 0; }
.ops-dependency:nth-child(odd) { border-right: 1px solid var(--td-component-border); }
.ops-dependency-label { display: inline-flex; align-items: center; gap: 8px; color: var(--td-text-color-primary); font-size: 13px; }
.ops-technical-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 32px; }
.ops-value-list { margin: 14px 0 0; }
.ops-value-list div { display: flex; justify-content: space-between; gap: 16px; padding: 10px 0; border-bottom: 1px solid var(--td-component-border); }
.ops-value-list dd { margin: 0; text-align: right; color: var(--td-text-color-primary); font-size: 13px; }
.ops-value--warning { color: var(--td-warning-color) !important; }.ops-value--critical { color: var(--td-error-color) !important; }
.ops-action-hint { margin: 14px 0 0; color: var(--td-warning-color); font-size: 13px; }
.ops-backup-grid { grid-template-columns: repeat(4, minmax(0, 1fr)); }
.ops-backup-grid > div { min-height: 70px; padding: 13px 15px; border-right: 1px solid var(--td-component-border); }
.ops-backup-grid > div:last-child { border-right: 0; }
.ops-backup-grid strong { display: block; margin-top: 7px; color: var(--td-text-color-primary); font-size: 13px; overflow-wrap: anywhere; }
.ops-backup-cell--warning strong { color: var(--td-warning-color); }
.ops-dialog-description { margin: 0 0 14px; color: var(--td-text-color-secondary); font-size: 13px; line-height: 1.6; }
.ops-dialog-meta { margin-top: 6px; color: var(--td-text-color-placeholder); font-size: 12px; text-align: right; }
.ops-dialog-alert { margin-top: 14px; }
.ops-result-list { margin: 18px 0 0; }.ops-result-list div { display: grid; grid-template-columns: 120px minmax(0, 1fr); gap: 14px; padding: 10px 0; border-bottom: 1px solid var(--td-component-border); }.ops-result-list dt { color: var(--td-text-color-secondary); }.ops-result-list dd { margin: 0; color: var(--td-text-color-primary); overflow-wrap: anywhere; }.ops-mono { font-family: var(--td-font-family-medium, monospace); }
@keyframes ops-spin { to { transform: rotate(360deg); } }
@media (max-width: 760px) { .ops-header, .ops-overview, .ops-section-heading { flex-direction: column; }.ops-header-actions { width: 100%; justify-content: space-between; }.ops-overview-metrics, .ops-backup-grid, .ops-technical-grid, .ops-loading { grid-template-columns: 1fr; width: 100%; }.ops-overview-metrics div { border-left: 0; border-top: 1px solid var(--td-component-border); padding: 10px 0 0; }.ops-dependency-grid { grid-template-columns: 1fr; }.ops-dependency { border-right: 0 !important; }.ops-dependency:nth-last-child(-n + 2) { border-bottom: 1px solid var(--td-component-border); }.ops-dependency:last-child { border-bottom: 0; }.ops-backup-grid > div { border-right: 0; border-bottom: 1px solid var(--td-component-border); }.ops-backup-grid > div:last-child { border-bottom: 0; } }
</style>
