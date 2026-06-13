<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
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
import DataSourceTypeIcon from './DataSourceTypeIcon.vue'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ kbId: string }>()
const emit = defineEmits<{ (e: 'count', value: number): void }>()
const { t } = useI18n()
const authStore = useAuthStore()

// 后端 /datasource 的 list/logs 是 Viewer+，但所有写操作（POST/PUT/DELETE
// 以及 sync/pause/resume/validate）都是 Admin+。低权限用户保留只读视图，
// 增删改和触发同步全部隐藏，而不是按下去再撞 403。
const canManageDataSource = computed(() => authStore.hasRole('admin'))

const dataSources = ref<DataSource[]>([])
const loading = ref(false)
const editorVisible = ref(false)
const editingDs = ref<DataSource | null>(null)
const logsVisible = ref(false)
const logsDsId = ref('')
const logsDsName = ref('')
const pollTimer = ref<number | null>(null)

function stopPolling() {
  if (pollTimer.value !== null) {
    window.clearTimeout(pollTimer.value)
    pollTimer.value = null
  }
}

function schedulePolling() {
  stopPolling()
  pollTimer.value = window.setTimeout(() => {
    loadList(true)
  }, 3000)
}

async function loadList(silent = false) {
  if (!silent) loading.value = true
  try {
    const res = await listDataSources(props.kbId)
    dataSources.value = res?.data || res || []
    emit('count', dataSources.value.length)

    const hasRunningSync = dataSources.value.some(ds => ds.latest_sync_log?.status === 'running')
    if (hasRunningSync) {
      schedulePolling()
    } else {
      stopPolling()
    }
  } catch (e: any) {
    console.error(e)
  } finally {
    if (!silent) loading.value = false
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
        await loadList()
        confirmDialog.hide()
      } catch (e: any) {
        MessagePlugin.error(e?.message || e?.error || t('datasource.deleteFailed'))
      }
    },
  })
}

async function handleSync(ds: DataSource) {
  try {
    await triggerSync(ds.id)
    MessagePlugin.success(t('datasource.syncTriggered'))
    await loadList(true)
  } catch (e: any) {
    MessagePlugin.error(e?.message || e?.error || t('datasource.syncFailed'))
  }
}

async function handlePause(ds: DataSource) {
  try {
    await pauseDataSource(ds.id)
    MessagePlugin.success(t('datasource.paused'))
    loadList()
  } catch (e: any) {
    MessagePlugin.error(e?.message || e?.error || t('datasource.pauseFailed'))
  }
}

