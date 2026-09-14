<template>
  <div v-if="loading" class="document-preview workbench-preview-state"><t-loading size="medium" /></div>
  <div v-else-if="error" class="document-preview preview-error workbench-preview-state workbench-error" role="alert">{{ error }}</div>
  <div v-else-if="restrictedHtml" class="document-preview workbench-preview-frame">
    <iframe :srcdoc="restrictedHtml" class="html-iframe" sandbox="" referrerpolicy="no-referrer" :title="fileName" />
  </div>
  <DocumentPreview
    v-else-if="preparedBlob" :source-blob="preparedBlob" :source-key="preparedKey"
    :file-name="fileName" :file-type="preparedType" :active="active" fill-height
  />
  <div v-else class="document-preview preview-unsupported workbench-preview-state">{{ t('preview.unsupported') }}</div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DocumentPreview from '@/components/document-preview.vue'
import { downloadArtifact } from '@/api/chat'
import { FILE_PREVIEW_SNIFF_BYTES, isValidUTF8, resolveFilePreviewExt } from '@/utils/filePreview'
import { renderDocumentPreviewMarkdown } from '@/utils/documentPreviewMarkdown'
import { prepareWorkbenchOfficePreview, WORKBENCH_OFFICE_LIMITS, WorkbenchOfficePreviewError } from '@/utils/workbenchOfficePreview'
import { sanitizeWorkbenchPreview, workbenchPreviewKind } from '@/utils/workbenchPreview'
import { validateWorkbenchImage } from '@/utils/workbenchImagePreview'
import { formatWorkbenchBytes, workbenchFileLimit } from '@/utils/sandboxWorkbench'

const props = defineProps<{
  sourceBlob?: Blob
  sourceKey?: string
  sessionId?: string
  messageId?: string
  artifactIndex?: number
  fileType: string
  fileName: string
  active: boolean
  requestSignal?: AbortSignal
  maxPreviewBytes?: number
}>()

const { t } = useI18n()
const loading = ref(false)
const error = ref('')
const restrictedHtml = ref('')
const preparedBlob = shallowRef<Blob>()
const preparedType = ref('')
const preparedKey = ref('')
let generation = 0
let request: AbortController | undefined

function decodeTable(bytes: Uint8Array): string {
  if (bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) return new TextDecoder().decode(bytes)
  return new TextDecoder(isValidUTF8(bytes) ? 'utf-8' : 'gbk').decode(bytes)
}

async function spreadsheetHTML(blob: Blob, type: string): Promise<string> {
  const XLSX = await import('xlsx')
  const bytes = new Uint8Array(await blob.arrayBuffer())
  const workbook = type === 'csv'
    ? XLSX.read(decodeTable(bytes), { type: 'string' })
    : type === 'tsv' || type === 'tab'
      ? XLSX.read(decodeTable(bytes), { type: 'string', FS: '\t' })
      : XLSX.read(bytes, { type: 'array' })
  let cells = 0
  let html = ''
  for (const [index, name] of workbook.SheetNames.entries()) {
    const sheet = workbook.Sheets[name]
    if (sheet['!ref']) {
      const range = XLSX.utils.decode_range(sheet['!ref'])
      cells += (range.e.r - range.s.r + 1) * (range.e.c - range.s.c + 1)
      if (!Number.isSafeInteger(cells) || cells > WORKBENCH_OFFICE_LIMITS.spreadsheetCells) {
        throw new WorkbenchOfficePreviewError('officeTooLarge')
      }
    }
    const label = document.createElement('div')
    label.textContent = name
    html += `<section><h3>${label.innerHTML}</h3>${XLSX.utils.sheet_to_html(sheet, { id: `sheet-${index}` })}</section>`
  }
  return html
}

async function sourceBlob(signal: AbortSignal): Promise<Blob> {
  if (props.sourceBlob) return props.sourceBlob
  if (props.sessionId && props.messageId && Number.isInteger(props.artifactIndex) && (props.artifactIndex as number) >= 0) {
    return downloadArtifact(props.sessionId, props.messageId, props.artifactIndex as number, { signal })
  }
  throw new Error('Missing preview source')
}

