<template>
  <div class="retrieval-settings">
    <div class="section-header">
      <h2>{{ t('retrievalSettings.title') }}</h2>
      <p class="section-description">{{ t('retrievalSettings.description') }}</p>
    </div>

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

    <!-- Feedback-driven recall policy (#1248). -->
    <div class="settings-group">
      <div class="settings-group__header">
        <h3>{{ t('feedback.retrieval.sectionTitle') }}</h3>
        <p class="settings-group__desc">{{ t('feedback.retrieval.description') }}</p>
      </div>

      <div class="setting-item">
        <div class="setting-label-row">
          <span>{{ t('feedback.retrieval.enable') }}</span>
          <t-switch
            v-model="localConfig.feedback_ranking_enabled"
            :disabled="!canEdit"
            @change="handleParamChange"
            data-test="retrieval-feedback-toggle"
          />
        </div>
      </div>

      <div v-if="localConfig.feedback_ranking_enabled" class="feedback-policy__grid">
        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('feedback.retrieval.boostThreshold') }}</span>
            <span class="value-display">{{ Number(localConfig.feedback_boost_threshold ?? 0.8).toFixed(2) }}</span>
          </div>
          <t-slider
            v-model="localConfig.feedback_boost_threshold"
            :min="0"
            :max="1"
            :step="0.05"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>

        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('feedback.retrieval.penaltyThreshold') }}</span>
            <span class="value-display">{{ Number(localConfig.feedback_penalty_threshold ?? 0.5).toFixed(2) }}</span>
          </div>
          <t-slider
            v-model="localConfig.feedback_penalty_threshold"
            :min="0"
            :max="1"
            :step="0.05"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>

        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('feedback.retrieval.boostFactor') }}</span>
            <span class="value-display">{{ Number(localConfig.feedback_boost_factor ?? 1.2).toFixed(2) }}x</span>
          </div>
          <t-slider
            v-model="localConfig.feedback_boost_factor"
            :min="1"
            :max="2"
            :step="0.05"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>

        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('feedback.retrieval.penaltyFactor') }}</span>
            <span class="value-display">{{ Number(localConfig.feedback_penalty_factor ?? 0.8).toFixed(2) }}x</span>
          </div>
          <t-slider
            v-model="localConfig.feedback_penalty_factor"
            :min="0.25"
            :max="1"
            :step="0.05"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>

        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('feedback.retrieval.minSamples') }}</span>
            <span class="value-display">{{ localConfig.feedback_min_samples ?? 3 }}</span>
          </div>
          <t-slider
            v-model="localConfig.feedback_min_samples"
            :min="1"
            :max="20"
            :step="1"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>

        <div class="setting-item">
          <div class="setting-label-row">
            <span>{{ t('feedback.retrieval.needsOptimizationThreshold') }}</span>
            <span class="value-display">{{ Number(localConfig.feedback_needs_optimization_threshold ?? 0.2).toFixed(2) }}</span>
          </div>
          <t-slider
            v-model="localConfig.feedback_needs_optimization_threshold"
            :min="0"
            :max="1"
            :step="0.05"
            :disabled="!canEdit"
            @change="handleParamChange"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, computed, onMounted, nextTick } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import ModelSelector from '@/components/ModelSelector.vue'
import {
  getTenantRetrievalConfig,
  updateTenantRetrievalConfig,
  FEEDBACK_POLICY_DEFAULTS,
  type RetrievalConfig,
} from '@/api/retrieval'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const authStore = useAuthStore()
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
  // Feedback policy defaults (#1248). The values mirror the backend defaults
  // so a freshly created tenant that has never opened this tab sees sensible
  // values; the backend still wins the merge on save.
  feedback_ranking_enabled: FEEDBACK_POLICY_DEFAULTS.feedback_ranking_enabled,
  feedback_boost_threshold: FEEDBACK_POLICY_DEFAULTS.feedback_boost_threshold,
  feedback_penalty_threshold: FEEDBACK_POLICY_DEFAULTS.feedback_penalty_threshold,
  feedback_boost_factor: FEEDBACK_POLICY_DEFAULTS.feedback_boost_factor,
  feedback_penalty_factor: FEEDBACK_POLICY_DEFAULTS.feedback_penalty_factor,
  feedback_min_samples: FEEDBACK_POLICY_DEFAULTS.feedback_min_samples,
  feedback_needs_optimization_threshold: FEEDBACK_POLICY_DEFAULTS.feedback_needs_optimization_threshold,
}

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
        feedback_ranking_enabled: cfg.feedback_ranking_enabled ?? defaultConfig.feedback_ranking_enabled,
        feedback_boost_threshold: cfg.feedback_boost_threshold ?? defaultConfig.feedback_boost_threshold,
        feedback_penalty_threshold: cfg.feedback_penalty_threshold ?? defaultConfig.feedback_penalty_threshold,
        feedback_boost_factor: cfg.feedback_boost_factor ?? defaultConfig.feedback_boost_factor,
        feedback_penalty_factor: cfg.feedback_penalty_factor ?? defaultConfig.feedback_penalty_factor,
        feedback_min_samples: cfg.feedback_min_samples ?? defaultConfig.feedback_min_samples,
        feedback_needs_optimization_threshold: cfg.feedback_needs_optimization_threshold ?? defaultConfig.feedback_needs_optimization_threshold,
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
.retrieval-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 24px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 6px 0;
  }

  .section-description {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-item {
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.setting-label {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  margin-bottom: 4px;
}

.setting-label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  margin-bottom: 10px;
}

.setting-desc {
  font-size: 12px;
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
  font-size: 13px;
  font-weight: 600;
  color: var(--td-brand-color);
  font-family: var(--app-font-family-mono);
}

.settings-group__header {
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px dashed var(--td-component-stroke);
  h3 {
    margin: 0 0 4px 0;
    font-size: 16px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }
}
.settings-group__desc {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  margin: 0 0 8px 0;
}

.feedback-policy__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 24px;
  @media (max-width: 768px) {
    grid-template-columns: 1fr;
  }
}
</style>
