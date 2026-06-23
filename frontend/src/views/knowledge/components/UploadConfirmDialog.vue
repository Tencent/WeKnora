<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="dialogVisible" class="upload-confirm-overlay">
        <div class="upload-confirm-modal" role="dialog" :aria-label="t('uploadConfirm.title')">
          <button class="close-btn" type="button" :aria-label="t('general.close')" @click="handleCancel">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            </svg>
          </button>

          <div class="upload-confirm-container">
            <aside class="files-panel">
              <div class="files-panel-header">
                <h2 class="files-panel-title">{{ sourcePanelTitle }}</h2>
                <div v-if="mode === 'file'" class="files-panel-actions">
                  <span class="files-count">{{ batchItemCount }}</span>
                  <KbUploadSourceDropdown
                    :accept-file-types="acceptFileTypes"
                    :supported-file-types="supportedFileTypes"
                    :tooltip="t('uploadConfirm.continueAdd')"
                    placement="bottom-left"
                    @files="appendFiles"
                    @url="appendUrl"
                  />
                </div>
              </div>
              <div v-if="mode === 'manual' && manualPreview" class="manual-source-panel">
                <p class="manual-source-title" :title="manualPreview.title">{{ manualPreview.title }}</p>
                <p class="manual-source-meta">
                  {{ t('uploadConfirm.manualCharCount', { count: manualCharCount }) }}
                </p>
              </div>
              <div v-else-if="mode === 'reparse' && reparsePreview" class="manual-source-panel">
                <p class="manual-source-title" :title="reparsePreview.fileName">
                  {{ reparsePreview.fileName || t('uploadConfirm.reparseSource') }}
                </p>
                <p class="manual-source-meta">{{ t('uploadConfirm.reparseHint') }}</p>
              </div>
              <ul v-else-if="mode === 'file' && batchItemCount > 0" class="files-list">
                <li v-for="(url, index) in localUrls" :key="`url-${url}-${index}`" class="file-item">
                  <t-icon name="link" class="file-icon" />
                  <div class="file-meta">
                    <span class="file-name" :title="url">{{ url }}</span>
                    <span class="file-size">{{ t('uploadConfirm.urlItemLabel') }}</span>
                  </div>
                  <t-button
                    theme="default"
                    variant="text"
                    size="small"
                    shape="square"
                    :aria-label="t('common.remove')"
                    @click="removeUrl(index)"
                  >
                    <t-icon name="close" />
                  </t-button>
                </li>
                <li v-for="(file, index) in localFiles" :key="`${file.name}-${index}`" class="file-item">
                  <t-icon :name="getFileIcon(file.name)" class="file-icon" />
                  <div class="file-meta">
                    <span class="file-name" :title="file.name">{{ file.name }}</span>
                    <span class="file-size">{{ formatFileSize(file.size) }}</span>
                  </div>
                  <t-button
                    theme="default"
                    variant="text"
                    size="small"
                    shape="square"
                    :aria-label="t('common.remove')"
                    @click="removeFile(index)"
                  >
                    <t-icon name="close" />
                  </t-button>
                </li>
              </ul>
              <div v-else-if="mode === 'file'" class="files-empty">{{ t('uploadConfirm.noItems') }}</div>
            </aside>

            <main class="main-panel">
              <header class="main-header">
                <template v-if="activeSection === 'overview'">
                  <h2 class="main-title">{{ dialogTitle }}</h2>
                  <p class="main-desc">{{ dialogDesc }}</p>
                </template>
                <template v-else>
                  <button
                    type="button"
                    class="back-link"
                    @click="activeSection = 'overview'"
                  >
                    <t-icon name="chevron-left" />
                    <span>{{ t('uploadConfirm.backToOverview') }}</span>
                  </button>
                  <h2 class="edit-title">{{ currentSectionTitle }}</h2>
                  <p v-if="currentSectionDesc" class="edit-desc">{{ currentSectionDesc }}</p>
                </template>
              </header>

              <div class="main-body">
                <div v-if="activeSection === 'overview'" class="overview-list">
                    <button
                      v-for="line in overviewLines"
                      :key="line.key"
                      type="button"
                      class="overview-row"
                      :class="{ 'overview-row--issue': issueSectionKeys.has(line.key) }"
                      @click="goToSection(line.key)"
                    >
                      <span class="overview-label">{{ line.title }}</span>
                      <span
                        class="overview-value"
                        :class="{ 'overview-value--issue': issueSectionKeys.has(line.key) }"
                        :title="line.value"
                      >{{ line.value }}</span>
                      <t-icon name="chevron-right" class="overview-chevron" />
                    </button>
                </div>

                <div v-else class="edit-section edit-section--embedded">
                  <div v-show="activeSection === 'parser'" class="section">
                    <KBParserSettings
                      embedded
                      :relevant-extensions="batchFileExts"
                      :parser-engine-rules="uiState.chunkingConfig.parserEngineRules"
                      @update:parser-engine-rules="handleParserEngineRulesUpdate"
                    />
                  </div>
                  <div v-show="activeSection === 'chunking'" class="section">
                    <KBChunkingSettings
                      embedded
                      :config="uiState.chunkingConfig"
                      @update:config="handleChunkingConfigUpdate"
                    />
                  </div>
                  <div v-show="activeSection === 'multimodal'" class="section">
                    <div class="kb-embedded-settings">
                      <div class="setting-row setting-row--toggle">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.advanced.multimodal.label') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.description') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-switch v-model="uiState.multimodalConfig.enabled" size="medium" />
                        </div>
                      </div>
                      <div v-if="uiState.multimodalConfig.enabled" class="setting-row setting-row--field">
                        <div class="setting-info">
                          <label>
                            {{ t('knowledgeEditor.advanced.multimodal.vllmLabel') }}
                            <span class="required">*</span>
                          </label>
                        </div>
                        <div class="setting-control setting-control--full">
                          <ModelSelector
                            model-type="VLLM"
                            :selected-model-id="uiState.multimodalConfig.vllmModelId"
                            :all-models="allModels"
                            :status="showMultimodalModelError ? 'error' : 'default'"
                            :placeholder="t('knowledgeEditor.advanced.multimodal.vllmPlaceholder')"
                            @update:selected-model-id="handleMultimodalVLLMChange"
                            @add-model="handleAddVLLMModel"
                          />
                          <p v-if="showMultimodalModelError" class="field-error">
                            {{ t('uploadConfirm.vlmModelSelectRequired') }}
                          </p>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div v-show="activeSection === 'asr'" class="section">
                    <div class="kb-embedded-settings">
                      <div class="setting-row setting-row--toggle">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.asr.label') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.asr.desc') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-switch v-model="uiState.asrConfig.enabled" size="medium" />
                        </div>
                      </div>
                      <div v-if="uiState.asrConfig.enabled" class="setting-row setting-row--field">
                        <div class="setting-info">
                          <label>
                            {{ t('knowledgeEditor.asr.modelLabel') }}
                            <span class="required">*</span>
                          </label>
                        </div>
                        <div class="setting-control setting-control--full">
                          <ModelSelector
                            model-type="ASR"
                            :selected-model-id="uiState.asrConfig.modelId"
                            :all-models="allModels"
                            :status="showAsrModelError ? 'error' : 'default'"
                            :placeholder="t('knowledgeEditor.asr.modelPlaceholder')"
                            @update:selected-model-id="(val: string) => { uiState.asrConfig.modelId = val }"
                            @add-model="handleAddASRModel"
                          />
                          <p v-if="showAsrModelError" class="field-error">
                            {{ t('uploadConfirm.asrModelSelectRequired') }}
                          </p>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div v-show="activeSection === 'question'" class="section">
                    <KBAdvancedSettings
                      embedded
                      :question-generation="uiState.questionGenerationConfig"
                      :rag-enabled="ragEnabled"
                      :all-models="allModels"
                      @update:question-generation="handleQuestionGenerationUpdate"
                    />
                  </div>
                  <div v-show="activeSection === 'graph'" class="section">
                    <GraphSettings
                      embedded
                      :graph-extract="uiState.nodeExtractConfig"
                      :model-id="llmModelId"
                      :all-models="allModels"
                      @update:graphExtract="handleNodeExtractUpdate"
                    />
                  </div>
                </div>
              </div>
            </main>
          </div>

          <footer class="modal-footer">
            <t-button theme="default" variant="outline" @click="handleCancel">
              {{ t('uploadConfirm.cancel') }}
            </t-button>
            <t-button theme="primary" :disabled="!canConfirm" @click="handleConfirm">
              {{ confirmButtonText }}
            </t-button>
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, withDefaults } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import ModelSelector from '@/components/ModelSelector.vue'
import KBParserSettings from '../settings/KBParserSettings.vue'
import KBChunkingSettings from '../settings/KBChunkingSettings.vue'
import KBAdvancedSettings from '../settings/KBAdvancedSettings.vue'
import GraphSettings from '../settings/GraphSettings.vue'
import { useChatResourcesStore } from '@/stores/chatResources'
import { useUIStore } from '@/stores/ui'
import { formatFileSize, getFileIcon } from '@/utils/files'
import { getUploadFileKey } from '../utils/uploadSources'
import KbUploadSourceDropdown from './KbUploadSourceDropdown.vue'
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'
import type {
  UploadConfirmManualSource,
  UploadConfirmMode,
  UploadConfirmReparseSource,
  UploadConfirmResult,
} from '@/stores/uploadConfirm'

