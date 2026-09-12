<template>
  <div class="evaluation-page">
    <header class="evaluation-header" style="--wails-draggable: drag">
      <div class="hero-copy">
        <div class="eyebrow"><FlaskConical size="15px" aria-hidden="true" />{{ t('evaluation.eyebrow') }}</div>
        <h1>{{ t('evaluation.title') }}</h1>
        <p>{{ t('evaluation.subtitle') }}</p>
      </div>
      <div class="hero-mark" aria-hidden="true"><ScanLine size="158px" /></div>
      <div class="header-stat" aria-live="polite">
        <span>{{ t('evaluation.selected') }}</span>
        <strong>{{ selectedTaskIds.length }}</strong>
        <span>/ 10</span>
      </div>
    </header>
    <div class="evaluation-dimensions">
      <span><Crosshair size="15px" aria-hidden="true" /><small>01</small>{{ t('evaluation.summary.retrieval') }}</span>
      <span><MessageSquareText size="15px" aria-hidden="true" /><small>02</small>{{ t('evaluation.summary.generation') }}</span>
      <span><Coins size="15px" aria-hidden="true" /><small>03</small>{{ t('evaluation.summary.cost') }}</span>
      <span><Timer size="15px" aria-hidden="true" /><small>04</small>{{ t('evaluation.summary.duration') }}</span>
    </div>

    <div class="workbench-actions">
      <p>{{ t('evaluationFlow.startHint') }}</p>
      <a v-if="parserBenchmarkUrl" :href="parserBenchmarkUrl" class="button button--quiet" target="_blank" rel="noreferrer"><ExternalLink size="16px" aria-hidden="true" />{{ t('evaluationFlow.parserBenchmark') }}</a>
      <button type="button" class="button button--quiet" @click="datasetsVisible = true"><Database size="16px" aria-hidden="true" />{{ t('evaluationFlow.datasets') }}</button>
      <button v-if="canManageLabels" type="button" class="button button--primary" @click="beginCreate()"><Plus size="16px" aria-hidden="true" />{{ t('evaluationFlow.newRun') }}</button>
    </div>
    <p v-if="pollingPaused" class="polling-notice" role="status">{{ t('evaluationFlow.pollingError') }}</p>

    <button type="button" class="filter-toggle" :aria-expanded="filtersExpanded" aria-controls="evaluation-filters" @click="filtersExpanded = !filtersExpanded">
      <SlidersHorizontal size="16px" aria-hidden="true" />{{ t('evaluation.filterTitle') }}<ChevronDown size="16px" :class="{ 'filter-toggle__arrow--open': filtersExpanded }" aria-hidden="true" />
    </button>
    <form id="evaluation-filters" class="filter-panel" :class="{ 'filter-panel--collapsed': !filtersExpanded }" @submit.prevent="applyFilters">
      <label class="filter-field">
        <span>{{ t('evaluation.statusLabel') }}</span>
        <select v-model="filters.status">
          <option value="">{{ t('evaluation.allStatuses') }}</option>
          <option v-for="option in statusOptions" :key="option.value" :value="String(option.value)">
            {{ option.label }}
          </option>
        </select>
      </label>
      <label class="filter-field">
        <span>{{ t('evaluation.dataset') }}</span>
        <input v-model.trim="filters.datasetId" :placeholder="t('evaluation.datasetPlaceholder')" />
      </label>
      <label class="filter-field">
        <span>{{ t('evaluation.datasetVersion') }}</span>
        <input v-model.trim="filters.datasetVersionId" :placeholder="t('evaluation.versionPlaceholder')" />
      </label>
      <label class="filter-field">
        <span>{{ t('evaluation.model') }}</span>
        <input v-model.trim="filters.modelId" :placeholder="t('evaluation.modelPlaceholder')" />
      </label>
      <label class="filter-field filter-field--time">
        <span>{{ t('evaluation.startedFrom') }}</span>
        <input v-model="filters.startedFrom" type="datetime-local" />
      </label>
      <label class="filter-field filter-field--time">
        <span>{{ t('evaluation.startedTo') }}</span>
        <input v-model="filters.startedTo" type="datetime-local" />
      </label>
      <label class="filter-field filter-field--labels">
        <span>{{ t('evaluation.labels') }}</span>
        <input v-model="filters.labels" :placeholder="t('evaluation.labelsPlaceholder')" />
      </label>
      <div class="filter-actions">
        <button type="button" class="button button--quiet" @click="resetFilters">
          {{ t('evaluation.reset') }}
        </button>
        <button type="submit" class="button button--primary">
          {{ t('evaluation.apply') }}
        </button>
      </div>
    </form>

    <div class="workbench">
      <aside class="run-browser">
        <div class="panel-heading">
          <div>
            <span class="section-kicker">{{ t('evaluation.runs') }}</span>
            <strong>{{ tasks.length }}</strong>
          </div>
          <button class="icon-button" type="button" :title="t('evaluation.refresh')" :aria-label="t('evaluation.refresh')" @click="applyFilters">
            <RefreshCw size="16px" aria-hidden="true" />
          </button>
        </div>

        <div v-if="selectedTaskIds.length" class="selection-bar">
          <div class="selection-copy">
            <strong>{{ selectedTaskIds.length }}</strong>
            <span>{{ t('evaluation.selectedRuns') }}</span>
          </div>
          <label v-if="selectedTaskIds.length >= 2" class="baseline-select">
            <span>{{ t('evaluation.baseline') }}</span>
            <select v-model="baselineTaskId" @change="invalidateComparisonSelection">
              <option v-for="taskId in selectedTaskIds" :key="taskId" :value="taskId">
                {{ shortTaskId(taskId) }}
              </option>
            </select>
          </label>
          <button
            type="button"
            class="button button--primary button--compact"
            :disabled="selectedTaskIds.length < 2 || comparisonLoading"
            @click="runComparison"
          >
            {{ comparisonLoading ? t('evaluation.comparing') : t('evaluation.compare') }}
          </button>
        </div>

        <div v-if="listLoading && tasks.length === 0" class="state-block">
          <LoaderCircle size="22px" class="icon-spinning" aria-hidden="true" />
          <span>{{ t('evaluation.loading') }}</span>
        </div>
        <div v-else-if="listError" class="state-block state-block--error">
          <CircleAlert size="20px" aria-hidden="true" />
          <span>{{ listError }}</span>
        </div>
        <div v-else-if="tasks.length === 0" class="state-block">
          <FlaskConical size="32px" aria-hidden="true" />
          <span>{{ t('evaluation.noRuns') }}</span>
          <button v-if="canManageLabels" type="button" class="button button--primary button--compact" @click="beginCreate()">{{ t('evaluationFlow.newRun') }}</button>
        </div>
        <div v-else class="run-list">
          <article
            v-for="task in tasks"
            :key="task.id"
            class="run-row"
            :class="{ 'run-row--active': activeTaskId === task.id }"
          >
            <label class="run-checkbox" @click.stop>
              <input
                type="checkbox"
                :checked="selectedTaskIds.includes(task.id)"
                :aria-label="t('evaluation.selectRun', { id: task.id })"
                @change="onComparisonCheckbox(task.id, $event)"
              />
              <span />
            </label>
            <button type="button" class="run-row__body" :aria-pressed="activeTaskId === task.id" @click="openTask(task)">
              <div class="run-row__top">
                <code :title="task.id">{{ shortTaskId(task.id) }}</code>
                <span class="status-pill" :class="`status-pill--${statusMeta(task.status).tone}`">
                  {{ statusMeta(task.status).label }}
                </span>
              </div>
              <div class="run-row__dataset" :title="task.dataset_id">{{ task.dataset_id }}</div>
              <div class="run-row__meta">
                <span>{{ formatDate(task.start_time) }}</span>
                <span>{{ task.finished ?? 0 }}/{{ task.total ?? 0 }}</span>
              </div>
              <div v-if="task.labels?.length" class="label-row">
                <span v-for="label in task.labels.slice(0, 3)" :key="label" class="label-chip">{{ label }}</span>
                <span v-if="task.labels.length > 3" class="label-chip">+{{ task.labels.length - 3 }}</span>
              </div>
            </button>
          </article>
          <button
            v-if="nextCursor"
            type="button"
            class="load-more"
            :disabled="listLoading"
            @click="loadTasks(true)"
          >
            {{ listLoading ? t('evaluation.loading') : t('evaluation.loadMore') }}
          </button>
        </div>
      </aside>

      <main class="inspection-panel">
        <div v-if="comparisonResult" class="comparison-view">
          <div class="inspection-heading">
            <div>
              <span class="section-kicker">{{ t('evaluation.comparison') }}</span>
              <h2>{{ comparisonResult.runs.length }} {{ t('evaluation.runsCompared') }}</h2>
            </div>
            <button type="button" class="button button--quiet button--compact" @click="comparisonResult = null">
              {{ t('evaluation.backToDetail') }}
            </button>
          </div>

          <div class="comparison-run-strip">
            <div v-for="run in comparisonResult.runs" :key="run.task_id" class="comparison-run">
              <span v-if="run.is_baseline" class="baseline-badge">{{ t('evaluation.baseline') }}</span>
              <code>{{ shortTaskId(run.task_id) }}</code>
              <small>v{{ run.version_number }} · {{ run.dataset_id }}</small>
              <small v-if="run.question_success_rate" class="confidence-copy">
                {{ t('evaluation.questionSuccess') }} {{ formatConfidence(run.question_success_rate) }}
              </small>
              <small v-else-if="run.question_success_status" class="confidence-copy">
                {{ t('evaluation.questionSuccess') }} · {{ run.question_success_status }} · n={{ run.question_n_valid }}
              </small>
            </div>
          </div>

          <section class="data-section">
            <div class="data-section__heading">
              <h3>{{ t('evaluation.parameterDifferences') }}</h3>
              <span>{{ comparisonResult.parameters.filter(parameter => parameter.differ).length }}</span>
            </div>
            <div class="table-scroll">
              <table class="comparison-table">
                <thead>
                  <tr>
                    <th>{{ t('evaluation.parameter') }}</th>
                    <th v-for="run in comparisonResult.runs" :key="run.task_id">
                      {{ shortTaskId(run.task_id) }}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="parameter in comparisonResult.parameters"
                    :key="parameter.pointer"
                    :class="{ 'row-differs': parameter.differ }"
                  >
                    <td><code>{{ parameter.pointer }}</code></td>
                    <td v-for="value in parameter.values" :key="value.task_id">
                      <span v-if="value.missing" class="missing-value">{{ t('evaluation.missing') }}</span>
                      <code v-else>{{ formatValue(value.value) }}</code>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>

          <section class="data-section">
            <div class="data-section__heading">
              <h3>{{ t('evaluation.metricDifferences') }}</h3>
              <span>{{ comparisonResult.metrics.length }}</span>
            </div>
            <div class="metric-grid">
              <article v-for="metric in comparisonResult.metrics" :key="metric.pointer" class="metric-card">
                <div class="metric-card__heading">
                  <div>
                    <code>{{ metric.key }}</code>
                    <small>{{ metric.version }} · {{ metric.pointer }}</small>
                  </div>
                  <span class="compatibility" :class="{ 'compatibility--bad': !metric.compatible }">
                    {{ metric.compatible ? t('evaluation.compatible') : t('evaluation.incompatible') }}
                  </span>
                </div>
                <div class="metric-values">
                  <div v-for="value in metric.values" :key="value.task_id" class="metric-value">
                    <span>{{ shortTaskId(value.task_id) }}</span>
                    <strong>{{ formatMetric(value.value) }}</strong>
                    <em v-if="value.confidence" class="confidence-copy">
                      {{ formatConfidence(value.confidence) }}
                    </em>
                    <em v-else class="confidence-copy">
                      {{ value.confidence_status }} · n={{ value.n_valid }}/{{ value.n_total }}
                    </em>
                    <small v-if="value.is_baseline">{{ t('evaluation.baseline') }}</small>
                    <small v-else-if="value.delta !== null">
                      Δ {{ signedNumber(value.delta) }} · {{ formatRelative(value.relative_delta) }}
                    </small>
                    <small v-else>{{ value.reason || value.relative_reason || t('evaluation.missing') }}</small>
                  </div>
                </div>
              </article>
            </div>
          </section>
        </div>

        <div v-else-if="detailLoading" class="state-block state-block--detail">
          <LoaderCircle size="26px" class="icon-spinning" aria-hidden="true" />
          <span>{{ t('evaluation.loadingDetail') }}</span>
        </div>
        <div v-else-if="detailError" class="state-block state-block--detail state-block--error">
          <CircleAlert size="20px" aria-hidden="true" />
          <span>{{ detailError }}</span>
        </div>
        <div v-else-if="detail" class="detail-view">
          <div class="inspection-heading">
            <div>
              <span class="section-kicker">{{ t('evaluation.runDetails') }}</span>
              <h2>{{ shortTaskId(detail.task.id) }}</h2>
              <code class="full-task-id">{{ detail.task.id }}</code>
            </div>
            <div class="export-actions">
              <button
                type="button"
                class="button button--quiet button--compact"
                :disabled="Boolean(exporting)"
                @click="download('json')"
              >
                <Download size="15px" aria-hidden="true" /> JSON
              </button>
              <button
                type="button"
                class="button button--quiet button--compact"
                :disabled="Boolean(exporting)"
                @click="download('csv')"
              >
                <Download size="15px" aria-hidden="true" /> CSV
              </button>
              <button
                v-if="exporting"
                type="button"
                class="button button--quiet button--compact"
                @click="cancelDownload"
              >
                {{ t('common.cancel') }}
              </button>
            </div>
          </div>

          <div class="fact-grid">
            <div class="fact-card">
              <span>{{ t('evaluation.statusLabel') }}</span>
              <strong>{{ statusMeta(detail.task.status).label }}</strong>
            </div>
            <div class="fact-card">
              <span>{{ t('evaluation.progress') }}</span>
              <strong>{{ detail.task.finished ?? 0 }} / {{ detail.task.total ?? 0 }}</strong>
            </div>
            <div class="fact-card">
              <span>{{ t('evaluation.datasetVersion') }}</span>
              <strong>{{ detail.task.dataset_version_id || '—' }}</strong>
            </div>
            <div class="fact-card">
              <span>{{ t('evaluation.provenance') }}</span>
              <strong :class="detail.provenance_complete ? 'text-success' : 'text-warning'">
                {{ detail.provenance_complete ? t('evaluation.complete') : t('evaluation.incomplete') }}
              </strong>
            </div>
          </div>

          <section class="label-editor">
            <div>
              <h3>{{ t('evaluation.labels') }}</h3>
              <div class="label-row">
                <span v-for="label in detail.task.labels || []" :key="label" class="label-chip">{{ label }}</span>
                <span v-if="!detail.task.labels?.length" class="muted">{{ t('evaluation.noLabels') }}</span>
              </div>
            </div>
            <div v-if="canManageLabels" class="label-editor__control">
              <input v-model="labelDraft" :placeholder="t('evaluation.labelsPlaceholder')" />
              <button type="button" class="button button--quiet button--compact" :disabled="savingLabels" @click="saveLabels">
                {{ t('evaluation.saveLabels') }}
              </button>
            </div>
          </section>

          <nav class="detail-tabs" :aria-label="t('evaluation.runDetails')">
            <button :class="{ active: activeTab === 'overview' }" :aria-current="activeTab === 'overview' ? 'page' : undefined" @click="activeTab = 'overview'">
              {{ t('evaluation.overview') }}
            </button>
            <button :class="{ active: activeTab === 'questions' }" :aria-current="activeTab === 'questions' ? 'page' : undefined" @click="activeTab = 'questions'">
              {{ t('evaluation.questions') }} <span>{{ questions.length }}</span>
            </button>
          </nav>

          <div v-if="activeTab === 'overview'" class="overview-grid">
            <section v-for="group in (['retrieval', 'generation'] as const)" :key="group" class="result-card">
              <h3><Crosshair v-if="group === 'retrieval'" size="16px" aria-hidden="true" /><MessageSquareText v-else size="16px" aria-hidden="true" />{{ t(`evaluation.summary.${group}`) }}</h3>
              <dl v-if="qualitySummary[group].length" class="result-scores">
                <div v-for="score in qualitySummary[group]" :key="score.id">
                  <dt>{{ score.label }}</dt>
                  <dd :class="{ 'result-missing': score.value === null }">{{ score.value === null ? t('evaluation.summary.unavailable') : score.value.toFixed(4) }}</dd>
                </div>
              </dl>
              <p v-else class="result-missing">{{ t('evaluation.summary.unavailable') }}</p>
              <p class="result-note">{{ t(`evaluation.summary.${group}Hint`) }}</p>
            </section>
            <section class="result-card result-card--cost">
              <h3><Coins size="16px" aria-hidden="true" />{{ t('evaluation.summary.cost') }}</h3>
              <strong class="result-value">{{ summaryCost(detail.runtime_metrics?.cost) }}</strong>
              <p v-if="detail.runtime_metrics?.cost" class="result-note">{{ t('modelSettings.observability.accountedCalls', { complete: detail.runtime_metrics.cost.accounting_complete_calls, total: detail.runtime_metrics.cost.call_count }) }}</p>
              <p v-else class="result-note">{{ t('evaluation.summary.unavailable') }}</p>
              <p class="result-note">{{ t('evaluation.summary.costHint') }}</p>
            </section>
            <section class="result-card">
              <h3><Timer size="16px" aria-hidden="true" />{{ t('evaluation.summary.duration') }}</h3>
              <strong class="result-value">{{ summaryDuration(detail.runtime_metrics?.durations?.total_ms) }}</strong>
              <p class="result-note">{{ t('evaluation.summary.execution') }}: {{ summaryDuration(detail.runtime_metrics?.durations?.execution_ms) }}</p>
              <p class="result-note">{{ t('evaluation.summary.durationHint') }}</p>
            </section>
            <details class="json-panel">
              <summary>{{ t('evaluation.experiment') }}</summary>
              <pre>{{ prettyJSON(detail.experiment) }}</pre>
            </details>
            <details class="json-panel">
              <summary>{{ t('evaluation.aggregateMetrics') }}</summary>
              <pre>{{ prettyJSON(detail.metric) }}</pre>
            </details>
            <details class="json-panel">
              <summary>{{ t('evaluation.summary.runtime') }}</summary>
              <pre>{{ prettyJSON(detail.runtime_metrics) }}</pre>
            </details>
          </div>

          <section v-else class="question-section">
            <div v-if="questionLoading && questions.length === 0" class="state-block">
              <LoaderCircle size="22px" class="icon-spinning" aria-hidden="true" />
              <span>{{ t('evaluation.loadingQuestions') }}</span>
            </div>
            <div v-else-if="questions.length === 0" class="state-block">{{ t('evaluation.noQuestions') }}</div>
            <article v-for="question in questions" :key="question.sample_index" class="question-card">
              <div class="question-card__index">{{ String(question.sample_index + 1).padStart(2, '0') }}</div>
              <div class="question-card__body">
                <div class="question-card__heading">
                  <h3>{{ question.question }}</h3>
                  <span class="status-pill" :class="question.status === 'success' ? 'status-pill--success' : 'status-pill--danger'">
                    {{ question.status }}
                  </span>
                </div>
                <dl>
                  <div><dt>QID</dt><dd>{{ question.qid }}</dd></div>
                  <div><dt>{{ t('evaluation.referenceAnswer') }}</dt><dd>{{ question.reference_answer || '—' }}</dd></div>
                  <div><dt>{{ t('evaluation.generatedText') }}</dt><dd>{{ question.generated_text || '—' }}</dd></div>
                </dl>
                <div class="ranking-row">
                  <span v-for="rank in question.search_results" :key="rank.rank" :class="{ unknown: rank.pid === -1 }">
                    #{{ rank.rank }} · PID {{ rank.pid }}
                  </span>
                </div>
                <section class="human-rating">
                  <button
                    type="button"
                    class="human-rating__toggle"
                    @click="toggleHumanRatings(question.sample_index)"
                  >
                    {{ t('evaluation.humanRating') }}
                    <span v-if="ratingPanel(question.sample_index).items.length">
                      {{ ratingPanel(question.sample_index).items[0].score }}/5 ·
                      r{{ ratingPanel(question.sample_index).items[0].revision }}
                    </span>
                    <span v-else>{{ t('evaluation.viewRevisions') }}</span>
                  </button>
                  <div v-if="ratingPanel(question.sample_index).open" class="human-rating__panel">
                    <div v-if="ratingPanel(question.sample_index).loading" class="muted">
                      {{ t('evaluation.loadingRatings') }}
                    </div>
                    <template v-else>
                      <p v-if="canManageLabels" class="human-rating__guidance">
                        {{ t('evaluation.ratingGuidance') }}
                      </p>
                      <form
                        v-if="canManageLabels"
                        class="human-rating__form"
                        @submit.prevent="saveHumanRating(question.sample_index)"
                      >
                        <label>
                          <span>{{ t('evaluation.score') }}</span>
                          <select v-model.number="ratingPanel(question.sample_index).score">
                            <option v-for="score in [1, 2, 3, 4, 5]" :key="score" :value="score">
                              {{ score }} / 5
                            </option>
                          </select>
                        </label>
                        <label class="human-rating__comment">
                          <span>{{ t('evaluation.ratingComment') }}</span>
                          <input
                            v-model="ratingPanel(question.sample_index).comment"
                            maxlength="4000"
                            :placeholder="t('evaluation.ratingCommentPlaceholder')"
                          />
                        </label>
                        <button
                          type="submit"
                          class="button button--primary button--compact"
                          :disabled="ratingPanel(question.sample_index).saving"
                        >
                          {{ ratingPanel(question.sample_index).saving ? t('evaluation.savingRating') : t('evaluation.appendRating') }}
                        </button>
                      </form>
                      <p v-if="ratingPanel(question.sample_index).error" class="human-rating__error">
                        {{ ratingPanel(question.sample_index).error }}
                      </p>
                      <ol v-if="ratingPanel(question.sample_index).items.length" class="human-rating__history">
                        <li v-for="rating in ratingPanel(question.sample_index).items" :key="rating.id">
                          <strong>{{ rating.score }}/5</strong>
                          <span>r{{ rating.revision }} · {{ rating.rubric_key }}@{{ rating.rubric_version }}</span>
                          <time>{{ formatDate(rating.created_at) }}</time>
                          <p v-if="rating.comment">{{ rating.comment }}</p>
                        </li>
                      </ol>
                      <p v-else class="muted">{{ t('evaluation.noRatings') }}</p>
                    </template>
                  </div>
                </section>
              </div>
            </article>
            <button
              v-if="questionCursor"
              type="button"
              class="load-more load-more--questions"
              :disabled="questionLoading"
              @click="loadQuestions(true)"
            >
              {{ questionLoading ? t('evaluation.loading') : t('evaluation.loadMoreQuestions') }}
            </button>
          </section>
        </div>
        <div v-else class="detail-empty">
          <ScanLine size="96px" class="detail-empty__icon" aria-hidden="true" />
          <h2>{{ t('evaluation.detailEmpty') }}</h2>
          <p>{{ t('evaluation.detailEmptyHint') }}</p>
        </div>
      </main>
    </div>
    <EvaluationDatasetsDrawer v-model:visible="datasetsVisible" :tenant-key="tenantKey" :can-manage="canManageLabels" @create="beginCreate" @imported="onDatasetImported" />
    <EvaluationCreateDrawer v-model:visible="createVisible" :tenant-key="tenantKey" :dataset-id="createDatasetId" :version-id="createVersionId" @created="onTaskCreated" @import="openImport" @refresh="applyFilters" />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { ChevronDownIcon as ChevronDown, ErrorCircleIcon as CircleAlert, MoneyIcon as Coins, FocusIcon as Crosshair, ServerIcon as Database, DownloadIcon as Download, LinkIcon as ExternalLink, ChartScatterIcon as FlaskConical, LoadingIcon as LoaderCircle, ChatIcon as MessageSquareText, AddIcon as Plus, RefreshIcon as RefreshCw, ScanIcon as ScanLine, ControlPlatformIcon as SlidersHorizontal, TimeIcon as Timer } from 'tdesign-icons-vue-next'

