<template>
  <div class="chat-artifacts-panel">
    <div v-if="previewItem" class="artifact-panel-header">
      <t-button
        class="artifact-back"
        variant="text"
        shape="square"
        size="small"
        :title="$t('agent.artifactDrawer.previewBack')"
        :aria-label="$t('agent.artifactDrawer.previewBack')"
        @click="closePreview"
      >
        <template #icon>
          <t-icon name="chevron-left" size="18px" />
        </template>
      </t-button>
      <div class="artifact-panel-header-icon">
        <t-icon :name="getFileIcon(previewItem.file_name)" />
      </div>
      <div class="artifact-panel-header-title" :title="previewItem.file_name">{{ previewItem.file_name }}</div>
      <t-button
        class="artifact-download"
        variant="text"
        shape="square"
        size="small"
        :title="$t('agent.artifactDrawer.download')"
        :loading="isDownloading(previewItem)"
        @click="handleDownload(previewItem)"
      >
        <template #icon>
          <t-icon name="download" size="16px" />
        </template>
      </t-button>
    </div>

    <div v-if="previewItem" class="artifact-preview-body">
      <DocumentPreview
        :session-id="sessionId"
        :message-id="previewItem.messageId"
        :artifact-index="previewItem.index"
        :file-type="previewFileType"
        :file-name="previewItem.file_name"
        :active="active"
        fill-height
      />
    </div>

    <div v-else-if="collecting && !items.length" class="artifact-panel-empty">
      <t-loading size="small" />
      <span>{{ $t('agent.artifactDrawer.collecting') }}</span>
    </div>
    <div v-else-if="!items.length" class="artifact-panel-empty">
      <t-icon name="folder-open" size="32px" />
      <span>{{ $t('chat.sandbox.artifactsEmpty') }}</span>
    </div>
    <template v-else>
      <div v-if="collecting" class="artifact-panel-banner">
        <t-icon name="loading" class="artifact-panel-banner-spin" />
        <span>{{ $t('agent.artifactDrawer.collecting') }}</span>
      </div>
      <ul ref="listRef" class="artifact-list">
        <li
          v-for="item in items"
          :key="`${item.messageId}:${item.index}-${item.file_name}`"
          class="artifact-item is-previewable"
          :class="{ 'is-focused': isFocused(item) }"
          :data-message-id="item.messageId"
          :data-artifact-index="item.index"
          @click="openPreview(item)"
        >
          <span class="artifact-icon">
            <t-icon :name="getFileIcon(item.file_name)" />
          </span>
          <div class="artifact-body">
            <div class="artifact-name" :title="item.file_name">{{ item.file_name }}</div>
            <div class="artifact-meta">
              <span>{{ formatArtifactSize(item.file_size) }}</span>
              <span class="artifact-meta-sep">·</span>
              <span>{{ formatArtifactDateTime(item.created_at) }}</span>
            </div>
          </div>
          <t-button
            class="artifact-preview-btn"
            variant="text"
            shape="square"
            size="small"
            :title="$t('agent.artifactDrawer.preview')"
            @click.stop="openPreview(item)"
          >
            <template #icon>
              <t-icon name="browse" size="16px" />
            </template>
          </t-button>
          <t-button
            class="artifact-download"
            variant="text"
            shape="square"
            size="small"
            :title="$t('agent.artifactDrawer.download')"
            :loading="isDownloading(item)"
            @click.stop="handleDownload(item)"
          >
            <template #icon>
              <t-icon name="download" size="16px" />
            </template>
          </t-button>
        </li>
      </ul>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { downloadArtifact } from '@/api/chat'
import { getFileIcon } from '@/utils/files'
import { resolveFilePreviewExt } from '@/utils/filePreview'
import {
  formatArtifactDateTime,
  formatArtifactSize,
  type SessionArtifactItem,
} from '@/utils/sessionArtifacts'
import { useChatSandboxPanel, type ArtifactPanelFocusState } from '@/composables/useChatSandboxPanel'
import DocumentPreview from '@/components/document-preview.vue'

const props = withDefaults(
  defineProps<{
    sessionId: string
    items: SessionArtifactItem[]
    collecting?: boolean
    active?: boolean
  }>(),
  {
    collecting: false,
    active: true,
  },
)

const { t } = useI18n()
const panel = useChatSandboxPanel()
const listRef = ref<HTMLElement | null>(null)
const previewItem = ref<SessionArtifactItem | null>(null)
const downloading = reactive<Record<string, boolean>>({})
const focusedMessageId = ref<string | null>(null)

const previewFileType = computed(() => {
  const item = previewItem.value
  if (!item) return ''
  return resolveFilePreviewExt(item.file_name, item.file_type)
})