const IMAGE_EXTENSIONS = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp']
const AUDIO_EXTENSIONS = ['mp3', 'wav', 'm4a', 'flac', 'ogg']

interface ChunkingUIConfig {
  chunkSize: number
  chunkOverlap: number
  separators: string[]
  parserEngineRules?: Array<{ file_types: string[]; engine: string }>
  enableParentChild: boolean
  parentChunkSize: number
  childChunkSize: number
  strategy?: string
  tokenLimit?: number
  languages?: string[]
}

interface UploadUIState {
  chunkingConfig: ChunkingUIConfig
  multimodalConfig: { enabled: boolean; vllmModelId: string }
  asrConfig: { enabled: boolean; modelId: string; language: string }
  questionGenerationConfig: { enabled: boolean; questionCount: number }
  nodeExtractConfig: {
    enabled: boolean
    text: string
    tags: string[]
    nodes: Array<{ name: string; attributes: string[] }>
    relations: Array<{ node1: string; node2: string; type: string }>
  }
  graphEnabled: boolean
}

const props = withDefaults(defineProps<{
  visible: boolean
  kbInfo: any
  mode?: UploadConfirmMode
  files?: File[]
  urls?: string[]
  manualPreview?: UploadConfirmManualSource | null
  reparsePreview?: UploadConfirmReparseSource | null
  tagId?: string
  acceptFileTypes?: string
  supportedFileTypes?: string[]
}>(), {
  mode: 'file',
  files: () => [],
  urls: () => [],
  manualPreview: null,
  reparsePreview: null,
  acceptFileTypes: '',
  supportedFileTypes: () => [],
})

