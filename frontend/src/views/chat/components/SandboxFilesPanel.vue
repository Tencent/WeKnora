<template>
  <section class="sandbox-files" :aria-label="t('chat.sandbox.filesTitle')">
    <header class="sandbox-files__toolbar">
      <nav class="sandbox-files__breadcrumbs" :aria-label="t('chat.sandbox.filesBreadcrumbs')">
        <button type="button" :disabled="!currentPath" @click="openDirectory('')">
          {{ t('chat.sandbox.filesRoot') }}
        </button>
        <template v-for="crumb in breadcrumbs" :key="crumb.path">
          <t-icon name="chevron-right" size="14px" aria-hidden="true" />
          <button type="button" @click="openDirectory(crumb.path)">{{ crumb.name }}</button>
        </template>
      </nav>
      <div class="sandbox-files__actions">
        <input
          ref="uploadInput"
          class="sandbox-files__upload-input"
          type="file"
          multiple
          :disabled="busy"
          @change="handleUpload"
        />
        <t-button
          variant="text"
          shape="square"
          size="small"
          :disabled="busy"
          :title="t('chat.sandbox.filesUpload')"
          :aria-label="t('chat.sandbox.filesUpload')"
          @click="uploadInput?.click()"
        >
          <template #icon><t-icon name="upload" /></template>
        </t-button>
        <t-button
          variant="text"
          shape="square"
          size="small"
          :loading="loading"
          :title="t('chat.sandbox.filesRefresh')"
          :aria-label="t('chat.sandbox.filesRefresh')"
          @click="refresh"
        >
          <template #icon><t-icon name="refresh" /></template>
        </t-button>
      </div>
    </header>

    <div v-if="loading && !loaded" class="sandbox-files__state">
      <t-loading size="small" />
      <span>{{ t('chat.sandbox.filesLoading') }}</span>
    </div>
    <div v-else-if="errorMessage" class="sandbox-files__state is-error">
      <t-icon name="error-circle" size="28px" />
      <span>{{ errorMessage }}</span>
      <t-button size="small" theme="primary" variant="outline" @click="refresh">
        {{ t('chat.sandbox.retry') }}
      </t-button>
    </div>
    <div v-else-if="!entries.length" class="sandbox-files__state">
      <t-icon name="folder-open" size="32px" />
      <span>{{ t('chat.sandbox.filesEmpty') }}</span>
    </div>
    <ul v-else class="sandbox-files__list">
      <li v-for="entry in entries" :key="entry.path" class="sandbox-files__row">
        <div v-if="renamingPath === entry.path" class="sandbox-files__edit">
          <t-icon :name="isDirectory(entry) ? 'folder' : 'file'" size="20px" />
          <t-input
            v-model="renameName"
            size="small"
            :maxlength="255"
            :aria-label="t('chat.sandbox.filesRename')"
            @keydown.enter.stop.prevent="commitRename(entry)"
            @keydown.esc.stop.prevent="cancelRename"
          />
        </div>
        <button
          v-else
          class="sandbox-files__open"
          type="button"
          :disabled="entry.type === 'other' || busy"
          @click="openEntry(entry)"
        >
          <t-icon :name="isDirectory(entry) ? 'folder' : 'file'" size="20px" />
          <span class="sandbox-files__details">
            <span class="sandbox-files__name" :title="entry.name">{{ entry.name }}</span>
            <span class="sandbox-files__meta">
              <span>{{ isDirectory(entry) ? t('chat.sandbox.filesDirectory') : formatSize(entry.size) }}</span>
              <span aria-hidden="true">·</span>
              <span>{{ formatDate(entry.mod_time) }}</span>
            </span>
          </span>
        </button>
        <div class="sandbox-files__row-actions">
          <template v-if="renamingPath === entry.path">
            <t-button
              variant="text"
              shape="square"
              size="small"
              :title="t('common.confirm')"
              :disabled="busy"
              @click="commitRename(entry)"
            >
              <template #icon><t-icon name="check" /></template>
            </t-button>
            <t-button
              variant="text"
              shape="square"
              size="small"
              :title="t('common.cancel')"
              :disabled="busy"
              @click="cancelRename"
            >
              <template #icon><t-icon name="close" /></template>
            </t-button>
          </template>
          <template v-else>
            <t-button
              v-if="entry.type === 'file'"
              variant="text"
              shape="square"
              size="small"
              :title="t('chat.sandbox.filesDownload')"
              :disabled="busy"
              @click="download(entry)"
            >
              <template #icon><t-icon name="download" /></template>
            </t-button>
            <t-button
              v-if="entry.type !== 'other'"
              variant="text"
              shape="square"
              size="small"
              :title="t('chat.sandbox.filesRename')"
              :disabled="busy"
              @click="startRename(entry)"
            >
              <template #icon><t-icon name="edit" /></template>
            </t-button>
            <t-button
              v-if="entry.type !== 'other'"
              variant="text"
              shape="square"
              size="small"
              theme="danger"
              :title="t('chat.sandbox.filesDelete')"
              :disabled="busy"
              @click="confirmDelete(entry)"
            >
              <template #icon><t-icon name="delete" /></template>
            </t-button>
          </template>
        </div>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import {
  deleteSandboxLiveFile,
  downloadSandboxLiveFile,
  listSandboxLiveFiles,
  renameSandboxLiveFile,
  uploadSandboxLiveFile,
  MAX_SANDBOX_LIVE_FILE_BYTES,
  type SandboxLiveFileEntry,
} from '@/api/chat/sandbox-files'
import { formatFileSize } from '@/utils/files'