function cancel() {
  request?.abort()
  request = undefined
}

async function load() {
  cancel()
  const current = ++generation
  preparedBlob.value = undefined
  preparedType.value = ''
  preparedKey.value = ''
  restrictedHtml.value = ''
  error.value = ''
  if (!props.active) return
  const controller = new AbortController()
  request = controller
  const signal = controller.signal
  const parentAbort = () => controller.abort()
  props.requestSignal?.addEventListener('abort', parentAbort, { once: true })
  if (props.requestSignal?.aborted) controller.abort()
  loading.value = true
  try {
    const blob = await sourceBlob(signal)
    signal.throwIfAborted()
    if (current !== generation) return
    const maxBytes = workbenchFileLimit(props.maxPreviewBytes)
    if (blob.size > maxBytes) throw new Error(t('workbench.fileTooLarge', { size: formatWorkbenchBytes(maxBytes) }))
    const type = resolveFilePreviewExt(props.fileName, props.fileType)
    const sample = new Uint8Array(await blob.slice(0, FILE_PREVIEW_SNIFF_BYTES).arrayBuffer())
    signal.throwIfAborted()
    if (current !== generation) return
    const kind = workbenchPreviewKind(type, sample)
    let nextBlob: Blob | undefined
    let nextHtml = ''
    let nextType = ''
    const zipSpreadsheet = kind === 'excel' && sample[0] === 0x50 && sample[1] === 0x4b && sample[2] === 3 && sample[3] === 4
    if (kind === 'pptx' || type === 'xlsx' || zipSpreadsheet) {
      const data = await prepareWorkbenchOfficePreview(blob, kind === 'pptx' ? 'pptx' : 'xlsx', maxBytes, signal)
      if (kind === 'pptx') {
        nextBlob = new Blob([data], { type: blob.type })
        nextType = 'pptx'
      } else {
        nextHtml = sanitizeWorkbenchPreview(await spreadsheetHTML(new Blob([data]), 'xlsx'))
      }
    } else if (kind === 'excel') {
      nextHtml = sanitizeWorkbenchPreview(await spreadsheetHTML(blob, type))
    } else if (kind === 'html' || kind === 'markdown') {
      let html = await blob.text()
      if (kind === 'markdown') html = renderDocumentPreviewMarkdown(html)
      nextHtml = sanitizeWorkbenchPreview(html)
    } else if (kind === 'image') {
      await validateWorkbenchImage(blob)
      nextBlob = blob
      nextType = type
    } else if (kind === 'text') {
      nextBlob = blob
      nextType = 'txt'
    }
    signal.throwIfAborted()
    if (current !== generation) return
    preparedBlob.value = nextBlob
    preparedType.value = nextType
    restrictedHtml.value = nextHtml
    preparedKey.value = props.sourceKey || JSON.stringify([props.fileName, blob.size, current])
  } catch (value) {
    if (!signal.aborted && current === generation) {
      if (value instanceof WorkbenchOfficePreviewError) error.value = t(`workbench.${value.code}`)
      else if ((value as Error)?.message === 'imageTooLarge') error.value = t('workbench.imageTooLarge')
      else error.value = (value as Error)?.message || t('preview.loadFailed')
    }
  } finally {
    props.requestSignal?.removeEventListener('abort', parentAbort)
    if (request === controller) request = undefined
    if (current === generation) loading.value = false
  }
}

watch(() => [props.active, props.sourceBlob, props.sourceKey, props.sessionId, props.messageId, props.artifactIndex, props.fileName, props.fileType, props.requestSignal, props.maxPreviewBytes], load, { immediate: true })
onBeforeUnmount(() => { ++generation; cancel() })
</script>

<style scoped lang="less">
.workbench-preview-state, .workbench-preview-frame { flex: 1; min-height: 0; }
.workbench-preview-state { display: flex; align-items: center; justify-content: center; padding: 24px; }
.workbench-preview-frame, .html-iframe { width: 100%; height: 100%; border: 0; }
</style>
