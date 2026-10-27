<template>
  <t-button variant="text" size="small" class="recovery-trigger" @click="open">
    <t-icon :name="failedTotal > 0 ? 'error-circle' : 'file'" />
    <span>{{ t('wikiRecovery.documents') }}</span>
    <span v-if="failedTotal > 0" class="recovery-trigger-count">{{ failedTotal }}</span>
  </t-button>
  <t-drawer v-model:visible="visible" :header="t('wikiRecovery.documents')" size="min(900px, 95vw)"
    :footer="false" class="wiki-recovery-drawer">
    <section class="recovery-panel">
      <div class="recovery-intro">
        <t-icon name="info-circle" size="16px" />
        <p>{{ t('wikiRecovery.description') }}</p>
      </div>
      <div class="recovery-controls">
        <div class="recovery-filters">
          <t-select v-model="status" class="recovery-filter" :options="filters" @change="changeFilter" />
          <t-button variant="outline" :loading="loading" @click="load">
            {{ t('wikiRecovery.refresh') }}
          </t-button>
        </div>
        <div v-if="canEdit && failedIds.length > 0" class="recovery-batch-actions">
          <t-checkbox :checked="selected.length === failedIds.length"
            @change="(checked: boolean) => selected = checked ? [...failedIds] : []">
            {{ t('wikiRecovery.selectPage') }}
          </t-checkbox>
          <t-button :disabled="selected.length === 0" :loading="retrying" @click="retry(selected)">
            {{ t('wikiRecovery.retrySelected', { count: selected.length }) }}
          </t-button>
        </div>
      </div>
      <t-alert v-if="error" theme="error" :message="error" />
      <t-loading :loading="loading" class="recovery-list">
        <article v-for="doc in documents" :key="doc.knowledge_id" class="recovery-document">
          <div class="recovery-document-heading">
            <div class="recovery-document-title">
              <t-checkbox v-if="canEdit && doc.wiki_status === 'failed'"
                :checked="selected.includes(doc.knowledge_id)" :aria-label="doc.title"
                @change="(checked: boolean) => select(doc.knowledge_id, checked)" />
              <t-button variant="text" class="recovery-title" :title="doc.title"
                @click="emit('open-source-doc', doc.knowledge_id)">{{ doc.title }}</t-button>
            </div>
            <t-tag :theme="wikiStatusTheme(doc.wiki_status)" variant="light" size="small">
              {{ t(`wikiRecovery.${doc.wiki_status}`) }}
            </t-tag>
          </div>
          <div class="recovery-source-status">
            {{ t('wikiRecovery.sourceStatus') }}：{{ sourceStatusLabel(doc.parse_status) }}
          </div>
          <details v-if="doc.wiki_status === 'failed'" class="recovery-error">
            <summary>{{ t('wikiRecovery.failureDetails', { count: doc.fail_count }) }}</summary>
            <pre>{{ doc.last_error }}</pre>
          </details>
          <div class="recovery-document-actions">
            <t-button size="small" variant="text" @click="showPages(doc.knowledge_id)">
              {{ t('wikiRecovery.relatedPages') }}
            </t-button>
            <t-button v-if="canEdit && doc.wiki_status === 'failed'" size="small" variant="outline"
              :loading="retrying" @click="retry([doc.knowledge_id])">{{ t('wikiRecovery.retry') }}</t-button>
          </div>
        </article>
        <div v-if="!loading && documents.length === 0" class="recovery-empty" role="status">
          <t-icon name="file" size="40px" />
          <p>{{ t('wikiRecovery.empty') }}</p>
        </div>
      </t-loading>
      <div v-if="total > 0" class="recovery-pagination">
        <t-pagination v-model:current="page" :total="total" :page-size="20" :show-page-size="false"
          @current-change="load" />
      </div>
    </section>
    <t-dialog v-model:visible="pagesVisible" :header="t('wikiRecovery.relatedPages')" :footer="false">
      <t-loading :loading="pagesLoading">
        <t-alert v-if="pagesError" theme="error" :message="pagesError" />
        <div v-for="item in relatedPages" :key="item.slug">
          <t-button variant="text" @click="openPage(item.slug)">{{ item.title }}</t-button>
        </div>
        <t-empty v-if="!pagesLoading && !pagesError && relatedPages.length === 0"
          :description="t('wikiRecovery.noPages')" />
        <t-pagination v-if="pagesTotal > 20" v-model:current="pagesPage" :total="pagesTotal" :page-size="20"
          :show-page-size="false" @current-change="loadPages" />
      </t-loading>
    </t-dialog>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { listWikiDocuments, retryWikiDocuments, listWikiPages, type WikiDocument, type WikiPage } from '@/api/wiki'

