<template>
  <div class="backup-management">
    <header class="section-header">
      <h2>{{ t('backup.title') }}</h2>
      <p class="section-description">{{ t('backup.description') }}</p>
    </header>

    <t-alert theme="warning" :message="t('backup.privacyNotice')" class="backup-alert" />

    <section class="backup-actions">
      <t-button :loading="exporting" @click="handleExport">
        <template #icon><t-icon name="download" /></template>
        {{ t('backup.exportNow') }}
      </t-button>
      <t-button variant="outline" :loading="creating" @click="handleCreateSnapshot">
        <template #icon><t-icon name="save" /></template>
        {{ t('backup.createSnapshot') }}
      </t-button>
      <t-button variant="outline" theme="default" @click="loadSnapshots">
        <template #icon><t-icon name="refresh" /></template>
        {{ t('backup.refresh') }}
      </t-button>
    </section>

    <section class="snapshots-section">
      <div v-if="loading" class="snapshots-state">
        <t-loading size="small" />
        <span>{{ t('backup.loading') }}</span>
      </div>
      <div v-else-if="snapshots.length === 0" class="snapshots-state snapshots-state--empty">
        <span>{{ t('backup.empty') }}</span>
      </div>
      <div v-else class="snapshot-table-wrap">
        <table class="snapshot-table">
          <thead>
            <tr>
              <th>{{ t('backup.colTime') }}</th>
              <th>{{ t('backup.colNote') }}</th>
              <th>{{ t('backup.colVersion') }}</th>
              <th>{{ t('backup.colSize') }}</th>
              <th>{{ t('backup.colActions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="snap in snapshots" :key="snap.id">
              <td>{{ formatTime(snap.created_at) }}</td>
              <td>
                <span class="snapshot-note">{{ snap.note || formatSnapshotKind(snap.id) }}</span>
              </td>
              <td>
                <code class="snapshot-version">{{ snap.manifest?.weknora_version || '-' }}</code>
              </td>
              <td>{{ formatSize(snap.size_bytes) }}</td>
              <td class="snapshot-actions">
                <t-link theme="primary" @click="handleDownload(snap)">{{ t('backup.download') }}</t-link>
                <t-link theme="danger" @click="handleDelete(snap)">{{ t('backup.delete') }}</t-link>
                <t-link theme="warning" @click="askRestore({ snapshotId: snap.id })">
                  {{ t('backup.restore') }}
                </t-link>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section class="upload-restore">
      <div class="upload-row">
        <input ref="fileInputRef" type="file" accept=".tar.gz,.gz" class="upload-input" @change="onFilePicked" />
        <t-button variant="outline" theme="default" @click="fileInputRef?.click()">
          <template #icon><t-icon name="upload" /></template>
          {{ pickedFile ? pickedFile.name : t('backup.pickFile') }}
        </t-button>
        <t-button
          v-if="pickedFile"
          theme="warning"
          @click="askRestore({ file: pickedFile })"
        >
          {{ t('backup.restore') }}
        </t-button>
      </div>
    </section>

    <t-dialog
      v-model:visible="confirmVisible"
      :header="t('backup.restoreConfirmTitle')"
      :confirm-btn="{ content: t('backup.restoreConfirmBtn'), theme: 'danger', loading: restoring }"
      :cancel-btn="t('backup.cancel')"
      @confirm="doRestore"
    >
      <p class="restore-warning">{{ t('backup.restoreWarning') }}</p>
    </t-dialog>

    <div v-if="restoreSummary" class="restore-summary">
      <t-alert :theme="restoreSummary.restart_required ? 'warning' : 'success'">
        <template #message>
          <div class="restore-summary-body">
            <p>{{ t('backup.restoreDone') }}</p>
            <p v-if="restoreSummary.pre_restore_snapshot">
              {{ t('backup.rollbackPoint') }}: <code>{{ restoreSummary.pre_restore_snapshot }}</code>
            </p>
            <p>{{ restoreSummary.note }}</p>
          </div>
        </template>
      </t-alert>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import {
  createSnapshot,
  deleteSnapshot,
  downloadSnapshot,
  exportBackup,
  listSnapshots,
  restoreBackup,
  saveBlob,
  type BackupSnapshot,
  type RestoreSummary,
} from '@/api/backup'

const { t } = useI18n()
const confirmDelete = useConfirmDelete()
const loading = ref(false)
const exporting = ref(false)
const creating = ref(false)
const restoring = ref(false)
const snapshots = ref<BackupSnapshot[]>([])
const restoreSummary = ref<RestoreSummary | null>(null)

const confirmVisible = ref(false)
const pendingRestore = ref<{ snapshotId?: string; file?: File } | null>(null)

const fileInputRef = ref<HTMLInputElement | null>(null)
const pickedFile = ref<File | null>(null)

const loadSnapshots = async () => {
  loading.value = true
  try {
    const res = await listSnapshots()
    snapshots.value = res.data ?? []
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('backup.loadFailed'))
  } finally {
    loading.value = false
  }
}

const handleExport = async () => {
  exporting.value = true
  try {
    const blob = await exportBackup()
    saveBlob(blob, `weknora-backup-${new Date().toISOString().replace(/[:.]/g, '-').slice(0, 17)}.tar.gz`)
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('backup.exportFailed'))
  } finally {
    exporting.value = false
  }
}

const handleCreateSnapshot = async () => {
  creating.value = true
  try {
    // Note is optional; keep the flow one-click and derive context from time.
    await createSnapshot('')
    MessagePlugin.success(t('backup.snapshotCreated'))
    await loadSnapshots()
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('backup.snapshotFailed'))
  } finally {
    creating.value = false
  }
}

