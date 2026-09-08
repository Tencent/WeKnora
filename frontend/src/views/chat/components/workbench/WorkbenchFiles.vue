<template>
  <section class="workbench-files" @dragover.prevent.stop @drop.prevent.stop="handleDrop">
    <template v-if="preview">
      <div class="workbench-toolbar">
        <t-button variant="text" shape="square" size="small" :title="t('workbench.back')" :aria-label="t('workbench.back')" @click="closePreview">
          <template #icon><t-icon name="chevron-left" /></template>
        </t-button>
        <span class="workbench-ellipsis" :title="preview.path">{{ preview.path }}</span>
      </div>
      <DocumentPreview
        :key="preview.key" :source-key="preview.key" :source-blob="preview.blob"
        :file-name="preview.name" :file-type="resolveFilePreviewExt(preview.name)"
        :active="active" :max-preview-bytes="maxBytes" restricted-preview fill-height
      />
    </template>
    <template v-else>
      <div class="workbench-toolbar">
        <t-button variant="text" shape="square" size="small" :disabled="!path || loading" :title="t('workbench.parentDirectory')" :aria-label="t('workbench.parentDirectory')" @click="goUp">
          <template #icon><t-icon name="chevron-left" /></template>
        </t-button>
        <span class="workbench-ellipsis workbench-path" :title="`/workspace/output/${path}`">/workspace/output<template v-if="path">/{{ path }}</template></span>
        <t-button variant="text" shape="square" size="small" :loading="loading" :title="t('workbench.refresh')" :aria-label="t('workbench.refresh')" @click="refresh">
          <template #icon><t-icon name="refresh" /></template>
        </t-button>
      </div>
      <div class="workbench-toolbar file-actions">
        <t-button size="small" variant="outline" :disabled="busy || loading" @click="picker?.click()">
          <template #icon><t-icon name="upload" /></template>{{ t('workbench.upload') }}
        </t-button>
        <t-button size="small" variant="text" :disabled="busy || loading" @click="openDialog('mkdir')">
          <template #icon><t-icon name="folder-add" /></template>{{ t('workbench.newDirectory') }}
        </t-button>
        <span class="file-limit">{{ formatWorkbenchBytes(maxBytes) }}</span>
        <input ref="picker" type="file" hidden @change="chooseFile" />
      </div>
      <div v-if="error" class="workbench-error" role="alert">{{ error }}</div>
      <div v-if="loading" class="workbench-empty"><t-loading size="small" /></div>
      <div v-else-if="!entries.length" class="workbench-empty">{{ error ? t('workbench.requestFailed') : t('workbench.emptyDirectory') }}</div>
      <ul v-else class="workbench-list">
        <li v-for="entry in entries" :key="entry.path" class="file-row">
          <button class="file-name" :disabled="busy" @click="openEntry(entry)">
            <t-icon :name="entry.type === 'dir' ? 'folder' : getFileIcon(entry.name)" />
            <span class="workbench-ellipsis" :title="entry.name">{{ entry.name }}</span>
          </button>
          <span class="file-size">{{ entry.type === 'dir' ? '' : formatWorkbenchBytes(entry.size) }}</span>
          <t-dropdown :options="entryActions(entry)" trigger="click" placement="bottom-right" @click="(option: { value?: string | number }) => handleAction(String(option.value), entry)">
            <t-button variant="text" shape="square" size="small" :disabled="busy" :title="t('workbench.fileActions')" :aria-label="t('workbench.fileActions')">
              <template #icon><t-icon name="ellipsis" /></template>
            </t-button>
          </t-dropdown>
        </li>
      </ul>
    </template>
    <t-dialog
      :visible="!!dialog" :header="dialogTitle" :footer="false" width="min(440px, calc(100vw - 32px))"
      :close-on-overlay-click="!busy" :close-on-esc-keydown="!busy" @close="!busy && (dialog = null)"
    >
      <form data-sandbox-workbench class="file-dialog" @submit.prevent="submitDialog">
        <div v-if="dialog === 'delete'" class="workbench-error">{{ t('workbench.deleteConfirm', { path: selected?.path }) }}</div>
        <template v-else>
          <label for="workbench-destination">{{ t('workbench.destination') }}</label>
          <t-input id="workbench-destination" v-model="destination" :disabled="busy" autofocus />
        </template>
        <div v-if="dialogError" class="workbench-error" role="alert">{{ dialogError }}</div>
        <div class="dialog-actions">
          <t-button variant="text" :disabled="busy" @click="dialog = null">{{ t('common.cancel') }}</t-button>
          <t-button type="submit" :theme="dialog === 'delete' ? 'danger' : 'primary'" :loading="busy">{{ dialog === 'delete' ? t('common.delete') : t('common.confirm') }}</t-button>
        </div>
      </form>
    </t-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { WorkbenchApi, WorkbenchFile } from '@/api/sandbox-workbench'
