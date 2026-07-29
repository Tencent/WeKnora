<template>
  <div ref="readerRootRef" class="wiki-reader-body wiki-provenance-reader">
    <section
      v-for="entry in renderEntries"
      :key="entry.key"
      :class="['wiki-provenance-block', `wiki-provenance-block--${entry.blockType || 'paragraph'}`]"
    >
      <div class="wiki-provenance-block-content" v-html="renderMarkdown(entry.content)" @click="handleContentClick" />
      <div
        v-if="entry.citationGroups.some((group) => !group.structural && (group.provenanceStatus || group.citationNumbers.length))"
        class="wiki-provenance-citations"
      >
        <template v-for="group in entry.citationGroups" :key="group.key">
          <span
            v-if="!group.structural && (group.provenanceStatus || group.citationNumbers.length)"
            class="wiki-provenance-citation-group"
          >
            <span v-if="entry.grouped" class="wiki-provenance-citation-label">
              {{ sourceGroupLabel(entry.blockType, group.position) }}
            </span>
            <t-tag
              v-if="group.provenanceStatus"
              size="small"
              variant="light"
              :theme="blockProvenanceTheme(group.provenanceStatus, group.authorType)"
            >
              {{ blockProvenanceLabel(group.provenanceStatus, group.authorType) }}
            </t-tag>
            <button
              v-for="citationNumber in group.citationNumbers"
              :key="citationNumber"
              type="button"
              class="wiki-provenance-citation"
              :aria-label="t('knowledgeEditor.wikiBrowser.openParagraphSource', { number: citationNumber })"
              @click="openSource(citationNumber)"
            >[{{ citationNumber }}]</button>
          </span>
        </template>
      </div>
    </section>

    <t-drawer
      v-model:visible="drawerVisible"
      :header="t('knowledgeEditor.wikiBrowser.sourceDrawerTitle')"
      size="480px"
      :footer="false"
    >
      <div class="wiki-provenance-source-summary">
        {{ t('knowledgeEditor.wikiBrowser.paragraphSourceCount', { count: citationModel.sources.length }) }}
      </div>
      <div class="wiki-provenance-source-list">
        <article
          v-for="source in citationModel.sources"
          :key="source.citationKey"
          :class="['wiki-provenance-source-card', { active: source.citationKey === activeCitationKey }]"
          @click="activeCitationKey = source.citationKey"
        >
          <header class="wiki-provenance-source-header">
            <span class="wiki-provenance-source-number">[{{ source.citationNumber }}]</span>
            <strong class="wiki-provenance-source-title">{{ sourceTitle(source) }}</strong>
            <t-tag size="small" variant="light" :theme="sourceStatusTheme(source.validation_status)">
              {{ sourceStatusLabel(source.validation_status) }}
            </t-tag>
          </header>
          <div class="wiki-provenance-source-meta">
            {{ t('knowledgeEditor.wikiBrowser.sourceChunk', { chunk: shortID(source.chunk_id) }) }}
          </div>
          <blockquote v-if="source.evidence" class="wiki-provenance-source-evidence">
            {{ source.evidence }}
          </blockquote>
          <div v-else class="wiki-provenance-source-empty">
            {{ t('knowledgeEditor.wikiBrowser.sourceEvidenceUnavailable') }}
          </div>
          <t-link
            v-if="source.knowledge_id"
            theme="primary"
            hover="color"
            class="wiki-provenance-open-document"
            @click.stop="emit('open-source-doc', source.knowledge_id)"
          >
            <template #prefixIcon><t-icon name="file" /></template>
            {{ t('knowledgeEditor.wikiBrowser.openSourceDocument') }}
          </t-link>
        </article>
      </div>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { WikiBlockSource, WikiPageBlock } from '@/api/wiki'
import { hydrateProtectedFileImages } from '@/utils/security'
import { buildWikiCitationModel, buildWikiProvenanceRenderEntries } from '@/utils/wikiBlockSources'
import { renderWikiMarkdown } from '@/utils/wikiMarkdown'

const props = defineProps<{
  blocks: WikiPageBlock[]
  knowledgeBaseId: string
}>()

const emit = defineEmits<{
  (event: 'navigate-wiki', slug: string): void
  (event: 'open-source-doc', knowledgeId: string): void
  (event: 'preview-image', url: string): void
}>()

const { t } = useI18n()
const readerRootRef = ref<HTMLElement | null>(null)
const drawerVisible = ref(false)
const activeCitationKey = ref<string | null>(null)
// Summary is stored as a sourced block for lifecycle/retraction, but the
// existing reader does not render WikiPage.summary above the Markdown body.
// Keep that wire-compatible presentation and number only visible body blocks.
const citationModel = computed(() => buildWikiCitationModel(
  props.blocks.filter((block) => block.block_type !== 'summary'),
))
const renderEntries = computed(() => buildWikiProvenanceRenderEntries(citationModel.value.blocks))

function renderMarkdown(content: string): string {
  return renderWikiMarkdown(content)
}

function sourceGroupLabel(blockType: string, position: number): string {
  const key = blockType === 'table_row' ? 'tableRowSource' : 'listItemSource'
  return t(`knowledgeEditor.wikiBrowser.${key}`, { index: position })
}

