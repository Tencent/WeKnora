<template>
  <div class="model-settings">
    <div class="section-header">
      <div class="section-header__top">
        <div>
          <h2>{{ $t('modelSettings.title') }}</h2>
          <p class="section-description">{{ $t('modelSettings.description') }}</p>
        </div>
        <t-button
          v-if="authStore.hasRole('admin')"
          type="button"
          theme="primary"
          variant="text"
          size="medium"
          class="model-test-trigger"
          @click="showDebugDrawer = true"
        >
          <template #icon><play-circle-icon /></template>
          {{ $t('modelSettings.actions.debugModel') }}
        </t-button>
      </div>

      <div class="builtin-models-hint" role="note">
        <p class="builtin-hint-label">{{ $t('modelSettings.builtinModels.title') }}</p>
        <p class="builtin-hint-text">
          {{ $t(authStore.isSystemAdmin
            ? 'modelSettings.builtinModels.descriptionAdmin'
            : 'modelSettings.builtinModels.description') }}
        </p>
        <a class="doc-link" href="https://github.com/Tencent/WeKnora/blob/main/docs/BUILTIN_MODELS.md" target="_blank"
          rel="noopener noreferrer">
          {{ $t('modelSettings.builtinModels.viewGuide') }}
          <t-icon name="link" class="link-icon" />
        </a>
      </div>
    </div>

    <t-tabs v-model="activeTypeFilter" class="model-type-tabs" data-guide="settings-models">
      <t-tab-panel value="all" :label="`${$t('common.all')}(${allLegacyModels.length})`" />
      <t-tab-panel value="chat" :label="`${$t('modelSettings.typeShort.chat')}(${countByType('chat')})`" />
      <t-tab-panel value="embedding"
        :label="`${$t('modelSettings.typeShort.embedding')}(${countByType('embedding')})`" />
      <t-tab-panel value="rerank" :label="`${$t('modelSettings.typeShort.rerank')}(${countByType('rerank')})`" />
      <t-tab-panel value="vllm" :label="`${$t('modelSettings.typeShort.vllm')}(${countByType('vllm')})`" />
      <t-tab-panel value="asr" :label="`${$t('modelSettings.typeShort.asr')}(${countByType('asr')})`" />
    </t-tabs>

    <div class="usage-range-filter">
      <span>{{ usageLabels.range }}</span>
      <t-select v-model="usageRange" size="small" :options="usageRangeOptions" />
    </div>

    <t-loading :loading="loading" size="small" class="model-list-loading">
      <div v-if="!loading && filteredModels.length === 0 && !authStore.hasRole('admin')" class="empty-state">
        <t-empty :description="emptyHint" />
      </div>
      <div v-else-if="!loading" class="model-grid">
        <div v-for="model in filteredModels" :key="`${model._modelType}-${model.id}`" class="model-card" :class="[
          `model-card--${model._modelType}`,
          {
            'model-card--builtin': model.isBuiltin,
            'model-card--clickable': isModelCardClickable(model),
          },
        ]" @click="onModelCardClick($event, model._modelType, model)">
          <div class="model-card__badge" :aria-label="typeLabel(model._modelType)">
            <t-icon :name="typeIcon(model._modelType)" size="18px" />
          </div>
          <div class="model-card__body">
            <div class="model-card__header">
              <h3 class="model-card__title">{{ modelDisplayName(model) }}</h3>
              <span v-if="model.isBuiltin" class="model-card__lock" :title="$t('modelSettings.builtinTag')"
                :aria-label="$t('modelSettings.builtinTag')">
                <t-icon :name="authStore.isSystemAdmin ? 'edit-1' : 'lock-on'" />
              </span>
              <div v-if="canManageModel(model)" class="model-card__actions" @click.stop>
                <t-dropdown :options="getModelOptions(model._modelType, model)" placement="bottom-right" attach="body"
                  trigger="click"
                  @click="(data: any) => handleMenuAction({ value: data.value }, model._modelType, model)">
                  <t-button variant="text" shape="square" size="small" class="model-card__action-btn model-card__more">
                    <t-icon name="ellipsis" />
                  </t-button>
                </t-dropdown>
                <t-popconfirm
                  v-if="canDeleteModel(model)"
                  :content="$t('modelSettings.confirmDelete', { name: modelDisplayName(model) })"
                  :confirm-btn="{ content: $t('common.delete'), theme: 'danger' }"
                  :cancel-btn="{ content: $t('common.cancel') }"
                  placement="bottom-right"
                  @confirm="deleteModel(model._modelType, model.id)"
                >
                  <t-tooltip :content="$t('common.delete')" placement="top">
                    <t-button
                      theme="danger"
                      shape="square"
                      variant="text"
                      size="small"
                      class="model-card__action-btn model-card__delete"
                      @click.stop
                    >
                      <template #icon><t-icon name="delete" /></template>
                    </t-button>
                  </t-tooltip>
                </t-popconfirm>
              </div>
            </div>
            <p class="model-card__subtitle">
              <span>{{ vendorLabel(model) }}</span>
              <template v-if="model._modelType === 'embedding' && model.dimension">
                <span class="model-card__sep">·</span>
                <span>{{ $t('model.editor.dimensionLabel') }} {{ model.dimension }}</span>
              </template>
              <template v-if="model._modelType === 'chat' || model._modelType === 'vllm'">
                <span class="model-card__sep">·</span>
                <span
                  class="model-card__ctx"
                  :class="{ 'model-card__ctx--default': isDefaultContextWindow(model.contextWindow) }"
                  :title="contextWindowTitle(model.contextWindow)"
                >{{ formatContextWindow(model.contextWindow) }}</span>
              </template>
              <template v-if="model._modelType === 'chat' && model.supportsVision">
                <span class="model-card__sep">·</span>
                <span class="model-card__vision" :title="$t('model.editor.supportsVisionLabel')"
                  :aria-label="$t('model.editor.supportsVisionLabel')">
                  <t-icon name="image" size="12px" />
                </span>
              </template>
            </p>
            <div v-if="hasModelUsage(model)" class="model-card__usage" @click.stop>
              <span class="model-card__usage-item" :title="usageLabels.calls">
                {{ usageLabels.calls }} {{ formatInteger(modelUsage(model)!.call_count) }}
              </span>
              <span class="model-card__usage-item" :title="usageLabels.tokens">
                {{ usageLabels.tokens }} {{ formatCompact(modelUsage(model)!.total_tokens) }}
              </span>
              <span v-if="modelUsage(model)!.cache_reported_calls > 0" class="model-card__usage-item"
                :title="cacheMetricTitle(modelUsage(model)!)">
                {{ usageLabels.cache }} {{ formatPercent(modelUsage(model)!.cache_hit_rate) }}
              </span>
              <span v-if="modelUsage(model)?.priced_calls" class="model-card__usage-item model-card__usage-item--cost"
                :title="modelUsage(model)!.unpriced_calls > 0 ? usageLabels.partialCost : usageLabels.estimatedCost">
                {{ formatCosts(modelUsage(model)!.cost_by_currency) }}
                <template v-if="modelUsage(model)!.unpriced_calls > 0"> · {{ usageLabels.partial }}</template>
              </span>
              <span v-else-if="modelUsage(model)?.unpriced_calls" class="model-card__usage-item model-card__usage-item--muted"
                :title="usageLabels.unpriced">
                {{ usageLabels.costUnknown }}
              </span>
              <span v-if="embeddingCacheUsage(model)?.lookup_count" class="model-card__usage-item model-card__usage-item--cache"
                :title="usageLabels.embeddingCacheHint">
                {{ usageLabels.embeddingCache }} {{ formatPercent(embeddingCacheUsage(model)!.hit_rate) }}
              </span>
              <span v-if="embeddingCacheUsage(model)?.avoided_computations" class="model-card__usage-item"
                :title="usageLabels.embeddingAvoidedHint">
                {{ usageLabels.embeddingAvoided }} {{ formatInteger(embeddingCacheUsage(model)!.avoided_computations) }}
              </span>
              <span v-if="wikiSummary(model)" class="model-card__usage-item model-card__usage-item--wiki">
                Wiki {{ formatInteger(wikiSummary(model)!.call_count) }} {{ usageLabels.callsUnit }}
              </span>
              <button
                v-if="modelPurposes(model).length"
                type="button"
                class="model-card__usage-toggle"
                :aria-expanded="isUsageExpanded(model)"
                @click.stop="toggleUsageDetails(model)"
              >
                {{ isUsageExpanded(model) ? usageLabels.hideDetails : usageLabels.details }}
                <t-icon :name="isUsageExpanded(model) ? 'chevron-up' : 'chevron-down'" size="12px" />
              </button>
            </div>
            <div v-if="isUsageExpanded(model)" class="model-card__purpose-list" @click.stop>
              <div v-if="wikiSummary(model)" class="model-card__wiki-summary">
                <strong>{{ usageLabels.wikiSummary }}</strong>
                <span>{{ usageLabels.calls }} {{ formatInteger(wikiSummary(model)!.call_count) }}</span>
                <span>{{ usageLabels.tokens }} {{ formatCompact(wikiSummary(model)!.total_tokens) }}</span>
                <span v-if="wikiSummary(model)!.cache_reported_calls > 0">
                  {{ usageLabels.cache }} {{ formatPercent(wikiSummary(model)!.cache_hit_rate) }}
                </span>
                <span v-if="wikiSummary(model)!.priced_calls > 0">
                  {{ formatCosts(wikiSummary(model)!.cost_by_currency) }}
                  <template v-if="wikiSummary(model)!.unpriced_calls > 0"> · {{ usageLabels.partial }}</template>
                </span>
                <span v-else-if="wikiSummary(model)!.unpriced_calls > 0">{{ usageLabels.costUnknown }}</span>
              </div>
              <div v-for="purpose in modelPurposes(model)" :key="purpose.purpose || '_unspecified'"
                class="model-card__purpose-row">
                <div class="model-card__purpose-heading">
                  <span>{{ purposeLabel(purpose.purpose) }}</span>
                  <span>{{ formatInteger(purpose.usage.call_count) }} {{ usageLabels.callsUnit }}</span>
                </div>
                <div class="model-card__purpose-bar" aria-hidden="true">
                  <span :style="{ width: purposeBarWidth(model, purpose.usage.call_count) }" />
                </div>
                <div class="model-card__purpose-metrics">
                  <span>{{ usageLabels.tokens }} {{ formatCompact(purpose.usage.total_tokens) }}</span>
                  <span v-if="purpose.usage.cache_reported_calls > 0">
                    {{ usageLabels.cache }} {{ formatPercent(purpose.usage.cache_hit_rate) }}
                  </span>
                  <span v-if="purpose.usage.priced_calls > 0" :title="purpose.usage.unpriced_calls > 0 ? usageLabels.partialCost : usageLabels.estimatedCost">
                    {{ formatCosts(purpose.usage.cost_by_currency) }}
                    <template v-if="purpose.usage.unpriced_calls > 0"> · {{ usageLabels.partial }}</template>
                  </span>
                  <span v-else-if="purpose.usage.unpriced_calls > 0">{{ usageLabels.costUnknown }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
        <button
          v-if="authStore.hasRole('admin')"
          type="button"
          class="model-card model-card--add"
          data-guide="settings-add-model"
          @click="openAddDialog"
        >
          <span class="model-card--add__icon" aria-hidden="true">
            <add-icon />
          </span>
          <span class="model-card--add__label">{{ $t('modelSettings.actions.addModel') }}</span>
        </button>
      </div>
    </t-loading>

    <t-dialog
      v-model:visible="showUsageDialog"
      :header="$t('modelSettings.dependencyUsage.title')"
      :footer="false"
      width="680px"
      destroy-on-close
    >
      <div v-if="usageConflict" class="model-usage-dialog">
        <p class="model-usage-dialog__description">
          {{ $t('modelSettings.dependencyUsage.description', { name: usageConflictModelName }) }}
        </p>

        <div class="model-usage-dialog__content">
          <section v-if="usageConflict.knowledge_bases.length" class="model-usage-group">
            <h3>
              {{ $t('modelSettings.dependencyUsage.knowledgeBases', {
                count: modelUsageResourceCount(usageConflict.knowledge_bases, usageConflict.knowledge_base_total)
              }) }}
            </h3>
            <ul>
              <li v-for="resource in usageConflict.knowledge_bases" :key="resource.id">
                <div class="model-usage-resource">
                  <strong>{{ resource.name || resource.id }}</strong>
                  <div class="model-usage-bindings">
                    <t-tag
                      v-for="(binding, index) in resource.bindings"
                      :key="`${binding}-${index}`"
                      size="small"
                      variant="light"
                    >
                      {{ $t(modelUsageBindingI18nKey(binding)) }}
                    </t-tag>
                  </div>
                </div>
                <t-button
                  theme="primary"
                  variant="text"
                  size="small"
                  @click="openUsageResource('knowledge_base', resource.id, resource.bindings)"
                >
                  {{ $t('modelSettings.dependencyUsage.openConfiguration') }}
                </t-button>
              </li>
            </ul>
            <p
              v-if="modelUsageListTruncated(usageConflict.knowledge_bases, usageConflict.knowledge_base_total)"
              class="model-usage-truncated"
            >
              {{ $t('modelSettings.dependencyUsage.truncated', {
                shown: usageConflict.knowledge_bases.length,
                total: modelUsageResourceCount(usageConflict.knowledge_bases, usageConflict.knowledge_base_total)
              }) }}
            </p>
          </section>

          <section v-if="usageConflict.agents.length" class="model-usage-group">
            <h3>{{ $t('modelSettings.dependencyUsage.agents', {
              count: modelUsageResourceCount(usageConflict.agents, usageConflict.agent_total)
            }) }}</h3>
            <ul>
              <li v-for="resource in usageConflict.agents" :key="resource.id">
                <div class="model-usage-resource">
                  <strong>{{ resource.name || resource.id }}</strong>
                  <div class="model-usage-bindings">
                    <t-tag
                      v-for="(binding, index) in resource.bindings"
                      :key="`${binding}-${index}`"
                      size="small"
                      variant="light"
                    >
                      {{ $t(modelUsageBindingI18nKey(binding)) }}
                    </t-tag>
                  </div>
                </div>
                <t-button
                  theme="primary"
                  variant="text"
                  size="small"
                  @click="openUsageResource('agent', resource.id, resource.bindings)"
                >
                  {{ $t('modelSettings.dependencyUsage.openConfiguration') }}
                </t-button>
              </li>
            </ul>
            <p
              v-if="modelUsageListTruncated(usageConflict.agents, usageConflict.agent_total)"
              class="model-usage-truncated"
            >
              {{ $t('modelSettings.dependencyUsage.truncated', {
                shown: usageConflict.agents.length,
                total: modelUsageResourceCount(usageConflict.agents, usageConflict.agent_total)
              }) }}
            </p>
          </section>

          <section v-if="usageConflict.long_term_memory.bindings.length" class="model-usage-group">
            <h3>{{ $t('modelSettings.dependencyUsage.longTermMemory') }}</h3>
            <div class="model-usage-memory">
              <div class="model-usage-bindings">
                <t-tag
                  v-for="(binding, index) in usageConflict.long_term_memory.bindings"
                  :key="`${binding}-${index}`"
                  size="small"
                  variant="light"
                >
                  {{ $t(modelUsageBindingI18nKey(binding)) }}
                </t-tag>
              </div>
            </div>
          </section>
        </div>

        <div class="model-usage-dialog__actions">
          <t-button @click="showUsageDialog = false">{{ $t('common.close') }}</t-button>
        </div>
      </div>
    </t-dialog>
    <div v-if="usageLoading || usageError || usageIsEmpty" class="usage-load-state" role="status">
      <span v-if="usageLoading">{{ usageLabels.loading }}</span>
      <template v-else-if="usageError">
        <span>{{ usageLabels.loadFailed }}</span>
        <button type="button" @click="loadUsage">{{ usageLabels.retry }}</button>
      </template>
      <span v-else>{{ usageLabels.noData }}</span>
    </div>

    <!-- 模型编辑器抽屉 -->
    <ModelEditorDialog v-model:visible="showDialog" :model-type="currentModelType" :model-data="editingModel"
      @confirm="handleModelSave" />
    <ModelDebugDrawer v-model:visible="showDebugDrawer" :models="allModels" />

  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onMounted, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { AddIcon, PlayCircleIcon } from 'tdesign-icons-vue-next'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import ModelEditorDialog from '@/components/ModelEditorDialog.vue'
import ModelDebugDrawer from '@/components/ModelDebugDrawer.vue'
import {
  listModels,
  createModel,
  updateModel as updateModelAPI,
  deleteModel as deleteModelAPI,
  ModelInUseError,
  modelUsageBindingI18nKey,
  modelUsageKnowledgeBaseSection,
  modelUsageListTruncated,
  modelUsageResourceCount,
  modelUsageResourceRoute,
  type ModelConfig,
  type ModelUsageDetails,
  type ModelUsageResourceKind,
} from '@/api/model'
import { getEvaluationModelUsage, type EvaluationModelUsageStat, type EvaluationUsage } from '@/api/evaluation'
import { useAuthStore } from '@/stores/auth'
import { useUIStore } from '@/stores/ui'
import { focusKbEditorSection } from '@/config/contextualGuides'
import { useChatResourcesStore } from '@/stores/chatResources'
import {
  formatContextWindow,
  isDefaultContextWindow,
  effectiveContextWindow,
} from '@/utils/contextWindow'

const { t, te, locale } = useI18n()
const authStore = useAuthStore()
const uiStore = useUIStore()
const chatResources = useChatResourcesStore()
const router = useRouter()
type ModelType = 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'
type FilterType = 'all' | ModelType

const showDialog = ref(false)
const showDebugDrawer = ref(false)
const showUsageDialog = ref(false)
const usageConflict = ref<ModelUsageDetails | null>(null)
const usageConflictModelName = ref('')
const currentModelType = ref<ModelType>('chat')
const editingModel = ref<any>(null)
const loading = ref(true)
const usageLoading = ref(false)
const usageError = ref(false)
const activeTypeFilter = ref<FilterType>('all')

const MODEL_TAB_TYPES: FilterType[] = ['chat', 'embedding', 'rerank', 'vllm', 'asr']
const KNOWLEDGE_BASE_EDITOR_HOST_ROUTES = new Set([
  'home',
  'knowledgeBaseList',
  'knowledgeBaseDetail',
  'globalCreatChat',
  'kbCreatChat',
  'chat',
])
watch(
  () => uiStore.settingsInitialSubSection,
  (sub) => {
    if (sub && MODEL_TAB_TYPES.includes(sub as FilterType)) {
      activeTypeFilter.value = sub as FilterType
    }
  },
  { immediate: true },
)

// 模型列表数据
const allModels = ref<ModelConfig[]>([])
const modelUsageByID = ref<Record<string, EvaluationUsage>>({})
const modelUsageStatsByID = ref<Record<string, EvaluationModelUsageStat>>({})
const expandedUsageModels = ref<Record<string, boolean>>({})
const usageRange = ref<'24h' | '7d' | '30d' | 'all'>('30d')

const usageLabels = computed(() => ({
  calls: t('modelSettings.usage.calls'),
  callsUnit: t('modelSettings.usage.callsUnit'),
  tokens: t('modelSettings.usage.tokens'),
  cache: t('modelSettings.usage.cache'),
  cacheHitRate: t('modelSettings.usage.cacheHitRate'),
  cacheCoverage: t('modelSettings.usage.cacheCoverage'),
  estimatedCost: t('modelSettings.usage.estimatedCost'),
  unpriced: t('modelSettings.usage.unpriced'),
  costUnknown: t('modelSettings.usage.costUnknown'),
  range: t('modelSettings.usage.range'),
  details: t('modelSettings.usage.details'),
  hideDetails: t('modelSettings.usage.hideDetails'),
  partial: t('modelSettings.usage.partial'),
  partialCost: t('modelSettings.usage.partialCost'),
  embeddingCache: t('modelSettings.usage.embeddingCache'),
  embeddingCacheHint: t('modelSettings.usage.embeddingCacheHint'),
  embeddingAvoided: t('modelSettings.usage.embeddingAvoided'),
  embeddingAvoidedHint: t('modelSettings.usage.embeddingAvoidedHint'),
  wikiSummary: t('modelSettings.usage.wikiSummary'),
  loading: t('modelSettings.usage.loading'),
  loadFailed: t('modelSettings.usage.loadFailed'),
  retry: t('modelSettings.usage.retry'),
  noData: t('modelSettings.usage.noData'),
}))

const usageRangeOptions = computed(() => [
  { label: t('modelSettings.usage.range24h'), value: '24h' },
  { label: t('modelSettings.usage.range7d'), value: '7d' },
  { label: t('modelSettings.usage.range30d'), value: '30d' },
  { label: t('modelSettings.usage.rangeAll'), value: 'all' },
])

const selectedUsageRange = () => {
  if (usageRange.value === 'all') return {}
  const durations = { '24h': 24, '7d': 24 * 7, '30d': 24 * 30 }
  const end = new Date()
  const start = new Date(end.getTime() - durations[usageRange.value] * 60 * 60 * 1000)
  return { startTime: start.toISOString(), endTime: end.toISOString() }
}

const modelUsage = (model: any) => modelUsageByID.value[model.id]
const modelPurposes = (model: any) => modelUsageStatsByID.value[model.id]?.purposes || []
const embeddingCacheUsage = (model: any) => modelUsageStatsByID.value[model.id]?.embedding_cache
const hasModelUsage = (model: any) => Boolean(modelUsage(model)?.call_count || embeddingCacheUsage(model)?.lookup_count)
const usageIsEmpty = computed(() => !loading.value && !usageLoading.value && !usageError.value
  && allModels.value.length > 0 && Object.keys(modelUsageStatsByID.value).length === 0)
const wikiSummary = (model: any): EvaluationUsage | null => {
  const wikiPurposes = modelPurposes(model).filter(item => item.purpose.startsWith('wiki_'))
  if (!wikiPurposes.length) return null
  const result: EvaluationUsage = {
    call_count: 0, successful_calls: 0, failed_calls: 0, prompt_tokens: 0,
    completion_tokens: 0, total_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0,
    cache_miss_tokens: 0, cache_reported_calls: 0, cache_hit_calls: 0, cache_hit_rate: 0,
    cache_coverage_rate: 0,
    model_duration_ms: 0, average_model_latency_ms: 0, priced_calls: 0, unpriced_calls: 0,
    cost_by_currency: {},
  }
  for (const { usage } of wikiPurposes) {
    for (const key of ['call_count', 'successful_calls', 'failed_calls', 'prompt_tokens', 'completion_tokens',
      'total_tokens', 'cache_read_tokens', 'cache_write_tokens', 'cache_miss_tokens', 'cache_reported_calls',
      'cache_hit_calls', 'model_duration_ms', 'priced_calls', 'unpriced_calls'] as const) {
      result[key] += usage[key]
    }
    for (const [currency, cost] of Object.entries(usage.cost_by_currency || {})) {
      result.cost_by_currency[currency] = (result.cost_by_currency[currency] || 0) + cost
    }
  }
  const reportedPromptTokens = result.cache_read_tokens + result.cache_miss_tokens
  result.cache_hit_rate = reportedPromptTokens > 0 ? result.cache_read_tokens / reportedPromptTokens : 0
  result.cache_coverage_rate = result.call_count > 0 ? result.cache_reported_calls / result.call_count : 0
  result.average_model_latency_ms = result.call_count > 0 ? result.model_duration_ms / result.call_count : 0
  return result
}
const isUsageExpanded = (model: any) => Boolean(expandedUsageModels.value[model.id])
const toggleUsageDetails = (model: any) => {
  expandedUsageModels.value = {
    ...expandedUsageModels.value,
    [model.id]: !expandedUsageModels.value[model.id],
  }
}
const purposeLabel = (purpose: string) => {
  if (!purpose) return t('modelSettings.usage.purposes.unspecified')
  const key = `modelSettings.usage.purposes.${purpose}`
  return te(key) ? t(key) : purpose.replaceAll('_', ' ')
}
const purposeBarWidth = (model: any, callCount: number) => {
  const maxCalls = Math.max(...modelPurposes(model).map(item => item.usage.call_count), 1)
  return `${Math.max((callCount / maxCalls) * 100, 3)}%`
}
const formatInteger = (value: number) => new Intl.NumberFormat(locale.value).format(value || 0)
const formatCompact = (value: number) => new Intl.NumberFormat(locale.value, {
  notation: 'compact', maximumFractionDigits: 1,
}).format(value || 0)
const formatPercent = (value: number) => new Intl.NumberFormat(locale.value, {
  style: 'percent', maximumFractionDigits: 1,
}).format(value || 0)
const cacheMetricTitle = (usage: EvaluationUsage) =>
  `${usageLabels.value.cacheHitRate} · ${usageLabels.value.cacheCoverage} ${formatPercent(usage.cache_coverage_rate)}`
const formatCosts = (costs: Record<string, number>) => Object.entries(costs || {})
  .sort(([left], [right]) => left.localeCompare(right))
  .map(([currency, amount]) => `${currency} ${amount.toLocaleString(locale.value, {
    minimumFractionDigits: amount > 0 && amount < 0.01 ? 6 : 2,
    maximumFractionDigits: amount > 0 && amount < 0.01 ? 6 : 2,
  })}`)
  .join(' · ')

// 后端 type → 前端分组 type 的映射
const backendTypeToModelType: Record<string, ModelType> = {
  KnowledgeQA: 'chat',
  Embedding: 'embedding',
  Rerank: 'rerank',
  VLLM: 'vllm',
  ASR: 'asr'
}

// 将后端模型格式转换为旧的前端格式（附带 _modelType 便于渲染）
// apiKey is always blank here: the server's main GET response does not
// include it (see internal/handler/dto/model.go — ModelParametersDTO omits
// secret fields). Credential read/write happens inside the editor dialog
// via the dedicated /credentials subresource.
function convertToLegacyFormat(model: ModelConfig) {
  return {
    id: model.id!,
    name: model.name,
    displayName: model.display_name || '',
    source: model.source,
    modelName: model.name,
    baseUrl: model.parameters.base_url || '',
    apiKey: '',
    provider: model.parameters.provider || '',
    dimension: model.parameters.embedding_parameters?.dimension,
    supportsDimensionOverride: model.parameters.embedding_parameters?.supports_dimension_override || false,
    isBuiltin: model.is_builtin || false,
    supportsVision: model.parameters.supports_vision || false,
    contextWindow: model.parameters.context_window || undefined,
    maxConcurrency: model.parameters.max_concurrency,
    customHeaders: model.parameters.custom_headers
      ? Object.entries(model.parameters.custom_headers).map(([key, value]) => ({ key, value: String(value) }))
      : [],
    lkeapRegion: model.parameters.extra_config?.region || 'ap-guangzhou',
    // 原始存库值，编辑弹窗内再 resolve（避免打开时被推断值覆盖）
    thinkingControl: model.parameters.extra_config?.thinking_control,
    pricingEnabled: model.parameters.extra_config?.pricing_enabled === 'true',
    pricingCurrency: model.parameters.extra_config?.pricing_currency || 'USD',
    inputPricePerMillion: Number(model.parameters.extra_config?.input_price_per_million || 0),
    outputPricePerMillion: Number(model.parameters.extra_config?.output_price_per_million || 0),
    cacheReadPricePerMillion: Number(model.parameters.extra_config?.cache_read_price_per_million || 0),
    cacheWritePricePerMillion: Number(model.parameters.extra_config?.cache_write_price_per_million || 0),
    embeddingCacheEnabled: model.parameters.extra_config?.embedding_cache_enabled !== 'false',
    embeddingCacheTTLSeconds: Number(model.parameters.extra_config?.embedding_cache_ttl_seconds || 86400),
    embeddingCacheMaxEntries: Number(model.parameters.extra_config?.embedding_cache_max_entries || 10000),
    _modelType: backendTypeToModelType[model.type] || 'chat' as ModelType,
    // Preserve the credential metadata map so the editor dialog can render
    // the "Configured" state without an extra round-trip.
    credentials: model.credentials,
  }
}

// 平铺 + 过滤
const allLegacyModels = computed(() => allModels.value.map(convertToLegacyFormat))
const filteredModels = computed(() => {
  if (activeTypeFilter.value === 'all') return allLegacyModels.value
  return allLegacyModels.value.filter(m => m._modelType === activeTypeFilter.value)
})

const countByType = (type: ModelType) => allLegacyModels.value.filter(m => m._modelType === type).length

// 类型徽章图标。沿用 TDesign 自带 icon name，避免再引第三方图标包。
const typeIcon = (type: ModelType): string => {
  const map: Record<ModelType, string> = {
    chat: 'chat',
    embedding: 'chart-bubble',
    rerank: 'filter-sort',
    vllm: 'image',
    asr: 'sound',
  }
  return map[type]
}

const typeLabel = (type: ModelType) => {
  const map: Record<ModelType, string> = {
    chat: t('modelSettings.typeShort.chat'),
    embedding: t('modelSettings.typeShort.embedding'),
    rerank: t('modelSettings.typeShort.rerank'),
    vllm: t('modelSettings.typeShort.vllm'),
    asr: t('modelSettings.typeShort.asr')
  }
  return map[type]
}

const sourceLabel = (type: ModelType) => {
  // vllm / asr 的 remote 文案特殊，其余走通用 remote 文案
  if (type === 'vllm' || type === 'asr') {
    return t('modelSettings.source.openaiCompatible')
  }
  return t('modelSettings.source.remote')
}

// Maps a backend `provider` id (e.g. "openai", "aliyun", "weknoracloud")
// to its localized short label. Reuses the same i18n keys the editor's
// provider dropdown uses, so the model card and the editor stay in sync
// when a provider is renamed. Falls back to '' when the backend didn't
// store a provider — caller falls back to sourceLabel().
const providerLabel = (model: any): string => {
  const id = model.provider
  if (!id) return ''
  const key = `model.editor.providers.${id}.label`
  return te(key) ? t(key) : id
}

// What the vendor chip on a card shows. Keeps the chip text uniformly
// short so cards line up:
//   local  → "Ollama"
//   remote → provider's localized short name (e.g. "腾讯云 LKEAP",
//            "阿里云 DashScope"). For the catch-all "generic" provider
//            we render a single short word ("自定义" / "Custom") — the
//            editor dropdown's longer "自定义 (OpenAI兼容接口)" label
//            blows out the card chip row, and the "OpenAI 兼容" framing
//            isn't meaningful to most end users (they didn't pick "I
//            want OpenAI compatibility", they just pasted a base URL).
const vendorLabel = (model: any): string => {
  if (model.source === 'local') return 'Ollama'
  if (model.provider === 'generic') {
    return t('modelSettings.source.custom')
  }
  return providerLabel(model) || sourceLabel(model._modelType)
}

const modelDisplayName = (model: any) => {
  const displayName = typeof model.displayName === 'string' ? model.displayName.trim() : ''
  return displayName || model.name
}

const contextWindowTitle = (tokens?: number) => {
  if (isDefaultContextWindow(tokens)) {
    return t('model.editor.contextWindowDefaultHint', { value: formatContextWindow(tokens) })
  }
  return t('model.editor.contextWindowTokens', { count: effectiveContextWindow(tokens) })
}

const emptyHint = computed(() => {
  if (activeTypeFilter.value === 'all') return t('modelSettings.chat.empty')
  const map: Record<ModelType, string> = {
    chat: t('modelSettings.chat.empty'),
    embedding: t('modelSettings.embedding.empty'),
    rerank: t('modelSettings.rerank.empty'),
    vllm: t('modelSettings.vllm.empty'),
    asr: t('modelSettings.asr.empty')
  }
  return map[activeTypeFilter.value as ModelType]
})

// 加载模型列表
let usageRequestID = 0
const loadUsage = async () => {
  const requestID = ++usageRequestID
  usageLoading.value = true
  usageError.value = false
  try {
    const usageStats = await getEvaluationModelUsage(selectedUsageRange())
    if (requestID !== usageRequestID) return
    modelUsageByID.value = Object.fromEntries(usageStats.map(stat => [stat.model_id, stat.usage]))
    modelUsageStatsByID.value = Object.fromEntries(usageStats.map(stat => [stat.model_id, stat]))
  } catch (error) {
    if (requestID !== usageRequestID) return
    console.warn('加载模型用量失败:', error)
    modelUsageByID.value = {}
    modelUsageStatsByID.value = {}
    usageError.value = true
  } finally {
    if (requestID === usageRequestID) usageLoading.value = false
  }
}

const loadModels = async () => {
  loading.value = true
  try {
    const models = await listModels()
    allModels.value = models
    // 设置页自己 listModels 之后立刻写回空间级缓存。否则对话输入栏 /
    // 智能体编辑器会继续拿 60s TTL 里的旧 context_window，刷新页面才对。
    chatResources.replaceModels(models)
  } catch (error: any) {
    console.error('加载模型列表失败:', error)
    MessagePlugin.error(error.message)
  } finally {
    loading.value = false
  }
}

watch(usageRange, () => {
  loadUsage()
})

// 打开添加对话框；类型在抽屉内选择，此处仅按当前 Tab 预填默认值
const openAddDialog = () => {
  currentModelType.value = activeTypeFilter.value === 'all' ? 'chat' : activeTypeFilter.value
  editingModel.value = null
  showDialog.value = true
}

// Tenant Admin+ manages tenant models; only SystemAdmin manages shared
// built-in models. The backend repeats this distinction authoritatively.
const canEditModel = (model: any) =>
  model.isBuiltin ? authStore.isSystemAdmin : authStore.hasRole('admin')

const isModelCardClickable = (model: any) => canEditModel(model)

const canManageModel = (model: any) => canEditModel(model)

// Built-in lifecycle remains deployment-managed (YAML / SQL). The UI only
// exposes configuration and credential editing to SystemAdmin.
const canDeleteModel = (model: any) =>
  authStore.hasRole('admin') && !model.isBuiltin

const onModelCardClick = (event: Event, type: ModelType, model: any) => {
  if (!isModelCardClickable(model)) return
  if (event.type === 'keydown') {
    const ke = event as KeyboardEvent
    if (ke.key !== 'Enter' && ke.key !== ' ') return
    ke.preventDefault()
  }
  const target = event.target as HTMLElement | null
  if (target?.closest('.model-card__actions')) return
  editModel(type, model)
}

// 编辑模型
const editModel = (type: ModelType, model: any) => {
  if (model.isBuiltin && !authStore.isSystemAdmin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotEdit'))
    return
  }
  if (!model.isBuiltin && !authStore.hasRole('admin')) {
    return
  }
  currentModelType.value = type
  editingModel.value = { ...model }
  showDialog.value = true
}

// 保存模型
const handleModelSave = async (modelData: any) => {
  const saveType: ModelType = modelData.modelType ?? currentModelType.value
  currentModelType.value = saveType

  try {
    if (!modelData.modelName || !modelData.modelName.trim()) {
      MessagePlugin.warning(t('modelSettings.toasts.nameRequired'))
      return
    }

    if (modelData.modelName.trim().length > 100) {
      MessagePlugin.warning(t('modelSettings.toasts.nameTooLong'))
      return
    }

    if (modelData.displayName && modelData.displayName.trim().length > 100) {
      MessagePlugin.warning(t('modelSettings.toasts.displayNameTooLong'))
      return
    }

    if (modelData.source === 'remote') {
      if (!modelData.baseUrl || !modelData.baseUrl.trim()) {
        MessagePlugin.warning(t('modelSettings.toasts.baseUrlRequired'))
        return
      }

      try {
        new URL(modelData.baseUrl.trim())
      } catch {
        MessagePlugin.warning(t('modelSettings.toasts.baseUrlInvalid'))
        return
      }
    }

    if (saveType === 'embedding') {
      if (!modelData.dimension || modelData.dimension < 128 || modelData.dimension > 4096) {
        MessagePlugin.warning(t('modelSettings.toasts.dimensionInvalid'))
        return
      }
    }

    const customHeadersMap: Record<string, string> = {}
    if (Array.isArray(modelData.customHeaders)) {
      for (const item of modelData.customHeaders) {
        const key = (item?.key ?? '').trim()
        const value = (item?.value ?? '').trim()
        if (key && value) {
          customHeadersMap[key] = value
        }
      }
    }

    // api_key flows in only on initial create (modelData.apiKey is wiped on
    // every edit-mode open). Edits to existing models commit credentials via
    // the /credentials subresource (handled inside ModelEditorDialog).
    const trimmedApiKey = (modelData.apiKey ?? '').trim()
    const apiKeyFields: { api_key?: string } =
      !editingModel.value && trimmedApiKey ? { api_key: trimmedApiKey } : {}
    const trimmedAppSecret = (modelData.appSecret ?? '').trim()
    const appSecretFields: { app_secret?: string } =
      !editingModel.value && trimmedAppSecret ? { app_secret: trimmedAppSecret } : {}
    // Updates replace ExtraConfig server-side, so begin with the persisted map
    // and overlay only fields owned by this form. This preserves provider
    // routing controls such as api_version and remote_model_name.
    const extraConfig: Record<string, string> = {
      ...(editingModel.value?.parameters?.extra_config || {}),
    }
    if (modelData.provider === 'lkeap' && saveType === 'rerank') {
      extraConfig.region = (modelData.lkeapRegion || 'ap-guangzhou').trim()
    }
    if (
      saveType === 'chat'
      && modelData.source === 'remote'
      && modelData.thinkingControl
    ) {
      extraConfig.thinking_control = modelData.thinkingControl
    }
    if (saveType === 'chat') {
      // Send the disabled state explicitly. Omitting ExtraConfig causes the
      // update handler to preserve the previous pricing map.
      extraConfig.pricing_enabled = String(modelData.pricingEnabled === true)
      if (modelData.pricingEnabled) {
        extraConfig.pricing_currency = String(modelData.pricingCurrency || 'USD').trim().toUpperCase()
        extraConfig.input_price_per_million = String(Math.max(0, Number(modelData.inputPricePerMillion) || 0))
        extraConfig.output_price_per_million = String(Math.max(0, Number(modelData.outputPricePerMillion) || 0))
        extraConfig.cache_read_price_per_million = String(Math.max(0, Number(modelData.cacheReadPricePerMillion) || 0))
        extraConfig.cache_write_price_per_million = String(Math.max(0, Number(modelData.cacheWritePricePerMillion) || 0))
      }
    }
    if (saveType === 'embedding') {
      extraConfig.embedding_cache_enabled = String(modelData.embeddingCacheEnabled !== false)
      extraConfig.embedding_cache_ttl_seconds = String(Math.max(1, Number(modelData.embeddingCacheTTLSeconds) || 86400))
      extraConfig.embedding_cache_max_entries = String(Math.max(1, Number(modelData.embeddingCacheMaxEntries) || 10000))
    }
    const extraConfigFields = Object.keys(extraConfig).length > 0
      ? { extra_config: extraConfig }
      : {}

    const apiModelData: ModelConfig = {
      name: modelData.modelName.trim(),
      display_name: modelData.displayName?.trim() || '',
      type: getModelType(saveType),
      source: modelData.source,
      description: '',
      parameters: {
        base_url: modelData.baseUrl?.trim() || '',
        ...apiKeyFields,
        ...appSecretFields,
        provider: modelData.provider || '',
        ...extraConfigFields,
        ...(Object.keys(customHeadersMap).length > 0 ? { custom_headers: customHeadersMap } : {}),
        ...(saveType === 'embedding' && modelData.dimension ? {
          embedding_parameters: {
            dimension: modelData.dimension,
            truncate_prompt_tokens: 0,
            supports_dimension_override: modelData.supportsDimensionOverride ?? false
          }
        } : {}),
        ...(saveType === 'vllm' ? {
          supports_vision: true
        } : saveType === 'chat' ? {
          supports_vision: modelData.supportsVision ?? false
        } : {}),
        ...((saveType === 'chat' || saveType === 'vllm')
          && Number(modelData.contextWindow) >= 1024
          ? { context_window: Math.round(Number(modelData.contextWindow)) }
          : {}),
        // 后台并发上限：仅 chat/embedding/vllm 受治理，>0 才写入（0/空沿用全局默认）。
        ...(['chat', 'embedding', 'vllm'].includes(saveType)
          && Number(modelData.maxConcurrency) > 0
          ? { max_concurrency: Number(modelData.maxConcurrency) }
          : {})
      }
    }

    if (editingModel.value && editingModel.value.id) {
      await updateModelAPI(editingModel.value.id, apiModelData)
      MessagePlugin.success(t('modelSettings.toasts.updated'))
    } else {
      await createModel(apiModelData)
      MessagePlugin.success(t('modelSettings.toasts.added'))
    }

    showDialog.value = false
    await loadModels()
  } catch (error: any) {
    console.error('保存模型失败:', error)
    MessagePlugin.error(error.message || t('modelSettings.toasts.saveFailed'))
  }
}

// 删除模型
const deleteModel = async (_type: ModelType, modelId: string) => {
  const model = allModels.value.find(m => m.id === modelId)
  if (model?.is_builtin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotDelete'))
    return
  }

  try {
    await deleteModelAPI(modelId)
    MessagePlugin.success(t('modelSettings.toasts.deleted'))
    await loadModels()
  } catch (error: any) {
    console.error('删除模型失败:', error)
    if (error instanceof ModelInUseError) {
      usageConflict.value = error.details
      usageConflictModelName.value = model?.display_name || model?.name || modelId
      showUsageDialog.value = true
      return
    }
    MessagePlugin.error(error.message || t('modelSettings.toasts.deleteFailed'))
  }
}

const openUsageResource = async (
  kind: ModelUsageResourceKind,
  id: string,
  bindings: readonly string[] = [],
) => {
  showUsageDialog.value = false
  const knowledgeBaseSection = modelUsageKnowledgeBaseSection(bindings)
  // The UI store can outlive the route component that owns the KB editor.
  // Only focus an existing editor when the current route still mounts one;
  // otherwise a stale visible/id pair would make a direct Settings page no-op.
  const routeName = router.currentRoute.value.name
  const canFocusExistingKnowledgeBase = kind === 'knowledge_base'
    && uiStore.showKBEditorModal
    && uiStore.currentKBId === id
    && typeof routeName === 'string'
    && KNOWLEDGE_BASE_EDITOR_HOST_ROUTES.has(routeName)

  uiStore.closeSettings()
  await nextTick()

  if (canFocusExistingKnowledgeBase) {
    // Keep unsaved edits when the referenced KB is already open underneath
    // global Settings; only move the existing editor to the relevant section.
    focusKbEditorSection(knowledgeBaseSection)
    return
  }

  if (kind === 'knowledge_base') {
    uiStore.closeKBEditor()
    await nextTick()
  }
  // Both the KB editor and global Settings can already be open underneath the
  // usage dialog. Flush their false state before reusing either component so
  // its visible watcher reloads the target object instead of keeping stale data.
  await router.push(modelUsageResourceRoute(kind, id, bindings))
  if (kind === 'knowledge_base') {
    uiStore.openKBSettings(id, knowledgeBaseSection)
  }
}

// 获取模型操作菜单选项
const getModelOptions = (type: ModelType, model: any) => {
  const options: any[] = []

  if (model.isBuiltin) {
    if (authStore.isSystemAdmin) {
      options.push({
        content: t('common.edit'),
        value: `edit-${type}-${model.id}`
      })
    }
    return options
  }

  // Models are tenant-wide infrastructure (LLM credentials); the
  // backend gates every mutation behind Admin+ (see RegisterModelRoutes).
  // Non-Admins get an empty action menu — viewing is fine, but editing,
  // copying (also goes through createModel), and deleting are not.
  if (!authStore.hasRole('admin')) {
    return options
  }

  options.push({
    content: t('common.edit'),
    value: `edit-${type}-${model.id}`
  })

  options.push({
    content: t('common.copy'),
    value: `copy-${type}-${model.id}`
  })

  return options
}

// 处理菜单操作
const handleMenuAction = (data: { value: string }, type: ModelType, model: any) => {
  const value = data.value

  if (value.indexOf('edit-') === 0) {
    editModel(type, model)
  } else if (value.indexOf('copy-') === 0) {
    copyModel(type, model.id)
  }
}

// 生成不重复的复制名称
const generateCopyName = (originalName: string): string => {
  const suffix = t('modelSettings.copySuffix')
  const existingNames = new Set(allModels.value.map(m => m.name))
  let candidate = `${originalName}${suffix}`
  let counter = 2
  while (existingNames.has(candidate)) {
    candidate = `${originalName}${suffix} ${counter}`
    counter += 1
  }
  return candidate
}

// 复制模型
const copyModel = async (_type: ModelType, modelId: string) => {
  const source = allModels.value.find(m => m.id === modelId)
  if (!source) {
    return
  }
  if (source.is_builtin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotCopy'))
    return
  }

  try {
    const newModel: ModelConfig = {
      name: generateCopyName(source.name),
      display_name: source.display_name || '',
      type: source.type,
      source: source.source,
      description: source.description || '',
      parameters: JSON.parse(JSON.stringify(source.parameters || {}))
    }

    await createModel(newModel)
    MessagePlugin.success(t('modelSettings.toasts.copied'))
    await loadModels()
  } catch (error: any) {
    console.error('复制模型失败:', error)
    MessagePlugin.error(error.message || t('modelSettings.toasts.copyFailed'))
  }
}

// 获取后端模型类型
function getModelType(type: ModelType): 'KnowledgeQA' | 'Embedding' | 'Rerank' | 'VLLM' | 'ASR' {
  const typeMap = {
    chat: 'KnowledgeQA' as const,
    embedding: 'Embedding' as const,
    rerank: 'Rerank' as const,
    vllm: 'VLLM' as const,
    asr: 'ASR' as const
  }
  return typeMap[type]
}

onMounted(() => {
  loadModels()
  loadUsage()
})
</script>

<style lang="less" scoped>
.model-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 28px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.6;
  }
}

