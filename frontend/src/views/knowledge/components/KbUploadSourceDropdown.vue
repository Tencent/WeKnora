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
          size="small"
        >
          <template #icon><t-icon :name="triggerIcon" size="16px" /></template>
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
        <t-textarea
          v-model="urlInputValue"
          :placeholder="t('knowledgeBase.urlPlaceholder')"
          :autosize="{ minRows: 3, maxRows: 10 }"
          autofocus
        />
        <div v-if="urlPreviewParts.length > 0" class="url-input-preview">
          <span
            v-for="part in urlPreviewParts"
            :key="part.key"
            :class="['url-input-preview-item', { 'is-warning': part.warning }]"
          >{{ part.text }}</span>
        </div>
        <div class="url-input-tip">{{ t('knowledgeBase.urlTip') }}</div>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, Icon as TIcon } from 'tdesign-vue-next'
import { filterUploadFiles } from '../utils/uploadSources'
import { parseImportUrls, summarizeImportUrls } from '@/utils/youtube'

const props = withDefaults(defineProps<{
  acceptFileTypes?: string
  supportedFileTypes?: string[]
  includeManual?: boolean
  triggerIcon?: string
  triggerClass?: string
  dataGuide?: string
  tooltip?: string
  placement?: 'top' | 'bottom' | 'bottom-right' | 'bottom-left'
}>(), {
  acceptFileTypes: '',
  supportedFileTypes: () => [],
  includeManual: false,
  triggerIcon: 'file-add',
  triggerClass: '',
  dataGuide: '',
  tooltip: '',
  placement: 'bottom-right',
})

const emit = defineEmits<{
  files: [files: File[]]
  urls: [urls: string[]]
  manual: []
}>()

const { t } = useI18n()

const fileInputRef = ref<HTMLInputElement | null>(null)
const folderInputRef = ref<HTMLInputElement | null>(null)
const urlDialogVisible = ref(false)
const urlInputValue = ref('')

// Live preview of how the pasted links will be imported, so it is visible
// before confirming which lines are recognised as YouTube videos or playlists.
const urlPreviewParts = computed(() => {
  const summary = summarizeImportUrls(urlInputValue.value)
  const parts: Array<{ key: string; text: string; warning?: boolean }> = []
  if (summary.webPages > 0) {
    parts.push({ key: 'web', text: t('knowledgeBase.urlPreviewWebPages', { count: summary.webPages }) })
  }
  if (summary.youTubeVideos > 0) {
    parts.push({ key: 'video', text: t('knowledgeBase.urlPreviewYouTubeVideos', { count: summary.youTubeVideos }) })
  }
  if (summary.youTubePlaylists > 0) {
    parts.push({
      key: 'playlist',
      text: t('knowledgeBase.urlPreviewYouTubePlaylists', { count: summary.youTubePlaylists }),
    })
  }
  if (summary.invalid > 0) {
    parts.push({ key: 'invalid', text: t('knowledgeBase.urlPreviewInvalid', { count: summary.invalid }), warning: true })
  }
  return parts
})

const tooltipText = computed(() => props.tooltip || t('knowledgeBase.addDocument'))

const dropdownOptions = computed(() => {
  const options = [
    {
      content: t('upload.uploadDocument'),
      value: 'upload',
      prefixIcon: () => h(TIcon, { name: 'upload', size: '16px' }),
    },
    {
      content: t('upload.uploadFolder'),
      value: 'uploadFolder',
      prefixIcon: () => h(TIcon, { name: 'folder-add', size: '16px' }),
    },
    {
      content: t('knowledgeBase.importURL'),
      value: 'importURL',
      prefixIcon: () => h(TIcon, { name: 'link', size: '16px' }),
    },
  ]
  if (props.includeManual) {
    options.push({
      content: t('upload.onlineEdit'),
      value: 'manualCreate',
      prefixIcon: () => h(TIcon, { name: 'edit', size: '16px' }),
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
      openUrlDialog()
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
  if (validFiles.length === 0) {
    if (skippedCount > 0) {
      MessagePlugin.warning(t(emptyAllSkippedKey))
    }
    return false
  }
  if (videoFilteredCount > 0) {
    MessagePlugin.warning(t('knowledgeBase.videosFilteredNoVLM', { count: videoFilteredCount }))
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
  })

  if (!notifyFilterResult(result, 'knowledgeBase.allFilesSkippedNoEngine')) {
    input.value = ''
    return
  }

  emit('files', result.validFiles)
  input.value = ''
}

const handleUrlDialogConfirm = () => {
  if (!urlInputValue.value.trim()) {
    MessagePlugin.warning(t('knowledgeBase.urlRequired'))
    return
  }
  const { urls, invalid } = parseImportUrls(urlInputValue.value)
  if (urls.length === 0) {
    MessagePlugin.warning(t('knowledgeBase.invalidURL'))
    return
  }
  if (invalid.length > 0) {
    MessagePlugin.warning(t('knowledgeBase.invalidURLsSkipped', { count: invalid.length }))
  }
  urlDialogVisible.value = false
  urlInputValue.value = ''
  emit('urls', urls)
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
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .url-input-preview {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 8px;
  }

  .url-input-preview-item {
    padding: 2px 8px;
    border-radius: 10px;
    font-size: 12px;
    line-height: 18px;
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);

    &.is-warning {
      color: var(--td-warning-color);
      background: var(--td-warning-color-light);
    }
  }

  .url-input-tip {
    margin-top: 8px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-placeholder);
  }
}
</style>
