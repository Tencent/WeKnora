<template>
  <section class="document-progress" :aria-label="t('documentProgress.title')">
    <div class="progress-heading">
      <div><h3>{{ t('documentProgress.title') }}</h3><p>{{ t('documentProgress.description') }}</p></div>
      <t-button variant="outline" :loading="loading" @click="reload">{{ t('documentProgress.refresh') }}</t-button>
    </div>
    <div class="progress-filters">
      <t-input v-model="searchDraft" :maxlength="200" clearable :placeholder="t('documentProgress.search')"
        @enter="applySearch" @clear="clearSearch" />
      <t-button variant="outline" @click="applySearch">{{ t('documentProgress.searchButton') }}</t-button>
      <t-select v-model="status" class="progress-status" :options="statusOptions" :aria-label="t('documentProgress.filter')" />
      <t-input-number v-model="count" :min="1" :max="100" :decimal-places="0"
        :aria-label="t('documentProgress.count')" />
      <t-popconfirm :content="t('documentProgress.confirmBatch', { count })" @confirm="startWiki()">
        <t-button :disabled="starting || !['all', 'unfinished'].includes(status) || !validCount"
          :loading="starting" >{{ t('documentProgress.batchStart') }}</t-button>
      </t-popconfirm>
    </div>
    <p v-if="error" role="alert" class="progress-error">{{ error }}</p>
    <t-table row-key="id" :data="documents" :columns="columns" :loading="loading" :max-height="560" hover>
      <template #title="{ row }">
        <strong>{{ row.title }}</strong><small>{{ row.knowledge_base_name }} · {{ t('documentProgress.attempt', { value: row.attempt }) }}</small>
      </template>
      <template #parse_status="{ row }">{{ stateLabel(row.ingestion_status) }}</template>
      <template #wiki_status="{ row }">
        <span>{{ stateLabel(row.wiki_status) }}</span>
        <small v-if="row.wiki_pending">{{ t('documentProgress.pendingOps', { count: row.wiki_pending, retries: row.wiki_retries }) }}</small>
      </template>
      <template #failure_count="{ row }">
        <t-button variant="text" @click="openDetail(row.id)">{{ t('documentProgress.historyCount', { count: row.failure_count }) }}</t-button>
      </template>
      <template #actions="{ row }">
        <t-button variant="text" @click="openDetail(row.id)">{{ t('documentProgress.details') }}</t-button>
        <t-popconfirm v-if="row.can_start" :content="t('documentProgress.confirmOne', { title: row.title })"
          @confirm="startWiki(row.id)">
          <t-button variant="text" :disabled="starting">{{ t('documentProgress.startNow') }}</t-button>
        </t-popconfirm>
      </template>
    </t-table>
    <t-pagination v-if="total > 0" v-model:current="page" :total="total" :page-size="pageSize"
      :show-page-size="false" class="progress-pagination" />
    <SettingDrawer v-model:visible="detailVisible" :title="t('documentProgress.details')" width="640px" hide-footer>
      <p v-if="detailError" role="alert" class="progress-error">{{ detailError }}</p>
      <t-loading v-else-if="detailLoading" />
      <template v-else-if="detail">
        <h3>{{ detail.document.title }}</h3>
        <p>{{ t('documentProgress.currentStatus') }}: {{ stateLabel(detail.document.ingestion_status) }} / {{ stateLabel(detail.document.wiki_status) }}</p>
        <ul class="progress-stages">
          <li v-for="stage in detail.document.stages" :key="stage.name">
            <strong>{{ stageLabel(stage.name) }}</strong><span>{{ stateLabel(stage.status) }}</span>
            <small v-if="stage.error_code">{{ stage.error_code }}</small>
          </li>
        </ul>
        <h4>{{ t('documentProgress.history') }}</h4>
        <p>{{ t('documentProgress.historyDescription') }}</p>
        <p v-if="!detail.failures.length">{{ t('documentProgress.noHistory') }}</p>
        <article v-for="failure in detail.failures" :key="failure.id" class="progress-failure">
          <strong>{{ failure.task_type }}</strong> · {{ new Date(failure.failed_at).toLocaleString() }}
          <small>{{ t('documentProgress.failureAttempts', { count: failure.fail_count }) }}</small>
          <pre>{{ failure.last_error }}</pre>
        </article>
        <p v-if="detail.has_more_failures">{{ t('documentProgress.historyLimit') }}</p>
      </template>
    </SettingDrawer>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  getDocumentProgress, listDocumentProgress, startDocumentWiki,
  type DocumentProgress, type DocumentProgressDetail, type DocumentProgressStatus, type WikiStartResult,
} from '@/api/system/documentProgress'

