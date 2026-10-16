<template>
  <div class="retrieval-settings">
    <h2 class="settings-title">{{ t('retrievalSettings.title') }}</h2>
    <div class="settings-tabs" role="tablist" :aria-label="t('retrievalSettings.title')">
      <button v-for="(tab, index) in tabs" :id="`retrieval-tab-${tab.key}`" :key="tab.key"
        type="button" class="settings-tab" :class="{ 'is-active': activeTab === tab.key }"
        role="tab" :aria-selected="activeTab === tab.key" :aria-controls="`retrieval-panel-${tab.key}`"
        :tabindex="activeTab === tab.key ? 0 : -1" @click="activeTab = tab.key"
        @keydown.right.prevent="moveTab(index, 1, $event)" @keydown.left.prevent="moveTab(index, -1, $event)">
        {{ tab.label }}
      </button>
    </div>
    <div v-show="activeTab === 'recall'" id="retrieval-panel-recall" role="tabpanel" aria-labelledby="retrieval-tab-recall">
      <div class="settings-group">
        <!-- Embedding Top K -->
        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('retrievalSettings.embeddingTopKLabel') }}</span>
            <span class="value-display">{{ localConfig.embedding_top_k }}</span>
          </div>
          <t-slider
            v-model="localConfig.embedding_top_k"
            :min="1"
            :max="100"
            :step="1"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>

        <!-- Vector Threshold -->
        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('retrievalSettings.vectorThresholdLabel') }}</span>
            <span class="value-display">{{ localConfig.vector_threshold.toFixed(2) }}</span>
          </div>
          <t-slider
            v-model="localConfig.vector_threshold"
            :min="0"
            :max="1"
            :step="0.05"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>

        <!-- Keyword Threshold -->
        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('retrievalSettings.keywordThresholdLabel') }}</span>
            <span class="value-display">{{ localConfig.keyword_threshold.toFixed(2) }}</span>
          </div>
          <t-slider
            v-model="localConfig.keyword_threshold"
            :min="0"
            :max="1"
            :step="0.05"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>
      </div>
    </div>
    <div v-show="activeTab === 'rrf'" id="retrieval-panel-rrf" role="tabpanel" aria-labelledby="retrieval-tab-rrf">
      <div class="settings-group rrf-settings">
        <div v-for="field in rrfFields" :key="field.key" class="rrf-row">
          <div class="rrf-label">
            <label :for="field.key">{{ field.label }}</label>
            <t-tooltip :content="field.description" placement="top" :overlay-style="{ maxWidth: '280px' }">
              <button type="button" class="rrf-help" :aria-label="field.description">
                <t-icon name="help-circle" size="14px" />
              </button>
            </t-tooltip>
          </div>
          <t-input-number
            theme="column"
            :value="localConfig[field.key] ?? field.defaultValue"
            :min="0" :step="field.step" :decimal-places="field.key === 'rrf_k' ? 0 : undefined"
            :disabled="!canEdit" :input-props="{ id: field.key, 'aria-label': field.label }"
            @change="handleRrfChange(field.key, $event)"
          />
        </div>
      </div>
    </div>
    <div v-show="activeTab === 'rerank'" id="retrieval-panel-rerank" role="tabpanel" aria-labelledby="retrieval-tab-rerank">
      <div class="settings-group">
        <!-- Rerank Model -->
        <div class="setting-item">
          <div class="setting-label">
            <span>{{ t('retrievalSettings.rerankModelLabel') }} <span class="required-mark">*</span></span>
          </div>
          <p class="setting-desc">{{ t('retrievalSettings.rerankModelDescription') }}</p>
          <p v-if="!localConfig.rerank_model_id" class="setting-desc warning-text">
            {{ t('retrievalSettings.rerankModelRequired') }}
          </p>
          <div class="setting-control-full">
            <ModelSelector
              model-type="Rerank"
              :selected-model-id="localConfig.rerank_model_id"
              :disabled="!canEdit"
              @update:selected-model-id="handleModelChange"
            />
          </div>
        </div>

        <!-- Rerank Top K -->
        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('retrievalSettings.rerankTopKLabel') }}</span>
            <span class="value-display">{{ localConfig.rerank_top_k }}</span>
          </div>
          <t-slider
            v-model="localConfig.rerank_top_k"
            :min="1"
            :max="100"
            :step="1"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>

        <!-- Rerank Threshold -->
        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('retrievalSettings.rerankThresholdLabel') }}</span>
            <span class="value-display">{{ localConfig.rerank_threshold.toFixed(2) }}</span>
          </div>
          <t-slider
            v-model="localConfig.rerank_threshold"
            :min="-10"
            :max="10"
            :step="0.1"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, computed, onMounted, nextTick, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import ModelSelector from '@/components/ModelSelector.vue'
import {
  getTenantRetrievalConfig,
  updateTenantRetrievalConfig,
  type RetrievalConfig,
} from '@/api/retrieval'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const authStore = useAuthStore()
const activeTab = ref('recall')
const tabs = computed(() => [
  { key: 'recall', label: t('retrievalSettings.recallTab') },
  { key: 'rrf', label: t('retrievalSettings.rrfTitle') },
  { key: 'rerank', label: t('retrievalSettings.rerankTab') },
])
const moveTab = (index: number, direction: number, event: KeyboardEvent) => {
  const next = (index + direction + tabs.value.length) % tabs.value.length
  activeTab.value = tabs.value[next]!.key
  const buttons = (event.currentTarget as HTMLElement).parentElement?.querySelectorAll('button')
  buttons?.[next]?.focus()
}
// PUT /tenants/kv/retrieval-config requires Admin+ on the server. Hide the
// banner + lock all controls for non-Admins so they can read the
// configuration without tripping a 403 mid-edit.
const canEdit = computed(() => authStore.hasRole('admin'))