const emit = defineEmits<{
  'update:visible': [value: boolean]
  confirm: [payload: UploadConfirmResult]
  cancel: []
}>()

const { t } = useI18n()
const chatResources = useChatResourcesStore()
const uiStore = useUIStore()

const allModels = ref<any[]>([])
const localFiles = ref<File[]>([])
const localUrls = ref<string[]>([])
const activeSection = ref('overview')
const uiState = ref<UploadUIState>(createDefaultUIState())

const dialogVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

function getEngineDisplayName(engineName: string): string {
  const key = `kbSettings.parser.engines.${engineName}.name`
  const translated = t(key)
  return translated !== key ? translated : engineName
}

function getStrategyLabel(strategy?: string): string {
  if (!strategy) return t('uploadConfirm.summaryStrategyDefault')
  const key = `knowledgeEditor.chunking.strategies.${strategy}.label`
  const translated = t(key)
  return translated !== key ? translated : strategy
}

function getModelName(modelId: string): string {
  if (!modelId) return t('uploadConfirm.notSet')
  const model = allModels.value.find((m: any) => m.id === modelId)
  return model?.name || modelId
}

function getFileExt(file: File): string {
  const dot = file.name.lastIndexOf('.')
  if (dot < 0) return ''
  return file.name.substring(dot + 1).toLowerCase()
}

function getExtFromUrl(url: string): string {
  try {
    const pathname = new URL(url).pathname
    const dot = pathname.lastIndexOf('.')
    if (dot < 0) return ''
    return pathname.substring(dot + 1).toLowerCase()
  } catch {
    return ''
  }
}