.section-header__top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
}

.model-test-trigger {
  --td-bg-color-container-hover: transparent;
  flex-shrink: 0;
  padding-left: 0;
  padding-right: 0;
  font-weight: 600;

  &:hover,
  &:focus,
  &.t-is-active,
  &:active {
    background-color: transparent !important;
    color: var(--td-brand-color-hover);
  }

  &:active {
    color: var(--td-brand-color-active);
  }
}

.builtin-models-hint {
  margin-top: 12px;
  padding: 10px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
}

.builtin-hint-label {
  margin: 0 0 4px 0;
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-placeholder);
  letter-spacing: 0.02em;
}

.builtin-hint-text {
  margin: 0 0 6px 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.builtin-models-hint .doc-link {
  font-size: 13px;
}

.model-list-loading {
  min-height: 120px;
}

.model-type-tabs {
  margin-bottom: 16px;

  :deep(.t-tabs__nav-item) {
    font-size: 13px;
  }

  :deep(.t-tabs__nav-item-wrapper) {
    padding: 0 12px;
    margin: 0;
  }

  :deep(.t-tabs__operations) {
    display: none;
  }

  :deep(.t-tabs__nav-scroll) {
    overflow-x: auto;
    scrollbar-width: none;

    &::-webkit-scrollbar {
      display: none;
    }
  }

  :deep(.t-tabs__content) {
    display: none;
  }
}

.usage-range-filter {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin: 8px 0 16px;
  color: var(--td-text-color-secondary);
  font-size: 13px;

  :deep(.t-select__wrap) {
    width: 160px;
  }
}

.model-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;

  .model-card--add {
    width: 100%;
    height: 100%;
  }
}

