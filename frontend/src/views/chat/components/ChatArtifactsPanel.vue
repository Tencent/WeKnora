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
      <ArtifactFileIcon class="artifact-panel-header-icon" :file-name="previewItem.file_name" />
      <div class="artifact-panel-header-title" :title="previewItem.file_name">{{ previewItem.file_name }}</div>
      <t-button
        class="artifact-download"
        variant="text"
        shape="square"
        size="small"
        :title="$t('agent.artifactDrawer.download')"
        :aria-label="$t('agent.artifactDrawer.download')"
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
      <div class="artifact-filters">
        <div class="artifact-scope" role="group" :aria-label="$t('chat.sandbox.artifactScope')">
          <button
            v-if="focusedMessageId"
            type="button"
            :aria-pressed="scope === 'current'"
            @click="scope = 'current'"
          >
            {{ $t('chat.sandbox.artifactsCurrent') }}
            <span>{{ currentItems.length }}</span>
          </button>
          <button type="button" :aria-pressed="scope === 'all'" @click="scope = 'all'">
            {{ $t('chat.sandbox.artifactsAll') }}
            <span>{{ items.length }}</span>
          </button>
        </div>
        <t-input
          v-model="searchQuery"
          class="artifact-search"
          :placeholder="$t('chat.sandbox.artifactsSearch')"
          :aria-label="$t('chat.sandbox.artifactsSearch')"
          clearable
        >
          <template #prefix-icon><t-icon name="search" size="16px" /></template>
        </t-input>
      </div>
      <div v-if="collecting" class="artifact-panel-banner">
        <t-icon name="loading" class="artifact-panel-banner-spin" />
        <span>{{ $t('agent.artifactDrawer.collecting') }}</span>
      </div>
      <div v-if="!visibleItems.length" class="artifact-panel-empty">
        <t-icon name="search" size="24px" />
        <span>{{ $t('chat.sandbox.artifactsNoMatches') }}</span>
      </div>
      <ul v-show="visibleItems.length" ref="listRef" class="artifact-list">
        <li
          v-for="item in visibleItems"
          :key="`${item.messageId}:${item.index}-${item.file_name}`"
          class="artifact-item is-previewable"
          :data-message-id="item.messageId"
          :data-artifact-index="item.index"
          @click="openPreview(item)"
        >
          <button type="button" class="artifact-open" :title="$t('agent.artifactDrawer.preview')">
            <ArtifactFileIcon :file-name="item.file_name" />
            <span class="artifact-body">
              <span class="artifact-name" :title="item.file_name">{{ item.file_name }}</span>
              <span class="artifact-meta">
                <span>{{ formatArtifactSize(item.file_size) }}</span>
                <span class="artifact-meta-sep">·</span>
                <span>{{ formatArtifactDateTime(item.created_at) }}</span>
              </span>
            </span>
          </button>
          <t-button
            class="artifact-download"
            variant="text"
            shape="square"
            size="small"
            :title="$t('agent.artifactDrawer.download')"
            :aria-label="$t('agent.artifactDrawer.download')"
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
import { resolveFilePreviewExt } from '@/utils/filePreview'
import {
  formatArtifactDateTime,
  formatArtifactSize,
  type SessionArtifactItem,
} from '@/utils/sessionArtifacts'
import { useChatSandboxPanel, type ArtifactPanelFocusState } from '@/composables/useChatSandboxPanel'
import DocumentPreview from '@/components/document-preview.vue'
import ArtifactFileIcon from './ArtifactFileIcon.vue'

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
const scope = ref<'current' | 'all'>('all')
const searchQuery = ref('')
const currentItems = computed(() => props.items.filter((item) => item.messageId === focusedMessageId.value))
const visibleItems = computed(() => {
  const items = scope.value === 'current' ? currentItems.value : props.items
  const query = searchQuery.value.trim().toLocaleLowerCase()
  return query ? items.filter((item) => item.file_name.toLocaleLowerCase().includes(query)) : items
})

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
    const container = listRef.value
    if (!container) return
    const rows = container.querySelectorAll<HTMLElement>('[data-message-id]')
    for (const row of rows) {
      if (row.getAttribute('data-message-id') === focus.messageId) {
        // Keep positioning inside the list: scrollIntoView can also scroll the
        // outer page horizontally while the fixed panel is sliding into view.
        const itemRect = row.getBoundingClientRect()
        const containerRect = container.getBoundingClientRect()
        let nextTop: number | null = null
        if (itemRect.top < containerRect.top) {
          nextTop = container.scrollTop + itemRect.top - containerRect.top
        } else if (itemRect.bottom > containerRect.bottom) {
          nextTop = container.scrollTop + itemRect.bottom - containerRect.bottom
        }
        if (nextTop !== null) {
          container.scrollTo({ top: Math.max(0, nextTop), behavior: 'instant' })
        }
        break
      }
    }
  })
}

watch(
  () => panel?.artifactFocus.value,
  (focus) => {
    scope.value = focus?.messageId ? 'current' : 'all'
    searchQuery.value = ''
    applyFocus(focus)
  },
  { immediate: true },
)

watch(
  () => props.sessionId,
  () => {
    previewItem.value = null
    focusedMessageId.value = null
    scope.value = 'all'
    searchQuery.value = ''
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
    if (scope.value === 'current' && !searchQuery.value.trim()) {
      applyFocus(panel?.artifactFocus.value)
    }
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
  width: 26px;
  height: 32px;
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
  padding: 0 4px 12px;
  list-style: none;
  overflow: auto;
  flex: 1;
  min-height: 0;
}

.artifact-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 8px;
  border-radius: 8px;

  &.is-previewable {
    cursor: pointer;
  }

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

}

.artifact-filters {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px 12px 12px;
}

.artifact-scope {
  display: inline-flex;
  align-self: flex-start;
  gap: 2px;
  padding: 3px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);

  button {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    padding: 5px 10px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--td-text-color-secondary);
    font: inherit;
    font-size: 12px;
    cursor: pointer;

    &[aria-pressed='true'] {
      background: var(--td-bg-color-container);
      color: var(--td-text-color-primary);
      box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }

    span {
      color: var(--td-text-color-placeholder);
      font-size: 11px;
      font-variant-numeric: tabular-nums;
    }
  }
}

.artifact-search :deep(.t-input) {
  border-radius: 7px;
}

.artifact-body {
  flex: 1;
  min-width: 0;
}

.artifact-open {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;

  &:focus-visible {
    outline: 2px solid var(--td-text-color-secondary);
    outline-offset: 4px;
  }
}

.artifact-name {
  display: block;
  font-size: 13px;
  font-weight: 500;
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

.artifact-download.t-button {
  flex-shrink: 0;
  width: 30px;
  height: 30px;
  border-radius: 7px;
  color: var(--td-text-color-secondary);
  transition: background-color 0.15s ease, color 0.15s ease;

  &:not(.t-is-disabled):not(.t-is-loading):hover {
    background: color-mix(in srgb, var(--td-text-color-primary) 10%, var(--td-bg-color-container));
    color: var(--td-text-color-primary);
  }

  &:not(.t-is-disabled):not(.t-is-loading):active {
    background: color-mix(in srgb, var(--td-text-color-primary) 16%, var(--td-bg-color-container));
  }

  &:focus-visible {
    outline: 2px solid var(--td-text-color-secondary);
    outline-offset: 2px;
  }

  :deep(.t-button__icon) {
    margin: 0;
  }
}
</style>
