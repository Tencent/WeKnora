<template>
  <div class="kb-advanced-settings">
    <div class="section-header">
      <h2>{{ $t('knowledgeEditor.advanced.title') }}</h2>
      <p class="section-description">{{ $t('knowledgeEditor.advanced.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- Question Generation feature (only useful for RAG indexing) -->
      <template v-if="ragEnabled !== false">
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.advanced.questionGeneration.label') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.advanced.questionGeneration.description') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="localQuestionGeneration.enabled"
            @change="handleQuestionGenerationToggle"
            size="medium"
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
      </template>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'

interface QuestionGenerationConfig {
  enabled: boolean
  questionCount: number
}

interface Props {
  questionGeneration?: QuestionGenerationConfig
  ragEnabled?: boolean
  allModels?: any[]
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:questionGeneration': [value: QuestionGenerationConfig]
}>()

const localQuestionGeneration = ref<QuestionGenerationConfig>(
  props.questionGeneration || { enabled: false, questionCount: 3 }
)

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
  }

  .desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }

  .hint {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    margin: 6px 0 0 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex: 0 0 55%;
  max-width: 55%;
  display: flex;
  justify-content: flex-end;
  align-items: center;
}

.subsection {
  padding: 16px 20px;
  margin: 12px 0 0 0;
  background: var(--td-bg-color-container);
  border-radius: 8px;
  border-left: 3px solid var(--td-brand-color);
  position: relative;
}

.required {
  color: var(--td-error-color);
  margin-left: 2px;
  font-weight: 500;
}

</style>