import {
  EVALUATION_STATUS,
  appendEvaluationHumanRating,
  compareEvaluationTasks,
  createEvaluationRequestGate,
  downloadEvaluationArtifact,
  getEvaluationDetail,
  listEvaluationQuestions,
  listEvaluationHumanRatings,
  listEvaluationTasks,
  replaceEvaluationLabels,
  type EvaluationComparisonResponse,
  type EvaluationConfidenceInterval,
  type EvaluationDetail,
  type EvaluationQuestionResult,
  type EvaluationHumanRatingRevision,
  type EvaluationTask,
  type EvaluationTaskFilters,
  type EvaluationRequestToken,
} from '@/api/evaluation'
import { useAuthStore } from '@/stores/auth'
import { summarizeEvaluation, summaryCost, summaryDuration } from './evaluationSummary'
import EvaluationDatasetsDrawer from './EvaluationDatasetsDrawer.vue'
import EvaluationCreateDrawer from './EvaluationCreateDrawer.vue'
import { createEvaluationPoller } from './evaluationPolling'
import type { DatasetImportResult } from '@/api/evaluation/datasets'

const { t } = useI18n()
const parserBenchmarkUrl = computed(() => {
  const configured = import.meta.env.VITE_PARSER_BENCHMARK_URL
  if (!configured) return ''
  try {
    const url = new URL(configured, window.location.origin)
    return ['http:', 'https:'].includes(url.protocol) ? url.href : ''
  } catch { return '' }
})
const authStore = useAuthStore()
const tenantKey = computed(() => `${authStore.currentUserId ?? ''}:${authStore.selectedTenantId ?? authStore.currentTenantId ?? ''}`)
const datasetsVisible = ref(false), createVisible = ref(false), createDatasetId = ref(''), createVersionId = ref('')
const pollingPaused = ref(false)
function beginCreate(datasetId = '', versionId = '') {
  datasetsVisible.value = false; createDatasetId.value = datasetId; createVersionId.value = versionId; createVisible.value = true
}
function openImport() { createVisible.value = false; datasetsVisible.value = true }
function onDatasetImported(result: DatasetImportResult) { createDatasetId.value = result.dataset.id; createVersionId.value = result.version.id }
async function onTaskCreated(task: EvaluationTask) {
  resetFilters()
  tasks.value = [task, ...tasks.value.filter(item => item.id !== task.id)]
  await openTask(task)
}