// 模型卡片 —— 可选类型徽章（仅「全部」Tab）+ 标题 + 一行副标题
.model-card {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  transition: border-color 0.18s ease, box-shadow 0.18s ease, transform 0.18s ease;
  min-width: 0;

  &:hover {
    border-color: var(--td-brand-color-3, var(--td-brand-color));
    box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
  }

  &--add {
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 68px;
    border-style: dashed;
    background: transparent;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    font: inherit;
    text-align: center;

    &:hover,
    &:focus-visible {
      color: var(--td-brand-color);
      border-color: var(--td-brand-color);
      background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
      box-shadow: none;
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }

    &__icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      border-radius: 8px;
      background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
      color: var(--td-brand-color);
      font-size: 18px;
    }

    &__label {
      font-size: 13px;
      font-weight: 500;
      line-height: 1.4;
    }
  }

  &--builtin {
    background: var(--td-bg-color-secondarycontainer);

    &:hover {
      box-shadow: none;
      border-color: var(--td-component-stroke);
    }
  }

  &--clickable {
    cursor: pointer;

    &:hover {
      border-color: var(--td-brand-color-3, var(--td-brand-color));
      box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }
  }
}

.model-card__badge {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: 1px;
  // 默认底色，被 type 修饰覆盖
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}

// 5 种类型的徽章配色 —— 比原 tag 配色饱和度低一档，避免炫光
.model-card--chat .model-card__badge {
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}

