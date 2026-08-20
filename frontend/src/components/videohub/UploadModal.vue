<template>
  <t-dialog
    :visible="visible"
    width="min(560px, calc(100vw - 32px))"
    :close-on-overlay-click="true"
    :destroy-on-close="true"
    :footer="false"
    header="上传视频"
    dialog-class-name="video-upload-dialog"
    @close="close"
  >
    <div class="upload-dialog-inner">
      <div v-if="state === 'form'" class="upload-form">
        <t-upload
          v-model="files"
          class="upload-dropzone"
          theme="custom"
          draggable
          :auto-upload="false"
          :multiple="false"
          :max="1"
          accept="video/*"
          @validate="handleValidate"
        >
          <div class="upload-dropzone__content">
            <span class="upload-dropzone__icon"><t-icon name="folder-open" /></span>
            <p><span>选择视频</span>或拖拽到此处</p>
            <small>支持单个视频文件</small>
          </div>
        </t-upload>

        <div v-if="selectedFile" class="upload-file-card">
          <span class="upload-file-card__icon"><t-icon name="video" /></span>
          <div class="upload-file-card__meta">
            <strong>{{ selectedFile.name }}</strong>
            <span>{{ formatSize(selectedFile.size) }}</span>
          </div>
          <t-button shape="square" variant="text" aria-label="移除文件" @click="removeFile">
            <t-icon name="delete" />
          </t-button>
        </div>

        <div class="upload-actions">
          <t-button variant="outline" @click="close">取消</t-button>
          <t-button :disabled="!selectedFile" @click="submit">确认上传</t-button>
        </div>
      </div>

      <div v-else-if="state === 'uploading' || state === 'refreshing'" class="upload-state upload-state--progress">
        <div class="upload-file-card upload-file-card--progress">
          <span class="upload-file-card__icon"><t-icon name="video" /></span>
          <div class="upload-file-card__meta">
            <strong>{{ selectedFile?.name }}</strong>
            <t-progress :percentage="progress.percent" :show-info="true" />
          </div>
        </div>
        <div class="upload-actions">
          <t-button variant="outline" @click="close">{{ state === 'uploading' ? '取消上传' : '关闭' }}</t-button>
          <t-button disabled>{{ state === 'uploading' ? '上传中...' : '正在更新列表...' }}</t-button>
        </div>
      </div>

      <div v-else-if="state === 'success'" class="upload-state upload-state--success">
        <t-icon name="check-circle-filled" />
        <h3>上传成功！</h3>
      </div>

      <div v-else class="upload-state upload-state--error">
        <t-icon name="error-circle-filled" />
        <h3>上传失败</h3>
        <t-alert theme="error" :message="errorMessage" />
        <div class="upload-actions">
          <t-button variant="outline" @click="close">关闭</t-button>
          <t-button theme="danger" @click="startUpload">重试</t-button>
        </div>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { uploadVideo, type UploadCancel } from '@/api/videohub/upload'
import type { UploadProgress } from '@/types/videohub'

type UploadFileItem = { name?: string; size?: number; raw?: File; type?: string }

const props = defineProps<{ visible: boolean; afterUpload?: () => Promise<void> }>()
const emit = defineEmits<{ 'update:visible': [value: boolean] }>()
const files = ref<UploadFileItem[]>([])
const state = ref<'form' | 'uploading' | 'refreshing' | 'success' | 'error'>('form')
const progress = ref<UploadProgress>({ stage: 'uploading', percent: 0 })
const errorMessage = ref('')
let cancel: UploadCancel | null = null
let successTimer: number | undefined

const selectedFile = computed(() => {
  const item = files.value[0]
  if (!item) return null
  return {
    name: item.raw?.name || item.name || '',
    size: item.raw?.size ?? item.size ?? 0,
  }
})

function reset() {
  if (cancel) cancel.cancelled = true
  cancel = null
  if (successTimer) window.clearTimeout(successTimer)
  files.value = []
  state.value = 'form'
  progress.value = { stage: 'uploading', percent: 0 }
  errorMessage.value = ''
}

function close() {
  reset()
  emit('update:visible', false)
}

function removeFile() {
  files.value = []
}

function handleValidate() {
  MessagePlugin.warning('请选择单个视频文件')
}

function submit() {
  if (!selectedFile.value) {
    MessagePlugin.warning('请选择本地视频文件')
    return
  }
  void startUpload()
}

async function startUpload() {
  const file = selectedFile.value
  if (!file) return
  state.value = 'uploading'
  progress.value.percent = 0
  errorMessage.value = ''
  cancel = { cancelled: false }
  const currentCancel = cancel
  try {
    await uploadVideo({ file }, { onProgress: percent => { progress.value.percent = percent } }, currentCancel)
    if (currentCancel.cancelled) return
    cancel = null
    state.value = 'refreshing'
    try {
      await props.afterUpload?.()
    } catch {
      MessagePlugin.warning('视频已上传，但列表刷新失败，请稍后刷新页面')
    }
    if (!props.visible || state.value !== 'refreshing') return
    state.value = 'success'
    successTimer = window.setTimeout(close, 800)
  } catch (reason) {
    if (currentCancel.cancelled) return
    state.value = 'error'
    errorMessage.value = reason instanceof Error ? reason.message : '上传失败，请重试'
  }
}