const ANSWER_QUALITY_RUBRIC_KEY = 'answer-quality'
const ANSWER_QUALITY_RUBRIC_VERSION = '1.1.0'
const ANSWER_QUALITY_RUBRIC_SNAPSHOT = {
  title: 'Overall answer quality',
  dimension: 'overall_answer_quality',
  considerations: ['correctness', 'relevance', 'grounding'],
  scale: {
    1: 'Incorrect or unsupported',
    2: 'Major quality issues',
    3: 'Acceptable with notable issues',
    4: 'Strong with minor issues',
    5: 'Correct, relevant, and well grounded',
  },
}

const filters = reactive({
  status: '',
  datasetId: '',
  datasetVersionId: '',
  modelId: '',
  startedFrom: '',
  startedTo: '',
  labels: '',
})
const filtersExpanded = ref(false)

const tasks = ref<EvaluationTask[]>([])
const nextCursor = ref('')
const listLoading = ref(false)
const listError = ref('')
const activeTaskId = ref('')
const detail = ref<EvaluationDetail | null>(null)
const qualitySummary = computed(() => summarizeEvaluation(detail.value))
const detailLoading = ref(false)
const detailError = ref('')
const questions = ref<EvaluationQuestionResult[]>([])
const questionCursor = ref('')
const questionLoading = ref(false)
const activeTab = ref<'overview' | 'questions'>('overview')
const selectedTaskIds = ref<string[]>([])
const baselineTaskId = ref('')
const comparisonResult = ref<EvaluationComparisonResponse | null>(null)
const comparisonLoading = ref(false)
const exporting = ref<'json' | 'csv' | ''>('')
const labelDraft = ref('')
const savingLabels = ref(false)
const taskListRequests = createEvaluationRequestGate()
const taskDetailRequests = createEvaluationRequestGate()
const questionRequests = createEvaluationRequestGate()
const comparisonRequests = createEvaluationRequestGate()
let activeExportController: AbortController | null = null
let labelSaveSequence = 0
let activeLabelSaveToken: { taskId: string; sequence: number } | null = null
const latestLabelSaveByTask = new Map<string, number>()
const isRunning = (task: EvaluationTask | undefined) => task?.status === EVALUATION_STATUS.pending || task?.status === EVALUATION_STATUS.running
const poller = createEvaluationPoller({
  hasRunning: () => tasks.value.some(isRunning) || isRunning(detail.value?.task),
  onPaused: () => { pollingPaused.value = true },
  refresh: async isCurrent => {
    if (listLoading.value || detailLoading.value) return
    const taskId = activeTaskId.value
    const listToken = taskListRequests.begin(JSON.stringify(filterInput()))
    const detailToken = taskDetailRequests.begin(taskId)
    const [page, run] = await Promise.all([listEvaluationTasks(filterInput()), taskId ? getEvaluationDetail(taskId) : Promise.resolve(null)])
    if (!isCurrent()) return
    if (taskListRequests.isCurrent(listToken)) { tasks.value = page.items; nextCursor.value = page.next_cursor }
    if (run && taskDetailRequests.isCurrent(detailToken) && activeTaskId.value === taskId) {
      run.task.labels = tasks.value.find(task => task.id === taskId)?.labels ?? detail.value?.task.labels ?? []
      detail.value = run
      if (!isRunning(run.task) || activeTab.value === 'questions') void loadQuestions(false)
    }
  },
})

