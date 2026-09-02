<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="upload-progress-overlay">
        <section
          class="upload-progress-modal"
          role="dialog"
          aria-modal="true"
          :aria-label="progressTitle"
        >
          <button
            v-if="!isActive"
            class="close-btn"
            type="button"
            :aria-label="t('general.close')"
            @click="handleClose"
          >
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            </svg>
          </button>

          <header class="progress-header">
            <div
              class="header-icon"
              :class="{
                'header-icon--done': !isActive && failedCount === 0,
                'header-icon--error': !isActive && failedCount > 0,
              }"
            >
              <t-icon :name="isActive ? 'upload' : failedCount > 0 ? 'error-circle-filled' : 'check-circle-filled'" />
            </div>
            <div>
              <h2>{{ progressTitle }}</h2>
              <p aria-live="polite">{{ progressSummary }}</p>
            </div>
          </header>

          <div class="overall-progress">
            <div class="overall-row">
              <span>{{ t('uploadConfirm.progress.overall') }}</span>
              <strong>{{ overallProgress }}%</strong>
            </div>
            <t-progress :percentage="overallProgress" :label="false" size="small" />
          </div>

          <ul class="progress-list">
            <li
              v-for="task in tasks"
              :key="task.uploadId"
              class="progress-item"
              :class="`progress-item--${task.status}`"
            >
              <div class="file-icon">
                <t-icon :name="getFileIcon(task.fileName)" />
              </div>
              <div class="file-progress">
                <div class="file-row">
                  <div class="file-details">
                    <span class="file-name" :title="task.fileName">{{ task.fileName }}</span>
                    <span v-if="formatFileSize(task.fileSize)" class="file-size">
                      {{ formatFileSize(task.fileSize) }}
                    </span>
                  </div>
                  <span class="file-status">{{ getStatusText(task) }}</span>
                </div>
                <t-progress :percentage="task.progress" :label="false" size="small" />
                <p v-if="task.status === 'error' && task.error" class="file-error" :title="task.error">
                  {{ task.error }}
                </p>
              </div>
            </li>
          </ul>

          <footer class="progress-footer">
            <span v-if="isActive" class="keep-open-hint">
              {{ t('uploadConfirm.progress.keepOpen') }}
            </span>
            <t-button v-else theme="primary" @click="handleClose">
              {{ t('uploadConfirm.progress.done') }}
            </t-button>
          </footer>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatFileSize, getFileIcon } from '@/utils/files'

type UploadProgressStatus = 'pending' | 'uploading' | 'success' | 'error'

interface UploadProgressTask {
  uploadId: string
  fileName: string
  fileSize?: number
  progress: number
  status: UploadProgressStatus
  error?: string
}

interface UploadProgressEventDetail {
  uploadId: string
  fileName?: string
  fileSize?: number
  progress?: number
  status?: UploadProgressStatus
  error?: string
}

const { t } = useI18n()
const visible = ref(false)
const tasks = ref<UploadProgressTask[]>([])

const clampProgress = (value: number) => Math.min(100, Math.max(0, Math.round(value)))

const completedCount = computed(() =>
  tasks.value.filter(task => task.status === 'success' || task.status === 'error').length,
)
const successCount = computed(() => tasks.value.filter(task => task.status === 'success').length)
const failedCount = computed(() => tasks.value.filter(task => task.status === 'error').length)
const isActive = computed(() => completedCount.value < tasks.value.length)
const overallProgress = computed(() => {
  if (tasks.value.length === 0) return 0
  const total = tasks.value.reduce((sum, task) => sum + task.progress, 0)
  return clampProgress(total / tasks.value.length)
})

const progressTitle = computed(() => {
  if (isActive.value) return t('uploadConfirm.progress.title')
  return failedCount.value > 0
    ? t('uploadConfirm.progress.completedWithErrors')
    : t('uploadConfirm.progress.completed')
})

const progressSummary = computed(() => {
  if (isActive.value) {
    return t('uploadConfirm.progress.runningSummary', {
      completed: completedCount.value,
      total: tasks.value.length,
    })
  }
  return t('uploadConfirm.progress.completedSummary', {
    success: successCount.value,
    failed: failedCount.value,
  })
})

const findTaskIndex = (uploadId: string) =>
  tasks.value.findIndex(task => task.uploadId === uploadId)

const upsertTask = (detail: UploadProgressEventDetail, defaultStatus: UploadProgressStatus) => {
  if (!detail.uploadId) return
  const index = findTaskIndex(detail.uploadId)
  const existing = index >= 0 ? tasks.value[index] : undefined
  const task: UploadProgressTask = {
    uploadId: detail.uploadId,
    fileName: detail.fileName || existing?.fileName || t('uploadConfirm.progress.unknownFile'),
    fileSize: detail.fileSize ?? existing?.fileSize,
    progress: typeof detail.progress === 'number'
      ? clampProgress(detail.progress)
      : existing?.progress || 0,
    status: detail.status || existing?.status || defaultStatus,
    error: detail.error ?? existing?.error,
  }
  if (index >= 0) {
    const nextTasks = [...tasks.value]
    nextTasks[index] = task
    tasks.value = nextTasks
  } else {
    tasks.value = [...tasks.value, task]
  }
}