function formatSize(size: number) {
  return size >= 1024 * 1024
    ? `${(size / 1024 / 1024).toFixed(1)} MB`
    : `${Math.max(1, Math.ceil(size / 1024))} KB`
}

watch(() => props.visible, value => { if (!value) reset() })
</script>

<style scoped>
:global(.video-upload-dialog) {
  border: var(--border-width-hairline, .5px) solid var(--color-stroke, var(--td-border-level-1-color));
  border-radius: var(--td-radius-extraLarge);
  background: var(--color-bg-popup, var(--td-bg-color-container));
  box-shadow: var(--shadow-popup, 0 8px 24px color-mix(in srgb, var(--td-text-color-primary) 10%, transparent));
  backdrop-filter: blur(20px) saturate(180%);
}
:global(.video-upload-dialog .t-dialog__header) {
  padding: 16px 24px 12px;
  border-bottom: none;
}
:global(.video-upload-dialog .t-dialog__body) { padding: 0 24px 20px; }
.upload-dialog-inner { padding: 0; }
.upload-form { display: grid; gap: 16px; color: var(--td-text-color-primary); }
.upload-dropzone { display: flex; justify-content: center; }
.upload-dropzone :deep(.t-upload__dragger) {
  width: 100%;
  max-width: 420px;
  margin: 0 auto;
  min-height: 200px;
  display: grid;
  place-items: center;
  border: 1px dashed var(--td-brand-color);
  border-radius: var(--td-radius-large);
  background: var(--td-bg-color-secondarycontainer);
  transition: border-color .2s ease, background-color .2s ease;
}
.upload-dropzone :deep(.t-upload__dragger:hover) { border-color: var(--td-brand-color-hover); background: var(--td-bg-color-container-hover); }
.upload-dropzone__content { display: grid; justify-items: center; gap: 10px; padding: 28px 20px; text-align: center; }
.upload-dropzone__icon, .upload-file-card__icon { display: grid; place-items: center; color: var(--td-brand-color); background: var(--td-brand-color-light); }
.upload-dropzone__icon { width: 64px; height: 56px; border-radius: var(--td-radius-large); font-size: 32px; }
.upload-dropzone__content p { margin: 4px 0 0; color: var(--td-text-color-primary); font-size: 15px; }
.upload-dropzone__content p span { color: var(--td-brand-color); font-weight: 500; text-decoration: underline; text-underline-offset: 3px; }
.upload-dropzone__content small { color: var(--td-text-color-placeholder); font-size: 12px; }
.upload-file-card { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 12px; padding: 14px 16px; border: 1px solid var(--td-component-stroke); border-radius: var(--td-radius-large); background: var(--td-bg-color-container); }
.upload-file-card__icon { width: 38px; height: 38px; border-radius: var(--td-radius-medium); font-size: 20px; }
.upload-file-card__meta { min-width: 0; display: grid; gap: 5px; }
.upload-file-card__meta strong { overflow: hidden; color: var(--td-text-color-primary); font-size: 14px; font-weight: 500; text-overflow: ellipsis; white-space: nowrap; }
.upload-file-card__meta span { color: var(--td-text-color-secondary); font-size: 12px; }
.upload-file-card__meta :deep(.t-progress) { width: 100%; }
.upload-file-card__percent { color: var(--td-text-color-primary); font-size: 14px; font-weight: 500; }
.upload-actions { display: flex; justify-content: center; gap: 8px; margin-top: 8px; }
.upload-state { display: grid; justify-items: center; gap: 12px; padding: 20px 0; color: var(--td-text-color-primary); text-align: center; }
.upload-state--progress { justify-items: stretch; padding: 0; }
.upload-state h3, .upload-state p { margin: 0; }
.upload-state p { max-width: 100%; overflow: hidden; color: var(--td-text-color-secondary); font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.upload-state > .t-icon { font-size: 48px; animation: state-in .24s ease-out; }
.upload-state--success > .t-icon { color: var(--td-success-color); }
.upload-state--error > .t-icon { color: var(--td-error-color); }
.upload-state--error :deep(.t-alert) { width: 100%; text-align: left; }
@keyframes state-in { from { opacity: 0; transform: scale(.7); } to { opacity: 1; transform: scale(1); } }
@media (max-width: 600px) {
  :global(.video-upload-dialog .t-dialog__header) { padding: 14px 16px 10px; }
  :global(.video-upload-dialog .t-dialog__body) { padding: 0 16px 16px; }
  .upload-dropzone :deep(.t-upload__dragger) { min-height: 180px; max-width: 100%; }
}
</style>
