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
type Status = 'idle' | 'locating' | 'found' | 'page' | 'missing'

const mode = ref<Mode>('loading')
const toolbarSlot = ref<HTMLElement | null>(null)
const status = ref<Status>('idle')
const locatedPage = ref(0)
const locate = ref<SourceLocateRequest | null>(null)
const locators = ref<SourceLocator[]>([])
const chunkContent = ref('')
const chunkType = ref('')
const meta = reactive({ fileName: '', fileType: '', knowledgeSource: '', knowledgeType: '', title: '' })
let loadVersion = 0

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
      return locatedPage.value ? t('chat.referenceSourceFoundPage', { page: locatedPage.value }) : ''
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
  if (meta.fileName && meta.knowledgeSource) return
  try {
    const res: any = await getKnowledgeDetails(props.target.knowledgeId)
    if (version !== loadVersion) return
    const data = res?.data || {}
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
  chunkContent.value = ''
  chunkType.value = props.target.chunkType || ''
  if (locators.value.length) return
  // Old conversations and agent tool results carry no locators; the chunk
  // itself has them (and its own, unexpanded text for matching).
  try {
    const res: any = await getChunkByIdOnly(props.target.chunkId)
    if (version !== loadVersion) return
    locators.value = parseSourceLocators(res?.data?.source_locators)
    chunkContent.value = String(res?.data?.content || '')
    chunkType.value = chunkType.value || String(res?.data?.chunk_type || '')
  } catch {
    chunkContent.value = props.target.content || ''
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
  const scope = chunkContent.value || props.target.content || ''
  return { locators: selected, quotes, token: Date.now(), sentence: anchor, scope }
}

async function load() {
  const version = ++loadVersion
  mode.value = 'loading'
  status.value = 'idle'
  locatedPage.value = 0
  locate.value = null
  await Promise.all([loadMeta(version), loadChunk(version)])
  if (version !== loadVersion) return
  const next = resolveMode()
  mode.value = next
  if (next === 'none') {
    emit('unavailable')
    return
  }
  // A summary speaks for the whole document: open it without hunting for a passage.
  if (next === 'file' && chunkType.value !== 'summary') {
    status.value = 'locating'
    locate.value = buildLocate()
  }
}

function relocate() {
  if (!locate.value) return
  status.value = 'locating'
  locate.value = { ...locate.value, token: Date.now() }
}

function onLocated(result: { found: boolean; precise: boolean; page?: number }) {
  locatedPage.value = result.page || locatorPages(locate.value?.locators || [])[0] || 0
  if (!result.found) status.value = 'missing'
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
