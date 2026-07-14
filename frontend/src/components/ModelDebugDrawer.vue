<template>
  <SettingDrawer
    v-model:visible="drawerVisible"
    :title="$t('modelSettings.debug.title')"
    :description="$t('modelSettings.debug.description')"
    icon="play-circle-stroke"
    width="560px"
    :min-width="480"
    :max-width="900"
    storage-key="setting-drawer:width:model-debug"
    :confirm-text="$t('modelSettings.debug.run')"
    :confirm-loading="running"
    :confirm-disabled="!canRun"
    :cancel-text="$t('common.close')"
    @confirm="runDebug"
  >
    <template v-if="result" #footer-left>
      <t-button variant="outline" @click="copyResult">
        <template #icon><t-icon name="file-copy" /></template>
        {{ $t('modelSettings.debug.copyResult') }}
      </t-button>
    </template>

    <div class="model-debug">
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('modelSettings.debug.groupModel') }}</h4>
        <div v-if="availableModelTypes.length > 1" class="form-item">
          <div class="model-type-options" role="radiogroup" :aria-label="$t('modelSettings.debug.modelType')">
            <button
              v-for="option in availableModelTypes"
              :key="option.value"
              type="button"
              class="model-type-option"
              :class="{ 'is-active': selectedModelType === option.value }"
              role="radio"
              :aria-checked="selectedModelType === option.value"
              @click="selectModelType(option.value)"
            >
              <t-icon :name="option.icon" class="model-type-option__icon" />
              <span class="model-type-option__label">{{ option.label }}</span>
            </button>
          </div>
        </div>
        <div class="form-item">
          <label class="form-label">{{ $t('modelSettings.debug.model') }}</label>
          <t-select
            v-model="selectedModelId"
            filterable
            :placeholder="$t('modelSettings.debug.modelPlaceholder')"
            :disabled="filteredModels.length === 0"
            @change="resetResult"
          >
            <t-option
              v-for="model in filteredModels"
              :key="model.id"
              :value="model.id!"
              :label="modelLabel(model)"
            >
              <div class="model-option">
                <span class="model-option__name">{{ modelLabel(model) }}</span>
                <span class="model-option__meta">{{ vendorLabel(model) }}</span>
              </div>
            </t-option>
          </t-select>
          <p v-if="filteredModels.length === 0" class="form-desc">
            {{ $t('modelSettings.debug.noModelsForType') }}
          </p>
        </div>
      </section>

      <template v-if="selectedModel">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('modelSettings.debug.groupInput') }}</h4>
          <div v-if="selectedModel.type !== 'ASR'" class="form-item">
            <label class="form-label">{{ inputLabel }}</label>
            <t-textarea
              v-model="input"
              :placeholder="inputPlaceholder"
              :autosize="{ minRows: 4, maxRows: 8 }"
            />
          </div>

          <div v-if="selectedModel.type === 'Rerank'" class="form-item">
            <label class="form-label">{{ $t('modelSettings.debug.documents') }}</label>
            <t-textarea
              v-model="documentsText"
              :placeholder="$t('modelSettings.debug.documentsPlaceholder')"
              :autosize="{ minRows: 4, maxRows: 8 }"
            />
            <p class="form-desc">{{ $t('modelSettings.debug.documentsHint') }}</p>
          </div>

          <div v-if="needsFile" class="form-item">
            <label class="form-label">{{ fileLabel }}</label>
            <div class="file-picker">
              <input
                ref="fileInputRef"
                class="file-picker__input"
                type="file"
                :accept="selectedModel.type === 'VLLM' ? 'image/*' : 'audio/*'"
                @change="onNativeFileChange"
              >
              <t-button variant="outline" size="small" @click="fileInputRef?.click()">
                <template #icon><t-icon name="upload" /></template>
                {{ $t('modelSettings.debug.chooseFile') }}
              </t-button>
            </div>
            <p v-if="file" class="form-desc">{{ file.name }} · {{ formatBytes(file.size) }}</p>
          </div>
        </section>

        <section v-if="isChat" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('modelSettings.debug.parameters') }}</h4>
          <div class="parameter-grid">
            <div class="form-item">
              <label class="form-label">Temperature</label>
              <t-input-number v-model="temperature" :min="0" :max="2" :step="0.1" theme="column" />
            </div>
            <div class="form-item">
              <label class="form-label">Top P</label>
              <t-input-number v-model="topP" :min="0.01" :max="1" :step="0.1" theme="column" />
            </div>
            <div class="form-item">
              <label class="form-label">Max Tokens</label>
              <t-input-number v-model="maxTokens" :min="1" :max="8192" :step="128" theme="column" />
            </div>
          </div>
          <div class="form-item">
            <label class="form-label">{{ $t('modelSettings.debug.systemPrompt') }}</label>
            <t-textarea
              v-model="systemPrompt"
              :placeholder="$t('modelSettings.debug.systemPromptPlaceholder')"
              :autosize="{ minRows: 2, maxRows: 4 }"
            />
          </div>
          <div v-if="supportsThinking" class="form-item">
            <label class="form-label">{{ $t('modelSettings.debug.thinking') }}</label>
            <div class="switch-field">
              <t-switch v-model="thinking" />
              <span class="form-desc form-desc--inline">{{ $t('modelSettings.debug.thinkingDesc') }}</span>
            </div>
          </div>
        </section>

        <section v-if="result || history.length > 0" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('modelSettings.debug.groupResult') }}</h4>

          <div v-if="history.length > 1" class="form-item">
            <label class="form-label">{{ $t('modelSettings.debug.history') }}</label>
            <div class="history-list">
              <button
                v-for="run in history"
                :key="run.id"
                type="button"
                class="history-item"
                :class="{ 'history-item--active': result === run.result }"
                @click="result = run.result"
              >
                <span class="history-item__label">{{ run.label }}</span>
                <span class="history-item__meta">{{ run.result.elapsed_ms }} ms</span>
              </button>
            </div>
          </div>

          <div v-if="result" class="debug-result">
            <div class="result-banner" :class="result.ok ? 'result-banner--ok' : 'result-banner--error'">
              <t-icon :name="result.ok ? 'check-circle-filled' : 'close-circle-filled'" />
              <div class="result-banner__text">
                <strong>{{ result.ok ? $t('modelSettings.debug.success') : $t('modelSettings.debug.failed') }}</strong>
                <span>{{ result.elapsed_ms }} ms</span>
              </div>
            </div>

            <div v-if="resultMetrics.length > 0" class="metric-list">
              <span v-for="metric in resultMetrics" :key="metric.key" class="metric-chip">
                {{ metric.label }}: {{ metric.value }}
              </span>
            </div>

            <p v-if="result.error" class="result-error">{{ result.error }}</p>

            <t-tabs v-model="resultTab" class="result-tabs">
              <t-tab-panel value="response" :label="$t('modelSettings.debug.rawResponse')" />
              <t-tab-panel value="request" :label="$t('modelSettings.debug.requestPreview')" />
            </t-tabs>
            <pre class="json-output">{{ formattedResult }}</pre>
          </div>
        </section>
      </template>
    </div>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { debugModel, type ModelConfig, type ModelDebugResult } from '@/api/model'