interface HumanRatingPanelState {
  open: boolean
  loading: boolean
  saving: boolean
  loaded: boolean
  items: EvaluationHumanRatingRevision[]
  score: number
  comment: string
  error: string
}

const humanRatingPanels = reactive<Record<number, HumanRatingPanelState>>({})

const canManageLabels = computed(() => authStore.hasRole('admin'))

const statusOptions = computed(() => [
  { value: EVALUATION_STATUS.pending, label: t('evaluation.status.pending') },
  { value: EVALUATION_STATUS.running, label: t('evaluation.status.running') },
  { value: EVALUATION_STATUS.success, label: t('evaluation.status.success') },
  { value: EVALUATION_STATUS.failed, label: t('evaluation.status.failed') },
  { value: EVALUATION_STATUS.timedOut, label: t('evaluation.status.timedOut') },
  { value: EVALUATION_STATUS.interrupted, label: t('evaluation.status.interrupted') },
  { value: EVALUATION_STATUS.canceled, label: t('evaluation.status.canceled') },
])

function errorMessage(error: unknown): string {
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') {
    return error.message
  }
  return t('evaluation.unknownError')
}

function filterInput(cursor = ''): EvaluationTaskFilters {
  return {
    status: filters.status === '' ? undefined : Number(filters.status),
    datasetId: filters.datasetId,
    datasetVersionId: filters.datasetVersionId,
    modelId: filters.modelId,
    startedFrom: filters.startedFrom ? new Date(filters.startedFrom).toISOString() : undefined,
    startedTo: filters.startedTo ? new Date(filters.startedTo).toISOString() : undefined,
    labels: filters.labels.split(',').map(label => label.trim()).filter(Boolean),
    pageSize: 30,
    cursor,
  }
}

async function loadTasks(append = false) {
  if (append && listLoading.value) return
  poller.stop()
  const input = filterInput(append ? nextCursor.value : '')
  const request = taskListRequests.begin(JSON.stringify(input))
  listLoading.value = true
  listError.value = ''
  try {
    const page = await listEvaluationTasks(input)
    if (!taskListRequests.isCurrent(request)) return
    tasks.value = append ? [...tasks.value, ...page.items] : page.items
    nextCursor.value = page.next_cursor
    pollingPaused.value = false
    poller.start()
  } catch (error) {
    if (taskListRequests.isCurrent(request)) listError.value = errorMessage(error)
  } finally {
    if (taskListRequests.isCurrent(request)) listLoading.value = false
  }
}

function applyFilters() {
  selectedTaskIds.value = []
  baselineTaskId.value = ''
  invalidateComparisonSelection()
  // Applying a new filter invalidates the previously opened task detail: an
  // empty filtered list must never sit next to a detail that no longer
  // belongs to the result set. In-flight detail/question requests are
  // isolated through their gates so a late response cannot restore it.
  activeTaskId.value = ''
  detail.value = null
  detailError.value = ''
  questions.value = []
  questionCursor.value = ''
  taskDetailRequests.invalidate()
  questionRequests.invalidate()
  // Invalidated requests cannot clear loading in their guarded finally blocks.
  detailLoading.value = false
  questionLoading.value = false
  void loadTasks(false)
}

function resetFilters() {
  Object.assign(filters, {
    status: '', datasetId: '', datasetVersionId: '', modelId: '', startedFrom: '', startedTo: '', labels: '',
  })
  applyFilters()
}