const props = defineProps<{ knowledgeBaseId: string; canEdit?: boolean }>()
const emit = defineEmits<{
  (event: 'open-source-doc', id: string): void
  (event: 'open-page', slug: string): void
  (event: 'retried'): void
}>()
const { t } = useI18n()
const visible = ref(false)
const status = ref('failed')
const page = ref(1)
const total = ref(0)
const failedTotal = ref(0)
const documents = ref<WikiDocument[]>([])
const selected = ref<string[]>([])
const failedIds = computed(() => documents.value.filter(doc => doc.wiki_status === 'failed').map(doc => doc.knowledge_id))
const loading = ref(false)
const retrying = ref(false)
const error = ref('')
function wikiStatusTheme(value: WikiDocument['wiki_status']): 'danger' | 'success' | 'primary' | 'default' {
  if (value === 'failed') return 'danger'
  if (value === 'completed') return 'success'
  if (value === 'pending' || value === 'running') return 'primary'
  return 'default'
}
function sourceStatusLabel(value: string): string {
  const labels: Record<string, string> = {
    pending: t('knowledgeBase.parseStatusPending'),
    processing: t('knowledgeBase.parseStatusProcessing'),
    finalizing: t('knowledgeBase.parseStatusFinalizing'),
    completed: t('knowledgeBase.parseStatusCompleted'),
    failed: t('knowledgeBase.parseStatusFailed'),
    cancelled: t('knowledgeBase.parseStatusCancelled'),
    draft: t('knowledgeBase.parseStatusDraft'),
  }
  return labels[value] ?? value
}
const filters = computed(() => [
  { value: 'failed', label: t('wikiRecovery.failed') },
  { value: '', label: t('wikiRecovery.all') },
  { value: 'pending', label: t('wikiRecovery.pending') },
  { value: 'running', label: t('wikiRecovery.running') },
  { value: 'completed', label: t('wikiRecovery.completed') },
])
let generation = 0
async function load() {
  const request = ++generation
  const kbId = props.knowledgeBaseId
  loading.value = true
  error.value = ''
  try {
    const result = await listWikiDocuments(kbId, { status: status.value, page: page.value, page_size: 20 })
    if (request !== generation || kbId !== props.knowledgeBaseId) return
    documents.value = result.documents
    total.value = result.total
    failedTotal.value = result.failed_total
    selected.value = selected.value.filter(id => result.documents.some(doc => doc.knowledge_id === id && doc.wiki_status === 'failed'))
  } catch (e: any) {
    if (request === generation && kbId === props.knowledgeBaseId) error.value = e?.message || t('wikiRecovery.loadFailed')
  } finally {
    if (request === generation) loading.value = false
  }
}
function open() { visible.value = true; void load() }
function changeFilter() { page.value = 1; void load() }
function select(id: string, checked: boolean) {
  selected.value = checked ? [...selected.value, id] : selected.value.filter(value => value !== id)
}
async function retry(ids: string[]) {
  if (retrying.value) return
  const kbId = props.knowledgeBaseId
  retrying.value = true
  error.value = ''
  try {
    const result = await retryWikiDocuments(kbId, ids)
    if (kbId !== props.knowledgeBaseId) return
    MessagePlugin.success(t('wikiRecovery.queued', { count: result.requeued }))
    emit('retried')
    await load()
  } catch (e: any) {
    if (kbId === props.knowledgeBaseId) error.value = e?.message || t('wikiRecovery.retryFailed')
  } finally { retrying.value = false }
}