import { modelSupportsThinking } from '@/utils/thinkingControl'

const props = defineProps<{
  visible: boolean
  models: ModelConfig[]
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
}>()

const { t, te } = useI18n()
const drawerVisible = computed({
  get: () => props.visible,
  set: value => emit('update:visible', value),
})

type DebugModelType = ModelConfig['type']

const selectedModelType = ref<DebugModelType>('KnowledgeQA')
const selectedModelId = ref('')
const input = ref('')
const documentsText = ref('')
const file = ref<File | null>(null)
const fileInputRef = ref<HTMLInputElement | null>(null)
const thinking = ref(false)
const temperature = ref(0.7)
const topP = ref(1)
const maxTokens = ref(1024)
const systemPrompt = ref('')
const running = ref(false)
const result = ref<ModelDebugResult | null>(null)
const resultTab = ref<'response' | 'request'>('response')
const history = ref<Array<{
  id: number
  label: string
  result: ModelDebugResult
}>>([])
let runSequence = 0

const selectedModel = computed(() => props.models.find(model => model.id === selectedModelId.value))
const filteredModels = computed(() => props.models.filter(model => model.type === selectedModelType.value))
const isChat = computed(() => selectedModel.value?.type === 'KnowledgeQA')
const supportsThinking = computed(() => selectedModel.value ? modelSupportsThinking(selectedModel.value) : false)
const needsFile = computed(() => ['VLLM', 'ASR'].includes(selectedModel.value?.type || ''))
const documents = computed(() => documentsText.value.split('\n').map(item => item.trim()).filter(Boolean))
const canRun = computed(() => {
  if (!selectedModel.value) return false
  if (needsFile.value && !file.value) return false
  if (selectedModel.value.type === 'ASR') return true
  if (selectedModel.value.type === 'Rerank') return !!input.value.trim() && documents.value.length > 0
  return !!input.value.trim()
})