function downloadKey(item: SessionArtifactItem): string {
  return `${item.messageId}:${item.index}`
}

function isDownloading(item: SessionArtifactItem): boolean {
  return !!downloading[downloadKey(item)]
}

function isFocused(item: SessionArtifactItem): boolean {
  return !!focusedMessageId.value && item.messageId === focusedMessageId.value
}

function findItem(messageId: string, previewIndex?: number | null): SessionArtifactItem | undefined {
  if (previewIndex != null && Number.isInteger(previewIndex)) {
    return props.items.find((item) => item.messageId === messageId && item.index === previewIndex)
  }
  return props.items.find((item) => item.messageId === messageId)
}

function applyFocus(focus: ArtifactPanelFocusState | null | undefined) {
  if (!focus?.messageId) return
  focusedMessageId.value = focus.messageId
  const wantsPreview = focus.previewIndex != null && Number.isInteger(focus.previewIndex)
  if (wantsPreview) {
    const target = findItem(focus.messageId, focus.previewIndex)
    if (target) previewItem.value = target
    return
  }
  previewItem.value = null
  void nextTick(() => {
    const rows = listRef.value?.querySelectorAll<HTMLElement>('[data-message-id]')
    if (!rows) return
    for (const row of rows) {
      if (row.getAttribute('data-message-id') === focus.messageId) {
        row.scrollIntoView({ block: 'nearest' })
        break
      }
    }
  })
}

watch(
  () => panel?.artifactFocus.value,
  (focus) => applyFocus(focus),
  { immediate: true },
)

watch(
  () => props.sessionId,
  () => {
    previewItem.value = null
    focusedMessageId.value = null
    panel?.clearArtifactFocus()
  },
)

watch(
  () => props.items,
  (items) => {
    const current = previewItem.value
    if (current) {
      const next = items.find(
        (item) => item.messageId === current.messageId && item.index === current.index,
      )
      if (!next) previewItem.value = null
      return
    }
    applyFocus(panel?.artifactFocus.value)
  },
)

function openPreview(item: SessionArtifactItem) {
  previewItem.value = item
}

function closePreview() {
  previewItem.value = null
}

async function handleDownload(item: SessionArtifactItem) {
  if (!props.sessionId || !item.messageId) {
    MessagePlugin.error(t('agent.artifactDrawer.downloadFailed'))
    return
  }
  const key = downloadKey(item)
  downloading[key] = true
  try {
    const blob = await downloadArtifact(props.sessionId, item.messageId, item.index)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = item.file_name || 'artifact'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err) {
    console.error('[ChatArtifactsPanel] download failed:', err)
    MessagePlugin.error(t('agent.artifactDrawer.downloadFailed'))
  } finally {
    downloading[key] = false
  }
}
</script>

<style scoped lang="less">
.chat-artifacts-panel {
  flex: 1;
  min-height: 0;
  width: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.artifact-panel-header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 8px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.artifact-back {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);

  :deep(.t-button__icon) {
    margin: 0;
  }
}

.artifact-panel-header-icon {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(7, 192, 95, 0.1);
  color: var(--td-brand-color);
  font-size: 15px;
}

.artifact-panel-header-title {
  min-width: 0;
  flex: 1;
  font-size: 13px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.artifact-preview-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 8px;
}

.artifact-panel-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 32px 16px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
  text-align: center;
}

.artifact-panel-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  border-bottom: 1px solid var(--td-component-stroke);
}

.artifact-panel-banner-spin {
  animation: artifact-panel-spin 0.8s linear infinite;
}

@keyframes artifact-panel-spin {
  to {
    transform: rotate(360deg);
  }
}

.artifact-list {
  margin: 0;
  padding: 4px 8px 12px;
  list-style: none;
  overflow: auto;
  flex: 1;
  min-height: 0;
}

.artifact-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 6px;
  border-bottom: 1px solid var(--td-component-stroke);
  border-radius: 8px;

  &:last-child {
    border-bottom: none;
  }

  &.is-previewable {
    cursor: pointer;
  }

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:hover .artifact-icon {
    color: var(--td-brand-color);
  }

  &.is-focused {
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
  }
}

.artifact-icon {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  border-radius: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  transition: color 0.15s ease;
}

.artifact-body {
  flex: 1;
  min-width: 0;
}

.artifact-name {
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0.01em;
  line-height: 1.35;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.artifact-meta {
  margin-top: 2px;
  font-size: 12px;
  line-height: 1.3;
  color: var(--td-text-color-placeholder);
  display: flex;
  align-items: center;
  gap: 4px;
}

.artifact-meta-sep {
  opacity: 0.6;
}

.artifact-preview-btn,
.artifact-download {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);

  :deep(.t-button__icon) {
    margin: 0;
  }
}
</style>