import DocumentPreview from '@/components/document-preview.vue'
import { getFileIcon } from '@/utils/files'
import { resolveFilePreviewExt } from '@/utils/filePreview'
import {
  formatWorkbenchBytes, saveWorkbenchBlob, validWorkbenchPath, workbenchChildPath,
  workbenchError, workbenchSnapshotKey,
} from '@/utils/sandboxWorkbench'

const props = defineProps<{ api: WorkbenchApi; signal: AbortSignal; active: boolean; revision: number; maxBytes: number }>()
const { t } = useI18n()
const path = ref('')
const entries = ref<WorkbenchFile[]>([])
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const picker = ref<HTMLInputElement>()
const dialog = ref<'upload' | 'mkdir' | 'rename' | 'delete' | null>(null)
const destination = ref('')
const selected = ref<WorkbenchFile>()
const uploadFile = shallowRef<File>()
const dialogError = ref('')
const preview = shallowRef<{ key: string; path: string; name: string; blob: Blob }>()
let listRequest = 0
let previewRequest = 0
let revision = 0
let alive = true
const current = () => alive && !props.signal.aborted
const dialogTitle = computed(() => dialog.value ? t(`workbench.${{ upload: 'upload', mkdir: 'newDirectory', rename: 'rename', delete: 'delete' }[dialog.value]}`) : '')

function closePreview() { ++previewRequest; preview.value = undefined }

async function refresh() {
  if (!current()) return
  const request = ++listRequest
  ++revision
  closePreview()
  loading.value = true
  error.value = ''
  try {
    const result = await props.api.files(path.value)
    if (!current() || request !== listRequest) return
    if (!validWorkbenchPath(result.path, true) || !Array.isArray(result.entries)) throw new Error(t('workbench.invalidPath'))
    path.value = result.path
    // Never present symlink/unknown entry types as ordinary files.
    entries.value = result.entries.filter(entry =>
      ['file', 'dir'].includes(entry.type) && validWorkbenchPath(entry.path),
    ).sort((a, b) => Number(b.type === 'dir') - Number(a.type === 'dir') || a.name.localeCompare(b.name))
  } catch (value) {
    if (current() && request === listRequest) { entries.value = []; error.value = workbenchError(value) || t('workbench.requestFailed') }
  } finally { if (current() && request === listRequest) loading.value = false }
}

function goUp() {
  path.value = path.value.split('/').slice(0, -1).join('/')
  void refresh()
}

async function openEntry(entry: WorkbenchFile) {
  if (busy.value || !current()) return
  if (entry.type === 'dir') { path.value = entry.path; void refresh(); return }
  if (entry.size > props.maxBytes) { error.value = t('workbench.fileTooLarge', { size: formatWorkbenchBytes(props.maxBytes) }); return }
  const request = ++previewRequest
  busy.value = true
  error.value = ''
  try {
    const blob = await props.api.download(entry.path)
    if (!current() || request !== previewRequest) return
    if (blob.size > props.maxBytes) throw new Error(t('workbench.fileTooLarge', { size: formatWorkbenchBytes(props.maxBytes) }))
    preview.value = { key: workbenchSnapshotKey(entry.path, ++revision), path: entry.path, name: entry.name, blob }
  } catch (value) {
    if (current() && request === previewRequest) error.value = workbenchError(value) || t('workbench.requestFailed')
  } finally { if (current()) busy.value = false }
}