const props = withDefaults(defineProps<{ refreshToken?: string }>(), { refreshToken: '' })
const { t } = useI18n()
const documents = ref<DocumentProgress[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 25
const searchDraft = ref('')
const search = ref('')
const status = ref<DocumentProgressStatus>('unfinished')
const count = ref<number | undefined>(1)
const validCount = computed(() => Number.isInteger(count.value) && Number(count.value) >= 1 && Number(count.value) <= 100)
const loading = ref(false)
const starting = ref(false)
const error = ref('')
const detail = ref<DocumentProgressDetail>()
const detailVisible = ref(false)
const detailLoading = ref(false)
const detailError = ref('')
let listVersion = 0
let detailVersion = 0
let alive = true
const statusOptions = computed(() => [
  { value: 'unfinished', label: t('documentProgress.unfinished') },
  { value: 'all', label: t('documentProgress.all') },
  { value: 'failed', label: t('documentProgress.states.failed') },
  { value: 'completed', label: t('documentProgress.states.completed') },
])
const columns = computed(() => [
  { colKey: 'title', title: t('documentProgress.document') },
  { colKey: 'parse_status', title: t('documentProgress.ingestion') },
  { colKey: 'wiki_status', title: t('documentProgress.wiki') },
  { colKey: 'failure_count', title: t('documentProgress.history') },
  { colKey: 'actions', title: t('documentProgress.actions') },
])

function stateLabel(state: string) {
  const labels: Record<string, string> = {
    pending: t('documentProgress.states.pending'), processing: t('documentProgress.states.processing'),
    running: t('documentProgress.states.processing'), finalizing: t('documentProgress.states.finalizing'),
    completed: t('documentProgress.states.completed'), done: t('documentProgress.states.completed'),
    failed: t('documentProgress.states.failed'), skipped: t('documentProgress.states.skipped'),
    cancelled: t('documentProgress.states.cancelled'), queued: t('documentProgress.states.queued'),
    retrying: t('documentProgress.states.retrying'), disabled: t('documentProgress.states.disabled'),
    not_started: t('documentProgress.states.notStarted'),
    ready: t('documentProgress.states.ready'),
  }
  return labels[state] || state
}
function stageLabel(name: string) {
  const labels: Record<string, string> = {
    docreader: t('documentProgress.stages.docreader'), chunking: t('documentProgress.stages.chunking'),
    embedding: t('documentProgress.stages.embedding'), multimodal: t('documentProgress.stages.multimodal'),
    postprocess: t('documentProgress.stages.postprocess'), 'postprocess.wiki': t('documentProgress.wiki'),
  }
  return labels[name] || name
}
async function reload() {
  const version = ++listVersion
  loading.value = true
  try {
    const response = await listDocumentProgress({ page: page.value, page_size: pageSize, status: status.value, search: search.value })
    if (!alive || version !== listVersion) return
    documents.value = response.data.documents
    total.value = response.data.total
    const lastPage = Math.max(1, Math.ceil(total.value / pageSize))
    if (page.value > lastPage) page.value = lastPage
    error.value = ''
  } catch {
    if (alive && version === listVersion) error.value = t('documentProgress.loadError')
  } finally {
    if (alive && version === listVersion) loading.value = false
  }
}
function applySearch() {
  search.value = searchDraft.value.trim()
  if (page.value === 1) void reload()
  else page.value = 1
}
function clearSearch() { searchDraft.value = ''; applySearch() }
async function openDetail(id: string) {
  const version = ++detailVersion
  detailVisible.value = true
  detailLoading.value = true
  detailError.value = ''
  detail.value = undefined
  try {
    const response = await getDocumentProgress(id)
    if (alive && version === detailVersion) detail.value = response.data
  } catch {
    if (alive && version === detailVersion) detailError.value = t('documentProgress.loadError')
  } finally {
    if (alive && version === detailVersion) detailLoading.value = false
  }
}
async function startWiki(id?: string) {
  if (starting.value || (!id && (!validCount.value || !['all', 'unfinished'].includes(status.value)))) return
  starting.value = true
  try {
    const response = await startDocumentWiki({ count: id ? 1 : Number(count.value), ...(id ? { knowledge_id: id } : { search: search.value }) })
    if (!alive) return
    MessagePlugin.success(t('documentProgress.started', { count: response.data.started, queued: response.data.already_queued }))
  } catch (cause) {
    if (!alive) return
    const partial = (cause as { data?: Partial<WikiStartResult> })?.data
    MessagePlugin.error(t('documentProgress.startError', { count: Number(partial?.started) || 0 }))
  } finally {
    if (alive) { starting.value = false; await reload() }
  }
}
watch(page, () => { void reload() })
watch(status, () => { if (page.value === 1) void reload(); else page.value = 1 })
watch(() => props.refreshToken, () => { void reload() })
watch(detailVisible, visible => { if (!visible) { detailVersion++; detailLoading.value = false } })
onMounted(() => { void reload() })
onUnmounted(() => { alive = false; listVersion++; detailVersion++ })
</script>

<style scoped lang="less">
.document-progress { margin: 24px 0; padding: 20px; border: 1px solid var(--td-component-stroke); border-radius: var(--app-radius-lg); }
.progress-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.progress-heading h3 { margin: 0; font-size: var(--app-text-lg); }
.progress-heading p, .document-progress small { color: var(--td-text-color-secondary); font-size: var(--app-text-sm); }
.document-progress small { display: block; margin-top: 4px; }
.progress-filters { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; margin: 16px 0; }
.progress-filters > :first-child { flex: 1; min-width: 180px; }
.progress-status { width: 160px; flex: 0 0 160px; }
.progress-error { color: var(--td-error-color); }
.progress-pagination { margin-top: 16px; }
.progress-stages { padding: 0; list-style: none; }
.progress-stages li { display: flex; gap: 12px; padding: 8px 0; }
.progress-failure { margin: 16px 0; padding: 12px; background: var(--td-bg-color-secondarycontainer); border-radius: var(--app-radius-md); }
.progress-failure pre { white-space: pre-wrap; overflow-wrap: anywhere; font-size: var(--app-text-sm); }
@media (max-width: 600px) { .document-progress { padding: 12px; } .progress-filters > * { flex: 1 1 100%; } }
</style>