function inferMediaExtsFromMarkdown(content: string): string[] {
  const exts = new Set<string>()
  const patterns = [
    /\.(jpg|jpeg|png|gif|bmp|webp)(\?|#|\)|\s|$)/gi,
    /\.(mp3|wav|m4a|flac|ogg)(\?|#|\)|\s|$)/gi,
  ]
  for (const pattern of patterns) {
    let match: RegExpExecArray | null
    while ((match = pattern.exec(content)) !== null) {
      let ext = match[1].toLowerCase()
      if (ext === 'jpeg') ext = 'jpg'
      exts.add(ext)
    }
  }
  return [...exts]
}

const manualCharCount = computed(() => props.manualPreview?.content?.length ?? 0)

const batchItemCount = computed(() => localFiles.value.length + localUrls.value.length)

const sourcePanelTitle = computed(() => {
  if (props.mode === 'manual') return t('uploadConfirm.manualSource')
  if (props.mode === 'reparse') return t('uploadConfirm.reparseSource')
  return t('uploadConfirm.fileList')
})

const dialogTitle = computed(() => {
  if (props.mode === 'manual') return t('uploadConfirm.titleManual')
  if (props.mode === 'reparse') return t('uploadConfirm.titleReparse')
  return t('uploadConfirm.title')
})

const dialogDesc = computed(() => {
  if (props.mode === 'manual') return t('uploadConfirm.overviewDescManual')
  if (props.mode === 'reparse') return t('uploadConfirm.overviewDescReparse')
  return t('uploadConfirm.overviewDesc')
})

const confirmButtonText = computed(() => {
  if (props.mode === 'manual') return t('uploadConfirm.confirmManual')
  if (props.mode === 'reparse') return t('uploadConfirm.confirmReparse')
  return t('uploadConfirm.confirm')
})

const batchFileExts = computed(() => {
  const set = new Set<string>()
  if (props.mode === 'manual' && props.manualPreview?.content) {
    for (const ext of inferMediaExtsFromMarkdown(props.manualPreview.content)) {
      set.add(ext)
    }
  }
  if (props.mode === 'reparse') {
    const ext = (props.reparsePreview?.fileType || '').toLowerCase()
    if (ext) set.add(ext)
  }
  for (const url of localUrls.value) {
    const ext = getExtFromUrl(url)
    if (ext) set.add(ext)
  }
  for (const file of localFiles.value) {
    const ext = getFileExt(file)
    if (ext) set.add(ext)
  }
  return [...set]
})

function resolveEngineForExt(ext: string): string {
  const rules = uiState.value.chunkingConfig.parserEngineRules
  if (rules?.length) {
    for (const rule of rules) {
      if (rule.file_types.includes(ext)) {
        return getEngineDisplayName(rule.engine)
      }
    }
  }
  return t('uploadConfirm.summaryParserBuiltin')
}

const parserOverviewValue = computed(() => {
  const exts = batchFileExts.value
  if (!exts.length) return t('uploadConfirm.summaryParserBuiltin')
  return exts.map(ext => `.${ext} → ${resolveEngineForExt(ext)}`).join(' · ')
})

const chunkingOverviewValue = computed(() => {
  const c = uiState.value.chunkingConfig
  const parts = [
    t('uploadConfirm.navChunkingSummary', { size: c.chunkSize }),
    t('uploadConfirm.summaryChunkOverlapShort', { overlap: c.chunkOverlap }),
    getStrategyLabel(c.strategy),
  ]
  parts.push(
    c.enableParentChild
      ? t('uploadConfirm.summaryParentChildShort')
      : t('uploadConfirm.summaryParentChildOff'),
  )
  return parts.join(' · ')
})

const overviewLines = computed(() => {
  const mm = uiState.value.multimodalConfig
  const asr = uiState.value.asrConfig
  const qg = uiState.value.questionGenerationConfig
  const graph = uiState.value.nodeExtractConfig

  return [
    { key: 'parser', title: t('uploadConfirm.tabParser'), value: parserOverviewValue.value },
    { key: 'chunking', title: t('uploadConfirm.tabChunking'), value: chunkingOverviewValue.value },
    {
      key: 'multimodal',
      title: t('uploadConfirm.tabMultimodal'),
      value: mm.enabled
        ? `${t('uploadConfirm.statusOn')} · ${mm.vllmModelId ? getModelName(mm.vllmModelId) : t('uploadConfirm.notSet')}`
        : (hasImages.value ? t('uploadConfirm.multimodalRequiredForImages') : t('uploadConfirm.statusOff')),
    },
    {
      key: 'asr',
      title: t('uploadConfirm.tabAsr'),
      value: asr.enabled
        ? `${t('uploadConfirm.statusOn')} · ${asr.modelId ? getModelName(asr.modelId) : t('uploadConfirm.notSet')}`
        : (hasAudio.value ? t('uploadConfirm.asrRequiredForAudio') : t('uploadConfirm.statusOff')),
    },
    {
      key: 'question',
      title: t('uploadConfirm.tabQuestion'),
      value: qg.enabled
        ? t('uploadConfirm.summaryQuestionCountValue', { count: qg.questionCount })
        : t('uploadConfirm.statusOff'),
    },
    {
      key: 'graph',
      title: t('uploadConfirm.tabGraph'),
      value: graph.enabled
        ? (graph.tags.length
          ? t('uploadConfirm.summaryGraphTagsValue', { count: graph.tags.length })
          : t('uploadConfirm.statusOn'))
        : t('uploadConfirm.statusOff'),
    },
  ]
})

const sectionMeta: Record<string, { titleKey: string; descKey?: string }> = {
  parser: { titleKey: 'uploadConfirm.tabParser', descKey: 'kbSettings.parser.description' },
  chunking: { titleKey: 'uploadConfirm.tabChunking', descKey: 'knowledgeEditor.chunking.description' },
  multimodal: { titleKey: 'uploadConfirm.tabMultimodal', descKey: 'knowledgeEditor.multimodal.description' },
  asr: { titleKey: 'uploadConfirm.tabAsr', descKey: 'knowledgeEditor.asr.description' },
  question: { titleKey: 'uploadConfirm.tabQuestion', descKey: 'knowledgeEditor.advanced.questionGeneration.description' },
  graph: { titleKey: 'uploadConfirm.tabGraph', descKey: 'graphSettings.description' },
}

const currentSectionTitle = computed(() => {
  const meta = sectionMeta[activeSection.value]
  return meta ? t(meta.titleKey) : ''
})

const currentSectionDesc = computed(() => {
  const meta = sectionMeta[activeSection.value]
  return meta?.descKey ? t(meta.descKey) : ''
})

const goToSection = (key: string) => {
  activeSection.value = key
}

const llmModelId = computed(() => props.kbInfo?.summary_model_id || '')
const ragEnabled = computed(() => {
  const strategy = props.kbInfo?.indexing_strategy
  return (strategy?.vector_enabled ?? true) || (strategy?.keyword_enabled ?? true)
})

const hasImages = computed(() => {
  if (props.mode === 'manual' && props.manualPreview?.content) {
    const content = props.manualPreview.content
    if (/data:image\/|!\[[^\]]*\]\([^)]+\)/i.test(content)) return true
  }
  return batchFileExts.value.some(ext => IMAGE_EXTENSIONS.includes(ext))
})

