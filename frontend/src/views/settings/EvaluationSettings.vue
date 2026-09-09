<template>
  <div class="evaluation-settings">
    <header class="evaluation-header">
      <div>
        <h2>{{ t('evaluationSettings.title') }}</h2>
        <p>{{ t('evaluationSettings.description') }}</p>
      </div>
      <t-button variant="outline" :loading="loading" @click="loadPage">
        {{ t('evaluationSettings.refresh') }}
      </t-button>
    </header>

    <section class="evaluation-panel">
      <div class="panel-heading">
        <div>
          <h3>{{ t('evaluationSettings.newRun') }}</h3>
          <p>{{ t('evaluationSettings.newRunHint') }}</p>
        </div>
      </div>
      <div class="evaluation-form">
        <label>
          <span>{{ t('evaluationSettings.dataset') }}</span>
          <t-select v-model="form.dataset_id" :placeholder="t('evaluationSettings.autoSelect')">
            <t-option
              v-for="dataset in datasets"
              :key="dataset.id"
              :value="dataset.id"
              :label="dataset.name"
              :disabled="!dataset.available"
            />
          </t-select>
        </label>
        <label>
          <span>{{ t('evaluationSettings.knowledgeBase') }}</span>
          <t-select v-model="form.knowledge_base_id" clearable :placeholder="t('evaluationSettings.autoSelect')">
            <t-option v-for="kb in knowledgeBases" :key="kb.id" :value="kb.id" :label="kb.name" />
          </t-select>
        </label>
        <label>
          <span>{{ t('evaluationSettings.chatModel') }}</span>
          <t-select v-model="form.chat_id" clearable :placeholder="t('evaluationSettings.autoSelect')">
            <t-option v-for="model in chatModels" :key="model.id" :value="model.id" :label="modelLabel(model)" />
          </t-select>
        </label>
        <label>
          <span>{{ t('evaluationSettings.rerankModel') }}</span>
          <t-select v-model="form.rerank_id" clearable :placeholder="t('evaluationSettings.autoSelect')">
            <t-option v-for="model in rerankModels" :key="model.id" :value="model.id" :label="modelLabel(model)" />
          </t-select>
        </label>
      </div>
      <div v-if="selectedDataset && !selectedDataset.available" class="dataset-error">
        {{ selectedDataset.validation_error }}
      </div>
      <div class="run-actions">
        <span v-if="!canRun">{{ t('evaluationSettings.adminOnly') }}</span>
        <span v-else-if="!form.chat_id">{{ t('evaluationSettings.chatRequired') }}</span>
        <div class="run-buttons">
          <t-button variant="text" :disabled="starting" @click="resetForm">{{ t('evaluationSettings.reset') }}</t-button>
          <t-button theme="primary" :loading="starting" :disabled="!canRun || !form.dataset_id || !form.chat_id" @click="runEvaluation">
            {{ t('evaluationSettings.start') }}
          </t-button>
        </div>
      </div>
      <div v-if="activeRun" class="active-run" role="status">
        <div>
          <strong>{{ statusLabel(activeRun.task.status) }}</strong>
          <span>{{ activeRun.task.finished || 0 }} / {{ activeRun.task.total || 0 }}</span>
        </div>
        <div class="progress-track"><span :style="{ width: `${progressPercent(activeRun.task)}%` }" /></div>
        <p v-if="activeRun.task.err_msg">{{ activeRun.task.err_msg }}</p>
      </div>
    </section>

    <section v-if="comparisonRows.length" class="evaluation-panel comparison-panel">
      <div class="panel-heading">
        <div>
          <h3>{{ t('evaluationSettings.comparison') }}</h3>
          <p>{{ t('evaluationSettings.comparisonHint', { count: comparisonLimit }) }}</p>
        </div>
        <t-button variant="text" @click="selectedRunIDs = []">{{ t('evaluationSettings.clear') }}</t-button>
      </div>
      <div class="comparison-status" :data-comparable="comparisonWarnings.length === 0">
        {{ comparisonWarnings.length ? comparisonWarnings.join(' ') : t('evaluationSettings.comparable') }}
      </div>
      <div class="comparison-scroll">
        <table>
          <thead>
            <tr>
              <th>{{ t('evaluationSettings.metric') }}</th>
              <th v-for="(run, index) in selectedRuns" :key="run.task.id">
                {{ runLabel(run, index) }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in comparisonRows" :key="row.key">
              <td>{{ row.label }}</td>
              <td v-for="(value, index) in row.values" :key="selectedRuns[index].task.id">
                <span>{{ value === undefined ? '—' : formatValue(value, row.format) }}</span>
                <small
                  v-if="index > 0 && value !== undefined && row.values[0] !== undefined"
                  :class="deltaClass(value - row.values[0]!, row.higherIsBetter)"
                >{{ formatDelta(value - row.values[0]!, row.format) }}</small>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section class="evaluation-panel">
      <div class="panel-heading">
        <div>
          <h3>{{ t('evaluationSettings.history') }}</h3>
          <p>{{ t('evaluationSettings.historyHint') }}</p>
        </div>
        <span>{{ t('evaluationSettings.total', { count: runPage.total }) }}</span>
      </div>
      <div v-if="!runPage.items.length && !loading" class="empty-state">{{ t('evaluationSettings.empty') }}</div>
      <div class="run-list">
        <article v-for="run in runPage.items" :key="run.task.id" class="run-card" :class="{ selected: selectedRunIDs.includes(run.task.id), invalid: isInvalidRun(run) }">
          <label class="run-select">
            <input type="checkbox" :checked="selectedRunIDs.includes(run.task.id)" :disabled="!isSelectableRun(run)" @change="toggleRun(run.task.id)" />
            <span>{{ t('evaluationSettings.selectCompare') }}</span>
          </label>
          <div class="run-main">
            <div class="run-identity">
              <strong>{{ run.task.dataset_id }}</strong>
              <span class="run-id">{{ run.task.id }}</span>
            </div>
            <div class="run-card-actions">
              <t-button v-if="run.task.status === 2" size="small" variant="text" :loading="exportingRunID === run.task.id" @click="exportEvidence(run.task.id)">
                {{ t('evaluationSettings.exportEvidence') }}
              </t-button>
              <span class="status-pill" :data-status="runDisplayStatus(run)">{{ runStatusLabel(run) }}</span>
            </div>
          </div>
          <div class="run-meta">
            <span>{{ formatDate(run.task.start_time) }}</span>
            <span>{{ t('evaluationSettings.modelValue', { value: runModelLabel(run) }) }}</span>
            <span>{{ t('evaluationSettings.knowledgeBaseValue', { value: runKnowledgeBaseLabel(run) }) }}</span>
            <span>{{ formatDuration(run.task.duration_ms) }}</span>
            <span v-if="run.metric">Recall {{ formatValue(run.metric.retrieval_metrics.recall, 'percent') }}</span>
            <span v-if="run.usage">{{ formatInteger(run.usage.total_tokens) }} Token</span>
            <span v-if="isInvalidRun(run)" class="invalid-hint">{{ t('evaluationSettings.invalidHint') }}</span>
          </div>
        </article>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { listModels, type ModelConfig } from '@/api/model'
import { listKnowledgeBases } from '@/api/knowledge-base'
import {
  getEvaluationDatasets,
  getEvaluationEvidence,
  getEvaluationResult,
  getEvaluationRuns,
  startEvaluation,
  type EvaluationDataset,
  type EvaluationRunPage,
  type EvaluationRunSummary,
  type EvaluationTask,
} from '@/api/evaluation'

const { t, locale } = useI18n()
const authStore = useAuthStore()
const loading = ref(false)
const starting = ref(false)
const exportingRunID = ref('')
const datasets = ref<EvaluationDataset[]>([])
const models = ref<ModelConfig[]>([])
const knowledgeBases = ref<Array<{ id: string; name: string }>>([])
const runPage = ref<EvaluationRunPage>({ items: [], total: 0, limit: 50, offset: 0 })
const activeRun = ref<EvaluationRunSummary | null>(null)
const selectedRunIDs = ref<string[]>([])
const form = reactive({ dataset_id: 'default', knowledge_base_id: '', chat_id: '', rerank_id: '' })
const evaluationFormStoragePrefix = 'weknora_evaluation_form_v1'
const comparisonLimit = 4
let pollTimer: ReturnType<typeof setTimeout> | undefined
let consecutivePollFailures = 0

const canRun = computed(() => authStore.canAccessAllTenants || authStore.hasRole('admin'))
const chatModels = computed(() => models.value.filter(model => model.type === 'KnowledgeQA' && model.id))
const rerankModels = computed(() => models.value.filter(model => model.type === 'Rerank' && model.id))
const selectedDataset = computed(() => datasets.value.find(item => item.id === form.dataset_id))
const selectedRuns = computed(() => selectedRunIDs.value.map(id => runPage.value.items.find(run => run.task.id === id)).filter(Boolean) as EvaluationRunSummary[])
const comparisonWarnings = computed(() => {
  if (selectedRuns.value.length < 2) return []
  const [base, ...candidates] = selectedRuns.value
  if (!base.run_config) return [t('evaluationSettings.comparabilityUnknown')]
  const warnings = new Set<string>()
  for (const candidate of candidates) {
    if (!candidate.run_config) {
      warnings.add(t('evaluationSettings.comparabilityUnknown'))
      continue
    }
    if (base.run_config.dataset_fingerprint !== candidate.run_config.dataset_fingerprint) warnings.add(t('evaluationSettings.datasetDiffers'))
    if (base.run_config.source_knowledge_base_id !== candidate.run_config.source_knowledge_base_id) warnings.add(t('evaluationSettings.knowledgeBaseDiffers'))
    if (stableStringify(base.run_config.chunking) !== stableStringify(candidate.run_config.chunking)) warnings.add(t('evaluationSettings.chunkingDiffers'))
    if (controlledPipeline(base.run_config.pipeline) !== controlledPipeline(candidate.run_config.pipeline)) warnings.add(t('evaluationSettings.pipelineDiffers'))
    if (controlledModelFingerprints(base) !== controlledModelFingerprints(candidate)) warnings.add(t('evaluationSettings.modelDependencyDiffers'))
  }
  return [...warnings]
})

const metricDefinitions = [
  ['precision', 'Precision'], ['recall', 'Recall'], ['ndcg3', 'NDCG@3'], ['ndcg10', 'NDCG@10'], ['mrr', 'MRR'], ['map', 'MAP'],
] as const
const generationDefinitions = [
  ['bleu1', 'BLEU-1'], ['bleu2', 'BLEU-2'], ['bleu4', 'BLEU-4'], ['rouge1', 'ROUGE-1'], ['rouge2', 'ROUGE-2'], ['rougel', 'ROUGE-L'],
] as const

type ComparisonRow = { key: string; label: string; values: Array<number | undefined>; format: 'number' | 'percent' | 'duration' | 'integer' | 'cost'; higherIsBetter: boolean }
const comparisonRows = computed<ComparisonRow[]>(() => {
  if (selectedRuns.value.length < 2) return []
  const rows: ComparisonRow[] = []
  for (const [key, label] of metricDefinitions) addRow(rows, `retrieval.${key}`, label, selectedRuns.value.map(run => run.metric?.retrieval_metrics[key]), 'percent', true)
  for (const [key, label] of generationDefinitions) addRow(rows, `generation.${key}`, label, selectedRuns.value.map(run => run.metric?.generation_metrics[key]), 'percent', true)
  addRow(rows, 'duration', t('evaluationSettings.duration'), selectedRuns.value.map(run => run.task.duration_ms), 'duration', false)
  addRow(rows, 'tokens', 'Token', selectedRuns.value.map(run => run.usage?.total_tokens), 'integer', false)
  const cacheHitRates = selectedRuns.value.map(run =>
    run.usage && run.usage.cache_reported_calls > 0 ? run.usage.cache_hit_rate : undefined)
  if (cacheHitRates.some(value => value !== undefined)) {
    rows.push({ key: 'cache', label: t('evaluationSettings.cacheHitRate'), values: cacheHitRates, format: 'percent', higherIsBetter: true })
  }
  const currencies = new Set(selectedRuns.value.flatMap(run => Object.keys(run.usage?.cost_by_currency || {})))
  for (const currency of [...currencies].sort()) {
    rows.push({
      key: `cost.${currency}`,
      label: `${t('evaluationSettings.cost')} (${currency})`,
      values: selectedRuns.value.map(run => {
        const costs = run.usage?.cost_by_currency
        return costs && Object.prototype.hasOwnProperty.call(costs, currency) ? costs[currency] : undefined
      }),
      format: 'cost',
      higherIsBetter: false,
    })
  }
  return rows
})

function addRow(rows: ComparisonRow[], key: string, label: string, values: Array<number | undefined>, format: ComparisonRow['format'], higherIsBetter: boolean) {
  if (values.filter(Number.isFinite).length < 2) return
  rows.push({ key, label, values, format, higherIsBetter })
}

async function loadPage() {
  loading.value = true
  try {
    const [datasetRows, modelRows, kbResponse, runs] = await Promise.all([
      getEvaluationDatasets(), listModels(), listKnowledgeBases({ creator: 'all' }), getEvaluationRuns(50, 0),
    ])
    datasets.value = datasetRows
    models.value = modelRows
    const kbRows = Array.isArray((kbResponse as any)?.data) ? (kbResponse as any).data : []
    knowledgeBases.value = kbRows.map((kb: any) => ({ id: String(kb.id), name: kb.name || kb.id }))
    runPage.value = runs
    if (!datasets.value.some(item => item.id === form.dataset_id && item.available)) {
      form.dataset_id = datasets.value.find(item => item.available)?.id || ''
    }
    if (form.knowledge_base_id && !knowledgeBases.value.some(item => item.id === form.knowledge_base_id)) form.knowledge_base_id = ''
    if (form.chat_id && !chatModels.value.some(item => item.id === form.chat_id)) form.chat_id = ''
    if (form.rerank_id && !rerankModels.value.some(item => item.id === form.rerank_id)) form.rerank_id = ''
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('evaluationSettings.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function runEvaluation() {
  starting.value = true
  try {
    activeRun.value = await startEvaluation({ ...form })
    consecutivePollFailures = 0
    schedulePoll()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('evaluationSettings.startFailed'))
  } finally {
    starting.value = false
  }
}

async function exportEvidence(taskID: string) {
  exportingRunID.value = taskID
  try {
    const report = await getEvaluationEvidence(taskID)
    const blob = new Blob([`${JSON.stringify(report, null, 2)}\n`], { type: 'application/json;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `evaluation-evidence-${taskID}.json`
    anchor.click()
    setTimeout(() => URL.revokeObjectURL(url), 0)
    MessagePlugin.success(t('evaluationSettings.exportSucceeded'))
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('evaluationSettings.exportFailed'))
  } finally {
    exportingRunID.value = ''
  }
}

function schedulePoll() {
  if (pollTimer) clearTimeout(pollTimer)
  if (!activeRun.value || [2, 3].includes(activeRun.value.task.status)) {
    void loadPage()
    return
  }
  pollTimer = setTimeout(async () => {
    try {
      activeRun.value = await getEvaluationResult(activeRun.value!.task.id)
      consecutivePollFailures = 0
      schedulePoll()
    } catch (error: any) {
      consecutivePollFailures += 1
      if (consecutivePollFailures < 3) {
        schedulePoll()
      } else {
        MessagePlugin.error(error?.message || t('evaluationSettings.pollFailed'))
      }
    }
  }, 2000)
}

function toggleRun(id: string) {
  if (selectedRunIDs.value.includes(id)) {
    selectedRunIDs.value = selectedRunIDs.value.filter(item => item !== id)
  } else {
    selectedRunIDs.value = [...selectedRunIDs.value.slice(-(comparisonLimit - 1)), id]
  }
}

function evaluationFormStorageKey(): string {
  return `${evaluationFormStoragePrefix}:${authStore.currentTenantId ?? 'none'}`
}

function restoreForm(): void {
  try {
    const saved = JSON.parse(sessionStorage.getItem(evaluationFormStorageKey()) || '{}')
    for (const key of ['dataset_id', 'knowledge_base_id', 'chat_id', 'rerank_id'] as const) {
      if (typeof saved[key] === 'string') form[key] = saved[key]
    }
  } catch {
    // Ignore malformed browser state; loadPage validates every restored ID.
  }
}

function resetForm(): void {
  form.dataset_id = datasets.value.find(item => item.available)?.id || 'default'
  form.knowledge_base_id = ''
  form.chat_id = ''
  form.rerank_id = ''
}

function isInvalidRun(run: EvaluationRunSummary): boolean {
  return run.task.status === 2 && !!run.usage && run.usage.successful_calls === 0
}

function isSelectableRun(run: EvaluationRunSummary): boolean {
  return run.task.status === 2 && !isInvalidRun(run)
}

function runDisplayStatus(run: EvaluationRunSummary): string | number {
  return isInvalidRun(run) ? 'invalid' : run.task.status
}

function runStatusLabel(run: EvaluationRunSummary): string {
  return isInvalidRun(run) ? t('evaluationSettings.status.invalid') : statusLabel(run.task.status)
}

function runModelLabel(run: EvaluationRunSummary): string {
  const model = run.run_config?.models?.find(item => item.role === 'chat')
  return model?.display_name || model?.name || t('evaluationSettings.unknownValue')
}

function runKnowledgeBaseLabel(run: EvaluationRunSummary): string {
  const id = run.run_config?.source_knowledge_base_id
  if (!id) return t('evaluationSettings.defaultConfiguration')
  return knowledgeBases.value.find(item => item.id === id)?.name || id
}

function stableStringify(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(',')}]`
  if (value && typeof value === 'object') {
    return `{${Object.entries(value as Record<string, unknown>).sort(([left], [right]) => left.localeCompare(right)).map(([key, item]) => `${JSON.stringify(key)}:${stableStringify(item)}`).join(',')}}`
  }
  return JSON.stringify(value) ?? 'undefined'
}

function controlledPipeline(pipeline: Record<string, unknown> | undefined): string {
  if (!pipeline) return ''
  const controlled = { ...pipeline }
  delete controlled.chat_model_id
  return stableStringify(controlled)
}

function controlledModelFingerprints(run: EvaluationRunSummary): string {
  return stableStringify((run.run_config?.models || [])
    .filter(model => model.role !== 'chat')
    .map(model => ({ role: model.role, fingerprint: model.config_fingerprint || model.id }))
    .sort((left, right) => left.role.localeCompare(right.role)))
}

function runLabel(run: EvaluationRunSummary, index: number): string {
  const chatModel = run.run_config?.models?.find(model => model.role === 'chat')
  const label = chatModel?.display_name || chatModel?.name || run.task.id.slice(-8)
  return index === 0 ? `${t('evaluationSettings.base')}: ${label}` : label
}

const statusLabel = (status: number) => t(`evaluationSettings.status.${['pending', 'running', 'success', 'failed'][status] || 'unknown'}`)
const progressPercent = (task: EvaluationTask) => task.total ? Math.min(100, (task.finished || 0) / task.total * 100) : 0
const modelLabel = (model: ModelConfig) => model.display_name || model.name
const formatInteger = (value: number) => new Intl.NumberFormat(locale.value).format(value || 0)
const formatDate = (value: string) => value ? new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) : '—'
const formatDuration = (value: number) => value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${value || 0} ms`
function formatValue(value: number, format: ComparisonRow['format']) {
  if (format === 'percent') return `${(value * 100).toFixed(2)}%`
  if (format === 'duration') return formatDuration(value)
  if (format === 'integer') return formatInteger(value)
  if (format === 'cost') return value.toFixed(6)
  return value.toFixed(4)
}
function formatDelta(value: number, format: ComparisonRow['format']) {
  const sign = value > 0 ? '+' : value < 0 ? '-' : ''
  const absoluteValue = Math.abs(value)
  if (format === 'percent') return `${sign}${(absoluteValue * 100).toFixed(2)} pp`
  if (format === 'duration') return `${sign}${formatDuration(absoluteValue)}`
  if (format === 'integer') return `${sign}${formatInteger(absoluteValue)}`
  return `${sign}${formatValue(absoluteValue, format)}`
}
function deltaClass(delta: number, higherIsBetter: boolean) {
  if (delta === 0) return 'delta-neutral'
  return (delta > 0) === higherIsBetter ? 'delta-positive' : 'delta-negative'
}

watch(form, value => {
  try {
    sessionStorage.setItem(evaluationFormStorageKey(), JSON.stringify(value))
  } catch {
    // Storage may be unavailable in hardened/private browser contexts.
  }
}, { deep: true })
onMounted(() => {
  restoreForm()
  void loadPage()
})
onUnmounted(() => { if (pollTimer) clearTimeout(pollTimer) })
</script>

<style scoped lang="less">
.evaluation-settings { display: flex; flex-direction: column; gap: 18px; }
.evaluation-header, .panel-heading, .run-main, .run-actions, .active-run > div { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
h2, h3, p { margin: 0; }
.evaluation-header p, .panel-heading p { margin-top: 5px; color: var(--td-text-color-secondary); }
.evaluation-panel { padding: 18px; border: 1px solid var(--td-component-stroke); border-radius: 12px; background: var(--td-bg-color-container); }
.evaluation-form { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px; margin-top: 16px; }
.evaluation-form label { display: flex; flex-direction: column; gap: 7px; color: var(--td-text-color-secondary); font-size: 13px; }
.run-actions { margin-top: 16px; color: var(--td-text-color-secondary); font-size: 13px; }
.run-buttons { display: flex; align-items: center; gap: 8px; margin-left: auto; }
.dataset-error, .active-run { margin-top: 14px; padding: 12px; border-radius: 8px; background: var(--td-warning-color-light); color: var(--td-warning-color); }
.progress-track { height: 6px; margin-top: 10px; overflow: hidden; border-radius: 999px; background: var(--td-bg-color-secondarycontainer); }
.progress-track span { display: block; height: 100%; background: var(--td-brand-color); transition: width .2s ease; }
.run-list { display: grid; gap: 10px; margin-top: 14px; }
.run-card { padding: 14px; border: 1px solid var(--td-component-stroke); border-radius: 9px; }
.run-card.selected { border-color: var(--td-brand-color); box-shadow: 0 0 0 1px var(--td-brand-color); }
.run-select { display: inline-flex; gap: 6px; margin-bottom: 9px; color: var(--td-text-color-secondary); font-size: 12px; }
.run-identity { min-width: 0; display: flex; align-items: baseline; gap: 10px; }
.run-card-actions { flex: none; display: flex; align-items: center; gap: 8px; }
.run-id { overflow: hidden; color: var(--td-text-color-placeholder); font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.run-meta { display: flex; flex-wrap: wrap; gap: 12px; margin-top: 9px; color: var(--td-text-color-secondary); font-size: 12px; }
.status-pill { padding: 2px 8px; border-radius: 999px; background: var(--td-bg-color-secondarycontainer); font-size: 12px; }
.status-pill[data-status='2'] { color: var(--td-success-color); background: var(--td-success-color-light); }
.status-pill[data-status='3'] { color: var(--td-error-color); background: var(--td-error-color-light); }
.status-pill[data-status='invalid'] { color: var(--td-warning-color); background: var(--td-warning-color-light); }
.run-card.invalid { border-color: var(--td-warning-color); }
.invalid-hint { color: var(--td-warning-color); }
.comparison-scroll { overflow-x: auto; margin-top: 14px; }
table { width: 100%; border-collapse: collapse; font-variant-numeric: tabular-nums; }
th, td { padding: 9px 12px; border-bottom: 1px solid var(--td-component-stroke); text-align: right; white-space: nowrap; }
th:first-child, td:first-child { text-align: left; }
td small { display: block; margin-top: 2px; font-size: 11px; }
.comparison-status { margin-top: 12px; padding: 9px 11px; border-radius: 7px; color: var(--td-warning-color); background: var(--td-warning-color-light); }
.comparison-status[data-comparable='true'] { color: var(--td-success-color); background: var(--td-success-color-light); }
.delta-positive { color: var(--td-success-color); }
.delta-negative { color: var(--td-error-color); }
.delta-neutral { color: var(--td-text-color-placeholder); }
.empty-state { padding: 32px; text-align: center; color: var(--td-text-color-placeholder); }
@media (max-width: 720px) { .evaluation-form { grid-template-columns: 1fr; } .evaluation-header, .panel-heading { align-items: flex-start; } }
</style>