async function handleResume(ds: DataSource) {
  try {
    await resumeDataSource(ds.id)
    MessagePlugin.success(t('datasource.resumed'))
    loadList()
  } catch (e: any) {
    MessagePlugin.error(e?.message || e?.error || t('datasource.resumeFailed'))
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

function isSyncRunning(ds: DataSource) {
  return ds.latest_sync_log?.status === 'running'
}

function onEditorSaved() {
  editorVisible.value = false
  loadList()
}

onMounted(loadList)
onBeforeUnmount(stopPolling)
</script>

<template>
  <div class="ds-settings">
    <div class="section-header">
      <h2 class="section-title">{{ t('datasource.title') }}</h2>
      <p class="section-desc">{{ t('datasource.description') }}</p>
    </div>

    <div class="channels-section">
      <div class="channels-header">
        <span class="channels-title">{{ t('datasource.channelsTitle') }}</span>
        <span class="channels-count">{{ dataSources.length }}</span>
      </div>

      <t-loading :loading="loading" size="small" class="channels-loading-wrap">
        <div
          v-if="!loading && dataSources.length === 0 && !canManageDataSource"
          class="channels-empty"
        >
          <t-empty :description="t('datasource.empty')" />
        </div>

        <div v-else-if="!loading" class="channel-grid">
          <component
            :is="canManageDataSource ? 'button' : 'div'"
            v-for="ds in dataSources"
            :key="ds.id"
            :type="canManageDataSource ? 'button' : undefined"
            :class="['channel-card', { 'channel-card--clickable': canManageDataSource }]"
            @click="canManageDataSource ? openEdit(ds) : undefined"
          >
            <div class="channel-card__badge">
              <DataSourceTypeIcon :type="ds.type" :size="22" />
            </div>
            <div class="channel-card__body">
              <div class="channel-card__header">
                <h3 class="channel-card__title" :title="ds.name">{{ ds.name }}</h3>
                <t-tag size="small" :theme="statusTheme(ds.status)" variant="light">
                  {{ statusLabel(ds.status) }}
                </t-tag>
              </div>
              <p class="channel-card__subtitle">
                {{ connectorLabel(ds.type) }} · {{ syncModeLabel(ds.sync_mode) }}
              </p>
              <div class="channel-card__meta">
                <div class="channel-card__meta-item">
                  <span class="channel-card__meta-label">{{ t('datasource.schedule') }}</span>
                  <span class="channel-card__meta-value">{{ scheduleLabel(ds.sync_schedule) }}</span>
                </div>
                <div class="channel-card__meta-item">
                  <span class="channel-card__meta-label">{{ t('datasource.lastSync') }}</span>
                  <t-tooltip :content="lastSyncFullTime(ds)" :disabled="!lastSyncFullTime(ds)">
                    <span class="channel-card__meta-value">{{ lastSyncTime(ds) }}</span>
                  </t-tooltip>
                </div>
                <div class="channel-card__meta-item channel-card__meta-item--wide">
                  <span class="channel-card__meta-label">{{ t('datasource.lastStatus') }}</span>
                  <span class="channel-card__meta-value">
                    <template v-if="ds.latest_sync_log">
                      <span :style="{ color: lastSyncStatusColor(ds), fontWeight: 500 }">
                        {{ lastSyncStatusLabel(ds) }}
                      </span>
                      <span
                        v-for="pill in syncResultPills(ds)"
                        :key="pill.cls"
                        :class="['ds-pill', pill.cls]"
                      >{{ pill.text }}</span>
                    </template>
                    <span v-else class="channel-card__meta-placeholder">--</span>
                  </span>
                </div>
              </div>
              <div v-if="ds.error_message" class="channel-card__error">
                <t-icon name="error-circle-filled" size="14px" />
                <span>{{ ds.error_message }}</span>
              </div>
            </div>
            <div class="channel-card__actions" @click.stop>
              <t-tooltip
                v-if="canManageDataSource"
                :content="isSyncRunning(ds) ? t('datasource.logStatus.running') : t('datasource.syncNow')"
              >
                <t-button
                  size="small"
                  variant="text"
                  theme="primary"
                  :disabled="isSyncRunning(ds)"
                  @click="handleSync(ds)"
                >
                  <template #icon>
                    <t-icon name="refresh" :class="{ 'ds-icon-spin': isSyncRunning(ds) }" />
                  </template>
                </t-button>
              </t-tooltip>
              <t-tooltip :content="t('datasource.logs')">
                <t-button size="small" variant="text" @click="openLogs(ds)">
                  <template #icon><t-icon name="root-list" /></template>
                </t-button>
              </t-tooltip>
              <t-dropdown v-if="canManageDataSource" trigger="click" :min-column-width="120">
                <t-button
                  variant="text"
                  shape="square"
                  size="small"
                  class="channel-card__more"
                  @click.stop
                >
                  <template #icon><t-icon name="ellipsis" /></template>
                </t-button>
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
          </component>

          <button
            v-if="canManageDataSource"
            type="button"
            class="channel-card channel-card--add"
            @click="openCreate"
          >
            <span class="channel-card--add__icon" aria-hidden="true">
              <t-icon name="add" />
            </span>
            <span class="channel-card--add__label">{{ t('datasource.add') }}</span>
          </button>
        </div>
      </t-loading>
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

<style scoped lang="less">
.ds-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 20px;

  .section-title {
    margin: 0 0 6px;
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .section-desc {
    margin: 0;
    font-size: 14px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }
}

.channels-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;

  .channels-title {
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .channels-count {
    padding: 2px 8px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 10px;
    font-size: 12px;
    color: var(--td-text-color-disabled);
  }
}

.channels-loading-wrap {
  min-height: 80px;
}

.channels-empty {
  padding: 32px 0;
}

.channel-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 12px;
}

.channel-card {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  text-align: left;
  font: inherit;
  color: inherit;
  transition: border-color 0.18s ease, box-shadow 0.18s ease;

  &--clickable {
    cursor: pointer;
    width: 100%;

    &:hover,
    &:focus-visible {
      border-color: var(--td-brand-color-3, var(--td-brand-color));
      box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
      outline: none;
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }
  }

  &--add {
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 68px;
    border-style: dashed;
    background: transparent;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    width: 100%;

    &:hover,
    &:focus-visible {
      color: var(--td-brand-color);
      border-color: var(--td-brand-color);
      background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
      box-shadow: none;
    }

    &__icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      border-radius: 8px;
      background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
      color: var(--td-brand-color);
      font-size: 18px;
    }

    &__label {
      font-size: 13px;
      font-weight: 500;
      line-height: 1.4;
    }
  }

  &__badge {
    flex-shrink: 0;
    width: 36px;
    height: 36px;
    border-radius: 9px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
    overflow: hidden;
  }

  &__body {
    flex: 1;
    min-width: 0;
  }

  &__header {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }

  &__title {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 14px;
    font-weight: 600;
    line-height: 1.4;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__subtitle {
    margin: 2px 0 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__meta {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px 12px;
    margin-top: 10px;
    padding-top: 10px;
    border-top: 1px solid var(--td-component-stroke);
  }

  &__meta-item {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;

    &--wide {
      grid-column: 1 / -1;
    }
  }

  &__meta-label {
    font-size: 11px;
    line-height: 1.4;
    color: var(--td-text-color-placeholder);
  }

  &__meta-value {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 4px;
    font-size: 12px;
    line-height: 1.4;
    color: var(--td-text-color-primary);
  }

  &__meta-placeholder {
    color: var(--td-text-color-disabled);
  }

  &__error {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    margin-top: 8px;
    padding: 8px 10px;
    border-radius: 6px;
    background: var(--td-error-color-1);
    color: var(--td-error-color);
    font-size: 12px;
    line-height: 1.45;
    text-align: left;
  }

  &__actions {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 2px;
    padding-top: 2px;
  }

  &__more {
    flex-shrink: 0;
    padding: 2px;
    opacity: 0;
    color: var(--td-text-color-placeholder);
    transition: opacity 0.15s ease;

    &:hover,
    &:focus-visible {
      background: var(--td-bg-color-secondarycontainer);
      color: var(--td-text-color-primary);
    }
  }

  &:hover .channel-card__more,
  &:focus-within .channel-card__more,
  &__actions:focus-within .channel-card__more {
    opacity: 1;
  }
}

.ds-pill {
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 4px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  line-height: 16px;

  &.created { background: var(--td-success-color-1); color: var(--td-success-color); }
  &.updated { background: var(--td-brand-color-light); color: var(--td-brand-color); }
  &.deleted { background: var(--td-warning-color-1); color: var(--td-warning-color); }
  &.skipped { background: var(--td-bg-color-component); color: var(--td-text-color-placeholder); }
  &.failed { background: var(--td-error-color-1); color: var(--td-error-color); }
}

.ds-icon-spin {
  animation: ds-spin 1s linear infinite;
}

@keyframes ds-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