const hasAudio = computed(() => {
  return batchFileExts.value.some(ext => AUDIO_EXTENSIONS.includes(ext))
})

const showMultimodalModelError = computed(() => {
  return uiState.value.multimodalConfig.enabled && !uiState.value.multimodalConfig.vllmModelId
})

const showAsrModelError = computed(() => {
  return uiState.value.asrConfig.enabled && !uiState.value.asrConfig.modelId
})

const issueSectionKeys = computed(() => {
  const keys = new Set<string>()
  if (hasImages.value) {
    if (!uiState.value.multimodalConfig.enabled || !uiState.value.multimodalConfig.vllmModelId) {
      keys.add('multimodal')
    }
  } else if (showMultimodalModelError.value) {
    keys.add('multimodal')
  }
  if (hasAudio.value) {
    if (!uiState.value.asrConfig.enabled || !uiState.value.asrConfig.modelId) {
      keys.add('asr')
    }
  } else if (showAsrModelError.value) {
    keys.add('asr')
  }
  return keys
})

const canConfirm = computed(() => {
  if (props.mode === 'file' && batchItemCount.value === 0) return false
  if (props.mode === 'manual' && !props.manualPreview?.content?.trim()) return false
  if (hasImages.value) {
    if (!uiState.value.multimodalConfig.enabled || !uiState.value.multimodalConfig.vllmModelId) {
      return false
    }
  }
  if (hasAudio.value) {
    if (!uiState.value.asrConfig.enabled || !uiState.value.asrConfig.modelId) {
      return false
    }
  }
  if (showMultimodalModelError.value || showAsrModelError.value) {
    return false
  }
  return true
})

function createDefaultUIState(): UploadUIState {
  return {
    chunkingConfig: {
      chunkSize: 512,
      chunkOverlap: 80,
      separators: ['\n\n', '\n', '。', '！', '？', ';', '；'],
      parserEngineRules: undefined,
      enableParentChild: true,
      parentChunkSize: 4096,
      childChunkSize: 384,
      strategy: 'auto',
      tokenLimit: 0,
      languages: [],
    },
    multimodalConfig: { enabled: false, vllmModelId: '' },
    asrConfig: { enabled: false, modelId: '', language: '' },
    questionGenerationConfig: { enabled: true, questionCount: 3 },
    nodeExtractConfig: {
      enabled: false,
      text: '',
      tags: [],
      nodes: [],
      relations: [],
    },
    graphEnabled: false,
  }
}

function initFromKbInfo(kb: any) {
  if (!kb) {
    uiState.value = createDefaultUIState()
    return
  }

  uiState.value = {
    chunkingConfig: {
      chunkSize: kb.chunking_config?.chunk_size || 512,
      chunkOverlap: kb.chunking_config?.chunk_overlap || 80,
      separators: kb.chunking_config?.separators || ['\n\n', '\n', '。', '！', '？', ';', '；'],
      parserEngineRules: kb.chunking_config?.parser_engine_rules || undefined,
      enableParentChild: kb.chunking_config?.enable_parent_child ?? false,
      parentChunkSize: kb.chunking_config?.parent_chunk_size || 4096,
      childChunkSize: kb.chunking_config?.child_chunk_size || 384,
      strategy: kb.chunking_config?.strategy || 'auto',
      tokenLimit: kb.chunking_config?.token_limit || 0,
      languages: kb.chunking_config?.languages || [],
    },
    multimodalConfig: {
      enabled: !!kb.vlm_config?.enabled,
      vllmModelId: kb.vlm_config?.model_id || '',
    },
    asrConfig: {
      enabled: !!kb.asr_config?.enabled,
      modelId: kb.asr_config?.model_id || '',
      language: kb.asr_config?.language || '',
    },
    questionGenerationConfig: {
      enabled: kb.question_generation_config?.enabled ?? true,
      questionCount: kb.question_generation_config?.question_count || 3,
    },
    nodeExtractConfig: {
      enabled: kb.extract_config?.enabled || false,
      text: kb.extract_config?.text || '',
      tags: kb.extract_config?.tags || [],
      nodes: (kb.extract_config?.nodes || []).map((node: any) => ({
        name: node.name,
        attributes: node.attributes || [],
      })),
      relations: kb.extract_config?.relations || [],
    },
    graphEnabled: kb.indexing_strategy?.graph_enabled ?? false,
  }
}