const props = withDefaults(defineProps<{ sessionId: string; active?: boolean }>(), { active: false })
const { t, locale } = useI18n()
const entries = ref<SandboxLiveFileEntry[]>([])
const currentPath = ref('')
const loading = ref(false)
const loaded = ref(false)
const mutating = ref(false)
const errorMessage = ref('')
const uploadInput = ref<HTMLInputElement | null>(null)
const renamingPath = ref('')
const renameName = ref('')
let requestSequence = 0

const busy = computed(() => loading.value || mutating.value)
const breadcrumbs = computed(() => {
  const parts = currentPath.value ? currentPath.value.split('/') : []
  return parts.map((name, index) => ({ name, path: parts.slice(0, index + 1).join('/') }))
})

function childPath(name: string): string {
  return currentPath.value ? `${currentPath.value}/${name}` : name
}

async function refresh() {
  if (!props.sessionId || !props.active) return
  const sequence = ++requestSequence
  loading.value = true
  errorMessage.value = ''
  try {
    const result = await listSandboxLiveFiles(props.sessionId, currentPath.value)
    if (sequence === requestSequence) entries.value = result
  } catch (error: any) {
    if (sequence === requestSequence) errorMessage.value = liveFileErrorMessage(error, 'chat.sandbox.filesLoadFailed')
  } finally {
    if (sequence === requestSequence) {
      loading.value = false
      loaded.value = true
    }
  }
}

function openDirectory(relativePath: string) {
  currentPath.value = relativePath
  cancelRename()
  void refresh()
}

function isDirectory(entry: SandboxLiveFileEntry) {
  return entry.type === 'directory' || entry.type === 'dir'
}

function liveFileErrorMessage(error: any, fallbackKey: string) {
  const status = error?.status ?? error?.$httpStatus
  const message = typeof error?.message === 'string' ? error.message : ''
  if (status === 413 || /16 MiB/i.test(message)) return t('chat.sandbox.filesTooLarge')
  if (/paused/i.test(message)) return t('chat.sandbox.filesPaused')
  if (/too large to list or delete/i.test(message)) return t('chat.sandbox.filesTooMany')
  return message || t(fallbackKey)
}

function openEntry(entry: SandboxLiveFileEntry) {
  if (renamingPath.value === entry.path) return
  if (isDirectory(entry)) openDirectory(entry.path)
  else if (entry.type === 'file') void download(entry)
}

async function download(entry: SandboxLiveFileEntry) {
  try {
    const blob = await downloadSandboxLiveFile(props.sessionId, entry.path)
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = entry.name
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (error: any) {
    MessagePlugin.error(liveFileErrorMessage(error, 'chat.sandbox.filesDownloadFailed'))
  }
}

async function handleUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  if (!files.length) return
  mutating.value = true
  try {
    for (const file of files) {
      if (file.size > MAX_SANDBOX_LIVE_FILE_BYTES) {
        MessagePlugin.error(t('chat.sandbox.filesTooLarge'))
        return
      }
      await uploadSandboxLiveFile(props.sessionId, childPath(file.name), file)
    }
    MessagePlugin.success(t('chat.sandbox.filesUploadComplete'))
  } catch (error: any) {
    MessagePlugin.error(liveFileErrorMessage(error, 'chat.sandbox.filesUploadFailed'))
  } finally {
    input.value = ''
    await refresh()
    mutating.value = false
  }
}