const allModelTypeOptions = computed(() => {
  const keys: Record<DebugModelType, { short: string; icon: string }> = {
    KnowledgeQA: { short: 'chat', icon: 'chat' },
    Embedding: { short: 'embedding', icon: 'chart-bubble' },
    Rerank: { short: 'rerank', icon: 'filter-sort' },
    VLLM: { short: 'vllm', icon: 'image' },
    ASR: { short: 'asr', icon: 'sound' },
  }
  return (Object.keys(keys) as DebugModelType[]).map(value => ({
    value,
    label: t(`modelSettings.typeShort.${keys[value].short}`),
    icon: keys[value].icon,
  }))
})

const modelCount = (type: DebugModelType) => props.models.filter(model => model.type === type).length

const availableModelTypes = computed(() =>
  allModelTypeOptions.value.filter(option => modelCount(option.value) > 0),
)

const modelLabel = (model: ModelConfig) => model.display_name?.trim() || model.name

const vendorLabel = (model: ModelConfig) => {
  const provider = model.parameters.provider || ''
  if (model.source === 'local') return 'Ollama'
  if (provider === 'generic') return t('modelSettings.source.custom')
  const key = `model.editor.providers.${provider}.label`
  return te(key) ? t(key) : provider || model.source
}

const inputLabel = computed(() => {
  if (selectedModel.value?.type === 'Embedding') return t('modelSettings.debug.embeddingInput')
  if (selectedModel.value?.type === 'VLLM') return t('modelSettings.debug.vlmPrompt')
  if (selectedModel.value?.type === 'Rerank') return t('modelSettings.debug.query')
  return t('modelSettings.debug.query')
})

const inputPlaceholder = computed(() => {
  if (selectedModel.value?.type === 'Embedding') return t('modelSettings.debug.embeddingPlaceholder')
  if (selectedModel.value?.type === 'VLLM') return t('modelSettings.debug.vlmPromptPlaceholder')
  return t('modelSettings.debug.queryPlaceholder')
})

const fileLabel = computed(() =>
  selectedModel.value?.type === 'VLLM'
    ? t('modelSettings.debug.imageFile')
    : t('modelSettings.debug.audioFile'),
)

const formattedResult = computed(() => {
  if (!result.value) return ''
  const value = resultTab.value === 'response'
    ? result.value.raw_response
    : result.value.request
  return JSON.stringify(value, null, 2)
})

const OBSERVATION_LABELS: Record<string, string> = {
  dimension: 'modelSettings.debug.metrics.dimension',
  result_count: 'modelSettings.debug.metrics.resultCount',
  answer_characters: 'modelSettings.debug.metrics.answerChars',
  reasoning_characters: 'modelSettings.debug.metrics.reasoningChars',
  reasoning_returned: 'modelSettings.debug.metrics.reasoningReturned',
  text_characters: 'modelSettings.debug.metrics.textChars',
  segment_count: 'modelSettings.debug.metrics.segmentCount',
}

const resultMetrics = computed(() => {
  if (!result.value?.observations) return []
  const obs = result.value.observations
  const keys = Object.keys(OBSERVATION_LABELS).filter(key => obs[key] !== undefined && obs[key] !== null)
  return keys.map(key => ({
    key,
    label: t(OBSERVATION_LABELS[key]),
    value: formatMetricValue(key, obs[key]),
  }))
})

const formatMetricValue = (key: string, value: unknown) => {
  if (typeof value === 'boolean') {
    return value ? t('common.yes') : t('common.no')
  }
  return String(value)
}

const ensureDefaultSelection = () => {
  const types = availableModelTypes.value
  if (types.length === 0) {
    selectedModelId.value = ''
    return
  }
  if (!types.some(option => option.value === selectedModelType.value)) {
    selectedModelType.value = types[0].value
  }
  const models = filteredModels.value
  if (!models.some(model => model.id === selectedModelId.value)) {
    selectedModelId.value = models[0]?.id || ''
  }
}

watch(() => props.visible, visible => {
  if (visible) ensureDefaultSelection()
})

watch(availableModelTypes, () => {
  if (props.visible) ensureDefaultSelection()
})

watch(() => selectedModel.value?.id, () => {
  if (!supportsThinking.value) thinking.value = false
})

watch(() => selectedModel.value?.type, () => {
  file.value = null
  result.value = null
  history.value = []
  resultTab.value = 'response'
})

const resetResult = () => {
  result.value = null
  history.value = []
  resultTab.value = 'response'
}