.model-card--embedding .model-card__badge {
  background: rgba(98, 53, 187, 0.1);
  color: #6235BB;
}

.model-card--rerank .model-card__badge {
  background: rgba(184, 92, 0, 0.1);
  color: #B85C00;
}

.model-card--vllm .model-card__badge {
  background: rgba(201, 62, 62, 0.1);
  color: #C93E3E;
}

.model-card--asr .model-card__badge {
  background: rgba(17, 128, 83, 0.1);
  color: #118053;
}

.model-card__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 2px;
}

.model-card__header {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.model-card__title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/*
  Built-in lock indicator. Most cards in a typical install ARE built-in,
  so loud styling everywhere becomes noise — instead the lock is muted
  and small by default, and lights up on hover. The signal that matters
  to users is "which models did I add" → user-added cards stand out by
  the absence of the lock.
*/
.model-card__lock {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  color: var(--td-text-color-placeholder);
  opacity: 0.6;
  transition: color 0.15s ease, opacity 0.15s ease;

  .t-icon {
    font-size: 13px;
  }
}

.model-card:hover .model-card__lock {
  opacity: 1;
  color: var(--td-text-color-secondary);
}

.model-card__subtitle {
  margin: 2px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-card__usage {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 6px;
  margin-top: 6px;
}

.model-card__usage-item {
  display: inline-flex;
  align-items: center;
  min-height: 20px;
  padding: 1px 6px;
  border-radius: 5px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 18px;
  white-space: nowrap;

  &--cost {
    color: var(--td-success-color);
    background: color-mix(in srgb, var(--td-success-color) 9%, transparent);
  }

  &--muted {
    color: var(--td-text-color-placeholder);
  }

  &--cache {
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
  }

  &--wiki {
    color: #7a4d00;
    background: rgba(184, 92, 0, 0.09);
  }
}

.model-card__usage-toggle {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  min-height: 20px;
  padding: 1px 4px;
  border: 0;
  background: transparent;
  color: var(--td-brand-color);
  font: inherit;
  font-size: 11px;
  line-height: 18px;
  cursor: pointer;

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 1px;
    border-radius: 4px;
  }
}

.model-card__purpose-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 6px;
  padding: 9px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 7px;
  background: color-mix(in srgb, var(--td-bg-color-secondarycontainer) 72%, transparent);
}

