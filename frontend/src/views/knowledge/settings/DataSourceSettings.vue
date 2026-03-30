<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
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
const emit = defineEmits<{ (e: 'count', value: number): void }>()
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
    emit('count', dataSources.value.length)
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

function handleDelete(ds: DataSource) {
  const confirmDialog = DialogPlugin.confirm({
    header: t('datasource.delete'),
    body: t('datasource.deleteConfirm'),
    confirmBtn: { content: t('datasource.delete'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      try {
        await deleteDataSource(ds.id)
        MessagePlugin.success(t('datasource.deleteSuccess'))
        loadList()
      } catch {
        MessagePlugin.error(t('datasource.deleteFailed'))
      }
      confirmDialog.destroy()
    },
  })
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

function statusTheme(status: string): 'success' | 'danger' | 'default' | 'warning' {
  if (status === 'active') return 'success'
  if (status === 'error') return 'danger'
  if (status === 'paused') return 'warning'
  return 'default'
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
    <div class="section-header">
      <h2 class="section-title">{{ t('datasource.title') }}</h2>
      <p class="section-desc">{{ t('datasource.description') }}</p>
    </div>

    <div v-if="loading" class="ds-loading">
      <t-loading size="small" />
    </div>

    <div v-else-if="dataSources.length === 0" class="ds-empty">
      <div class="ds-empty-icon">
        <t-icon name="cloud-download" size="32px" />
      </div>
      <div class="ds-empty-text">
        <p class="ds-empty-title">{{ t('datasource.empty') }}</p>
      </div>
      <t-button theme="primary" @click="openCreate">
        <template #icon><t-icon name="add" /></template>
        {{ t('datasource.addFirst') }}
      </t-button>
    </div>

    <div v-else class="ds-list">
      <div v-for="ds in dataSources" :key="ds.id" class="ds-card">
        <div class="ds-card-header">
          <div class="ds-card-identity">
            <div class="ds-name-row">
              <span class="ds-name">{{ ds.name }}</span>
              <t-tag size="small" :theme="statusTheme(ds.status)" variant="light-outline">
                {{ statusLabel(ds.status) }}
              </t-tag>
            </div>
            <span class="ds-type-label">{{ connectorLabel(ds.type) }}</span>
          </div>
          <div class="ds-card-actions">
            <t-tooltip :content="t('datasource.syncNow')">
              <t-button size="small" variant="text" theme="primary" @click="handleSync(ds)">
                <template #icon><t-icon name="refresh" /></template>
              </t-button>
            </t-tooltip>
            <t-tooltip :content="t('datasource.logs')">
              <t-button size="small" variant="text" @click="openLogs(ds)">
                <template #icon><t-icon name="history" /></template>
              </t-button>
            </t-tooltip>
            <t-dropdown trigger="click" :min-column-width="120">
              <t-tooltip :content="t('datasource.moreActions')">
                <t-button size="small" variant="text" shape="square">
                  <template #icon><t-icon name="ellipsis" /></template>
                </t-button>
              </t-tooltip>
              <template #dropdown>
                <t-dropdown-menu>
                  <t-dropdown-item @click="openEdit(ds)">
                    <t-icon name="edit" /> {{ t('datasource.edit') }}
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
            <t-tooltip :content="lastSyncFullTime(ds)" :disabled="!lastSyncFullTime(ds)">
              <span class="ds-info-value">{{ lastSyncTime(ds) }}</span>
            </t-tooltip>
          </div>
          <div class="ds-info-cell">
            <span class="ds-info-label">{{ t('datasource.lastStatus') }}</span>
            <span class="ds-info-value">
              <template v-if="ds.latest_sync_log">
                <span :style="{ color: lastSyncStatusColor(ds), fontWeight: 500 }">{{ lastSyncStatusLabel(ds) }}</span>
                <span v-for="pill in syncResultPills(ds)" :key="pill.cls" :class="['ds-pill', pill.cls]">{{ pill.text }}</span>
              </template>
              <span v-else class="ds-info-placeholder">--</span>
            </span>
          </div>
        </div>

        <div v-if="ds.error_message" class="ds-error">
          <t-icon name="error-circle-filled" size="14px" />
          <span>{{ ds.error_message }}</span>
        </div>
      </div>

      <div class="ds-card-add" @click="openCreate">
        <t-icon name="add" size="18px" />
        <span>{{ t('datasource.addCard') }}</span>
      </div>
    </div>

    <DataSourceEditorDialog
      v-model:visible="editorVisible"
      :kb-id="kbId"
      :data-source="editingDs"
      @saved="onEditorSaved"
    />

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

/* --- Section header --- */
.section-header {
  margin-bottom: 20px;
}

.section-title {
  margin: 0 0 6px 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  letter-spacing: -0.01em;
}

.section-desc {
  margin: 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  line-height: 20px;
}

/* --- Loading --- */
.ds-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 60px 0;
}

/* --- Empty state --- */
.ds-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 56px 24px;
  gap: 16px;
  border: 1px dashed var(--td-border-level-2-color);
  border-radius: 12px;
  background: var(--td-bg-color-container);
}

.ds-empty-icon {
  width: 56px;
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 14px;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
}

.ds-empty-text {
  text-align: center;
}

.ds-empty-title {
  margin: 0;
  font-size: 14px;
  color: var(--td-text-color-secondary);
  line-height: 22px;
}

/* --- List --- */
.ds-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

/* --- Card --- */
.ds-card {
  position: relative;
  border: 1px solid var(--td-border-level-2-color);
  border-radius: 10px;
  padding: 16px 18px;
  background: var(--td-bg-color-container);
  transition: border-color 0.2s, box-shadow 0.2s;
}

.ds-card:hover {
  border-color: var(--td-brand-color-hover);
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.04);
}

/* --- Card header --- */
.ds-card-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.ds-card-identity {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.ds-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.ds-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 22px;
}

.ds-type-label {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 18px;
}

.ds-card-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
  margin-top: 2px;
}

/* --- Info grid --- */
.ds-card-body {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 24px;
  margin-top: 14px;
  padding: 12px 14px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
}

.ds-info-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.ds-info-label {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 18px;
}

.ds-info-value {
  font-size: 13px;
  color: var(--td-text-color-primary);
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
  line-height: 20px;
}

.ds-info-placeholder {
  color: var(--td-text-color-placeholder);
}

/* --- Sync result pills --- */
.ds-pill {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  line-height: 18px;
}

.ds-pill.created { background: var(--td-success-color-1); color: var(--td-success-color); }
.ds-pill.updated { background: var(--td-brand-color-light); color: var(--td-brand-color); }
.ds-pill.deleted { background: var(--td-warning-color-1); color: var(--td-warning-color); }
.ds-pill.skipped { background: var(--td-bg-color-component); color: var(--td-text-color-placeholder); }
.ds-pill.failed  { background: var(--td-error-color-1); color: var(--td-error-color); }

/* --- Error alert --- */
.ds-error {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  color: var(--td-error-color);
  font-size: 12px;
  background: var(--td-error-color-1);
  padding: 8px 12px;
  border-radius: 6px;
  margin-top: 10px;
  line-height: 20px;
}

/* --- Add card --- */
.ds-card-add {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 14px;
  border: 1px dashed var(--td-border-level-2-color);
  border-radius: 10px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s;
}

.ds-card-add:hover {
  border-color: var(--td-brand-color);
  color: var(--td-brand-color);
  background: rgba(0, 82, 217, 0.04);
}
</style>
