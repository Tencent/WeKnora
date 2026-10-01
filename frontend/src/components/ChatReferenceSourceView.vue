<template>
  <div class="reference-source">
    <div class="reference-source__meta">
      <ArtifactFileIcon :file-name="displayName" />
      <div class="reference-source__meta-main">
        <h4 class="reference-source__title" :title="displayTitle">{{ displayTitle }}</h4>
        <p v-if="statusText" class="reference-source__status" :class="`is-${status}`">{{ statusText }}</p>
      </div>
      <div ref="toolbarSlot" class="reference-source__toolbar" />
      <t-button
        v-if="mode === 'file' && locate"
        theme="default" variant="text" size="small" shape="square"
        :title="t('chat.referenceSourceRelocate')" :aria-label="t('chat.referenceSourceRelocate')"
        @click="relocate"
      >
        <template #icon><t-icon name="location" /></template>
      </t-button>
      <a
        v-if="documentHref"
        class="reference-source__open"
        :href="documentHref"
        target="_blank"
        rel="noopener noreferrer"
        :title="t('chat.navigateToDocument')"
        :aria-label="t('chat.navigateToDocument')"
      >
        <t-icon name="jump" size="16px" />
      </a>
    </div>

    <div v-if="mode === 'loading'" class="reference-source__placeholder">
      <t-loading size="small" />
    </div>

    <DocumentPreview
      v-else-if="mode === 'file'"
      class="reference-source__preview"
      :knowledge-id="target.knowledgeId"
      :file-type="meta.fileType"
      :file-name="meta.fileName"
      :active="active"
      source-mode
      fill-height
      :toolbar-target="toolbarSlot"
      :locate="locate"
      @located="onLocated"
    />

    <div v-else-if="mode === 'web'" class="reference-source__web">
      <p class="reference-source__quote">{{ webQuote }}</p>
      <t-button theme="primary" variant="outline" size="small" @click="openWebSource">
        <template #icon><t-icon name="jump" /></template>
        {{ t('chat.referenceSourceOpenWeb') }}
      </t-button>
      <p class="reference-source__url">{{ meta.knowledgeSource }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { sourceImageDigest } from '@/utils/sourceImage'
import { buildProtectedFileRequest, resolveProtectedFileAccess } from '@/utils/protectedFileAccess'
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import DocumentPreview from '@/components/document-preview.vue'
import ArtifactFileIcon from '@/views/chat/components/ArtifactFileIcon.vue'
import { getChunkByIdOnly, getKnowledgeDetails } from '@/api/knowledge-base/index'
import { isKnownPreviewableFile } from '@/utils/filePreview'
import type { ReferenceSourceTarget } from '@/utils/referenceSources'
import {
  locatorPages,
  parseSourceLocators,
  selectLocatorsForSentence,
  selectQuoteForSentence,
  sourceQuoteText,
  sourceImageContext,
  quotedSourceExcerpt,
  textFragmentUrl,
  type SourceLocateRequest,
  type SourceLocator,
} from '@/utils/sourceLocator'

const props = defineProps<{
  target: ReferenceSourceTarget
  active: boolean
}>()

const emit = defineEmits<{
  /** The cited document has no original file this view can show. */
  unavailable: []
}>()

const { t } = useI18n()
const router = useRouter()

type Mode = 'loading' | 'file' | 'web' | 'none'
type Status = 'idle' | 'locating' | 'found' | 'page' | 'block' | 'ambiguous' | 'stale' | 'missing' | 'partial'

const mode = ref<Mode>('loading')
const toolbarSlot = ref<HTMLElement | null>(null)
const status = ref<Status>('idle')
const locatedPage = ref(0)
const locate = ref<SourceLocateRequest | null>(null)
const locators = ref<SourceLocator[]>([])
const chunkContent = ref('')
const chunkType = ref('')
const sourceHash = ref('')
const edited = ref(false)
const imageDigest = ref<string>()
const imageContext = ref<SourceLocateRequest['imageContext']>()
let locateToken = 0
const meta = reactive({ fileName: '', fileType: '', knowledgeSource: '', knowledgeType: '', title: '' })
let loadVersion = 0
/** The knowledge whose file the preview currently shows. */
let shownKnowledgeId = ''

const displayName = computed(() => meta.fileName || props.target.fileName || props.target.title || '')
const displayTitle = computed(() => props.target.title || meta.title || displayName.value)

const documentHref = computed(() => {
  if (!props.target.knowledgeBaseId) return ''
  return router.resolve({
    path: `/platform/knowledge-bases/${props.target.knowledgeBaseId}`,
    query: { knowledge_id: props.target.knowledgeId },
  }).href
})

const statusText = computed(() => {
  switch (status.value) {
    case 'locating':
      return t('chat.referenceSourceLocating')
    case 'found':
      return t('chat.referenceSourceExact')
    case 'partial':
      return t('chat.referenceSourcePartial')
    case 'block':
      return t('chat.referenceSourceBlock')
    case 'ambiguous':
      return t('chat.referenceSourceAmbiguous')
    case 'stale':
      return t('chat.referenceSourceStale')
    case 'page':
      return t('chat.referenceSourceFoundPage', { page: locatedPage.value })
    case 'missing':
      return t('chat.referenceSourceNotFound')
    default:
      return ''
  }
})

const webQuote = computed(() => {
  const quote = locators.value.find((l) => l.quote)?.quote || chunkContent.value || props.target.content || ''
  return quote.replace(/\s+/g, ' ').trim().slice(0, 280)
})

function isWebUrl(value: string) {
  return /^https?:\/\//i.test(value || '')
}

async function loadMeta(version: number) {
  meta.fileName = props.target.fileName || ''
  meta.fileType = ''
  meta.knowledgeSource = props.target.knowledgeSource || ''
  meta.knowledgeType = ''
  meta.title = props.target.title || ''
  sourceHash.value = ''
  try {
    const res: any = await getKnowledgeDetails(props.target.knowledgeId)
    if (version !== loadVersion) return
    const data = res?.data || {}
    sourceHash.value = data.file_hash || ''
    meta.fileName = meta.fileName || data.file_name || ''
    meta.fileType = data.file_type || ''
    meta.knowledgeSource = meta.knowledgeSource || data.source || ''
    meta.knowledgeType = data.type || ''
    meta.title = meta.title || data.title || ''
  } catch {
    // Without details the file name from the reference decides below.
  }
}

async function loadChunk(version: number) {
  locators.value = parseSourceLocators(props.target.locators)
  chunkContent.value = props.target.content || ''
  edited.value = false
  imageContext.value = undefined
  imageDigest.value = undefined
  chunkType.value = props.target.chunkType || ''

  let loadedCurrent = false
  // Old conversations and agent tool results carry no locators; the chunk
  // itself has them (and its own, unexpanded text for matching).
  try {
    const res: any = await getChunkByIdOnly(props.target.chunkId)
    if (version !== loadVersion) return
    // The current chunk overrides snapshots embedded in historical answers.
    // In particular, an edit must invalidate even previously cached locators.
    const current = res?.data
    if (!current || current.knowledge_id !== props.target.knowledgeId) {
      edited.value = true
      locators.value = []
      return
    }
    loadedCurrent = true
    locators.value = parseSourceLocators(current.source_locators)
    edited.value = (current.content_revision || 0) > 0 && !locators.value.length
    chunkContent.value = String(current.content || '')
    chunkType.value = String(current.chunk_type || '')
    const chunk = res?.data
    if (!edited.value && chunk?.chunk_type?.startsWith('image_')) {
      let info = chunk.image_info
      try { if (typeof info === 'string') info = JSON.parse(info) } catch { info = [] }
      if (Array.isArray(info) && info.length === 1) {
        const digest = info[0].sha256
        if (typeof digest === 'string' && /^[a-f0-9]{64}$/.test(digest)) imageDigest.value = digest
        else {
          const request = buildProtectedFileRequest(info[0].url || '', resolveProtectedFileAccess({ mode: 'knowledgeBase', kbId: props.target.knowledgeBaseId || '' }))
          if (request) {
            try {
              const response = await fetch(request.url, { headers: request.headers, credentials: 'include' })
              if (response.ok) {
                const hash = await sourceImageDigest(await response.arrayBuffer())
                if (version !== loadVersion) return
                imageDigest.value = hash
              }
            } catch { /* Existing structural locators may still locate the image. */ }
          }
        }
      }
    }
    // Legacy expanded contexts were cited under a child ID. Only recover a
    // verbatim quotation in its verified parent, never search the whole file
    // for a merely similar answer or silently select a neighbouring chunk.
    if (!edited.value && chunk?.chunk_type === 'text' && chunk.parent_chunk_id &&
        !quotedSourceExcerpt(chunkContent.value, props.target.anchorText || '')) {
      const parent: any = await getChunkByIdOnly(chunk.parent_chunk_id)
      if (version !== loadVersion) return
      const p = parent?.data
      if (p?.knowledge_id === props.target.knowledgeId && p.chunk_type === 'parent_text' && !p.content_revision &&
          quotedSourceExcerpt(p.content || '', props.target.anchorText || '')) {
        const parentLocators = parseSourceLocators(p.source_locators)
        if (parentLocators.length && parentLocators.every(l => l.mapping === 'exact' && l.source_hash)) {
          chunkContent.value = p.content
          locators.value = parentLocators
        }
      }
    }
    if (!edited.value && chunk?.chunk_type?.startsWith('image_') && chunk.parent_chunk_id) {
      const parent: any = await getChunkByIdOnly(chunk.parent_chunk_id)
      if (version !== loadVersion) return
      if (parent?.data?.knowledge_id === props.target.knowledgeId && !parent.data.content_revision) {
        let info = chunk.image_info
        try { if (typeof info === 'string') info = JSON.parse(info) } catch { info = [] }
        const urls = Array.isArray(info) ? info.flatMap(i => [i.url, i.original_url].filter(Boolean)) : []
        imageContext.value = sourceImageContext(parent.data.content || '', urls)
      }
    }
  } catch {
    if (version !== loadVersion) return
    if (!loadedCurrent) chunkContent.value = props.target.content || ''
  }
}

function resolveMode(): Mode {
  const type = (meta.knowledgeType || meta.fileType || '').toLowerCase()
  if (meta.knowledgeSource === 'manual' || type === 'manual' || type === 'faq' || props.target.chunkType === 'faq') return 'none'
  if (meta.fileName && isKnownPreviewableFile(meta.fileName, meta.fileType)) return 'file'
  if (isWebUrl(meta.knowledgeSource)) return 'web'
  return 'none'
}

function buildLocate(): SourceLocateRequest {
  const anchor = props.target.anchorText || ''
  const selected = selectLocatorsForSentence(locators.value, anchor)
  const quotes = chunkContent.value ? selectQuoteForSentence(chunkContent.value, anchor) : []
  const scope = sourceQuoteText(chunkContent.value || props.target.content || '')
  const unavailable = edited.value || locators.value.some((loc) => loc.source_hash && sourceHash.value && loc.source_hash !== sourceHash.value)
  return { locators: unavailable ? [] : selected, quotes: unavailable ? [] : quotes, token: ++locateToken, sentence: anchor, sourceMarkdown: unavailable ? undefined : chunkContent.value, imageDigest: unavailable ? undefined : imageDigest.value, imageContext: unavailable ? undefined : imageContext.value, scope: unavailable ? '' : scope, unavailable }
}

async function load() {
  const version = ++loadVersion
  status.value = 'idle'
  locatedPage.value = 0
  locate.value = null
  // Another citation in the open file: keep the preview mounted and only
  // move the highlight, instead of downloading and rendering the file again.
  const sameFile = mode.value === 'file' && shownKnowledgeId === props.target.knowledgeId
  if (!sameFile) mode.value = 'loading'
  await Promise.all([sameFile ? Promise.resolve() : loadMeta(version), loadChunk(version)])
  if (version !== loadVersion) return
  shownKnowledgeId = props.target.knowledgeId
  const next = resolveMode()
  mode.value = next
  if (next === 'none') {
    emit('unavailable')
    return
  }
  // A summary speaks for the whole document: open it without hunting for a passage.
  if (next === 'file' && chunkType.value !== 'summary') {
    locate.value = buildLocate()
    status.value = locate.value.unavailable ? 'stale' : 'locating'
  }
}

function relocate() {
  if (!locate.value) return
  status.value = 'locating'
  locate.value = { ...locate.value, token: ++locateToken }
}

function onLocated(result: { found: boolean; precise: boolean; page?: number; granularity?: string; reason?: string }) {
  if (locate.value?.unavailable) { status.value = 'stale'; return }
  locatedPage.value = result.page || locatorPages(locate.value?.locators || [])[0] || 0
  if (result.reason === 'ambiguous') status.value = 'ambiguous'
  else if (result.reason === 'partial' && result.found) status.value = 'partial'
  else if (result.reason) status.value = 'missing'
  else if (!result.found) status.value = 'missing'
  else if (result.granularity === 'block') status.value = 'block'
  else if (!result.precise && locatedPage.value) status.value = 'page'
  else status.value = 'found'
}

function openWebSource() {
  const url = textFragmentUrl(meta.knowledgeSource, webQuote.value.slice(0, 120))
  window.open(url, '_blank', 'noopener,noreferrer')
}

watch(
  () => [props.target.chunkId, props.target.knowledgeId, props.target.anchorText],
  () => void load(),
  { immediate: true },
)
</script>

<style scoped lang="less">
.reference-source {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.reference-source__meta {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: 8px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.reference-source__meta-main {
  flex: 1;
  min-width: 0;
}

.reference-source__title {
  margin: 0;
  font-size: var(--app-text-base);
  font-weight: 500;
  line-height: 20px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.reference-source__status {
  margin: 2px 0 0;
  font-size: var(--app-text-sm);
  line-height: 1.4;
  color: var(--td-text-color-placeholder);

  &.is-missing {
    color: var(--td-warning-color);
  }
}

.reference-source__toolbar {
  display: flex;
  align-items: center;
}

.reference-source__open {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--app-radius-md);
  color: var(--td-text-color-placeholder);

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }
}

.reference-source__placeholder {
  display: flex;
  justify-content: center;
  padding: 32px 0;
}

.reference-source__preview {
  flex: 1;
  min-height: 0;
}

.reference-source__web {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 12px;
  padding: 16px 12px;
}

.reference-source__quote {
  margin: 0;
  padding: 10px 12px;
  border-left: 3px solid var(--app-source-highlight);
  background: color-mix(in srgb, var(--app-source-highlight) 18%, transparent);
  font-size: var(--app-text-md);
  line-height: 1.6;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;
}

.reference-source__url {
  margin: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  word-break: break-all;
}
</style>