.model-card__purpose-row {
  min-width: 0;
}

.model-card__wiki-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 10px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-secondary);
  font-size: 10px;

  strong {
    width: 100%;
    color: var(--td-text-color-primary);
    font-size: 11px;
  }
}

.model-card__purpose-heading,
.model-card__purpose-metrics {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.model-card__purpose-heading {
  color: var(--td-text-color-primary);
  font-size: 11px;
  font-weight: 500;

  span:first-child {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  span:last-child {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);
    font-weight: 400;
  }
}

.model-card__purpose-bar {
  height: 4px;
  margin: 4px 0;
  overflow: hidden;
  border-radius: 999px;
  background: var(--td-bg-color-component);

  span {
    display: block;
    height: 100%;
    border-radius: inherit;
    background: var(--td-brand-color);
  }
}

.model-card__purpose-metrics {
  flex-wrap: wrap;
  justify-content: flex-start;
  color: var(--td-text-color-secondary);
  font-size: 10px;

  span + span::before {
    margin-right: 8px;
    color: var(--td-text-color-placeholder);
    content: '·';
  }
}

.model-card__sep {
  margin: 0 4px;
  color: var(--td-text-color-placeholder);
}

.model-card__vision {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.model-card__ctx {
  font-variant-numeric: tabular-nums;
}

.model-card__ctx--default {
  color: var(--td-text-color-placeholder);
}

.model-card__actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 2px;
}

.model-card__action-btn {
  flex-shrink: 0;
  padding: 2px;
  opacity: 0;
  transition: opacity 0.15s ease;
}

.model-card__more {
  color: var(--td-text-color-placeholder);

  &:hover,
  &:focus-visible {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

// Hover / 键盘焦点 时显示操作按钮，避免静态卡片上有"杂物"。
.model-card:hover .model-card__action-btn,
.model-card:focus-within .model-card__action-btn,
.model-card__actions:focus-within .model-card__action-btn {
  opacity: 1;
}

.empty-state {
  padding: 64px 0;
  text-align: center;

  :deep(.t-empty__description) {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    margin-bottom: 16px;
  }
}

.model-usage-dialog__description {
  margin: 0 0 16px;
  color: var(--td-text-color-secondary);
  line-height: 1.6;
}

.model-usage-dialog__content {
  max-height: min(58vh, 560px);
  overflow-y: auto;
  padding-right: 4px;
}

.model-usage-group {
  & + & {
    margin-top: 20px;
  }

  h3 {
    margin: 0 0 8px;
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
    border: 1px solid var(--td-component-stroke);
    border-radius: 8px;
    overflow: hidden;
  }

  li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 12px;

    & + li {
      border-top: 1px solid var(--td-component-stroke);
    }
  }
}

.model-usage-truncated {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}

.model-usage-resource {
  min-width: 0;

  strong {
    display: block;
    margin-bottom: 6px;
    overflow: hidden;
    color: var(--td-text-color-primary);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.model-usage-bindings {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.model-usage-memory {
  padding: 10px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
}

.model-usage-dialog__actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 20px;
}
.usage-load-state {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 10px;
  color: var(--td-text-color-secondary);
  font-size: 12px;

  button {
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--td-brand-color);
    font: inherit;
    cursor: pointer;
  }
}

@media (max-width: 640px) {
  .section-header__top {
    align-items: flex-start;
    flex-direction: column;
    gap: 8px;
  }

  .usage-range-filter,
  .usage-load-state {
    justify-content: flex-start;
  }

  .usage-range-filter :deep(.t-select__wrap) {
    width: min(100%, 220px);
  }

  .model-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .model-card {
    padding: 12px;
  }
}
</style>
