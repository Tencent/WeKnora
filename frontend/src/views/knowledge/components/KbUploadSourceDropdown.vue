<template>
  <div class="kb-upload-source-dropdown">
    <input
      ref="fileInputRef"
      type="file"
      class="hidden-file-input"
      multiple
      :accept="acceptFileTypes || undefined"
      @change="(e) => handleFilesChange(e, false)"
    />
    <input
      ref="folderInputRef"
      type="file"
      class="hidden-file-input"
      webkitdirectory
      multiple
      @change="(e) => handleFilesChange(e, true)"
    />

    <t-tooltip :content="tooltipText" placement="top">
      <t-dropdown
        :options="dropdownOptions"
        trigger="click"
        :placement="placement"
        @click="handleActionSelect"
      >
        <t-button
          variant="text"
          theme="default"
          :class="['kb-upload-source-trigger', triggerClass]"
          :data-guide="dataGuide || undefined"
          :aria-label="tooltipText"
          size="small"
        >
          <template #icon>
            <component v-if="triggerIconComponent" :is="triggerIconComponent" size="18px" :stroke-width="1.7" class="kb-source-icon" />
            <t-icon v-else :name="triggerIcon" size="18px" />
          </template>
          <span v-if="triggerLabel">{{ triggerLabel }}</span>
        </t-button>
      </t-dropdown>
    </t-tooltip>

    <t-dialog
      v-model:visible="urlDialogVisible"
      :header="t('knowledgeBase.importURLTitle')"
      :confirm-btn="{ content: t('common.confirm'), theme: 'primary' }"
      :cancel-btn="{ content: t('common.cancel') }"
      width="500px"
      @confirm="handleUrlDialogConfirm"
      @cancel="handleUrlDialogCancel"
    >
      <div class="url-import-form">
        <div class="url-input-label">{{ t('knowledgeBase.urlLabel') }}</div>
        <t-input
          v-model="urlInputValue"
          :placeholder="t('knowledgeBase.urlPlaceholder')"
          clearable
          autofocus
          @enter="handleUrlDialogConfirm"
        />
        <div class="url-input-tip">{{ t('knowledgeBase.urlTip') }}</div>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { AddIcon, FileAddIcon, UploadIcon, FolderAddIcon, LinkIcon, EditIcon } from 'tdesign-icons-vue-next'
import { filterUploadFiles, getUploadFileExt, UPLOAD_VIDEO_EXTENSIONS } from '../utils/uploadSources'

const props = withDefaults(defineProps<{
  acceptFileTypes?: string
  supportedFileTypes?: string[]
  includeManual?: boolean
  triggerIcon?: string
  triggerLabel?: string
  triggerClass?: string
  dataGuide?: string
  tooltip?: string
  placement?: 'top' | 'bottom' | 'bottom-right' | 'bottom-left'
  /** KB `video_config.enabled`: when true the picker admits video files. */
  allowVideo?: boolean
  /** `isVideoParsingReady(kbInfo)`: false only triggers an advisory warning. */
  videoParseReady?: boolean
}>(), {
  acceptFileTypes: '',
  supportedFileTypes: () => [],
  includeManual: false,
  triggerIcon: 'file-add',
  triggerClass: '',
  dataGuide: '',
  tooltip: '',
  placement: 'bottom-right',
  allowVideo: false,
  videoParseReady: true,
})

const emit = defineEmits<{
  files: [files: File[]]
  url: [url: string]
  manual: []
}>()

const { t } = useI18n()

const fileInputRef = ref<HTMLInputElement | null>(null)
const folderInputRef = ref<HTMLInputElement | null>(null)
const urlDialogVisible = ref(false)
const urlInputValue = ref('')

const tooltipText = computed(() => props.tooltip || t('knowledgeBase.addDocument'))

const triggerIconComponent = computed(() => ({
  add: AddIcon,
  'file-add': FileAddIcon,
  upload: UploadIcon,
}[props.triggerIcon]));

const sourceIconProps = { size: '18px', strokeWidth: 1.7, class: 'kb-source-icon' };

