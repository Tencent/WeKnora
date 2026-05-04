<template>
  <div class="kb-chunking-settings">
    <div class="section-header">
      <h2>{{ $t('knowledgeEditor.chunking.title') }}</h2>
      <p class="section-description">{{ $t('knowledgeEditor.chunking.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- Strategy -->
      <div class="setting-row strategy-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.chunking.strategyLabel') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.chunking.strategyDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localStrategy"
            :options="strategyOptions"
            :placeholder="$t('knowledgeEditor.chunking.strategyPlaceholder')"
            :clearable="true"
            @change="handleStrategyChange"
            style="width: 280px;"
          />
        </div>
      </div>

      <!-- Strategy explanation panel -->
      <div v-if="currentStrategyInfo" class="strategy-info-panel">
        <p>
          <strong>{{ currentStrategyInfo.label }}:</strong>
          {{ currentStrategyInfo.tooltip }}
        </p>
      </div>

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
          <p v-if="overlapTooHigh" class="warn">{{ $t('knowledgeEditor.chunking.overlapWarning') }}</p>
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

      <!-- Parent-Child Chunking -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.chunking.parentChildLabel') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.chunking.parentChildDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="localEnableParentChild"
            @change="handleParentChildChange"
          />
        </div>
      </div>

      <!-- Parent Chunk Size -->
      <div v-if="localEnableParentChild" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.chunking.parentChunkSizeLabel') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.chunking.parentChunkSizeDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="slider-container">
            <t-slider
              v-model="localParentChunkSize"
              :min="512"
              :max="8192"
              :step="64"
              :marks="{ 512: '512', 2048: '2048', 4096: '4096', 8192: '8192' }"
              @change="handleParentChunkSizeChange"
              style="width: 200px;"
            />
            <span class="value-display">{{ localParentChunkSize }} {{ $t('knowledgeEditor.chunking.characters') }}</span>
          </div>
        </div>
      </div>

      <!-- Child Chunk Size -->
      <div v-if="localEnableParentChild" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.chunking.childChunkSizeLabel') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.chunking.childChunkSizeDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="slider-container">
            <t-slider
              v-model="localChildChunkSize"
              :min="64"
              :max="2048"
              :step="32"
              :marks="{ 64: '64', 384: '384', 1024: '1024', 2048: '2048' }"
              @change="handleChildChunkSizeChange"
              style="width: 200px;"
            />
            <span class="value-display">{{ localChildChunkSize }} {{ $t('knowledgeEditor.chunking.characters') }}</span>
          </div>
        </div>
      </div>

      <!-- Advanced section toggle -->
      <div class="advanced-toggle" @click="advancedOpen = !advancedOpen">
        <span class="toggle-arrow" :class="{ 'open': advancedOpen }">▸</span>
        <span>{{ $t('knowledgeEditor.chunking.advancedLabel') }}</span>
      </div>

      <div v-if="advancedOpen" class="advanced-section">
        <!-- Token Limit -->
        <div class="setting-row" :class="{ disabled: advancedDisabled }">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.chunking.tokenLimitLabel') }}</label>
            <p class="desc">{{ $t('knowledgeEditor.chunking.tokenLimitDescription') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              v-model="localTokenLimit"
              :min="0"
              :max="8192"
              :step="64"
              :disabled="advancedDisabled"
              @change="handleTokenLimitChange"
              style="width: 200px;"
            />
          </div>
        </div>

        <!-- Languages -->
        <div class="setting-row" :class="{ disabled: advancedDisabled }">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.chunking.languagesLabel') }}</label>
            <p class="desc">{{ $t('knowledgeEditor.chunking.languagesDescription') }}</p>
          </div>
          <div class="setting-control">
            <t-select
              v-model="localLanguages"
              :options="languageOptions"
              multiple
              :disabled="advancedDisabled"
              :placeholder="$t('knowledgeEditor.chunking.languagesPlaceholder')"
              @change="handleLanguagesChange"
              style="width: 280px;"
            />
          </div>
        </div>
      </div>

      <!-- Debug panel: try a sample text against the current config without re-uploading. -->
      <KBChunkingDebug :config="debugConfig" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import KBChunkingDebug from './KBChunkingDebug.vue'

interface ParserEngineRule {
  file_types: string[]
  engine: string
}

// Slider ranges defined in this file (min/max props on t-slider) mirror
// the validated bounds in the backend splitter:
//   ChunkSize:      100–4000  (default 512). 100 = too fragmented to be
//                   useful; 4000 = approaches the 7500-char absoluteMaxSize
//                   that the splitter hard-caps to anyway.
//   ChunkOverlap:   0–500     (default 80). Backend caps to ChunkSize/2
//                   when set higher than that.
//   ParentChunkSize: 512–8192 (default 4096 ≈ 1000 EN tokens).
//   ChildChunkSize:  64–2048  (default 384 ≈ 80 EN tokens, sweet spot for
//                   sentence-transformer / BGE embedders).
//   TokenLimit:      0–8192   (default 0 = off, char-based budget only).
//                   Set to 200 for MiniLM (256-tok limit), 400 for BGE/
//                   Cohere (512-tok), leave at 0 for OpenAI/Voyage/Jina-v3.
interface ChunkingConfig {
  chunkSize: number
  chunkOverlap: number
  separators: string[]
  parserEngineRules?: ParserEngineRule[]
  enableParentChild: boolean
  parentChunkSize: number
  childChunkSize: number
  // Adaptive chunking strategy. Empty string = legacy / not set.
  strategy?: string
  // Cap chunk size in approx tokens. 0 = char-based budget only.
  tokenLimit?: number
  // Language hints for heuristic patterns (de/en/zh).
  languages?: string[]
}

interface Props {
  config: ChunkingConfig
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:config': [value: ChunkingConfig]
}>()

const { t } = useI18n()

const localChunkSize = ref(props.config.chunkSize)
const localChunkOverlap = ref(props.config.chunkOverlap)
const localSeparators = ref([...props.config.separators])
const localEnableParentChild = ref(props.config.enableParentChild ?? false)
const localParentChunkSize = ref(props.config.parentChunkSize || 4096)
const localChildChunkSize = ref(props.config.childChunkSize || 384)
const localStrategy = ref(props.config.strategy ?? '')
const localTokenLimit = ref(props.config.tokenLimit ?? 0)
const localLanguages = ref<string[]>([...(props.config.languages ?? [])])
const advancedOpen = ref(false)

const strategyOptions = computed(() => [
  {
    label: t('knowledgeEditor.chunking.strategies.auto.label'),
    value: 'auto',
    tooltip: t('knowledgeEditor.chunking.strategies.auto.tooltip')
  },
  {
    label: t('knowledgeEditor.chunking.strategies.heading.label'),
    value: 'heading',
    tooltip: t('knowledgeEditor.chunking.strategies.heading.tooltip')
  },
  {
    label: t('knowledgeEditor.chunking.strategies.heuristic.label'),
    value: 'heuristic',
    tooltip: t('knowledgeEditor.chunking.strategies.heuristic.tooltip')
  },
  {
    label: t('knowledgeEditor.chunking.strategies.legacy.label'),
    value: 'legacy',
    tooltip: t('knowledgeEditor.chunking.strategies.legacy.tooltip')
  }
])

const currentStrategyInfo = computed(() => {
  if (!localStrategy.value) {
    return null
  }
  return strategyOptions.value.find(o => o.value === localStrategy.value) ?? null
})

const advancedDisabled = computed(() => localStrategy.value === 'legacy')

const overlapTooHigh = computed(
  () => localChunkOverlap.value > 0 && localChunkOverlap.value >= localChunkSize.value / 2
)

// Live config snapshot for the debug panel — uses current local form values
// so the panel reflects edits immediately without waiting for save.
const debugConfig = computed(() => ({
  chunkSize: localChunkSize.value,
  chunkOverlap: localChunkOverlap.value,
  separators: localSeparators.value,
  strategy: localStrategy.value,
  tokenLimit: localTokenLimit.value,
  languages: localLanguages.value
}))

const languageOptions = computed(() => [
  { label: t('knowledgeEditor.chunking.languageOptions.de'), value: 'de' },
  { label: t('knowledgeEditor.chunking.languageOptions.en'), value: 'en' },
  { label: t('knowledgeEditor.chunking.languageOptions.zh'), value: 'zh' }
])

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

watch(() => props.config, (newConfig) => {
  localChunkSize.value = newConfig.chunkSize
  localChunkOverlap.value = newConfig.chunkOverlap
  localSeparators.value = [...newConfig.separators]
  localEnableParentChild.value = newConfig.enableParentChild ?? false
  localParentChunkSize.value = newConfig.parentChunkSize || 4096
  localChildChunkSize.value = newConfig.childChunkSize || 384
  localStrategy.value = newConfig.strategy ?? ''
  localTokenLimit.value = newConfig.tokenLimit ?? 0
  localLanguages.value = [...(newConfig.languages ?? [])]
}, { deep: true })

const handleChunkSizeChange = () => { emitUpdate() }
const handleChunkOverlapChange = () => { emitUpdate() }
const handleSeparatorsChange = () => { emitUpdate() }
const handleParentChildChange = () => { emitUpdate() }
const handleParentChunkSizeChange = () => { emitUpdate() }
const handleChildChunkSizeChange = () => { emitUpdate() }
const handleStrategyChange = () => { emitUpdate() }
const handleTokenLimitChange = () => { emitUpdate() }
const handleLanguagesChange = () => { emitUpdate() }

const emitUpdate = () => {
  // Spread arrays so the parent gets its own copy. Mutating the emitted
  // arrays from outside must not leak back into our reactive state and
  // cause two-way ref drift between the form and the editor model.
  emit('update:config', {
    chunkSize: localChunkSize.value,
    chunkOverlap: localChunkOverlap.value,
    separators: [...localSeparators.value],
    parserEngineRules: props.config.parserEngineRules,
    enableParentChild: localEnableParentChild.value,
    parentChunkSize: localParentChunkSize.value,
    childChunkSize: localChildChunkSize.value,
    strategy: localStrategy.value,
    tokenLimit: localTokenLimit.value,
    languages: [...localLanguages.value]
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

  &.disabled {
    opacity: 0.5;
  }
}

.strategy-row {
  // Strategy is the most prominent setting — slight emphasis.
  background: linear-gradient(to right, var(--td-bg-color-container-hover), transparent);
  padding-left: 12px;
  margin-left: -12px;
  border-radius: 6px;
}

.strategy-info-panel {
  margin: -8px 0 12px 12px;
  padding: 10px 14px;
  background: var(--td-bg-color-container-hover);
  border-left: 3px solid var(--td-brand-color);
  border-radius: 0 4px 4px 0;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;

  p {
    margin: 0;
  }

  strong {
    color: var(--td-text-color-primary);
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

  .warn {
    font-size: 12px;
    color: var(--td-warning-color);
    margin: 4px 0 0 0;
    line-height: 1.4;
  }
}

.setting-control {
  flex: 0 0 55%;
  max-width: 55%;
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
  color: var(--td-text-color-primary);
  font-weight: 500;
  min-width: 80px;
  text-align: right;
}

.advanced-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 16px 0 8px 0;
  cursor: pointer;
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  user-select: none;

  &:hover {
    color: var(--td-text-color-primary);
  }
}

.toggle-arrow {
  display: inline-block;
  transition: transform 0.15s ease;
  font-size: 12px;

  &.open {
    transform: rotate(90deg);
  }
}

.advanced-section {
  padding-left: 12px;
  border-left: 2px solid var(--td-component-stroke);
}
</style>