async function openTask(task: EvaluationTask) {
  poller.stop()
  if (activeTaskId.value === task.id && detail.value) return
  const detailRequest = taskDetailRequests.begin(task.id)
  const questionRequest = questionRequests.begin(task.id)
  activeTaskId.value = task.id
  comparisonResult.value = null
  detailLoading.value = true
  detailError.value = ''
  detail.value = null
  activeLabelSaveToken = null
  savingLabels.value = false
  activeTab.value = 'overview'
  questions.value = []
  questionLoading.value = false
  for (const key of Object.keys(humanRatingPanels)) delete humanRatingPanels[Number(key)]
  questionCursor.value = ''
  const requestedTaskId = task.id
  try {
    const result = await getEvaluationDetail(task.id)
    if (!taskDetailRequests.isCurrent(detailRequest) || activeTaskId.value !== requestedTaskId) return
    result.task.labels = task.labels ?? []
    detail.value = result
    labelDraft.value = result.task.labels.join(', ')
  } catch (error) {
    if (taskDetailRequests.isCurrent(detailRequest) && activeTaskId.value === requestedTaskId) {
      detailError.value = errorMessage(error)
    }
  } finally {
    if (taskDetailRequests.isCurrent(detailRequest) && activeTaskId.value === requestedTaskId) {
      detailLoading.value = false
    }
  }
  if (taskDetailRequests.isCurrent(detailRequest) && activeTaskId.value === requestedTaskId) {
    void loadQuestions(false, questionRequest)
    poller.start()
  }
}

async function loadQuestions(append: boolean, initialRequest?: EvaluationRequestToken) {
  const requestedTaskId = activeTaskId.value
  if (!requestedTaskId || (append && questionLoading.value)) return
  const request = initialRequest ?? questionRequests.begin(requestedTaskId)
  const cursor = append ? questionCursor.value : ''
  questionLoading.value = true
  try {
    const page = await listEvaluationQuestions(
      requestedTaskId,
      cursor,
      100,
    )
    if (!questionRequests.isCurrent(request) || activeTaskId.value !== requestedTaskId) return
    questions.value = append ? [...questions.value, ...page.items] : page.items
    questionCursor.value = page.next_cursor
  } catch (error) {
    if (questionRequests.isCurrent(request) && activeTaskId.value === requestedTaskId) {
      MessagePlugin.error(errorMessage(error))
    }
  } finally {
    if (questionRequests.isCurrent(request) && activeTaskId.value === requestedTaskId) {
      questionLoading.value = false
    }
  }
}

function onComparisonCheckbox(taskId: string, event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  if (checked) {
    if (selectedTaskIds.value.length >= 10) {
      ;(event.target as HTMLInputElement).checked = false
      MessagePlugin.warning(t('evaluation.selectAtMostTen'))
      return
    }
    if (!selectedTaskIds.value.includes(taskId)) selectedTaskIds.value.push(taskId)
  } else {
    selectedTaskIds.value = selectedTaskIds.value.filter(id => id !== taskId)
  }
  if (!selectedTaskIds.value.includes(baselineTaskId.value)) {
    baselineTaskId.value = selectedTaskIds.value[0] ?? ''
  }
  invalidateComparisonSelection()
}

function comparisonSelectionSignature(taskIds = selectedTaskIds.value, baseline = baselineTaskId.value) {
  return JSON.stringify({ taskIds, baseline: baseline || taskIds[0] || '' })
}

function invalidateComparisonSelection() {
  comparisonRequests.invalidate()
  comparisonResult.value = null
  comparisonLoading.value = false
}

async function runComparison() {
  if (selectedTaskIds.value.length < 2) {
    MessagePlugin.warning(t('evaluation.selectAtLeastTwo'))
    return
  }
  const taskIds = [...selectedTaskIds.value]
  const baseline = baselineTaskId.value || taskIds[0]
  const signature = comparisonSelectionSignature(taskIds, baseline)
  const request = comparisonRequests.begin(signature)
  comparisonLoading.value = true
  comparisonResult.value = null
  try {
    const result = await compareEvaluationTasks({
      task_ids: taskIds,
      baseline_task_id: baseline,
    })
    if (!comparisonRequests.isCurrent(request) || comparisonSelectionSignature() !== signature) return
    comparisonResult.value = result
  } catch (error) {
    if (comparisonRequests.isCurrent(request) && comparisonSelectionSignature() === signature) {
      MessagePlugin.error(errorMessage(error))
    }
  } finally {
    if (comparisonRequests.isCurrent(request) && comparisonSelectionSignature() === signature) {
      comparisonLoading.value = false
    }
  }
}

async function saveLabels() {
  if (!detail.value) return
  const taskId = detail.value.task.id
  const labels = labelDraft.value.split(',').map(label => label.trim()).filter(Boolean)
  const token = { taskId, sequence: ++labelSaveSequence }
  latestLabelSaveByTask.set(taskId, token.sequence)
  activeLabelSaveToken = token
  savingLabels.value = true
  try {
    const saved = await replaceEvaluationLabels(taskId, labels)
    if (latestLabelSaveByTask.get(taskId) !== token.sequence) return
    const listed = tasks.value.find(task => task.id === taskId)
    if (listed) listed.labels = saved
    if (activeTaskId.value === taskId && detail.value?.task.id === taskId) {
      detail.value.task.labels = saved
      labelDraft.value = saved.join(', ')
      MessagePlugin.success(t('evaluation.labelsSaved'))
    }
  } catch (error) {
    if (latestLabelSaveByTask.get(taskId) === token.sequence && activeTaskId.value === taskId) {
      MessagePlugin.error(errorMessage(error))
    }
  } finally {
    if (activeLabelSaveToken === token) {
      activeLabelSaveToken = null
      savingLabels.value = false
    }
  }
}

async function download(format: 'json' | 'csv') {
  if (!detail.value || exporting.value) return
  const controller = new AbortController()
  activeExportController = controller
  exporting.value = format
  try {
    await downloadEvaluationArtifact(detail.value.task.id, format, { signal: controller.signal })
  } catch (error) {
    if (!controller.signal.aborted) MessagePlugin.error(errorMessage(error))
  } finally {
    if (activeExportController === controller) {
      activeExportController = null
      exporting.value = ''
    }
  }
}

function cancelDownload() {
  activeExportController?.abort()
}

function statusMeta(status: number) {
  const byStatus: Record<number, { label: string; tone: string }> = {
    [EVALUATION_STATUS.pending]: { label: t('evaluation.status.pending'), tone: 'neutral' },
    [EVALUATION_STATUS.running]: { label: t('evaluation.status.running'), tone: 'running' },
    [EVALUATION_STATUS.success]: { label: t('evaluation.status.success'), tone: 'success' },
    [EVALUATION_STATUS.failed]: { label: t('evaluation.status.failed'), tone: 'danger' },
    [EVALUATION_STATUS.timedOut]: { label: t('evaluation.status.timedOut'), tone: 'danger' },
    [EVALUATION_STATUS.interrupted]: { label: t('evaluation.status.interrupted'), tone: 'warning' },
    [EVALUATION_STATUS.canceled]: { label: t('evaluation.status.canceled'), tone: 'warning' },
  }
  return byStatus[status] ?? { label: String(status), tone: 'neutral' }
}

function shortTaskId(taskId: string) {
  if (taskId.length <= 22) return taskId
  return `${taskId.slice(0, 11)}…${taskId.slice(-8)}`
}

