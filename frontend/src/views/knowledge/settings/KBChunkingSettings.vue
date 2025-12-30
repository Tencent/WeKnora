<template>
  <div class="kb-chunking-settings">
    <div class="section-header">
      <h2>{{ $t('knowledgeEditor.chunking.title') }}</h2>
      <p class="section-description">{{ $t('knowledgeEditor.chunking.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- Chunk Size -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.chunking.sizeLabel') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.chunking.sizeDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="slider-container">
            <t-slider
              v-model="localChunkSize"
              :min="100"
              :max="4000"
              :step="50"
              :marks="{ 100: '100', 1000: '1000', 2000: '2000', 4000: '4000' }"
              @change="handleChunkSizeChange"
              style="width: 200px;"
            />
            <span class="value-display">{{ localChunkSize }} {{ $t('knowledgeEditor.chunking.characters') }}</span>
          </div>
        </div>
      </div>

      <!-- Chunk Overlap -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.chunking.overlapLabel') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.chunking.overlapDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="slider-container">
            <t-slider
              v-model="localChunkOverlap"
              :min="0"
              :max="500"
              :step="20"
              :marks="{ 0: '0', 250: '250', 500: '500' }"
              @change="handleChunkOverlapChange"
              style="width: 200px;"
            />
            <span class="value-display">{{ localChunkOverlap }} {{ $t('knowledgeEditor.chunking.characters') }}</span>
          </div>
        </div>
      </div>

      <!-- Separators -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.chunking.separatorsLabel') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.chunking.separatorsDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localSeparators"
            :options="separatorOptions"
            multiple
            creatable
            filterable
            :placeholder="$t('knowledgeEditor.chunking.separatorsPlaceholder')"
            @change="handleSeparatorsChange"
            style="width: 280px;"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'

interface ChunkingConfig {
  chunkSize: number
  chunkOverlap: number
  separators: string[]
}

interface Props {
  config: ChunkingConfig
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:config': [value: ChunkingConfig]
}>()

const localChunkSize = ref(props.config.chunkSize)
const localChunkOverlap = ref(props.config.chunkOverlap)
const localSeparators = ref([...props.config.separators])
const { t } = useI18n()

// Separator options
const separatorOptions = computed(() => [
  { label: t('knowledgeEditor.chunking.separators.doubleNewline'), value: '\n\n' },
  { label: t('knowledgeEditor.chunking.separators.singleNewline'), value: '\n' },
  { label: t('knowledgeEditor.chunking.separators.periodCn'), value: '。' },
  { label: t('knowledgeEditor.chunking.separators.exclamationCn'), value: '！' },
  { label: t('knowledgeEditor.chunking.separators.questionCn'), value: '？' },
  { label: t('knowledgeEditor.chunking.separators.semicolonCn'), value: '；' },
  { label: t('knowledgeEditor.chunking.separators.semicolonEn'), value: ';' },
  { label: t('knowledgeEditor.chunking.separators.space'), value: ' ' }
])

// Watch for prop changes
watch(() => props.config, (newConfig) => {
  localChunkSize.value = newConfig.chunkSize
  localChunkOverlap.value = newConfig.chunkOverlap
  localSeparators.value = [...newConfig.separators]
}, { deep: true })

// Handle chunk size change
const handleChunkSizeChange = () => {
  emitUpdate()
}

// Handle chunk overlap change
const handleChunkOverlapChange = () => {
  emitUpdate()
}

// Handle separator change
const handleSeparatorsChange = () => {
  emitUpdate()
}

// Emit update event
const emitUpdate = () => {
  emit('update:config', {
    chunkSize: localChunkSize.value,
    chunkOverlap: localChunkOverlap.value,
    separators: localSeparators.value
  })
}
</script>

<style lang="less" scoped>
.kb-chunking-settings {
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
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
}

.slider-container {
  display: flex;
  align-items: center;
  gap: 16px;
  width: 100%;
  justify-content: flex-end;
}

.value-display {
  font-size: 14px;
  color: #333333;
  font-weight: 500;
  min-width: 80px;
  text-align: right;
}
</style>