function entryActions(entry: WorkbenchFile) {
  return [
    ...(entry.type === 'file' ? [{ content: t('workbench.download'), value: 'download' }] : []),
    { content: t('workbench.rename'), value: 'rename' },
    { content: t('workbench.delete'), value: 'delete', theme: 'error' as const },
  ]
}

async function handleAction(action: string, entry: WorkbenchFile) {
  if (busy.value || !current()) return
  if (action !== 'download') { openDialog(action as 'rename' | 'delete', entry); return }
  busy.value = true
  error.value = ''
  try {
    const blob = await props.api.download(entry.path)
    if (current()) saveWorkbenchBlob(blob, entry.name, props.signal)
  } catch (value) { if (current()) error.value = workbenchError(value) || t('workbench.requestFailed') }
  finally { if (current()) busy.value = false }
}

function openDialog(mode: NonNullable<typeof dialog.value>, entry?: WorkbenchFile) {
  dialog.value = mode
  dialogError.value = ''
  selected.value = entry
  destination.value = entry?.path || workbenchChildPath(path.value, '')
}

function prepareUpload(file?: File) {
  if (!file || busy.value || !current()) return
  if (file.size > props.maxBytes) { error.value = t('workbench.fileTooLarge', { size: formatWorkbenchBytes(props.maxBytes) }); return }
  uploadFile.value = file
  openDialog('upload')
  destination.value = workbenchChildPath(path.value, file.name)
}

function chooseFile(event: Event) {
  const input = event.target as HTMLInputElement
  prepareUpload(input.files?.[0])
  input.value = ''
}

function handleDrop(event: DragEvent) {
  const transfer = event.dataTransfer
  if (!transfer || !props.active || busy.value) return
  if (transfer.files.length !== 1 || Array.from(transfer.items).some(item => item.webkitGetAsEntry?.()?.isDirectory)) {
    error.value = t('workbench.singleFileOnly')
    return
  }
  prepareUpload(transfer.files[0])
}

async function submitDialog() {
  if (!dialog.value || busy.value || !current()) return
  const target = destination.value
  if (dialog.value !== 'delete' && !validWorkbenchPath(target)) { dialogError.value = t('workbench.invalidPath'); return }
  if (dialog.value !== 'delete' && entries.value.some(entry => entry.path === target)) { dialogError.value = t('workbench.noOverwrite'); return }
  busy.value = true
  dialogError.value = ''
  try {
    if (dialog.value === 'upload' && uploadFile.value) await props.api.upload(target, uploadFile.value)
    else if (dialog.value === 'mkdir') await props.api.mkdir(target)
    else if (dialog.value === 'rename' && selected.value) await props.api.rename(selected.value.path, target)
    else if (dialog.value === 'delete' && selected.value) await props.api.remove(selected.value.path)
    if (!current()) return
    dialog.value = null
    uploadFile.value = undefined
    await refresh()
  } catch (value) { if (current()) dialogError.value = workbenchError(value) || t('workbench.requestFailed') }
  finally { if (current()) busy.value = false }
}

watch(() => [props.active, props.revision], () => { if (props.active) void refresh(); else closePreview() }, { immediate: true })
onBeforeUnmount(() => { alive = false; ++listRequest; closePreview() })
</script>

<style scoped lang="less">
.workbench-files { flex: 1; display: flex; flex-direction: column; min-height: 0; min-width: 0; }
.workbench-path { font-family: ui-monospace, monospace; font-size: 12px; }
.file-actions { padding-top: 0; flex-wrap: wrap; }
.file-limit { margin-left: auto; font-size: 12px; color: var(--td-text-color-placeholder); }
.file-row { display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-bottom: 1px solid var(--td-component-stroke); }
.file-name { display: flex; flex: 1; min-width: 0; gap: 8px; align-items: center; padding: 4px 0; background: none; border: 0; color: var(--td-text-color-primary); text-align: left; cursor: pointer; }
.file-size { font-size: 12px; color: var(--td-text-color-placeholder); }
.file-dialog { display: flex; flex-direction: column; gap: 12px; }
.dialog-actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