function startRename(entry: SandboxLiveFileEntry) {
  renamingPath.value = entry.path
  renameName.value = entry.name
  void nextTick(() => {
    const input = document.querySelector<HTMLInputElement>('.sandbox-files__row .t-input__inner')
    input?.focus()
    input?.select()
  })
}

function cancelRename() {
  renamingPath.value = ''
  renameName.value = ''
}

async function commitRename(entry: SandboxLiveFileEntry) {
  const nextName = renameName.value.trim()
  if (!nextName || nextName.includes('/') || nextName.includes('\\')) {
    MessagePlugin.warning(t('chat.sandbox.filesInvalidName'))
    return
  }
  if (nextName === entry.name) {
    cancelRename()
    return
  }
  mutating.value = true
  try {
    await renameSandboxLiveFile(props.sessionId, entry.path, childPath(nextName))
    cancelRename()
    await refresh()
  } catch (error: any) {
    MessagePlugin.error(liveFileErrorMessage(error, 'chat.sandbox.filesRenameFailed'))
  } finally {
    mutating.value = false
  }
}

function confirmDelete(entry: SandboxLiveFileEntry) {
  const dialog = DialogPlugin.confirm({
    header: t('chat.sandbox.filesDelete'),
    body: t('chat.sandbox.filesDeleteConfirm', { name: entry.name }),
    confirmBtn: t('common.confirm'),
    cancelBtn: t('common.cancel'),
    theme: 'warning',
    onConfirm: async () => {
      dialog.destroy()
      mutating.value = true
      try {
        await deleteSandboxLiveFile(props.sessionId, entry.path)
        await refresh()
      } catch (error: any) {
        MessagePlugin.error(liveFileErrorMessage(error, 'chat.sandbox.filesDeleteFailed'))
      } finally {
        mutating.value = false
      }
    },
    onCancel: () => dialog.destroy(),
  })
}

function formatSize(size: number) {
  return formatFileSize(size)
}

function formatDate(value: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : new Intl.DateTimeFormat(locale.value, {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  }).format(date)
}

watch(
  () => [props.active, props.sessionId] as const,
  ([active], previous) => {
    if (!active) return
    if (!previous || previous[1] !== props.sessionId) {
      currentPath.value = ''
      loaded.value = false
      entries.value = []
    }
    void refresh()
  },
  { immediate: true },
)
</script>

<style scoped lang="less">
.sandbox-files { flex: 1; min-height: 0; display: flex; flex-direction: column; overflow: hidden; }
.sandbox-files__toolbar { min-height: 48px; padding: 8px 10px; border-bottom: 1px solid var(--td-component-stroke); display: flex; align-items: center; gap: 8px; }
.sandbox-files__breadcrumbs { min-width: 0; flex: 1; display: flex; align-items: center; overflow-x: auto; white-space: nowrap; }
.sandbox-files__breadcrumbs button { border: 0; padding: 4px 5px; background: transparent; color: var(--td-brand-color); cursor: pointer; font-size: 13px; }
.sandbox-files__breadcrumbs button:disabled { color: var(--td-text-color-primary); cursor: default; font-weight: 600; }
.sandbox-files__actions, .sandbox-files__row-actions { display: flex; align-items: center; flex-shrink: 0; }
.sandbox-files__upload-input { display: none; }
.sandbox-files__state { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px; padding: 24px; text-align: center; color: var(--td-text-color-placeholder); font-size: 13px; }
.sandbox-files__state.is-error { color: var(--td-error-color); }
.sandbox-files__list { flex: 1; min-height: 0; margin: 0; padding: 6px 0; list-style: none; overflow-y: auto; }
.sandbox-files__row { min-height: 54px; padding: 4px 8px 4px 12px; display: flex; align-items: center; border-bottom: 1px solid color-mix(in srgb, var(--td-component-stroke) 70%, transparent); }
.sandbox-files__row:hover { background: var(--td-bg-color-container-hover); }
.sandbox-files__open { min-width: 0; flex: 1; border: 0; padding: 4px 0; background: transparent; color: var(--td-text-color-primary); display: flex; align-items: center; gap: 10px; text-align: left; cursor: pointer; }
.sandbox-files__open:disabled { cursor: default; opacity: 0.6; }
.sandbox-files__edit { min-width: 0; flex: 1; display: flex; align-items: center; gap: 10px; }
.sandbox-files__details { min-width: 0; flex: 1; display: flex; flex-direction: column; gap: 2px; }
.sandbox-files__name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; }
.sandbox-files__meta { display: flex; gap: 5px; color: var(--td-text-color-placeholder); font-size: 11px; }
</style>