const selectModelType = (type: DebugModelType) => {
  if (selectedModelType.value === type) return
  selectedModelType.value = type
  selectedModelId.value = filteredModels.value[0]?.id || ''
  input.value = ''
  documentsText.value = ''
  file.value = null
  resetResult()
}

const onNativeFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement
  file.value = target.files?.[0] || null
  resetResult()
}

const formatBytes = (bytes: number) => {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

const historyLabel = (thinkingValue: boolean) => {
  if (supportsThinking.value) {
    return thinkingValue ? t('modelSettings.debug.thinkOn') : t('modelSettings.debug.thinkOff')
  }
  return t('modelSettings.debug.runLabel', { n: runSequence })
}

const runDebug = async () => {
  if (!selectedModel.value?.id || !canRun.value || running.value) return
  running.value = true
  try {
    const thinkingValue = supportsThinking.value ? thinking.value : false
    const nextResult = await debugModel(selectedModel.value.id, {
      input: input.value.trim(),
      documents: documents.value,
      file: file.value,
      options: isChat.value ? {
        system_prompt: systemPrompt.value.trim() || undefined,
        temperature: temperature.value,
        top_p: topP.value,
        max_tokens: maxTokens.value,
        thinking: thinkingValue,
      } : {},
    })
    result.value = nextResult
    history.value.unshift({
      id: ++runSequence,
      label: historyLabel(thinkingValue),
      result: nextResult,
    })
    history.value = history.value.slice(0, 6)
    resultTab.value = 'response'
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('modelSettings.debug.requestFailed'))
  } finally {
    running.value = false
  }
}

const copyResult = async () => {
  if (!result.value) return
  try {
    await navigator.clipboard.writeText(JSON.stringify(result.value, null, 2))
    MessagePlugin.success(t('common.copied'))
  } catch {
    MessagePlugin.error(t('common.copyFailed'))
  }
}
</script>

<style scoped lang="less">
.form-item {
  margin-bottom: 0;
}

.form-label {
  display: block;
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  line-height: 1.4;
}

.form-desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);

  &--inline {
    margin: 0;
  }
}

.model-type-options {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.model-type-option {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  min-height: 32px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.4;
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease, background 0.15s ease;

  &__icon {
    font-size: 15px;
    flex-shrink: 0;
  }

  &__label {
    white-space: nowrap;
  }

  &:hover:not(.is-active) {
    border-color: var(--td-brand-color-3, var(--td-brand-color));
    color: var(--td-text-color-primary);
  }

  &.is-active {
    border-color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
    color: var(--td-brand-color);
    font-weight: 500;
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 2px;
  }
}

.model-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-width: 0;

  &__name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__meta {
    flex-shrink: 0;
    color: var(--td-text-color-placeholder);
    font-size: 12px;
  }
}

.parameter-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;

  :deep(.t-input-number) {
    width: 100%;
  }
}

.switch-field {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 32px;
}

.file-picker {
  position: relative;

  &__input {
    position: absolute;
    width: 0;
    height: 0;
    opacity: 0;
    pointer-events: none;
  }
}

.history-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.history-item {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 5px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 12px;

  &__label {
    color: var(--td-text-color-primary);
    font-weight: 500;
  }

  &__meta {
    color: var(--td-text-color-placeholder);
  }

  &:hover,
  &--active {
    border-color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 6%, var(--td-bg-color-container));
  }
}

.result-banner {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 8px;
  font-size: 13px;

  &--ok {
    background: var(--td-success-color-light);
    color: var(--td-success-color);

    .result-banner__text strong {
      color: var(--td-text-color-primary);
    }
  }

  &--error {
    background: var(--td-error-color-light);
    color: var(--td-error-color);

    .result-banner__text strong {
      color: var(--td-text-color-primary);
    }
  }

  &__text {
    display: flex;
    align-items: baseline;
    gap: 8px;

    span {
      color: var(--td-text-color-placeholder);
      font-size: 12px;
    }
  }
}

.metric-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 10px;
}

.metric-chip {
  padding: 2px 8px;
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.result-error {
  margin: 10px 0 0;
  color: var(--td-error-color);
  font-size: 13px;
  white-space: pre-wrap;
}

.result-tabs {
  margin-top: 12px;

  :deep(.t-tabs__content) {
    display: none;
  }
}

.json-output {
  overflow: auto;
  max-height: 420px;
  min-height: 140px;
  margin: 8px 0 0;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  font: 12px/1.6 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  white-space: pre-wrap;
  word-break: break-word;
}

@media (max-width: 640px) {
  .parameter-grid {
    grid-template-columns: 1fr;
  }
}
</style>