function blockProvenanceLabel(status: string, authorType: string): string {
  if (authorType === 'user') return t('knowledgeEditor.wikiBrowser.blockSourceManual')
  if (authorType === 'agent') return t('knowledgeEditor.wikiBrowser.blockSourceAgent')
  switch ((status || '').toLowerCase()) {
    case 'verified':
    case 'complete':
      return t('knowledgeEditor.wikiBrowser.blockProvenanceComplete')
    case 'partial':
      return t('knowledgeEditor.wikiBrowser.blockProvenancePartial')
    case 'unsupported':
      return t('knowledgeEditor.wikiBrowser.blockProvenanceUnsupported')
    case 'legacy_inferred':
      return t('knowledgeEditor.wikiBrowser.blockProvenanceLegacy')
    default:
      return t('knowledgeEditor.wikiBrowser.blockProvenancePending')
  }
}

function blockProvenanceTheme(
  status: string,
  authorType: string,
): 'primary' | 'success' | 'warning' | 'danger' | 'default' {
  if (authorType === 'user' || authorType === 'agent') return 'primary'
  switch ((status || '').toLowerCase()) {
    case 'verified':
    case 'complete':
      return 'success'
    case 'partial':
      return 'warning'
    case 'unsupported':
      return 'danger'
    default:
      return 'default'
  }
}

function handleContentClick(event: MouseEvent) {
  const target = event.target as HTMLElement
  const wikiLink = target.closest<HTMLAnchorElement>('a.wiki-content-link')
  if (wikiLink) {
    event.preventDefault()
    const slug = wikiLink.getAttribute('data-slug')
    if (slug) emit('navigate-wiki', slug)
    return
  }

  const image = target.closest<HTMLImageElement>('img')
  if (image) {
    event.preventDefault()
    const url = image.getAttribute('src') || ''
    if (url) emit('preview-image', url)
  }
}

function openSource(citationNumber: number) {
  const source = citationModel.value.sources.find((candidate) => candidate.citationNumber === citationNumber)
  if (!source) return
  activeCitationKey.value = source.citationKey
  drawerVisible.value = true
}

function sourceTitle(source: WikiBlockSource): string {
  const title = source.document_title?.trim()
  if (title) return title
  return shortID(source.knowledge_id)
}

function shortID(value: string): string {
  if (!value) return '-'
  return value.length > 20 ? `${value.substring(0, 8)}...` : value
}

function sourceStatusLabel(status: string): string {
  switch ((status || '').toLowerCase()) {
    case 'verified':
    case 'valid':
      return t('knowledgeEditor.wikiBrowser.sourceStatusVerified')
    case 'located':
      return t('knowledgeEditor.wikiBrowser.sourceStatusLocated')
    case 'partial':
      return t('knowledgeEditor.wikiBrowser.sourceStatusPartial')
    case 'invalid':
      return t('knowledgeEditor.wikiBrowser.sourceStatusInvalid')
    case 'unsupported':
      return t('knowledgeEditor.wikiBrowser.sourceStatusUnsupported')
    default:
      return t('knowledgeEditor.wikiBrowser.sourceStatusPending')
  }
}

function sourceStatusTheme(status: string): 'primary' | 'success' | 'warning' | 'danger' | 'default' {
  switch ((status || '').toLowerCase()) {
    case 'verified':
    case 'valid':
      return 'success'
    case 'located':
      return 'primary'
    case 'partial':
      return 'warning'
    case 'unsupported':
    case 'invalid':
      return 'danger'
    default:
      return 'default'
  }
}

async function hydrateImages() {
  await nextTick()
  if (readerRootRef.value) {
    await hydrateProtectedFileImages(readerRootRef.value, undefined, props.knowledgeBaseId)
  }
}

watch(
  () => props.blocks,
  () => {
    if (
      activeCitationKey.value !== null &&
      !citationModel.value.sources.some((source) => source.citationKey === activeCitationKey.value)
    ) {
      activeCitationKey.value = null
      drawerVisible.value = false
    }
    void hydrateImages()
  },
  { deep: true },
)

onMounted(() => void hydrateImages())
</script>

<style scoped lang="less">
.wiki-provenance-reader {
  display: flex;
  flex-direction: column;
}

.wiki-provenance-block {
  position: relative;
}

.wiki-provenance-block-content {
  min-width: 0;
}

.wiki-provenance-citations {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin: -10px 0 14px 4px;
  vertical-align: super;
}

.wiki-provenance-citation-group {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.wiki-provenance-citation-label {
  margin-right: 2px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.wiki-provenance-citation {
  appearance: none;
  border: 0;
  padding: 0 2px;
  background: transparent;
  color: var(--td-brand-color);
  font: inherit;
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
  cursor: pointer;

  &:hover,
  &:focus-visible {
    text-decoration: underline;
    outline: none;
  }
}

.wiki-provenance-source-summary {
  margin-bottom: 12px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.wiki-provenance-source-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.wiki-provenance-source-card {
  padding: 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  transition: border-color 0.15s, background 0.15s;

  &.active {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}

.wiki-provenance-source-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.wiki-provenance-source-number {
  color: var(--td-brand-color);
  font-size: 12px;
  font-weight: 600;
}

.wiki-provenance-source-title {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wiki-provenance-source-meta,
.wiki-provenance-source-empty {
  margin-top: 8px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.wiki-provenance-source-evidence {
  margin: 10px 0;
  padding: 10px 12px;
  border-left: 3px solid var(--td-brand-color-light-active);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
}

.wiki-provenance-open-document {
  margin-top: 2px;
}
</style>