function buildProcessOverrides(): KnowledgeProcessOverrides {
  const state = uiState.value
  const chunking = state.chunkingConfig

  return {
    parser_engine_rules: chunking.parserEngineRules,
    chunking_config: {
      chunk_size: chunking.chunkSize,
      chunk_overlap: chunking.chunkOverlap,
      separators: chunking.separators,
      enable_parent_child: chunking.enableParentChild,
      parent_chunk_size: chunking.parentChunkSize,
      child_chunk_size: chunking.childChunkSize,
      strategy: chunking.strategy,
      token_limit: chunking.tokenLimit,
      languages: chunking.languages,
    },
    enable_multimodel: state.multimodalConfig.enabled,
    vlm_config: {
      enabled: state.multimodalConfig.enabled,
      model_id: state.multimodalConfig.vllmModelId,
    },
    asr_config: {
      enabled: state.asrConfig.enabled,
      model_id: state.asrConfig.modelId,
      language: state.asrConfig.language,
    },
    question_generation_config: {
      enabled: state.questionGenerationConfig.enabled,
      question_count: state.questionGenerationConfig.questionCount,
    },
    graph_enabled: state.nodeExtractConfig.enabled && state.graphEnabled,
    extract_config: {
      enabled: state.nodeExtractConfig.enabled,
      text: state.nodeExtractConfig.text,
      tags: state.nodeExtractConfig.tags,
      nodes: state.nodeExtractConfig.nodes,
      relations: state.nodeExtractConfig.relations,
    },
  }
}

function applyOverridesToState(o?: KnowledgeProcessOverrides | null) {
  if (!o) return
  const s = uiState.value
  const cc = o.chunking_config
  if (cc) {
    if (cc.chunk_size != null) s.chunkingConfig.chunkSize = cc.chunk_size
    if (cc.chunk_overlap != null) s.chunkingConfig.chunkOverlap = cc.chunk_overlap
    if (cc.separators) s.chunkingConfig.separators = cc.separators
    if (cc.enable_parent_child != null) s.chunkingConfig.enableParentChild = cc.enable_parent_child
    if (cc.parent_chunk_size != null) s.chunkingConfig.parentChunkSize = cc.parent_chunk_size
    if (cc.child_chunk_size != null) s.chunkingConfig.childChunkSize = cc.child_chunk_size
    if (cc.strategy != null) s.chunkingConfig.strategy = cc.strategy
    if (cc.token_limit != null) s.chunkingConfig.tokenLimit = cc.token_limit
    if (cc.languages) s.chunkingConfig.languages = cc.languages
    if (cc.parser_engine_rules) s.chunkingConfig.parserEngineRules = cc.parser_engine_rules
  }
  if (o.parser_engine_rules) s.chunkingConfig.parserEngineRules = o.parser_engine_rules
  if (o.enable_multimodel != null) s.multimodalConfig.enabled = o.enable_multimodel
  if (o.vlm_config) {
    if (o.vlm_config.enabled != null) s.multimodalConfig.enabled = o.vlm_config.enabled
    if (o.vlm_config.model_id != null) s.multimodalConfig.vllmModelId = o.vlm_config.model_id
  }
  if (o.asr_config) {
    if (o.asr_config.enabled != null) s.asrConfig.enabled = o.asr_config.enabled
    if (o.asr_config.model_id != null) s.asrConfig.modelId = o.asr_config.model_id
    if (o.asr_config.language != null) s.asrConfig.language = o.asr_config.language
  }
  const qg = o.question_generation_config
  if (qg) {
    if (qg.enabled != null) s.questionGenerationConfig.enabled = qg.enabled
    if (qg.question_count != null) s.questionGenerationConfig.questionCount = qg.question_count
  }
  const ec = o.extract_config
  if (ec) {
    if (ec.enabled != null) s.nodeExtractConfig.enabled = ec.enabled
    if (ec.text != null) s.nodeExtractConfig.text = ec.text
    if (ec.tags) s.nodeExtractConfig.tags = ec.tags
    if (ec.nodes) s.nodeExtractConfig.nodes = ec.nodes.map(n => ({ name: n.name, attributes: n.attributes || [] }))
    if (ec.relations) s.nodeExtractConfig.relations = ec.relations
  }
  if (o.graph_enabled != null) s.graphEnabled = o.graph_enabled
}