const pagesVisible = ref(false)
const pagesLoading = ref(false)
const pagesError = ref('')
const pagesPage = ref(1)
const pagesTotal = ref(0)
const sourceId = ref('')
const relatedPages = ref<WikiPage[]>([])
let pagesGeneration = 0
async function loadPages() {
  const request = ++pagesGeneration
  const kbId = props.knowledgeBaseId
  pagesLoading.value = true
  pagesError.value = ''
  try {
    const result = await listWikiPages(kbId, { source_ref: sourceId.value, page: pagesPage.value, page_size: 20 })
    if (request !== pagesGeneration || kbId !== props.knowledgeBaseId) return
    relatedPages.value = result.pages
    pagesTotal.value = result.total
  } catch (e: any) {
    if (request === pagesGeneration) pagesError.value = e?.message || t('wikiRecovery.loadFailed')
  } finally { if (request === pagesGeneration) pagesLoading.value = false }
}
function showPages(id: string) {
  sourceId.value = id; pagesPage.value = 1; relatedPages.value = []; pagesVisible.value = true; void loadPages()
}
function openPage(slug: string) { pagesVisible.value = false; visible.value = false; emit('open-page', slug) }
watch(() => props.knowledgeBaseId, () => {
  generation++; pagesGeneration++; documents.value = []; selected.value = []; failedTotal.value = 0
  pagesVisible.value = false; page.value = 1; void load()
})
onMounted(load)
defineExpose({ refresh: load })
</script>

<style scoped>
.recovery-trigger { gap: 6px; }
.recovery-trigger-count {
  min-width: 18px;
  padding: 0 5px;
  border-radius: 9px;
  background: var(--td-error-color-1);
  color: var(--td-error-color);
  font-size: 12px;
  line-height: 18px;
}
.wiki-recovery-drawer {
  font-family: var(--td-font-family);
  font-size: var(--app-text-base);
  line-height: 22px;
}
.recovery-panel { display: flex; flex-direction: column; gap: 20px; min-width: 0; }
.recovery-intro {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 12px 16px;
  border-radius: var(--td-radius-default);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
  line-height: 20px;
}
.recovery-intro .t-icon { flex-shrink: 0; margin-top: 2px; }
.recovery-intro p { margin: 0; }
.recovery-controls { display: flex; flex-wrap: wrap; align-items: center; gap: 12px 16px; }
.recovery-filters { display: flex; align-items: center; gap: 8px; }
.recovery-filter { width: 180px; }
.recovery-batch-actions { display: flex; align-items: center; gap: 16px; margin-left: auto; white-space: nowrap; }
.recovery-batch-actions :deep(.t-checkbox) { min-height: 32px; }
.recovery-batch-actions :deep(.t-checkbox__label) { white-space: nowrap; line-height: 22px; }
.recovery-list { display: block; width: 100%; min-width: 0; }
.recovery-document { padding: 16px 0; border-bottom: 1px solid var(--td-component-border); }
.recovery-document:first-child { padding-top: 0; }
.recovery-document-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.recovery-document-heading > .t-tag { flex-shrink: 0; }
.recovery-document-title { display: flex; align-items: center; gap: 8px; min-width: 0; }
.recovery-title { min-width: 0; padding: 0; height: auto; font-weight: 500; text-align: left; }
.recovery-title :deep(.t-button__text) { display: block; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.recovery-source-status { margin-top: 6px; color: var(--td-text-color-secondary); font-size: var(--app-text-md); }
.recovery-error { margin-top: 12px; color: var(--td-error-color); font-size: var(--app-text-md); }
.recovery-error summary { cursor: pointer; line-height: 22px; }
.recovery-error pre {
  margin: 8px 0 0;
  padding: 10px 12px;
  max-height: 160px;
  overflow: auto;
  border-radius: var(--td-radius-default);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: inherit;
}
.recovery-document-actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; margin-top: 12px; }
.recovery-empty { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; min-height: 240px; color: var(--td-text-color-disabled); }
.recovery-empty p { margin: 0; color: var(--td-text-color-secondary); font-size: var(--app-text-base); line-height: 22px; }
.recovery-pagination { padding-top: 4px; }
@media (max-width: 720px) {
  .recovery-batch-actions { margin-left: 0; }
  .recovery-controls { gap: 12px; }
  .recovery-document-heading { align-items: flex-start; gap: 8px; }
}
</style>