const handleDownload = async (snap: BackupSnapshot) => {
  try {
    const blob = await downloadSnapshot(snap.id)
    saveBlob(blob, `${snap.id}.tar.gz`)
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('backup.downloadFailed'))
  }
}

const handleDelete = (snap: BackupSnapshot) => {
  confirmDelete({
    body: t('backup.deleteConfirm', { id: snap.id }),
    onConfirm: async () => {
      try {
        await deleteSnapshot(snap.id)
        MessagePlugin.success(t('backup.deleted'))
        await loadSnapshots()
      } catch (err: any) {
        MessagePlugin.error(err?.message || t('backup.deleteFailed'))
      }
    },
  })
}

const askRestore = (payload: { snapshotId?: string; file?: File }) => {
  pendingRestore.value = payload
  confirmVisible.value = true
}

const doRestore = async () => {
  if (!pendingRestore.value) return
  restoring.value = true
  restoreSummary.value = null
  try {
    const res = await restoreBackup(pendingRestore.value)
    restoreSummary.value = res.data ?? null
    confirmVisible.value = false
    pickedFile.value = null
    MessagePlugin.success(t('backup.restoreDone'))
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('backup.restoreFailed'))
  } finally {
    restoring.value = false
  }
}

const onFilePicked = (e: Event) => {
  const input = e.target as HTMLInputElement
  pickedFile.value = input.files?.[0] ?? null
  // 允许再次选择同一个文件
  input.value = ''
}

const formatSize = (bytes: number): string => {
  if (!bytes || bytes <= 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB']
  let v = bytes
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`
}

const formatTime = (iso: string): string => {
  if (!iso) return '-'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

const formatSnapshotKind = (id: string): string =>
  id.startsWith('pre-restore-') ? t('backup.autoPreRestore') : t('backup.manualSnapshot')

onMounted(loadSnapshots)
</script>

<style scoped>
.backup-management {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.backup-actions {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}

.snapshot-table-wrap {
  overflow-x: auto;
}

.snapshot-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.snapshot-table th,
.snapshot-table td {
  text-align: left;
  padding: 8px 12px;
  border-bottom: 1px solid var(--td-component-border, #e7e7e7);
}

.snapshot-table th {
  color: var(--td-text-color-placeholder, #999);
  font-weight: 500;
}

.snapshot-actions {
  display: flex;
  gap: 12px;
  white-space: nowrap;
}

.snapshot-note {
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  display: inline-block;
  vertical-align: middle;
}

.snapshots-state {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 24px 0;
  color: var(--td-text-color-placeholder, #999);
  justify-content: center;
}

.upload-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.upload-input {
  display: none;
}

.restore-warning {
  margin: 0;
  line-height: 1.6;
}

.restore-summary-body p {
  margin: 4px 0;
}
</style>