function formatDate(value?: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat(undefined, {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(new Date(value))
}

function prettyJSON(value: unknown) {
  return JSON.stringify(value ?? null, null, 2)
}

function formatValue(value: unknown) {
  if (typeof value === 'string') return value
  return JSON.stringify(value)
}

function formatMetric(value: number | null) {
  return value === null ? '—' : value.toLocaleString(undefined, { maximumFractionDigits: 6 })
}

function signedNumber(value: number) {
  const formatted = Math.abs(value).toLocaleString(undefined, { maximumFractionDigits: 6 })
  return `${value >= 0 ? '+' : '−'}${formatted}`
}

function formatRelative(value: number | null) {
  return value === null ? '—' : `${(value * 100).toFixed(2)}%`
}

function formatConfidence(interval: EvaluationConfidenceInterval) {
  const percent = (value: number) => `${(value * 100).toFixed(1)}%`
  return `${percent(interval.lower)}–${percent(interval.upper)} · ${Math.round(interval.confidence * 100)}% CI · n=${interval.samples}`
}

function ratingPanel(sampleIndex: number): HumanRatingPanelState {
  if (!humanRatingPanels[sampleIndex]) {
    humanRatingPanels[sampleIndex] = {
      open: false, loading: false, saving: false, loaded: false,
      items: [], score: 3, comment: '', error: '',
    }
  }
  return humanRatingPanels[sampleIndex]
}

async function toggleHumanRatings(sampleIndex: number) {
  const panel = ratingPanel(sampleIndex)
  panel.open = !panel.open
  if (!panel.open || panel.loaded || !activeTaskId.value) return
  panel.loading = true
  panel.error = ''
  try {
    panel.items = await listEvaluationHumanRatings(activeTaskId.value, sampleIndex)
    panel.loaded = true
    const latest = panel.items[0]
    panel.score = latest?.rubric_key === ANSWER_QUALITY_RUBRIC_KEY
      && latest.rubric_version === ANSWER_QUALITY_RUBRIC_VERSION
      ? latest.score
      : 3
  } catch (error) {
    panel.error = errorMessage(error)
  } finally {
    panel.loading = false
  }
}

async function saveHumanRating(sampleIndex: number) {
  const panel = ratingPanel(sampleIndex)
  if (!activeTaskId.value || panel.saving) return
  panel.saving = true
  panel.error = ''
  try {
    const rating = await appendEvaluationHumanRating(activeTaskId.value, sampleIndex, {
      rubric_key: ANSWER_QUALITY_RUBRIC_KEY,
      rubric_version: ANSWER_QUALITY_RUBRIC_VERSION,
      rubric_snapshot: ANSWER_QUALITY_RUBRIC_SNAPSHOT,
      score: panel.score,
      comment: panel.comment,
    })
    panel.items = [rating, ...panel.items]
    panel.loaded = true
    panel.comment = ''
    MessagePlugin.success(t('evaluation.ratingSaved'))
  } catch (error) {
    panel.error = errorMessage(error)
  } finally {
    panel.saving = false
  }
}

onMounted(() => {
  void loadTasks(false)
})

onBeforeUnmount(() => {
  poller.stop()
  taskListRequests.invalidate()
  taskDetailRequests.invalidate()
  questionRequests.invalidate()
  activeExportController?.abort()
})

watch(tenantKey, () => {
  poller.stop(); taskListRequests.invalidate(); taskDetailRequests.invalidate(); questionRequests.invalidate(); comparisonRequests.invalidate()
  activeExportController?.abort(); latestLabelSaveByTask.clear(); activeLabelSaveToken = null; savingLabels.value = false
  datasetsVisible.value = false; createVisible.value = false; createDatasetId.value = ''; createVersionId.value = ''
  tasks.value = []; nextCursor.value = ''; detail.value = null; activeTaskId.value = ''; questions.value = []; questionCursor.value = ''
  detailLoading.value = false; detailError.value = ''; questionLoading.value = false; pollingPaused.value = false
  for (const key of Object.keys(humanRatingPanels)) delete humanRatingPanels[Number(key)]
  resetFilters()
}, { flush: 'sync' })
</script>

<style scoped lang="less">
.evaluation-page {
  --eval-ink: #17211d;
  --eval-muted: #66756e;
  --eval-line: #e3e9e6;
  --eval-green: #078a63;
  --eval-green-soft: #e9f7f1;
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  padding: 24px 28px;
  overflow: hidden;
  color: var(--eval-ink);
  background:
    radial-gradient(circle at 92% 0%, rgba(7, 168, 114, 0.08), transparent 28%),
    #f7f9f8;
}
.workbench-actions { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; flex-shrink: 0; margin-bottom: 15px; }
.workbench-actions > p { margin: 0 auto 0 0; color: var(--eval-muted); font-size: '12px'px; line-height: 1.6; }
.polling-notice { color: var(--eval-muted); margin: 0 0 10px; font-size: '12px'px; }

.evaluation-header {
  position: relative;
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 24px;
  flex-shrink: 0;
  padding: 28px 32px;
  border: 1px solid #244b3f;
  border-radius: 18px 18px 0 0;
  overflow: hidden;
  color: #f2f7f4;
  background: radial-gradient(ellipse at 68% -30%, #315e4c, transparent 65%), #102c22;
  h1 { margin: 13px 0 12px; font-size: clamp(30px, 3.2vw, 46px); line-height: 1.15; letter-spacing: -0.05em; font-weight: 650; }
  p { max-width: 640px; margin: 0; color: #c0d4c9; font-size: '12px'px; line-height: 1.65; }
  .eyebrow { display: flex; align-items: center; gap: 8px; color: #b9dfc3; font-size: '10px'px; letter-spacing: .18em; }
}
.hero-copy, .header-stat { position: relative; z-index: 1; }
.hero-mark { position: absolute; top: 12px; right: 21%; color: #aad6bf; opacity: .15; transform: rotate(-12deg); pointer-events: none; }
.evaluation-dimensions { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); flex-shrink: 0; margin-bottom: 18px; border: 1px solid #dbe5df; border-top: 0; border-radius: 0 0 14px 14px; background: #eef3ef; }
.evaluation-dimensions > span { display: flex; align-items: center; justify-content: center; gap: 9px; padding: 12px 8px; color: #476254; font-size: '11px'px; }
.evaluation-dimensions > span + span { border-left: 1px solid #dbe5df; }
.evaluation-dimensions small { font: 10px monospace; color: #73867c; }

.eyebrow, .section-kicker {
  color: var(--eval-green);
  font-size: '11px'px;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.header-stat {
  display: flex;
  align-items: baseline;
  gap: 4px;
  color: #b4c9bd;
  font-size: '11px'px;
  flex-wrap: wrap;
  min-width: 96px;
  padding-left: 22px;
  border-left: 1px solid #416354;
  > span:first-child { flex-basis: 100%; }
  strong { color: #dbefb8; font-size: '60px'px; font-weight: 500; letter-spacing: -.065em; line-height: 1.05; font-variant-numeric: tabular-nums; }
}

.filter-panel {
  display: grid;
  grid-template-columns: repeat(4, minmax(130px, 1fr));
  gap: 10px;
  padding: 14px;
  margin-bottom: 14px;
  border: 1px solid var(--eval-line);
  border-radius: 14px;
  background: rgba(255, 255, 255, 0.88);
  box-shadow: 0 8px 30px rgba(32, 60, 48, 0.04);
}

.filter-field {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 5px;
  span { color: var(--eval-muted); font-size: '10px'px; font-weight: 650; letter-spacing: 0.04em; }
  input, select {
    width: 100%; height: 34px; padding: 0 10px; border: 1px solid #dce4e0; border-radius: 8px;
    outline: none; color: var(--eval-ink); background: #fff; font-size: '12px'px;
    &:focus { border-color: #54b999; box-shadow: 0 0 0 3px rgba(7, 168, 114, 0.09); }
  }
}

.filter-field--labels { grid-column: span 1; }
.filter-toggle { display: none; }
.filter-actions { display: flex; align-items: flex-end; justify-content: flex-end; gap: 8px; }

.button {
  display: inline-flex; height: 34px; align-items: center; justify-content: center; gap: 6px;
  padding: 0 14px; border: 1px solid transparent; border-radius: 8px; cursor: pointer;
  font-size: '12px'px; font-weight: 650; transition: 0.18s ease;
  &:disabled { cursor: not-allowed; opacity: 0.48; }
}
.button--primary { color: #fff; background: var(--eval-green); &:hover:not(:disabled) { background: #067b59; } }
.button--quiet { border-color: var(--eval-line); color: #35443e; background: #fff; &:hover:not(:disabled) { border-color: #a9cfc1; } }
.button--compact { height: 30px; padding: 0 10px; }

.workbench {
  display: grid;
  grid-template-columns: minmax(300px, 0.34fr) minmax(0, 1fr);
  flex: 1;
  min-height: 0;
  overflow: hidden;
  border: 1px solid var(--eval-line);
  border-radius: 16px;
  background: #fff;
  box-shadow: 0 18px 54px rgba(26, 55, 43, 0.07);
}

.run-browser { display: flex; min-height: 0; flex-direction: column; border-right: 1px solid var(--eval-line); background: #fbfcfb; }
.panel-heading, .inspection-heading {
  display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 18px 20px;
  border-bottom: 1px solid var(--eval-line);
}
.panel-heading > div { display: flex; align-items: baseline; gap: 8px; }
.panel-heading strong { font-size: '18px'px; }
.icon-button { display: grid; width: 30px; height: 30px; place-items: center; border: 1px solid var(--eval-line); border-radius: 8px; color: var(--eval-muted); background: #fff; cursor: pointer; }

.selection-bar { padding: 12px 14px; border-bottom: 1px solid #cae6da; background: var(--eval-green-soft); }
.selection-copy { display: flex; align-items: baseline; gap: 5px; margin-bottom: 9px; color: var(--eval-muted); font-size: '11px'px; strong { color: var(--eval-green); font-size: '18px'px; } }
.baseline-select { display: flex; align-items: center; gap: 6px; margin-bottom: 9px; font-size: '10px'px; color: var(--eval-muted); select { min-width: 0; flex: 1; height: 28px; border: 1px solid #b9d9cd; border-radius: 7px; background: #fff; font: 11px monospace; } }

.run-list { min-height: 0; overflow: auto; }
.run-row { display: flex; gap: 11px; padding: 15px 14px; border-bottom: 1px solid #edf1ef; cursor: pointer; transition: background 0.16s ease; &:hover { background: #f5f9f7; } }
.run-row--active { background: #edf8f3; box-shadow: inset 3px 0 0 var(--eval-green); }
.run-checkbox { padding-top: 2px; input { position: absolute; opacity: 0; } span { display: block; width: 16px; height: 16px; border: 1px solid #b8c5bf; border-radius: 5px; background: #fff; } input:checked + span { border-color: var(--eval-green); background: var(--eval-green); box-shadow: inset 0 0 0 3px #fff; } }
.run-row__body { min-width: 0; flex: 1; padding: 0; border: 0; color: inherit; background: transparent; font: inherit; text-align: left; cursor: pointer; }
.run-row__body:focus-visible { outline: 2px solid var(--eval-green); outline-offset: 5px; border-radius: 4px; }
.run-row__top { display: flex; align-items: center; justify-content: space-between; gap: 8px; code { overflow: hidden; font-size: '11px'px; text-overflow: ellipsis; white-space: nowrap; } }
.run-row__dataset { margin: 7px 0 5px; overflow: hidden; font-size: '13px'px; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.run-row__meta { display: flex; justify-content: space-between; color: var(--eval-muted); font-size: '10px'px; font-variant-numeric: tabular-nums; }

.status-pill { display: inline-flex; align-items: center; padding: 3px 7px; border-radius: 99px; color: #5d6964; background: #edf1ef; font-size: '9px'px; font-weight: 700; white-space: nowrap; }
.status-pill--success { color: #087552; background: #ddf5eb; }
.status-pill--running { color: #1767a2; background: #e3f1fb; }
.status-pill--danger { color: #a33a3a; background: #fbe7e7; }
.status-pill--warning { color: #8b6421; background: #fbf0d8; }
.label-row { display: flex; flex-wrap: wrap; gap: 5px; margin-top: 8px; }
.label-chip { padding: 2px 7px; border: 1px solid #cfe4db; border-radius: 99px; color: #34735d; background: #f4faf7; font-size: '9px'px; }

.load-more { width: 100%; padding: 12px; border: 0; border-top: 1px solid var(--eval-line); color: var(--eval-green); background: #fff; cursor: pointer; font-size: '11px'px; }
.inspection-panel { min-width: 0; min-height: 0; overflow: auto; background: #fff; }
.inspection-heading { position: sticky; top: 0; z-index: 3; background: rgba(255, 255, 255, 0.96); backdrop-filter: blur(12px); h2 { margin: 3px 0 0; font-size: '20px'px; letter-spacing: -0.02em; } }
.full-task-id { display: block; max-width: 520px; margin-top: 6px; overflow: hidden; color: var(--eval-muted); font-size: '10px'px; text-overflow: ellipsis; }
.export-actions { display: flex; gap: 7px; }

.state-block { display: flex; min-height: 150px; align-items: center; justify-content: center; gap: 8px; color: var(--eval-muted); font-size: '12px'px; }
.state-block--detail { min-height: 100%; flex-direction: column; }
.state-block--error { color: #ad4141; }
.empty-orbit { position: relative; width: 28px; height: 28px; border: 1px solid #bbd8cd; border-radius: 50%; span { position: absolute; top: 5px; left: 15px; width: 6px; height: 6px; border-radius: 50%; background: var(--eval-green); } }

.fact-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 10px; padding: 18px 20px 0; }
.fact-card { padding: 13px 14px; border: 1px solid var(--eval-line); border-radius: 11px; background: #fafcfb; span { display: block; margin-bottom: 6px; color: var(--eval-muted); font-size: '9px'px; text-transform: uppercase; } strong { font-size: '13px'px; } }
.text-success { color: var(--eval-green); }
.text-warning { color: #a16d17; }

.label-editor { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin: 14px 20px 0; padding: 13px 15px; border: 1px solid var(--eval-line); border-radius: 11px; h3 { margin: 0; font-size: '12px'px; } .label-row { margin-top: 6px; } }
.label-editor__control { display: flex; gap: 7px; input { width: 230px; height: 30px; padding: 0 9px; border: 1px solid var(--eval-line); border-radius: 7px; outline: 0; } }
.muted { color: var(--eval-muted); font-size: '11px'px; }

.detail-tabs { display: flex; gap: 20px; padding: 20px 20px 0; border-bottom: 1px solid var(--eval-line); button { padding: 0 0 10px; border: 0; border-bottom: 2px solid transparent; color: var(--eval-muted); background: none; cursor: pointer; font-size: '12px'px; font-weight: 650; &.active { border-color: var(--eval-green); color: var(--eval-green); } span { margin-left: 4px; color: #9aa7a1; } } }
.overview-grid { display: grid; grid-template-columns: 1.15fr 0.85fr; gap: 12px; padding: 18px 20px 24px; }
.result-card { min-width: 0; padding: 20px; border: 1px solid var(--eval-line); border-radius: 12px; background: #fbfdfc; animation: result-enter .5s cubic-bezier(.22, 1, .36, 1) both; }
.result-card:nth-child(2) { animation-delay: .045s; }
.result-card:nth-child(3) { animation-delay: .09s; }
.result-card:nth-child(4) { animation-delay: .135s; }
.result-card h3 { display: flex; align-items: center; gap: 8px; margin: 0 0 20px; color: var(--eval-muted); font-size: '12px'px; }
.result-card--cost { background: #edf4ea; border-color: #d6e4cf; }
.result-scores { display: grid; gap: 9px; margin: 0; }
.result-scores div { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; padding-bottom: 8px; border-bottom: 1px solid #eaf0ec; }
.result-scores dt { min-width: 0; overflow-wrap: anywhere; color: var(--eval-muted); font-size: '11px'px; }
.result-scores dd { margin: 0; font-size: '14px'px; font-weight: 650; font-variant-numeric: tabular-nums; white-space: nowrap; }
.result-value { display: block; color: #23503c; font-size: clamp(23px, 2.6vw, 34px); letter-spacing: -.045em; font-weight: 550; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.result-note, .result-missing { color: var(--eval-muted); font-size: '11px'px; line-height: 1.6; }
.result-note { margin: 12px 0 0; }
.json-panel summary { padding: 12px 14px; font-size: '12px'px; font-weight: 650; cursor: pointer; }
.json-panel summary:focus-visible { outline: 2px solid var(--eval-green); outline-offset: -3px; }
.json-panel, .data-section { min-width: 0; border: 1px solid var(--eval-line); border-radius: 12px; overflow: hidden; }
.json-panel pre { max-height: 430px; margin: 0; padding: 14px; overflow: auto; color: #2d4139; background: #f8faf9; font-size: '10px'px; line-height: 1.65; }
.data-section__heading { display: flex; align-items: center; justify-content: space-between; padding: 12px 14px; border-bottom: 1px solid var(--eval-line); h3 { margin: 0; font-size: '12px'px; } span { color: var(--eval-muted); font-size: '10px'px; } }

.question-section { padding: 16px 20px 24px; }
.question-card { display: grid; grid-template-columns: 42px minmax(0, 1fr); gap: 12px; padding: 15px 0; border-bottom: 1px solid var(--eval-line); }
.question-card__index { color: #9db2a9; font: 700 18px/1 monospace; }
.question-card__heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; h3 { margin: 0; font-size: '13px'px; line-height: 1.45; } }
.question-card dl { display: grid; gap: 7px; margin: 12px 0; div { display: grid; grid-template-columns: 105px minmax(0, 1fr); } dt { color: var(--eval-muted); font-size: '10px'px; } dd { margin: 0; color: #3d4d46; font-size: '11px'px; white-space: pre-wrap; } }
.ranking-row { display: flex; flex-wrap: wrap; gap: 6px; span { padding: 3px 7px; border-radius: 5px; color: #456058; background: #edf4f1; font: 9px monospace; &.unknown { color: #955b2d; background: #fbefe5; } } }
.human-rating { margin-top: 10px; border: 1px solid var(--eval-line); border-radius: 8px; overflow: hidden; }
.human-rating__toggle { display: flex; width: 100%; align-items: center; justify-content: space-between; padding: 8px 10px; border: 0; color: #315c4d; background: #f6faf8; cursor: pointer; font-size: '10px'px; font-weight: 650; span { color: var(--eval-muted); font: 9px monospace; } }
.human-rating__panel { padding: 10px; }
.human-rating__guidance { margin: 0 0 8px; color: var(--eval-muted); font-size: '9px'px; line-height: 1.45; }
.human-rating__form { display: grid; grid-template-columns: 100px minmax(160px, 1fr) auto; align-items: end; gap: 8px; label { display: grid; gap: 4px; color: var(--eval-muted); font-size: '9px'px; } select, input { height: 30px; padding: 0 8px; border: 1px solid var(--eval-line); border-radius: 7px; background: #fff; font-size: '10px'px; } }
.human-rating__history { display: grid; gap: 6px; padding: 0; margin: 10px 0 0; list-style: none; li { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: baseline; gap: 8px; padding: 7px 8px; border-radius: 6px; background: #f8faf9; font-size: '9px'px; } strong { color: var(--eval-green); } time { color: var(--eval-muted); } p { grid-column: 2 / -1; margin: 0; color: #45574f; line-height: 1.45; } }
.human-rating__error { margin: 8px 0 0; color: #ad4141; font-size: '10px'px; }
.load-more--questions { border: 1px solid var(--eval-line); border-radius: 8px; margin-top: 12px; }

.detail-empty { display: flex; min-height: 100%; align-items: center; justify-content: center; flex-direction: column; color: var(--eval-muted); text-align: center; h2 { margin: 22px 0 6px; color: var(--eval-ink); font-size: '18px'px; } p { max-width: 360px; margin: 0; font-size: '12px'px; line-height: 1.6; } }
.detail-empty__visual { position: relative; width: 150px; height: 90px; border-bottom: 1px solid #c9d9d2; border-left: 1px solid #c9d9d2; .point { position: absolute; width: 11px; height: 11px; border: 3px solid #fff; border-radius: 50%; background: var(--eval-green); box-shadow: 0 0 0 1px #7ec7ae; } .point--one { bottom: 18px; left: 24px; } .point--two { bottom: 45px; left: 70px; } .point--three { right: 18px; bottom: 67px; } &::after { position: absolute; right: 21px; bottom: 23px; width: 112px; height: 48px; border-top: 2px solid #71bda4; transform: skewY(-22deg); content: ''; } }

.comparison-view { padding-bottom: 28px; }
.comparison-run-strip { display: grid; grid-template-columns: repeat(auto-fit, minmax(155px, 1fr)); gap: 8px; padding: 16px 20px; }
.comparison-run { position: relative; display: flex; min-width: 0; flex-direction: column; gap: 5px; padding: 12px; border: 1px solid var(--eval-line); border-radius: 10px; background: #fafcfb; code { overflow: hidden; font-size: '10px'px; text-overflow: ellipsis; } small { overflow: hidden; color: var(--eval-muted); font-size: '9px'px; text-overflow: ellipsis; white-space: nowrap; } }
.baseline-badge { position: absolute; top: -7px; right: 8px; padding: 2px 6px; border-radius: 99px; color: #fff; background: var(--eval-green); font-size: '8px'px; font-weight: 700; }
.comparison-view .data-section { margin: 0 20px 14px; }
.table-scroll { overflow: auto; }
.comparison-table { width: 100%; border-collapse: collapse; font-size: '10px'px; th, td { min-width: 135px; padding: 10px 12px; border-bottom: 1px solid #edf1ef; text-align: left; vertical-align: top; } th { position: sticky; top: 0; color: var(--eval-muted); background: #f8faf9; font-size: '9px'px; } th:first-child, td:first-child { min-width: 220px; } .row-differs { background: #fffaf1; } code { font-size: '9px'px; overflow-wrap: anywhere; } }
.missing-value { color: #a06d28; font-style: italic; }
.metric-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(270px, 1fr)); gap: 10px; padding: 12px; }
.metric-card { padding: 12px; border: 1px solid #e7ece9; border-radius: 9px; }
.metric-card__heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; code { font-size: '10px'px; font-weight: 700; } small { display: block; max-width: 220px; margin-top: 4px; overflow: hidden; color: var(--eval-muted); font-size: '8px'px; text-overflow: ellipsis; white-space: nowrap; } }
.compatibility { padding: 2px 6px; border-radius: 99px; color: #087552; background: #ddf5eb; font-size: '8px'px; font-weight: 700; }
.compatibility--bad { color: #9a4b32; background: #f8e7df; }
.metric-values { display: grid; gap: 5px; margin-top: 10px; }
.metric-value { display: grid; grid-template-columns: minmax(80px, 1fr) auto minmax(135px, auto) minmax(90px, auto); align-items: baseline; gap: 8px; padding: 6px 8px; border-radius: 6px; background: #f8faf9; span { overflow: hidden; font: 9px monospace; text-overflow: ellipsis; } strong { font-size: '12px'px; font-variant-numeric: tabular-nums; } small { color: var(--eval-muted); font-size: '8px'px; text-align: right; } }
.confidence-copy { color: #356c58; font-size: '8px'px; font-style: normal; font-variant-numeric: tabular-nums; white-space: nowrap; }

@media (max-width: 1180px) {
  .filter-panel { grid-template-columns: repeat(3, minmax(120px, 1fr)); }
  .filter-field--labels { grid-column: span 1; }
  .workbench { grid-template-columns: 300px minmax(0, 1fr); }
  .fact-grid { grid-template-columns: repeat(2, 1fr); }
  .overview-grid { grid-template-columns: 1fr; }
}

@media (max-width: 820px) {
  .evaluation-page { padding: 18px; overflow: auto; }
  .evaluation-header { padding: 24px 20px; align-items: center; }
  .header-stat strong { font-size: '44px'px; }
  .hero-mark { display: none; }
  .evaluation-dimensions { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .evaluation-dimensions > span { justify-content: flex-start; padding-left: 16px; }
  .evaluation-dimensions > span:nth-child(3) { border-left: 0; }
  .filter-toggle { display: flex; flex-shrink: 0; align-items: center; gap: 10px; width: 100%; padding: 12px 14px; margin-bottom: 12px; border: 1px solid var(--eval-line); border-radius: 10px; background: #fff; color: var(--eval-ink); font: inherit; font-size: '12px'px; cursor: pointer; }
  .filter-toggle > svg:last-child { margin-left: auto; transition: transform .2s ease; }
  .filter-toggle__arrow--open { transform: rotate(180deg); }
  .filter-panel--collapsed { display: none; }
  .filter-panel { grid-template-columns: repeat(2, minmax(120px, 1fr)); }
  .workbench { display: flex; min-height: 900px; flex-direction: column; overflow: visible; }
  .run-browser { max-height: 420px; border-right: 0; border-bottom: 1px solid var(--eval-line); }
  .inspection-panel { min-height: 500px; }
}
@media (max-width: 480px) {
  .evaluation-page { padding: 12px; }
  .evaluation-header { padding: 22px 16px; }
  .evaluation-header h1 { font-size: '28px'px; }
  .header-stat { min-width: 62px; padding-left: 12px; }
  .filter-panel { grid-template-columns: 1fr; }
  .filter-field--labels { grid-column: auto; }
  .label-editor { flex-direction: column; align-items: stretch; }
  .label-editor__control input { width: 100%; min-width: 0; }
  .inspection-heading { flex-wrap: wrap; gap: 12px; }
}
.button:focus-visible, .icon-button:focus-visible, .detail-tabs button:focus-visible { outline: 2px solid var(--eval-green); outline-offset: 3px; }
@media (hover: hover) { .button:hover:not(:disabled) { transform: translateY(-1px); box-shadow: 0 4px 10px rgba(16, 46, 36, .08); } }
.icon-spinning { animation: icon-spin 1s linear infinite; }
@keyframes icon-spin { to { transform: rotate(360deg); } }
@keyframes result-enter { from { opacity: 0; transform: translateY(9px); } to { opacity: 1; transform: translateY(0); } }
@media (prefers-reduced-motion: reduce) { .result-card, .icon-spinning { animation: none; } .button, .run-row, .filter-toggle > svg { transition: none; } .button:hover:not(:disabled) { transform: none; } }
</style>
