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
      width="520px"
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

        <div class="url-crawl-section">
          <div class="url-crawl-header">
            <t-switch v-model="crawlEnabled" size="medium" />
            <span class="url-crawl-title">{{ t('knowledgeBase.crawlEnabledLabel') }}</span>
          </div>
          <div v-if="crawlEnabled" class="url-crawl-options">
            <div class="url-crawl-row">
              <span class="url-crawl-label">{{ t('knowledgeBase.crawlDepthLabel') }}</span>
              <t-select
                v-model="crawlDepth"
                :options="crawlDepthOptions"
                :style="{ width: '120px' }"
              />
            </div>
            <div class="url-crawl-tip">{{ t('knowledgeBase.crawlTip') }}</div>
          </div>
        </div>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, Icon as TIcon } from 'tdesign-vue-next'
import { filterUploadFiles } from '../utils/uploadSources'

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
  url: [payload: { url: string; crawlDepth: number }]
  manual: []
}>()

const { t } = useI18n()

const fileInputRef = ref<HTMLInputElement | null>(null)
const folderInputRef = ref<HTMLInputElement | null>(null)
const urlDialogVisible = ref(false)
const urlInputValue = ref('')
const crawlEnabled = ref(false)
const crawlDepth = ref(1)

const crawlDepthOptions = computed(() => [
  { label: t('knowledgeBase.crawlDepth1'), value: 1 },
  { label: t('knowledgeBase.crawlDepth2'), value: 2 },
  { label: t('knowledgeBase.crawlDepth3'), value: 3 },
])

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
      urlInputValue.value = ''
      crawlEnabled.value = false
      crawlDepth.value = 1
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
  const depth = crawlEnabled.value ? crawlDepth.value : 0
  urlDialogVisible.value = false
  urlInputValue.value = ''
  emit('url', { url, crawlDepth: depth })
}

const handleUrlDialogCancel = () => {
  urlDialogVisible.value = false
  urlInputValue.value = ''
  crawlEnabled.value = false
  crawlDepth.value = 1
}

const openUrlDialog = () => {
  urlInputValue.value = ''
  crawlEnabled.value = false
  crawlDepth.value = 1
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

  .url-input-tip {
    margin-top: 8px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-placeholder);
  }

  .url-crawl-section {
    margin-top: 16px;
    padding-top: 16px;
    border-top: 1px solid var(--td-component-stroke);

    .url-crawl-header {
      display: flex;
      align-items: center;
      gap: 10px;
    }

    .url-crawl-title {
      font-size: 14px;
      font-weight: 500;
      color: var(--td-text-color-primary);
    }

    .url-crawl-options {
      margin-top: 12px;
      padding: 12px;
      background: var(--td-bg-color-secondarycontainer);
      border-radius: 6px;
    }

    .url-crawl-row {
      display: flex;
      align-items: center;
      gap: 12px;
    }

    .url-crawl-label {
      font-size: 13px;
      color: var(--td-text-color-secondary);
    }

    .url-crawl-tip {
      margin-top: 8px;
      font-size: 12px;
      line-height: 1.5;
      color: var(--td-text-color-placeholder);
    }
  }
}
</style>