const defaultConfig: RetrievalConfig = {
  embedding_top_k: 50,
  vector_threshold: 0.15,
  keyword_threshold: 0.3,
  rerank_top_k: 10,
  rerank_threshold: 0.2,
  rerank_model_id: '',
}

type RrfField = 'rrf_k' | 'rrf_vector_weight' | 'rrf_keyword_weight'
const rrfFields = computed<{ key: RrfField; label: string; description: string; defaultValue: number; step: number }[]>(() => [
  { key: 'rrf_k', label: t('retrievalSettings.rrfKLabel'), description: t('retrievalSettings.rrfKDescription'), defaultValue: 60, step: 1 },
  { key: 'rrf_vector_weight', label: t('retrievalSettings.rrfVectorWeightLabel'), description: t('retrievalSettings.rrfVectorWeightDescription'), defaultValue: 0.7, step: 0.1 },
  { key: 'rrf_keyword_weight', label: t('retrievalSettings.rrfKeywordWeightLabel'), description: t('retrievalSettings.rrfKeywordWeightDescription'), defaultValue: 0.3, step: 0.1 },
])

const localConfig = reactive<RetrievalConfig>({ ...defaultConfig })
let initialConfig: RetrievalConfig = { ...defaultConfig }
let isInitializing = true

const loadConfig = async () => {
  try {
    const response = await getTenantRetrievalConfig()
    if (response.data) {
      const cfg = response.data
      Object.assign(localConfig, {
        embedding_top_k: cfg.embedding_top_k || defaultConfig.embedding_top_k,
        vector_threshold: cfg.vector_threshold || defaultConfig.vector_threshold,
        keyword_threshold: cfg.keyword_threshold || defaultConfig.keyword_threshold,
        rerank_top_k: cfg.rerank_top_k || defaultConfig.rerank_top_k,
        rerank_threshold: cfg.rerank_threshold ?? defaultConfig.rerank_threshold,
        rerank_model_id: cfg.rerank_model_id || '',
        rrf_k: cfg.rrf_k,
        rrf_vector_weight: cfg.rrf_vector_weight,
        rrf_keyword_weight: cfg.rrf_keyword_weight,
      })
      initialConfig = { ...localConfig }
    }
  } catch (error: any) {
    console.error('Failed to load retrieval config:', error)
  } finally {
    await nextTick()
    await nextTick()
    setTimeout(() => { isInitializing = false }, 100)
  }
}

const hasConfigChanged = (): boolean => {
  return JSON.stringify(localConfig) !== JSON.stringify(initialConfig)
}

const saveConfig = async () => {
  if (!hasConfigChanged()) return
  try {
    const response = await updateTenantRetrievalConfig({ ...localConfig })
    if (response.data) {
      initialConfig = { ...localConfig }
    }
    MessagePlugin.success(t('retrievalSettings.toasts.saveSuccess'))
  } catch (error: any) {
    console.error('Failed to save retrieval config:', error)
    const errorMessage = error?.message || 'Unknown error'
    MessagePlugin.error(t('retrievalSettings.toasts.saveFailed', { message: errorMessage }))
  }
}

let saveTimer: number | null = null
const debouncedSave = () => {
  if (isInitializing) return
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = window.setTimeout(() => {
    saveConfig().catch(() => {})
  }, 500)
}

const handleParamChange = () => debouncedSave()
const handleRrfChange = (field: RrfField, value: number | string) => {
  // Ignore cleared inputs; zero explicitly selects the server default.
  if (!canEdit.value || typeof value !== 'number' || !Number.isFinite(value) || value < 0) return
  if (field === 'rrf_k' && !Number.isSafeInteger(value)) return
  localConfig[field] = value
  debouncedSave()
}
const handleModelChange = (modelId: string) => {
  localConfig.rerank_model_id = modelId
  debouncedSave()
}

onMounted(async () => {
  isInitializing = true
  await loadConfig()
})
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/settings-section.less';

.retrieval-settings {
  width: 100%;
}

.settings-title {
  margin: 0 28px 12px 0;
  font-size: var(--app-text-lg);
  font-weight: 600;
  line-height: 24px;
  color: var(--td-text-color-primary);
}

.settings-tabs {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  margin-right: 24px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
}
.settings-tab {
  height: 24px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  font: inherit;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  &:hover { color: var(--td-text-color-primary); }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; }
  &.is-active {
    color: var(--td-text-color-primary);
    font-weight: 500;
    background: var(--td-bg-color-container);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
  margin-top: 4px;
  :deep(.t-input) { border-radius: var(--app-radius-md); }
}

.setting-item {
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.setting-label {
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);
  margin-bottom: 4px;
}

.setting-label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);
  margin-bottom: 10px;
}

.setting-desc {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  margin: 0 0 8px 0;
  line-height: 1.5;
}

.required-mark {
  color: var(--td-error-color);
}

.warning-text {
  color: var(--td-warning-color) !important;
}

.setting-control-full {
  width: 100%;
}

.value-display {
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);
  font-variant-numeric: tabular-nums;
}
.rrf-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);
  &:last-child { border-bottom: none; }
  :deep(.t-input-number) { width: 112px; flex-shrink: 0; }
  :deep(input) { font-variant-numeric: tabular-nums; }
}
.rrf-label { display: flex; align-items: center; gap: 4px; font-size: var(--app-text-md); font-weight: 500; }
.rrf-help { .settings-title-help(); }
</style>