const dropdownOptions = computed(() => {
  const options = [
    {
      content: t('upload.uploadDocument'),
      value: 'upload',
      prefixIcon: () => h(FileAddIcon, sourceIconProps),
    },
    {
      content: t('upload.uploadFolder'),
      value: 'uploadFolder',
      prefixIcon: () => h(FolderAddIcon, sourceIconProps),
    },
    {
      content: t('knowledgeBase.importURL'),
      value: 'importURL',
      prefixIcon: () => h(LinkIcon, sourceIconProps),
    },
  ]
  if (props.includeManual) {
    options.push({
      content: t('upload.onlineEdit'),
      value: 'manualCreate',
      prefixIcon: () => h(EditIcon, sourceIconProps),
    })
  }
  return options
})

const handleActionSelect = (data: { value: string }) => {
  switch (data.value) {
    case 'upload':
      fileInputRef.value?.click()
      break
    case 'uploadFolder':
      folderInputRef.value?.click()
      break
    case 'importURL':
      urlInputValue.value = ''
      urlDialogVisible.value = true
      break
    case 'manualCreate':
      emit('manual')
      break
    default:
      break
  }
}

const notifyFilterResult = (result: ReturnType<typeof filterUploadFiles>, emptyAllSkippedKey: string) => {
  const { validFiles, skippedCount, videoFilteredCount } = result
  // Announce skipped videos first: when the selection was videos only there is
  // nothing to upload, and the hint about where to enable parsing is the whole
  // message the user needs. Returning early before this used to say nothing.
  if (videoFilteredCount > 0) {
    MessagePlugin.warning(t('knowledgeBase.videosFilteredParsingDisabled', { count: videoFilteredCount }))
  }
  if (validFiles.length === 0) {
    if (skippedCount > 0) {
      MessagePlugin.warning(t(emptyAllSkippedKey))
    }
    return false
  }
  if (skippedCount > 0) {
    MessagePlugin.warning(t('knowledgeBase.filesSkippedNoEngine', { count: skippedCount }))
  }
  return true
}

const handleFilesChange = (event: Event, fromFolder: boolean) => {
  const input = event.target as HTMLInputElement
  const files = input.files
  if (!files || files.length === 0) return

  const result = filterUploadFiles(files, {
    supportedFileTypes: props.supportedFileTypes,
    fromFolder,
    multiFile: files.length > 1,
    allowVideo: props.allowVideo,
  })

  if (!notifyFilterResult(result, 'knowledgeBase.allFilesSkippedNoEngine')) {
    input.value = ''
    return
  }

  // The switch is on, so the menu admits videos — but the server rejects them
  // when the selected mode has no ASR/VLM model yet. Warn before the upload
  // (never block: the KB detail can be stale, and the server is authoritative).
  const hasVideo = result.validFiles.some(file => UPLOAD_VIDEO_EXTENSIONS.includes(getUploadFileExt(file)))
  if (props.allowVideo && !props.videoParseReady && hasVideo) {
    MessagePlugin.warning(t('knowledgeBase.videoModelNotConfigured'))
  }

  emit('files', result.validFiles)
  input.value = ''
}

const handleUrlDialogConfirm = () => {
  const url = urlInputValue.value.trim()
  if (!url) {
    MessagePlugin.warning(t('knowledgeBase.urlRequired'))
    return
  }
  try {
    new URL(url)
  } catch {
    MessagePlugin.warning(t('knowledgeBase.invalidURL'))
    return
  }
  urlDialogVisible.value = false
  urlInputValue.value = ''
  emit('url', url)
}

const handleUrlDialogCancel = () => {
  urlDialogVisible.value = false
  urlInputValue.value = ''
}

const openUrlDialog = () => {
  urlInputValue.value = ''
  urlDialogVisible.value = true
}

defineExpose({ openUrlDialog })
</script>

<style lang="less" scoped>
.hidden-file-input {
  position: absolute;
  width: 0;
  height: 0;
  opacity: 0;
  pointer-events: none;
}

.kb-upload-source-trigger {
  color: var(--td-text-color-secondary);

  &:hover {
    color: var(--td-brand-color);
  }
}

.url-import-form {
  .url-input-label {
    margin-bottom: 8px;
    font-size: var(--app-text-base);
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .url-input-tip {
    margin-top: 8px;
    font-size: var(--app-text-sm);
    line-height: 1.5;
    color: var(--td-text-color-placeholder);
  }
}
</style>

<style lang="less">
// Use consistent rounded strokes for this small family of document-source actions.
.kb-source-icon path {
  stroke-linecap: round;
  stroke-linejoin: round;
}
</style>