async function loadModels() {
  try {
    await chatResources.ensureModels()
    allModels.value = chatResources.allModels || []
  } catch {
    allModels.value = []
  }
}

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    localFiles.value = props.mode === 'file' ? [...(props.files || [])] : []
    localUrls.value = props.mode === 'file' ? [...(props.urls || [])] : []
    initFromKbInfo(props.kbInfo)
    if (props.mode === 'reparse') {
      applyOverridesToState(props.reparsePreview?.processOverrides)
    }
    activeSection.value = 'overview'
    loadModels()
  },
)

const appendFiles = (incoming: File[]) => {
  const existingKeys = new Set(localFiles.value.map(getUploadFileKey))
  const toAdd: File[] = []
  let duplicateCount = 0

  for (const file of incoming) {
    const key = getUploadFileKey(file)
    if (existingKeys.has(key)) {
      duplicateCount++
      continue
    }
    existingKeys.add(key)
    toAdd.push(file)
  }

  if (toAdd.length > 0) {
    localFiles.value = [...localFiles.value, ...toAdd]
    MessagePlugin.success(t('uploadConfirm.filesAdded', { count: toAdd.length }))
  } else if (duplicateCount > 0) {
    MessagePlugin.warning(t('uploadConfirm.filesAllDuplicate'))
  }
}

const appendUrl = (url: string) => {
  if (localUrls.value.includes(url)) {
    MessagePlugin.warning(t('uploadConfirm.urlDuplicate'))
    return
  }
  localUrls.value = [...localUrls.value, url]
  MessagePlugin.success(t('uploadConfirm.urlAdded'))
}

const removeUrl = (index: number) => {
  localUrls.value = localUrls.value.filter((_, i) => i !== index)
}

const removeFile = (index: number) => {
  localFiles.value = localFiles.value.filter((_, i) => i !== index)
}

const handleParserEngineRulesUpdate = (rules: Array<{ file_types: string[]; engine: string }>) => {
  uiState.value.chunkingConfig.parserEngineRules = rules
}

const handleChunkingConfigUpdate = (config: ChunkingUIConfig) => {
  uiState.value.chunkingConfig = { ...config }
}

const handleMultimodalVLLMChange = (modelId: string) => {
  uiState.value.multimodalConfig.vllmModelId = modelId
}

const handleAddVLLMModel = () => {
  uiStore.openSettings('models', 'vllm')
}

const handleAddASRModel = () => {
  uiStore.openSettings('models', 'asr')
}

const handleQuestionGenerationUpdate = (config: { enabled: boolean; questionCount: number }) => {
  uiState.value.questionGenerationConfig = { ...config }
}

const handleNodeExtractUpdate = (config: UploadUIState['nodeExtractConfig']) => {
  uiState.value.nodeExtractConfig = { ...config }
}

const validateBeforeConfirm = (): boolean => {
  if (hasImages.value) {
    if (!uiState.value.multimodalConfig.enabled || !uiState.value.multimodalConfig.vllmModelId) {
      MessagePlugin.warning(t('uploadConfirm.vlmModelRequired'))
      activeSection.value = 'multimodal'
      return false
    }
  } else if (showMultimodalModelError.value) {
    MessagePlugin.warning(t('uploadConfirm.vlmModelSelectRequired'))
    activeSection.value = 'multimodal'
    return false
  }

  if (hasAudio.value) {
    if (!uiState.value.asrConfig.enabled || !uiState.value.asrConfig.modelId) {
      MessagePlugin.warning(t('uploadConfirm.asrModelRequired'))
      activeSection.value = 'asr'
      return false
    }
  } else if (showAsrModelError.value) {
    MessagePlugin.warning(t('uploadConfirm.asrModelSelectRequired'))
    activeSection.value = 'asr'
    return false
  }
  return true
}

const handleCancel = () => {
  emit('cancel')
  emit('update:visible', false)
}

const handleConfirm = () => {
  if (props.mode === 'file' && batchItemCount.value === 0) {
    MessagePlugin.warning(t('uploadConfirm.noItems'))
    return
  }
  if (!validateBeforeConfirm()) return

  const processConfig = buildProcessOverrides()
  if (props.mode === 'manual' && props.manualPreview) {
    emit('confirm', { processConfig, mode: 'manual', manual: { ...props.manualPreview } })
  } else if (props.mode === 'reparse' && props.reparsePreview) {
    emit('confirm', { processConfig, mode: 'reparse', reparse: { ...props.reparsePreview } })
  } else {
    emit('confirm', {
      processConfig,
      mode: 'file',
      files: [...localFiles.value],
      urls: [...localUrls.value],
    })
  }
  emit('update:visible', false)
}
</script>

