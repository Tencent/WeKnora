<template>
  <div class="kb-model-config">
    <div class="section-header">
      <h2>{{ $t('knowledgeEditor.models.title') }}</h2>
      <p class="section-description">{{ $t('knowledgeEditor.models.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- LLM 大语言模型 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.models.llmLabel') }} <span class="required">*</span></label>
          <p class="desc">{{ $t('knowledgeEditor.models.llmDesc') }}</p>
        </div>
        <div class="setting-control">
          <ModelSelector
            ref="llmSelectorRef"
            model-type="KnowledgeQA"
            :selected-model-id="config.llmModelId"
            :all-models="allModels"
            @update:selected-model-id="handleLLMChange"
            @add-model="handleAddModel('chat')"
            :placeholder="$t('knowledgeEditor.models.llmPlaceholder')"
          />
        </div>
      </div>

      <!-- Embedding 嵌入模型 (仅 RAG 检索启用时必填) -->
      <div v-if="ragEnabled !== false" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.models.embeddingLabel') }} <span v-if="ragEnabled" class="required">*</span></label>
          <p class="desc">{{ $t('knowledgeEditor.models.embeddingDesc') }}</p>
          <t-alert
            v-if="hasFiles"
            theme="warning"
            :message="$t('knowledgeEditor.models.embeddingLocked')"
            style="margin-top: 8px;"
          />
        </div>
        <div class="setting-control">
          <ModelSelector
            ref="embeddingSelectorRef"
            model-type="Embedding"
            :selected-model-id="config.embeddingModelId"
            :all-models="allModels"
            :disabled="hasFiles"
            @update:selected-model-id="handleEmbeddingChange"
            @add-model="handleAddModel('embedding')"
            :placeholder="$t('knowledgeEditor.models.embeddingPlaceholder')"
          />
        </div>
      </div>

      <!-- Wiki 合成模型 (仅当 Wiki 启用时显示) -->
      <div v-if="wikiEnabled" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.wiki.synthesisModelLabel') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.wiki.synthesisModelTip') }}</p>
        </div>
        <div class="setting-control">
          <ModelSelector
            model-type="KnowledgeQA"
            :selected-model-id="config.wikiSynthesisModelId"
            :all-models="allModels"
            @update:selected-model-id="handleWikiModelChange"
            @add-model="handleAddModel('knowledgeqa')"
            :placeholder="$t('knowledgeEditor.wiki.synthesisModelPlaceholder')"
          />
        </div>
      </div>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useUIStore } from '@/stores/ui'
import ModelSelector from '@/components/ModelSelector.vue'
import { useI18n } from 'vue-i18n'

interface ModelConfig {
  llmModelId?: string
  embeddingModelId?: string
  vllmModelId?: string
  wikiSynthesisModelId?: string
}

interface Props {
  config: ModelConfig
  hasFiles: boolean
  wikiEnabled?: boolean
  ragEnabled?: boolean
  allModels?: any[]
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:config': [value: ModelConfig]
}>()

const uiStore = useUIStore()
const { t } = useI18n()

const llmSelectorRef = ref<InstanceType<typeof ModelSelector>>()
const embeddingSelectorRef = ref<InstanceType<typeof ModelSelector>>()

const handleLLMChange = (modelId: string) => {
  emit('update:config', {
    ...props.config,
    llmModelId: modelId
  })
}

const handleEmbeddingChange = (modelId: string) => {
  emit('update:config', {
    ...props.config,
    embeddingModelId: modelId
  })
}

const handleWikiModelChange = (modelId: string) => {
  emit('update:config', {
    ...props.config,
    wikiSynthesisModelId: modelId
  })
}

const handleAddModel = (subSection: string) => {
  uiStore.openSettings('models', subSection)
}
</script>

<style lang="less" scoped>
.kb-model-config {
  width: 100%;
}

.section-header {
  margin-bottom: 32px;

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
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.setting-info {
  flex: 0 0 40%;
  max-width: 40%;
  padding-right: 24px;

  label {
    font-size: 15px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    display: block;
    margin-bottom: 4px;

    .required {
      color: var(--td-error-color);
      margin-left: 2px;
    }
  }

  .desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex: 0 0 55%;
  max-width: 55%;
  display: flex;
  justify-content: flex-end;
  align-items: flex-start;
}
</style>

