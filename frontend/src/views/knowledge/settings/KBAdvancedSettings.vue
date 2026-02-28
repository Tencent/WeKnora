<template>
  <div class="kb-advanced-settings">
    <div class="section-header">
      <h2>{{ $t('knowledgeEditor.advanced.title') }}</h2>
      <p class="section-description">{{ $t('knowledgeEditor.advanced.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- Question Generation feature -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.advanced.questionGeneration.label') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.advanced.questionGeneration.description') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="localQuestionGeneration.enabled"
            @change="handleQuestionGenerationToggle"
            size="large"
          />
        </div>
      </div>

      <!-- Question Generation configuration -->
      <div v-if="localQuestionGeneration.enabled" class="subsection">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.advanced.questionGeneration.countLabel') }}</label>
            <p class="desc">{{ $t('knowledgeEditor.advanced.questionGeneration.countDescription') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              v-model="localQuestionGeneration.questionCount"
              :min="1"
              :max="10"
              :step="1"
              theme="normal"
              @change="handleQuestionGenerationChange"
              style="width: 120px;"
            />
          </div>
        </div>
      </div>

      <!-- Multimodal feature：仅选择多模态模型，存储引擎在「存储引擎」页配置 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.advanced.multimodal.label') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.description') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="localMultimodal.enabled"
            @change="handleMultimodalToggle"
            size="large"
          />
        </div>
      </div>

      <!-- 多模态开启时仅配置 VLLM 模型 -->
      <div v-if="localMultimodal.enabled" class="subsection">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.advanced.multimodal.vllmLabel') }} <span class="required">*</span></label>
            <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.vllmDescription') }}</p>
          </div>
          <div class="setting-control">
            <ModelSelector
              ref="vllmSelectorRef"
              model-type="VLLM"
              :selected-model-id="localMultimodal.vllmModelId"
              :all-models="allModels"
              @update:selected-model-id="handleVLLMChange"
              @add-model="handleAddModel('vllm')"
              :placeholder="$t('knowledgeEditor.advanced.multimodal.vllmPlaceholder')"
            />
          </div>
        </div>
      </div>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import ModelSelector from '@/components/ModelSelector.vue'
import { useUIStore } from '@/stores/ui'

const uiStore = useUIStore()

interface MultimodalConfig {
  enabled: boolean
  vllmModelId?: string
}

interface QuestionGenerationConfig {
  enabled: boolean
  questionCount: number
}

interface Props {
  multimodal: MultimodalConfig
  questionGeneration?: QuestionGenerationConfig
  allModels?: any[]
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:multimodal': [value: MultimodalConfig]
  'update:questionGeneration': [value: QuestionGenerationConfig]
}>()

const localMultimodal = ref<MultimodalConfig>({ ...props.multimodal })
const localQuestionGeneration = ref<QuestionGenerationConfig>(
  props.questionGeneration || { enabled: false, questionCount: 3 }
)

const vllmSelectorRef = ref()

watch(() => props.multimodal, (newVal) => {
  localMultimodal.value = { ...newVal }
}, { deep: true })

watch(() => props.questionGeneration, (newVal) => {
  if (newVal) {
    localQuestionGeneration.value = { ...newVal }
  }
}, { deep: true })

const handleQuestionGenerationToggle = () => {
  if (!localQuestionGeneration.value.enabled) {
    localQuestionGeneration.value.questionCount = 3
  }
  emit('update:questionGeneration', localQuestionGeneration.value)
}

const handleQuestionGenerationChange = () => {
  emit('update:questionGeneration', localQuestionGeneration.value)
}

const handleMultimodalToggle = () => {
  if (!localMultimodal.value.enabled) {
    localMultimodal.value.vllmModelId = ''
  }
  emit('update:multimodal', localMultimodal.value)
}

const handleVLLMChange = (modelId: string) => {
  localMultimodal.value.vllmModelId = modelId
  emit('update:multimodal', localMultimodal.value)
}

const handleAddModel = (subSection: string) => {
  uiStore.openSettings('models', subSection)
}
</script>

<style lang="less" scoped>
.kb-advanced-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 32px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: #333333;
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: #666666;
    margin: 0;
    line-height: 1.5;
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 20px 0;
  border-bottom: 1px solid #e5e7eb;

  &:last-child {
    border-bottom: none;
  }
}

.setting-info {
  flex: 1;
  max-width: 65%;
  padding-right: 24px;

  label {
    font-size: 15px;
    font-weight: 500;
    color: #333333;
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: #666666;
    margin: 0;
    line-height: 1.5;
  }

  .hint {
    font-size: 12px;
    color: #999999;
    margin: 6px 0 0 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
}

.subsection {
  padding: 16px 20px;
  margin: 12px 0 0 0;
  background: #f8fafb;
  border-radius: 8px;
  border-left: 3px solid #07C05F;
  position: relative;
}

.required {
  color: #e34d59;
  margin-left: 2px;
  font-weight: 500;
}

</style>