<style lang="less" scoped>
.upload-confirm-overlay {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(4px);
}

.upload-confirm-modal {
  position: relative;
  display: flex;
  flex-direction: column;
  width: min(880px, 92vw);
  height: min(640px, 86vh);
  overflow: hidden;
  border-radius: 12px;
  background: var(--td-bg-color-container);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.12);
}

.close-btn {
  position: absolute;
  top: 16px;
  right: 16px;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-primary);
  }
}

.upload-confirm-container {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.files-panel {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  width: 220px;
  border-right: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-settings-modal, var(--td-bg-color-secondarycontainer));
}

.files-panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 20px 16px 12px;
}

.files-panel-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.files-panel-title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.files-count {
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  border-radius: 10px;
  font-size: 11px;
  font-weight: 600;
  line-height: 20px;
  text-align: center;
  background: var(--td-bg-color-component);
  color: var(--td-text-color-secondary);
}

.files-list {
  flex: 1;
  margin: 0;
  padding: 4px 8px 12px;
  overflow-y: auto;
  list-style: none;
}

.file-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px;
  border-radius: 6px;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.file-icon {
  flex-shrink: 0;
  font-size: 18px;
  color: var(--td-brand-color);
}

.file-meta {
  flex: 1;
  min-width: 0;
}

.file-name {
  display: block;
  overflow: hidden;
  font-size: 13px;
  color: var(--td-text-color-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-size {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.files-empty {
  flex: 1;
  padding: 24px 16px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  text-align: center;
}

.manual-source-panel {
  flex: 1;
  min-height: 0;
  padding: 8px 16px 12px;
  overflow-y: auto;
}

.manual-source-title {
  margin: 0 0 6px;
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.manual-source-meta {
  margin: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.main-panel {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
}

.main-header {
  flex-shrink: 0;
  padding: 20px 48px 0 24px;
}

.main-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.main-desc {
  margin: 6px 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.back-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin: 0 0 10px;
  padding: 0;
  border: none;
  background: none;
  font-size: 13px;
  color: var(--td-brand-color);
  cursor: pointer;

  &:hover {
    opacity: 0.85;
  }
}

.edit-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.edit-desc {
  margin: 6px 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.main-body {
  flex: 1;
  min-height: 0;
  padding: 16px 24px 20px;
  overflow-y: auto;
}

.overview-list {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  overflow: hidden;
}

.overview-row {
  display: grid;
  grid-template-columns: 108px 1fr 20px;
  gap: 12px;
  align-items: center;
  width: 100%;
  margin: 0;
  padding: 14px 16px;
  border: none;
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  text-align: left;
  cursor: pointer;
  transition: background 0.15s ease;

  &:last-child {
    border-bottom: none;
  }

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &--issue {
    background: var(--td-error-color-1);

    &:hover {
      background: var(--td-error-color-2);
    }
  }
}

.overview-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
}

.overview-value {
  overflow: hidden;
  font-size: 13px;
  color: var(--td-text-color-primary);
  text-overflow: ellipsis;
  white-space: nowrap;

  &--issue {
    color: var(--td-error-color);
  }
}

.overview-chevron {
  font-size: 16px;
  color: var(--td-text-color-placeholder);
}

.edit-section {
  width: 100%;
}

.section {
  width: 100%;
}

.kb-embedded-settings {
  .setting-row {
    padding: 12px 0;
    border-bottom: 1px solid var(--td-component-stroke);

    &:last-child {
      border-bottom: none;
    }
  }

  .setting-row--toggle {
    display: flex;
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
    gap: 16px;

    .setting-info {
      flex: 1;
      min-width: 0;
    }

    .setting-control {
      flex: none;
      flex-shrink: 0;
    }
  }

  .setting-row--field {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 8px;
  }

  .setting-info {
    label {
      font-size: 14px;
      font-weight: 500;
      color: var(--td-text-color-primary);
    }

    .desc {
      margin: 4px 0 0;
      font-size: 12px;
      line-height: 1.5;
      color: var(--td-text-color-secondary);
    }
  }

  .setting-control--full {
    width: 100%;
  }

  .required {
    color: var(--td-error-color);
  }

  .field-error {
    margin: 6px 0 0;
    font-size: 12px;
    line-height: 1.4;
    color: var(--td-error-color);
  }
}

.edit-section--embedded {
  :deep(.setting-row) {
    &:last-child {
      border-bottom: none;
    }
  }
}

.modal-footer {
  display: flex;
  flex-shrink: 0;
  justify-content: flex-end;
  gap: 12px;
  padding: 14px 20px;
  border-top: 1px solid var(--td-component-stroke);
}

.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
</style>
