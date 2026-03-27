<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  listDataSources,
  deleteDataSource,
  triggerSync,
  pauseDataSource,
  resumeDataSource,
  type DataSource,
} from '@/api/datasource'
import { humanizeCron, relativeTime } from '@/utils/cronHumanize'
import DataSourceEditorDialog from './DataSourceEditorDialog.vue'
import DataSourceSyncLogs from './DataSourceSyncLogs.vue'

const props = defineProps<{ kbId: string }>()
const { t } = useI18n()

const dataSources = ref<DataSource[]>([])
const loading = ref(false)
const editorVisible = ref(false)
const editingDs = ref<DataSource | null>(null)
const logsVisible = ref(false)
const logsDsId = ref('')
const logsDsName = ref('')

async function loadList() {
  loading.value = true
  try {
    const res = await listDataSources(props.kbId)
    dataSources.value = res?.data || res || []
  } catch (e: any) {
    console.error(e)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingDs.value = null
  editorVisible.value = true
}

function openEdit(ds: DataSource) {
  editingDs.value = ds
  editorVisible.value = true
}

function openLogs(ds: DataSource) {
  logsDsId.value = ds.id
  logsDsName.value = ds.name
  logsVisible.value = true
}

async function handleDelete(ds: DataSource) {
  try {
    await deleteDataSource(ds.id)
    MessagePlugin.success(t('datasource.deleteSuccess'))
    loadList()
  } catch {
    MessagePlugin.error(t('datasource.deleteFailed'))
  }
}

async function handleSync(ds: DataSource) {
  try {
    await triggerSync(ds.id)
    MessagePlugin.success(t('datasource.syncTriggered'))
    loadList()
  } catch {
    MessagePlugin.error(t('datasource.syncFailed'))
  }
}

async function handlePause(ds: DataSource) {
  try {
    await pauseDataSource(ds.id)
    MessagePlugin.success(t('datasource.paused'))
    loadList()
  } catch {
    MessagePlugin.error(t('datasource.pauseFailed'))
  }
}

async function handleResume(ds: DataSource) {
  try {
    await resumeDataSource(ds.id)
    MessagePlugin.success(t('datasource.resumed'))
    loadList()
  } catch {
    MessagePlugin.error(t('datasource.resumeFailed'))
  }
}

function statusColor(status: string) {
  if (status === 'active') return 'var(--td-success-color)'
  if (status === 'error') return 'var(--td-error-color)'
  return 'var(--td-text-color-placeholder)'
}

function statusLabel(status: string) {
  return t(`datasource.status.${status}`)
}

function syncModeLabel(mode: string) {
  return t(`datasource.syncMode.${mode}`)
}

function connectorLabel(type: string) {
  return t(`datasource.connector.${type}`) || type
}

function scheduleLabel(cron: string) {
  return humanizeCron(cron, t)
}

function lastSyncTime(ds: DataSource) {
  return relativeTime(ds.last_sync_at, t)
}

function lastSyncFullTime(ds: DataSource) {
  if (!ds.last_sync_at) return ''
  return new Date(ds.last_sync_at).toLocaleString()
}

// Build sync result pills from latest_sync_log
function syncResultPills(ds: DataSource) {
  const log = ds.latest_sync_log
  if (!log) return []
  const pills: { text: string; cls: string }[] = []
  if (log.items_created > 0) pills.push({ text: `+${log.items_created}`, cls: 'created' })
  if (log.items_updated > 0) pills.push({ text: `~${log.items_updated}`, cls: 'updated' })
  if (log.items_deleted > 0) pills.push({ text: `-${log.items_deleted}`, cls: 'deleted' })
  if (log.items_failed > 0) pills.push({ text: `${log.items_failed} ${t('datasource.logMetric.failed')}`, cls: 'failed' })
  if (log.items_skipped > 0) pills.push({ text: `${log.items_skipped} ${t('datasource.logMetric.skipped')}`, cls: 'skipped' })
  return pills
}

function lastSyncStatusLabel(ds: DataSource) {
  const log = ds.latest_sync_log
  if (!log) return '--'
  return t(`datasource.logStatus.${log.status}`)
}

function lastSyncStatusColor(ds: DataSource) {
  const log = ds.latest_sync_log
  if (!log) return ''
  switch (log.status) {
    case 'success': return 'var(--td-success-color)'
    case 'failed': return 'var(--td-error-color)'
    case 'running': return 'var(--td-brand-color)'
    case 'partial': return 'var(--td-warning-color)'
    default: return ''
  }
}

function onEditorSaved() {
  editorVisible.value = false
  loadList()
}

onMounted(loadList)
</script>

<template>
  <div class="ds-settings">
    <!-- Section header: title only, no button here -->
    <div class="section-header">
      <h2 class="section-title">{{ t('datasource.title') }}</h2>
      <p class="section-desc">{{ t('datasource.description') }}</p>
    </div>

    <div v-if="loading" class="ds-loading">
      <t-loading />
    </div>

    <div v-else-if="dataSources.length === 0" class="ds-empty">
      <t-icon name="cloud-download" size="48px" style="color: var(--td-text-color-placeholder)" />
      <p>{{ t('datasource.empty') }}</p>
      <t-button variant="outline" @click="openCreate">{{ t('datasource.addFirst') }}</t-button>
    </div>

    <div v-else class="ds-list">
      <!-- Data source cards -->
      <div v-for="ds in dataSources" :key="ds.id" class="ds-card">
        <div class="ds-card-header">
          <div class="ds-card-title">
            <span class="ds-type-badge">{{ connectorLabel(ds.type) }}</span>
            <span class="ds-name">{{ ds.name }}</span>
          </div>
          <div class="ds-card-header-right">
            <div class="ds-status">
              <span class="ds-status-dot" :style="{ background: statusColor(ds.status) }"></span>
              {{ statusLabel(ds.status) }}
            </div>
            <t-dropdown trigger="click" :min-column-width="100">
              <t-button size="small" variant="text" shape="square">
                <template #icon><t-icon name="more" /></template>
              </t-button>
              <template #dropdown>
                <t-dropdown-menu>
                  <t-dropdown-item @click="openEdit(ds)">
                    <t-icon name="edit" /> {{ t('datasource.edit') }}
                  </t-dropdown-item>
                  <t-dropdown-item @click="openLogs(ds)">
                    <t-icon name="history" /> {{ t('datasource.logs') }}
                  </t-dropdown-item>
                  <t-dropdown-item
                    v-if="ds.status === 'active'"
                    @click="handlePause(ds)"
                  >
                    <t-icon name="pause-circle" /> {{ t('datasource.pause') }}
                  </t-dropdown-item>
                  <t-dropdown-item
                    v-else-if="ds.status === 'paused'"
                    @click="handleResume(ds)"
                  >
                    <t-icon name="play-circle" /> {{ t('datasource.resume') }}
                  </t-dropdown-item>
                  <t-dropdown-item theme="error" @click="handleDelete(ds)">
                    <t-icon name="delete" /> {{ t('datasource.delete') }}
                  </t-dropdown-item>
                </t-dropdown-menu>
              </template>
            </t-dropdown>
          </div>
        </div>

        <!-- Info grid -->
        <div class="ds-card-body">
          <div class="ds-info-cell">
            <span class="ds-info-label">{{ t('datasource.syncModeLabel') }}</span>
            <span class="ds-info-value">{{ syncModeLabel(ds.sync_mode) }}</span>
          </div>
          <div class="ds-info-cell">
            <span class="ds-info-label">{{ t('datasource.schedule') }}</span>
            <span class="ds-info-value">{{ scheduleLabel(ds.sync_schedule) }}</span>
          </div>
          <div class="ds-info-cell">
            <span class="ds-info-label">{{ t('datasource.lastSync') }}</span>
            <span class="ds-info-value" :title="lastSyncFullTime(ds)">{{ lastSyncTime(ds) }}</span>
          </div>
          <div class="ds-info-cell">
            <span class="ds-info-label">{{ t('datasource.lastStatus') }}</span>
            <span class="ds-info-value">
              <span v-if="ds.latest_sync_log" :style="{ color: lastSyncStatusColor(ds) }">{{ lastSyncStatusLabel(ds) }}</span>
              <span v-else>--</span>
              <span v-for="pill in syncResultPills(ds)" :key="pill.cls" :class="['ds-pill', pill.cls]">{{ pill.text }}</span>
            </span>
          </div>
        </div>

        <div v-if="ds.error_message" class="ds-error">
          <t-icon name="error-circle" size="14px" />
          {{ ds.error_message }}
        </div>

        <!-- Quick action -->
        <div class="ds-card-footer">
          <t-button size="small" variant="outline" theme="primary" @click="handleSync(ds)">
            <template #icon><t-icon name="refresh" /></template>
            {{ t('datasource.syncNow') }}
          </t-button>
          <t-button size="small" variant="text" @click="openLogs(ds)">
            {{ t('datasource.logs') }}
          </t-button>
        </div>
      </div>

      <!-- Add card at the end -->
      <div class="ds-card-add" @click="openCreate">
        <t-icon name="add" size="20px" />
        <span>{{ t('datasource.addCard') }}</span>
      </div>
    </div>

    <!-- Editor dialog -->
    <DataSourceEditorDialog
      v-model:visible="editorVisible"
      :kb-id="kbId"
      :data-source="editingDs"
      @saved="onEditorSaved"
    />

    <!-- Sync logs drawer -->
    <DataSourceSyncLogs
      v-model:visible="logsVisible"
      :data-source-id="logsDsId"
      :data-source-name="logsDsName"
    />
  </div>
</template>

<style scoped>
.ds-settings {
  padding: 0;
}

/* --- Section header: matches KBModelConfig / KBStorageSettings pattern --- */
.section-header {
  margin-bottom: 20px;
}

.section-title {
  margin: 0 0 8px 0;
  font-size: 20px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.section-desc {
  margin: 0;
  font-size: 14px;
  color: var(--td-text-color-placeholder);
  line-height: 22px;
}

/* --- Loading / Empty --- */
.ds-loading, .ds-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 0;
  gap: 12px;
  color: var(--td-text-color-placeholder);
}

/* --- List --- */
.ds-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* --- Card --- */
.ds-card {
  border: 1px solid var(--td-border-level-2-color);
  border-radius: 8px;
  padding: 16px;
  transition: border-color 0.2s;
}

.ds-card:hover {
  border-color: var(--td-brand-color-hover);
}

.ds-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.ds-card-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.ds-type-badge {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 4px;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  font-weight: 500;
  flex-shrink: 0;
}

.ds-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ds-card-header-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.ds-status {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.ds-status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

/* --- Info grid --- */
.ds-card-body {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px 24px;
  margin-bottom: 12px;
}

.ds-info-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.ds-info-label {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}

.ds-info-value {
  font-size: 13px;
  color: var(--td-text-color-primary);
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}

/* --- Sync result pills --- */
.ds-pill {
  font-size: 11px;
  padding: 0 5px;
  border-radius: 3px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  line-height: 18px;
}

.ds-pill.created { background: var(--td-success-color-1); color: var(--td-success-color); }
.ds-pill.updated { background: var(--td-brand-color-light); color: var(--td-brand-color); }
.ds-pill.deleted { background: var(--td-warning-color-1); color: var(--td-warning-color); }
.ds-pill.skipped { background: var(--td-bg-color-component); color: var(--td-text-color-placeholder); }
.ds-pill.failed { background: var(--td-error-color-1); color: var(--td-error-color); }

/* --- Error --- */
.ds-error {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  color: var(--td-error-color);
  font-size: 12px;
  background: var(--td-error-color-1);
  padding: 6px 10px;
  border-radius: 4px;
  margin-bottom: 12px;
}

/* --- Footer actions --- */
.ds-card-footer {
  display: flex;
  align-items: center;
  gap: 8px;
  border-top: 1px solid var(--td-border-level-2-color);
  padding-top: 12px;
}

/* --- Add card --- */
.ds-card-add {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 12px;
  border: 1px dashed var(--td-border-level-2-color);
  border-radius: 8px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  cursor: pointer;
  transition: all 0.2s;
}

.ds-card-add:hover {
  border-color: var(--td-brand-color);
  color: var(--td-brand-color);
  background: var(--td-brand-color-light);
}
</style>