const handleUploadStart = (event: Event) => {
  const detail = (event as CustomEvent<UploadProgressEventDetail>).detail
  if (!detail?.uploadId) return
  if (!visible.value && !isActive.value) {
    tasks.value = []
  }
  visible.value = true
  upsertTask(detail, 'pending')
}

const handleUploadProgress = (event: Event) => {
  const detail = (event as CustomEvent<UploadProgressEventDetail>).detail
  if (!detail?.uploadId || typeof detail.progress !== 'number') return
  upsertTask({ ...detail, status: 'uploading' }, 'uploading')
}

const handleUploadComplete = (event: Event) => {
  const detail = (event as CustomEvent<UploadProgressEventDetail>).detail
  if (!detail?.uploadId) return
  upsertTask({
    ...detail,
    progress: typeof detail.progress === 'number' ? detail.progress : 100,
    status: detail.status === 'error' ? 'error' : 'success',
  }, 'success')
}

const getStatusText = (task: UploadProgressTask) => {
  if (task.status === 'pending') return t('uploadConfirm.progress.pending')
  if (task.status === 'uploading') {
    return t('uploadConfirm.progress.uploading', { progress: task.progress })
  }
  if (task.status === 'error') return t('uploadConfirm.progress.failed')
  return t('uploadConfirm.progress.success')
}

const handleClose = () => {
  if (isActive.value) return
  visible.value = false
  tasks.value = []
}

onMounted(() => {
  window.addEventListener('knowledgeFileUploadStart', handleUploadStart as EventListener)
  window.addEventListener('knowledgeFileUploadProgress', handleUploadProgress as EventListener)
  window.addEventListener('knowledgeFileUploadComplete', handleUploadComplete as EventListener)
})

onUnmounted(() => {
  window.removeEventListener('knowledgeFileUploadStart', handleUploadStart as EventListener)
  window.removeEventListener('knowledgeFileUploadProgress', handleUploadProgress as EventListener)
  window.removeEventListener('knowledgeFileUploadComplete', handleUploadComplete as EventListener)
})
</script>

<style scoped lang="less">
.upload-progress-overlay {
  position: fixed;
  inset: 0;
  z-index: 3000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(4px);
}

.upload-progress-modal {
  position: relative;
  display: flex;
  flex-direction: column;
  width: min(640px, 92vw);
  max-height: min(720px, 86vh);
  overflow: hidden;
  border-radius: 12px;
  background: var(--td-bg-color-container);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.16);
}

.close-btn {
  position: absolute;
  top: 18px;
  right: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: 0;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-primary);
  }
}

.progress-header {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 24px 56px 16px 24px;

  h2 {
    margin: 0 0 4px;
    color: var(--td-text-color-primary);
    font-size: 20px;
    line-height: 28px;
  }

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: 14px;
  }
}

.header-icon {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  font-size: 22px;

  &--done {
    background: var(--td-success-color-light);
    color: var(--td-success-color);
  }

  &--error {
    background: var(--td-error-color-light);
    color: var(--td-error-color);
  }
}

.overall-progress {
  padding: 0 24px 18px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.overall-row,
.file-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.overall-row {
  margin-bottom: 8px;
  color: var(--td-text-color-secondary);
  font-size: 13px;

  strong {
    color: var(--td-text-color-primary);
  }
}

.progress-list {
  flex: 1;
  min-height: 0;
  margin: 0;
  padding: 8px 24px;
  overflow-y: auto;
  list-style: none;
}

.progress-item {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: 0;
  }

  &--error .file-status,
  &--error .file-error {
    color: var(--td-error-color);
  }

  &--success .file-status {
    color: var(--td-success-color);
  }
}

.file-icon {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-brand-color);
  font-size: 18px;
}

.file-progress {
  min-width: 0;
  flex: 1;
}

.file-row {
  margin-bottom: 7px;
}

.file-details {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: 8px;
}

.file-name {
  overflow: hidden;
  color: var(--td-text-color-primary);
  font-size: 14px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-size,
.file-status {
  flex: 0 0 auto;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.file-error {
  margin: 5px 0 0;
  overflow: hidden;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.progress-footer {
  display: flex;
  min-height: 64px;
  align-items: center;
  justify-content: flex-end;
  padding: 12px 24px;
  border-top: 1px solid var(--td-component-stroke);
}

.keep-open-hint {
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
</style>
