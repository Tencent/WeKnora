<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="dialogVisible" class="upload-confirm-overlay">
        <div class="upload-confirm-modal" role="dialog" :aria-label="dialogTitle">
          <button class="close-btn" type="button" :aria-label="t('general.close')" @click="handleCancel">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            </svg>
          </button>

          <div class="upload-confirm-container">
            <aside class="files-panel">
              <div class="sidebar-header">
                <div class="sidebar-header-row">
                  <h2 class="sidebar-title">{{ dialogTitle }}</h2>
                  <div v-if="mode === 'file'" class="sidebar-header-actions">
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
              </div>

              <!-- Where the batch lands. Shown for every file upload so browsing
                   a folder never silently decides the destination. -->
              <div v-if="mode === 'file'" class="destination-row">
                <t-icon :name="localTargetFolder ? 'folder' : 'folder-open'" class="destination-icon" />
                <span class="destination-label">{{ t('uploadConfirm.destinationLabel') }}</span>
                <span class="destination-path" :title="destinationTitle">{{ destinationText }}</span>
                <t-tooltip v-if="localTargetFolder" :content="t('uploadConfirm.destinationToRoot')" placement="top">
                  <button
                    type="button"
                    class="destination-clear"
                    :aria-label="t('uploadConfirm.destinationToRoot')"
                    @click="localTargetFolder = ''"
                  >
                    <t-icon name="close-circle-filled" />
                  </button>
                </t-tooltip>
              </div>

              <div class="files-list-wrap">
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
                    <span class="file-icon-wrap">
                      <t-icon name="link" class="file-icon" />
                    </span>
                    <div class="file-meta">
                      <span class="file-name" :title="url">{{ url }}</span>
                      <span class="file-size">{{ t('uploadConfirm.urlItemLabel') }}</span>
                    </div>
                    <button
                      type="button"
                      class="file-remove"
                      :aria-label="t('common.remove')"
                      @click="removeUrl(index)"
                    >
                      <t-icon name="close" />
                    </button>
                  </li>
                  <li v-for="(file, index) in localFiles" :key="`${file.name}-${index}`" class="file-item">
                    <span class="file-icon-wrap">
                      <t-icon :name="getFileIcon(file.name)" class="file-icon" />
                    </span>
                    <div class="file-meta">
                      <span class="file-name" :title="file.name">{{ file.name }}</span>
                      <span class="file-size">
                        {{ formatFileSize(file.size) }}
                        <template v-if="fileRelativeDir(file)">
                          · <span class="file-relative-dir" :title="fileRelativeDir(file)">{{ fileRelativeDir(file) }}</span>
                        </template>
                      </span>
                    </div>
                    <button
                      type="button"
                      class="file-remove"
                      :aria-label="t('common.remove')"
                      @click="removeFile(index)"
                    >
                      <t-icon name="close" />
                    </button>
                  </li>
                </ul>
                <div v-else-if="mode === 'file'" class="files-empty">{{ t('uploadConfirm.noItems') }}</div>
              </div>
            </aside>

            <aside class="settings-sidebar">
              <div class="settings-sidebar-header">
                <h2 class="settings-sidebar-title">{{ t('uploadConfirm.parseConfig') }}</h2>
              </div>
              <nav class="settings-nav" :aria-label="t('uploadConfirm.configNav')">
                <button
                  v-for="item in navItems"
                  :key="item.key"
                  type="button"
                  class="nav-item"
                  :class="{
                    active: activeSection === item.key,
                    'nav-item--issue': item.issue,
                  }"
                  @click="activeSection = item.key"
                >
                  <t-icon :name="item.icon" class="nav-icon" />
                  <span class="nav-label-wrap">
                    <span class="nav-label">{{ item.label }}</span>
                    <span
                      class="nav-status"
                      :class="{
                        'nav-status--warning': item.statusTone === 'warning',
                        'nav-status--error': item.statusTone === 'error',
                        'nav-status--muted': item.statusTone === 'muted',
                      }"
                      :title="item.statusFull"
                    >{{ item.status }}</span>
                  </span>
                  <span v-if="item.issue" class="nav-dot" aria-hidden="true" />
                </button>
              </nav>
            </aside>

            <div class="config-panel">
              <div class="content-wrapper upload-confirm-content">
                  <div v-show="activeSection === 'tags'" class="section">
                    <div class="section-content">
                      <div class="section-header">
                        <h2 class="section-title">{{ t('uploadConfirm.tabTags') }}</h2>
                        <p class="section-desc">{{ t('uploadConfirm.tagsDescription') }}</p>
                      </div>
                      <div class="settings-group">
                        <div class="setting-row setting-row-vertical">
                          <div class="setting-info">
                            <label>{{ t('uploadConfirm.tagsPlaceholder') }}</label>
                          </div>
                          <div class="setting-control setting-control-full">
                            <t-select
                              v-model="selectedTagIds"
                              :options="tagOptions"
                              :loading="tagsLoading"
                              multiple
                              filterable
                              clearable
                              :placeholder="t('uploadConfirm.tagsPlaceholder')"
                            />
                            <p v-if="tagsLoadFailed" class="field-hint field-hint--error">
                              {{ t('uploadConfirm.tagsLoadFailed') }}
                            </p>
                            <p v-else-if="!tagsLoading && tagOptions.length === 0" class="field-hint">
                              {{ t('uploadConfirm.tagsEmpty') }}
                            </p>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>

                  <div v-show="activeSection === 'parser'" class="section">
                    <KBParserSettings
                      :relevant-extensions="batchFileExts"
                      :parser-engine-rules="uiState.chunkingConfig.parserEngineRules"
                      @update:parser-engine-rules="handleParserEngineRulesUpdate"
                    />
                    <div v-if="hasPdf" class="kb-settings-block">
                      <div class="settings-group">
                        <div class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('uploadConfirm.pdfForceScanned.label') }}</label>
                            <p class="desc">{{ t('uploadConfirm.pdfForceScanned.description') }}</p>
                          </div>
                          <div class="setting-control">
                            <t-switch v-model="uiState.pdfForceScanned" size="medium" />
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>

                  <div v-show="activeSection === 'chunking'" class="section">
                    <div class="section-content">
                      <div class="section-header">
                        <h2 class="section-title">{{ t('knowledgeEditor.chunking.title') }}</h2>
                        <p class="section-desc">{{ t('knowledgeEditor.chunking.description') }}</p>
                      </div>
                      <div class="settings-group">
                        <div class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.chunking.strategyLabel') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.chunking.strategyDescription') }}</p>
                          </div>
                          <div class="setting-control">
                            <t-select
                              v-model="uiState.chunkingConfig.strategy"
                              :options="chunkingStrategyOptions"
                              :clearable="false"
                              :style="{ width: '280px' }"
                            />
                          </div>
                        </div>
                        <div class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.chunking.sizeLabel') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.chunking.sizeDescription') }}</p>
                          </div>
                          <div class="setting-control">
                            <t-input-number
                              v-model="uiState.chunkingConfig.chunkSize"
                              :min="100"
                              :max="4000"
                              :step="50"
                              theme="normal"
                              :style="{ width: '200px' }"
                            />
                          </div>
                        </div>
                        <div class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.chunking.overlapLabel') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.chunking.overlapDescription') }}</p>
                          </div>
                          <div class="setting-control">
                            <t-input-number
                              v-model="uiState.chunkingConfig.chunkOverlap"
                              :min="0"
                              :max="500"
                              :step="20"
                              theme="normal"
                              :style="{ width: '200px' }"
                            />
                          </div>
                        </div>
                      </div>

                      <button
                        type="button"
                        class="more-options-toggle"
                        :aria-expanded="chunkingMoreOpen"
                        @click="chunkingMoreOpen = !chunkingMoreOpen"
                      >
                        <t-icon name="chevron-down" :class="{ 'is-open': chunkingMoreOpen }" />
                        <span>{{ t('uploadConfirm.moreOptions') }}</span>
                      </button>

                      <div v-if="chunkingMoreOpen" class="settings-group settings-group--more">
                        <div class="setting-row setting-row--separators">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.chunking.separatorsLabel') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.chunking.separatorsDescription') }}</p>
                          </div>
                          <div class="setting-control">
                            <t-select
                              v-model="uiState.chunkingConfig.separators"
                              :options="separatorOptions"
                              multiple
                              creatable
                              filterable
                              :style="{ width: '280px' }"
                            />
                          </div>
                        </div>
                        <div class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.chunking.tokenLimitLabel') }}</label>
                          </div>
                          <div class="setting-control">
                            <t-input-number
                              v-model="uiState.chunkingConfig.tokenLimit"
                              :min="0"
                              :max="8192"
                              :step="64"
                              theme="normal"
                              :style="{ width: '200px' }"
                            />
                          </div>
                        </div>
                        <div class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.chunking.languagesLabel') }}</label>
                          </div>
                          <div class="setting-control">
                            <t-select
                              v-model="uiState.chunkingConfig.languages"
                              :options="languageOptions"
                              multiple
                              :style="{ width: '280px' }"
                            />
                          </div>
                        </div>
                        <div class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.chunking.parentChildLabel') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.chunking.parentChildDescription') }}</p>
                          </div>
                          <div class="setting-control">
                            <t-switch v-model="uiState.chunkingConfig.enableParentChild" />
                          </div>
                        </div>
                        <div v-if="uiState.chunkingConfig.enableParentChild" class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.chunking.parentChunkSizeLabel') }}</label>
                          </div>
                          <div class="setting-control">
                            <t-input-number v-model="uiState.chunkingConfig.parentChunkSize" :min="512" :max="8192" :step="64" theme="normal" :style="{ width: '200px' }" />
                          </div>
                        </div>
                        <div v-if="uiState.chunkingConfig.enableParentChild" class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.chunking.childChunkSizeLabel') }}</label>
                          </div>
                          <div class="setting-control">
                            <t-input-number v-model="uiState.chunkingConfig.childChunkSize" :min="64" :max="2048" :step="32" theme="normal" :style="{ width: '200px' }" />
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>

                  <div v-show="activeSection === 'multimodal'" class="section" data-section="multimodal">
                    <div class="kb-settings-block">
                      <div class="section-header">
                        <h2 class="section-title">{{ t('knowledgeEditor.multimodal.title') }}</h2>
                        <p class="section-desc">{{ t('knowledgeEditor.multimodal.description') }}</p>
                      </div>
                      <div v-if="issueSectionKeys.has('multimodal')" class="section-notice">
                        <t-icon name="info-circle-filled" />
                        <span>{{ t('uploadConfirm.multimodalSetupHint') }}</span>
                      </div>
                      <div class="settings-group">
                        <div class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.advanced.multimodal.label') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.description') }}</p>
                          </div>
                          <div class="setting-control">
                            <t-switch v-model="uiState.multimodalConfig.enabled" size="medium" />
                          </div>
                        </div>
                        <div v-if="uiState.multimodalConfig.enabled" class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.advanced.multimodal.vllmLabel') }} <span class="required">*</span></label>
                            <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.vllmDescription') }}</p>
                          </div>
                          <div class="setting-control">
                            <ModelSelector
                              model-type="VLLM"
                              :selected-model-id="uiState.multimodalConfig.vllmModelId"
                              :all-models="allModels"
                              :status="showMultimodalModelError ? 'error' : 'default'"
                              :placeholder="t('knowledgeEditor.advanced.multimodal.vllmPlaceholder')"
                              @update:selected-model-id="handleMultimodalVLLMChange"
                              @add-model="handleAddVLLMModel"
                            />
                          </div>
                        </div>
                        <div v-if="uiState.multimodalConfig.enabled" class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.advanced.multimodal.descriptionLanguageLabel') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.descriptionLanguageDescription') }}</p>
                          </div>
                          <div class="setting-control">
                            <t-select
                              v-model="uiState.multimodalConfig.descriptionLanguage"
                              clearable
                              :placeholder="t('knowledgeEditor.advanced.multimodal.descriptionLanguageAuto')"
                              :style="{ width: '280px' }"
                            >
                              <t-option value="Chinese" :label="t('language.zhCN')" />
                              <t-option value="English" :label="t('language.enUS')" />
                              <t-option value="Korean" :label="t('language.koKR')" />
                              <t-option value="Russian" :label="t('language.ruRU')" />
                            </t-select>
                          </div>
                        </div>
                        <div v-if="uiState.multimodalConfig.enabled" class="setting-row setting-row-vertical">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.advanced.multimodal.customInstructionsLabel') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.customInstructionsDescription') }}</p>
                          </div>
                          <div class="setting-control setting-control-full">
                            <t-textarea
                              v-model="uiState.multimodalConfig.customInstructions"
                              :placeholder="t('knowledgeEditor.advanced.multimodal.customInstructionsPlaceholder')"
                              :maxlength="4000"
                              :autosize="{ minRows: 3, maxRows: 8 }"
                            />
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>

                  <div v-show="activeSection === 'asr'" class="section" data-section="asr">
                    <div class="kb-settings-block">
                      <div class="section-header">
                        <h2 class="section-title">{{ t('knowledgeEditor.asr.title') }}</h2>
                        <p class="section-desc">{{ t('knowledgeEditor.asr.description') }}</p>
                      </div>
                      <div v-if="issueSectionKeys.has('asr')" class="section-notice">
                        <t-icon name="info-circle-filled" />
                        <span>{{ t('uploadConfirm.asrSetupHint') }}</span>
                      </div>
                      <div class="settings-group">
                        <div class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.asr.label') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.asr.desc') }}</p>
                          </div>
                          <div class="setting-control">
                            <t-switch v-model="uiState.asrConfig.enabled" size="medium" />
                          </div>
                        </div>
                        <div v-if="uiState.asrConfig.enabled" class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.asr.modelLabel') }} <span class="required">*</span></label>
                            <p class="desc">{{ t('knowledgeEditor.asr.modelDescription') }}</p>
                          </div>
                          <div class="setting-control">
                            <ModelSelector
                              model-type="ASR"
                              :selected-model-id="uiState.asrConfig.modelId"
                              :all-models="allModels"
                              :status="showAsrModelError ? 'error' : 'default'"
                              :placeholder="t('knowledgeEditor.asr.modelPlaceholder')"
                              @update:selected-model-id="(val: string) => { uiState.asrConfig.modelId = val }"
                              @add-model="handleAddASRModel"
                            />
                          </div>
                        </div>
                        <div v-if="uiState.asrConfig.enabled" class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.asr.languageLabel') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.asr.languageDescription') }}</p>
                          </div>
                          <div class="setting-control">
                            <t-input
                              v-model="uiState.asrConfig.language"
                              clearable
                              :placeholder="t('knowledgeEditor.asr.languagePlaceholder')"
                              :style="{ width: '280px' }"
                            />
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>

                  <div v-show="activeSection === 'question'" class="section">
                    <div class="kb-settings-block">
                      <div class="section-header">
                        <h2 class="section-title">{{ t('knowledgeEditor.advanced.questionGeneration.label') }}</h2>
                        <p class="section-desc">{{ t('knowledgeEditor.advanced.questionGeneration.description') }}</p>
                      </div>
                      <div class="settings-group">
                        <div class="setting-row">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.advanced.questionGeneration.label') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.advanced.questionGeneration.countDescription') }}</p>
                          </div>
                          <div class="setting-control setting-control-inline">
                            <t-input-number
                              v-if="uiState.questionGenerationConfig.enabled"
                              v-model="uiState.questionGenerationConfig.questionCount"
                              :min="1"
                              :max="10"
                              :step="1"
                              theme="normal"
                              :style="{ width: '88px' }"
                            />
                            <t-switch v-model="uiState.questionGenerationConfig.enabled" size="medium" />
                          </div>
                        </div>
                        <div v-if="uiState.questionGenerationConfig.enabled" class="setting-row setting-row-vertical">
                          <div class="setting-info">
                            <label>{{ t('knowledgeEditor.advanced.questionGeneration.instructionsLabel') }}</label>
                            <p class="desc">{{ t('knowledgeEditor.advanced.questionGeneration.instructionsDescription') }}</p>
                          </div>
                          <div class="setting-control setting-control-full">
                            <t-textarea
                              v-model="uiState.questionGenerationConfig.customInstructions"
                              :placeholder="t('knowledgeEditor.advanced.questionGeneration.instructionsPlaceholder')"
                              :maxlength="4000"
                              :autosize="{ minRows: 3, maxRows: 8 }"
                            />
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>

                  <div v-if="isGraphSectionAvailable" v-show="activeSection === 'graph'" class="section">
                    <GraphSettings
                      :graph-extract="uiState.nodeExtractConfig"
                      :model-id="llmModelId"
                      :all-models="allModels"
                      @update:graphExtract="handleNodeExtractUpdate"
                    />
                  </div>
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
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import ModelSelector from '@/components/ModelSelector.vue'
import KBParserSettings from '../settings/KBParserSettings.vue'
import GraphSettings from '../settings/GraphSettings.vue'
import { useChatResourcesStore } from '@/stores/chatResources'
import { useEditorResourcesStore } from '@/stores/editorResources'
import { useUIStore } from '@/stores/ui'
import { formatFileSize, getFileIcon } from '@/utils/files'
import { getUploadFileKey } from '../utils/uploadSources'
import { listKnowledgeTags } from '@/api/knowledge-base'
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

type ConfigSectionKey = 'tags' | 'parser' | 'chunking' | 'multimodal' | 'asr' | 'question' | 'graph'
type IssueSectionKey = 'multimodal' | 'asr'

interface ChunkingUIConfig {
  chunkSize: number
  chunkOverlap: number
  separators: string[]
  parserEngineRules?: Array<{
    file_types: string[]
    engine: string
    xlsx_first_row_as_header?: boolean
  }>
  enableParentChild: boolean
  parentChunkSize: number
  childChunkSize: number
  strategy?: string
  tokenLimit?: number
  languages?: string[]
  tableMetadataInstructions?: string
}

interface UploadUIState {
  chunkingConfig: ChunkingUIConfig
  multimodalConfig: { enabled: boolean; vllmModelId: string; descriptionLanguage?: string; customInstructions?: string }
  asrConfig: { enabled: boolean; modelId: string; language: string }
  questionGenerationConfig: { enabled: boolean; questionCount: number; customInstructions?: string }
  nodeExtractConfig: {
    enabled: boolean
    text: string
    tags: string[]
    nodes: Array<{ name: string; attributes: string[] }>
    relations: Array<{ node1: string; node2: string; type: string }>
    customInstructions?: string
  }
  graphEnabled: boolean
  pdfForceScanned: boolean
}

const props = withDefaults(defineProps<{
  visible: boolean
  kbInfo: any
  mode?: UploadConfirmMode
  files?: File[]
  urls?: string[]
  tagIds?: string[]
  manualPreview?: UploadConfirmManualSource | null
  reparsePreview?: UploadConfirmReparseSource | null
  tagId?: string
  acceptFileTypes?: string
  supportedFileTypes?: string[]
  /** Folder the batch will be uploaded into; '' means the knowledge base root. */
  targetFolder?: string
}>(), {
  mode: 'file',
  files: () => [],
  urls: () => [],
  tagIds: () => [],
  manualPreview: null,
  reparsePreview: null,
  acceptFileTypes: '',
  supportedFileTypes: () => [],
  targetFolder: '',
})

const emit = defineEmits<{
  'update:visible': [value: boolean]
  confirm: [payload: UploadConfirmResult]
  cancel: []
}>()

const { t } = useI18n()
const chatResources = useChatResourcesStore()
const editorResources = useEditorResourcesStore()
const uiStore = useUIStore()

const allModels = ref<any[]>([])
const localFiles = ref<File[]>([])
const localUrls = ref<string[]>([])
const availableTags = ref<Array<{ id: string; name: string }>>([])
const selectedTagIds = ref<string[]>([])
const tagsLoading = ref(false)
const tagsLoadFailed = ref(false)
const chunkingMoreOpen = ref(false)
const activeSection = ref<ConfigSectionKey>('tags')
const uiState = ref<UploadUIState>(createDefaultUIState())
// Destination folder for this batch. Pre-filled from the sidebar tree, but
// editable here so browsing a folder never silently decides where files land.
const localTargetFolder = ref('')

const dialogVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

const destinationText = computed(() =>
  localTargetFolder.value || t('uploadConfirm.destinationRoot'),
)

const destinationTitle = computed(() =>
  localTargetFolder.value
    ? t('uploadConfirm.destinationTip', { folder: localTargetFolder.value })
    : t('uploadConfirm.destinationRootTip'),
)

/**
 * Directory a folder-upload file came from, shown under its name so a batch of
 * same-named files (README.md in five folders) stays distinguishable and the
 * resulting structure is visible before confirming.
 */
function fileRelativeDir(file: File): string {
  const relativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath
  if (!relativePath) return ''
  return relativePath.split('/').filter(Boolean).slice(0, -1).join('/')
}

function getModelName(modelId: string): string {
  if (!modelId) return t('uploadConfirm.notSet')
  const model = allModels.value.find((m: any) => m.id === modelId)
  return model?.name || modelId
}

function truncateNavText(text: string, max = 18): string {
  if (!text) return text
  if (text.length <= max) return text
  return `${text.slice(0, max - 1)}…`
}

function hasParserCustomization(): boolean {
  const rules = uiState.value.chunkingConfig.parserEngineRules
  if (!rules?.length) return false
  return rules.some(rule => rule.engine && rule.engine !== 'builtin')
    || rules.some(rule => rule.xlsx_first_row_as_header)
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

const dialogTitle = computed(() => {
  if (props.mode === 'manual') return t('uploadConfirm.titleManual')
  if (props.mode === 'reparse') return t('uploadConfirm.titleReparse')
  return t('uploadConfirm.title')
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

const hasPdf = computed(() => batchFileExts.value.includes('pdf'))

const chunkingStrategyOptions = computed(() => [
  { label: t('knowledgeEditor.chunking.strategies.auto.label'), value: 'auto' },
  { label: t('knowledgeEditor.chunking.strategies.heading.label'), value: 'heading' },
  { label: t('knowledgeEditor.chunking.strategies.heuristic.label'), value: 'heuristic' },
  { label: t('knowledgeEditor.chunking.strategies.legacy.label'), value: 'legacy' },
])

const separatorOptions = computed(() => [
  { label: t('knowledgeEditor.chunking.separators.doubleNewline'), value: '\n\n' },
  { label: t('knowledgeEditor.chunking.separators.singleNewline'), value: '\n' },
  { label: t('knowledgeEditor.chunking.separators.periodCn'), value: '。' },
  { label: t('knowledgeEditor.chunking.separators.exclamationCn'), value: '！' },
  { label: t('knowledgeEditor.chunking.separators.questionCn'), value: '？' },
  { label: t('knowledgeEditor.chunking.separators.semicolonCn'), value: '；' },
  { label: t('knowledgeEditor.chunking.separators.semicolonEn'), value: ';' },
  { label: t('knowledgeEditor.chunking.separators.space'), value: ' ' },
])

const languageOptions = computed(() => [
  { label: t('knowledgeEditor.chunking.languageOptions.de'), value: 'de' },
  { label: t('knowledgeEditor.chunking.languageOptions.en'), value: 'en' },
  { label: t('knowledgeEditor.chunking.languageOptions.zh'), value: 'zh' },
])

const tagOptions = computed(() => availableTags.value.map(tag => ({
  label: tag.name,
  value: tag.id,
})))

const llmModelId = computed(() => props.kbInfo?.summary_model_id || '')

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

const isGraphDatabaseEnabled = computed(() => {
  const engine = editorResources.systemInfo?.graph_database_engine
  return !!engine && engine !== 'Not Enabled'
})

const isGraphSectionAvailable = computed(() => {
  return isGraphDatabaseEnabled.value && uiState.value.graphEnabled
})

const showMultimodalModelError = computed(() => {
  return uiState.value.multimodalConfig.enabled && !uiState.value.multimodalConfig.vllmModelId
})

const showAsrModelError = computed(() => {
  return uiState.value.asrConfig.enabled && !uiState.value.asrConfig.modelId
})

const issueSectionKeys = computed(() => {
  const keys = new Set<IssueSectionKey>()
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

const navItems = computed(() => {
  const items: Array<{
    key: ConfigSectionKey
    icon: string
    label: string
    status: string
    statusFull: string
    statusTone?: 'warning' | 'error' | 'muted'
    issue?: boolean
  }> = []

  const push = (key: ConfigSectionKey, icon: string, label: string, issue?: boolean) => {
    const statusMeta = getSectionNavStatus(key, issue)
    const full = statusMeta.status
    items.push({
      key,
      icon,
      label,
      status: truncateNavText(full),
      statusFull: full,
      statusTone: statusMeta.statusTone,
      issue,
    })
  }

  if (props.mode !== 'reparse') {
    push('tags', 'tag', t('uploadConfirm.tabTags'))
  }
  push('parser', 'file-search', t('settings.parserEngine'))
  push('chunking', 'file-copy', t('knowledgeEditor.sidebar.chunking'))
  push(
    'multimodal',
    'image',
    t('knowledgeEditor.sidebar.multimodal'),
    issueSectionKeys.value.has('multimodal'),
  )
  push(
    'asr',
    'sound',
    t('knowledgeEditor.sidebar.asr'),
    issueSectionKeys.value.has('asr'),
  )
  push('question', 'chat', t('knowledgeEditor.advanced.questionGeneration.label'))
  if (isGraphSectionAvailable.value) {
    push('graph', 'chart-bubble', t('knowledgeEditor.sidebar.graph'))
  }
  return items
})

function getSectionNavStatus(
  key: ConfigSectionKey,
  issue?: boolean,
): { status: string; statusTone?: 'warning' | 'error' | 'muted' } {
  switch (key) {
    case 'tags':
      if (selectedTagIds.value.length === 0) {
        return { status: t('uploadConfirm.summaryNoTags'), statusTone: 'muted' }
      }
      return {
        status: t('uploadConfirm.summaryTagsCount', { count: selectedTagIds.value.length }),
      }
    case 'parser':
      if (uiState.value.pdfForceScanned && hasPdf.value) {
        return { status: t('uploadConfirm.summaryParserForceScanned') }
      }
      if (hasParserCustomization()) {
        return { status: t('uploadConfirm.navParserCustomized') }
      }
      return { status: t('uploadConfirm.navParserDefault'), statusTone: 'muted' }
    case 'chunking': {
      const chunking = uiState.value.chunkingConfig
      const parts = [t('uploadConfirm.navChunkingSummary', { size: chunking.chunkSize })]
      if (chunking.enableParentChild) {
        parts.push(t('uploadConfirm.summaryParentChildShort'))
      }
      return { status: parts.join(' · ') }
    }
    case 'multimodal': {
      if (issue) {
        return { status: t('uploadConfirm.statusNeedsSetup'), statusTone: 'error' }
      }
      const mm = uiState.value.multimodalConfig
      if (!mm.enabled) {
        return { status: t('uploadConfirm.statusOff'), statusTone: 'muted' }
      }
      return {
        status: mm.vllmModelId ? getModelName(mm.vllmModelId) : t('uploadConfirm.notSet'),
        statusTone: mm.vllmModelId ? undefined : 'warning',
      }
    }
    case 'asr': {
      if (issue) {
        return { status: t('uploadConfirm.statusNeedsSetup'), statusTone: 'error' }
      }
      const asr = uiState.value.asrConfig
      if (!asr.enabled) {
        return { status: t('uploadConfirm.statusOff'), statusTone: 'muted' }
      }
      return {
        status: asr.modelId ? getModelName(asr.modelId) : t('uploadConfirm.notSet'),
        statusTone: asr.modelId ? undefined : 'warning',
      }
    }
    case 'question': {
      const question = uiState.value.questionGenerationConfig
      if (!question.enabled) {
        return { status: t('uploadConfirm.statusOff'), statusTone: 'muted' }
      }
      return {
        status: t('uploadConfirm.summaryQuestionCountValue', { count: question.questionCount }),
      }
    }
    case 'graph': {
      if (!uiState.value.graphEnabled || !uiState.value.nodeExtractConfig.enabled) {
        return { status: t('uploadConfirm.statusOff'), statusTone: 'muted' }
      }
      const tagCount = uiState.value.nodeExtractConfig.tags?.length ?? 0
      if (tagCount > 0) {
        return { status: t('uploadConfirm.summaryGraphTagsValue', { count: tagCount }) }
      }
      return { status: t('uploadConfirm.statusOn') }
    }
    default:
      return { status: '' }
  }
}

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

function getDefaultSection(): ConfigSectionKey {
  if (props.mode === 'reparse') return 'parser'
  if (issueSectionKeys.value.has('multimodal')) return 'multimodal'
  if (issueSectionKeys.value.has('asr')) return 'asr'
  return 'tags'
}

function goToSection(key: ConfigSectionKey) {
  activeSection.value = key
  nextTick(() => {
    document.querySelector(`[data-section="${key}"]`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  })
}

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
      tableMetadataInstructions: '',
    },
    multimodalConfig: { enabled: false, vllmModelId: '', descriptionLanguage: '', customInstructions: '' },
    asrConfig: { enabled: false, modelId: '', language: '' },
    questionGenerationConfig: { enabled: true, questionCount: 3, customInstructions: '' },
    nodeExtractConfig: {
      enabled: false,
      text: '',
      tags: [],
      nodes: [],
      relations: [],
      customInstructions: '',
    },
    graphEnabled: false,
    pdfForceScanned: false,
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
      tableMetadataInstructions: kb.chunking_config?.table_metadata_instructions || '',
    },
    multimodalConfig: {
      enabled: !!kb.vlm_config?.enabled,
      vllmModelId: kb.vlm_config?.model_id || '',
      descriptionLanguage: kb.vlm_config?.description_language || '',
      customInstructions: kb.vlm_config?.custom_instructions || '',
    },
    asrConfig: {
      enabled: !!kb.asr_config?.enabled,
      modelId: kb.asr_config?.model_id || '',
      language: kb.asr_config?.language || '',
    },
    questionGenerationConfig: {
      enabled: kb.question_generation_config?.enabled ?? true,
      questionCount: kb.question_generation_config?.question_count || 3,
      customInstructions: kb.question_generation_config?.custom_instructions || '',
    },
    nodeExtractConfig: {
      enabled: !!kb.extract_config?.enabled && !!kb.indexing_strategy?.graph_enabled,
      text: kb.extract_config?.text || '',
      tags: kb.extract_config?.tags || [],
      nodes: (kb.extract_config?.nodes || []).map((node: any) => ({
        name: node.name,
        attributes: node.attributes || [],
      })),
      relations: kb.extract_config?.relations || [],
      customInstructions: kb.extract_config?.custom_instructions || '',
    },
    graphEnabled: kb.indexing_strategy?.graph_enabled ?? false,
    pdfForceScanned: false,
  }
}

function buildProcessOverrides(): KnowledgeProcessOverrides {
  const state = uiState.value
  const chunking = state.chunkingConfig

  const overrides: KnowledgeProcessOverrides = {
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
      table_metadata_instructions: chunking.tableMetadataInstructions,
    },
    enable_multimodel: state.multimodalConfig.enabled,
    vlm_config: {
      enabled: state.multimodalConfig.enabled,
      model_id: state.multimodalConfig.vllmModelId,
      description_language: state.multimodalConfig.descriptionLanguage,
      custom_instructions: state.multimodalConfig.customInstructions,
    },
    asr_config: {
      enabled: state.asrConfig.enabled,
      model_id: state.asrConfig.modelId,
      language: state.asrConfig.language,
    },
    question_generation_config: {
      enabled: state.questionGenerationConfig.enabled,
      question_count: state.questionGenerationConfig.questionCount,
      custom_instructions: state.questionGenerationConfig.customInstructions,
    },
    graph_enabled: state.nodeExtractConfig.enabled && state.graphEnabled,
    extract_config: {
      enabled: state.nodeExtractConfig.enabled,
      text: state.nodeExtractConfig.text,
      tags: state.nodeExtractConfig.tags,
      nodes: state.nodeExtractConfig.nodes,
      relations: state.nodeExtractConfig.relations,
      custom_instructions: state.nodeExtractConfig.customInstructions,
    },
  }

  if (state.pdfForceScanned) {
    overrides.parser_engine_overrides = {
      pdf_force_scanned: 'true',
    }
  }

  return overrides
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
    if (cc.table_metadata_instructions != null) s.chunkingConfig.tableMetadataInstructions = cc.table_metadata_instructions
    if (cc.parser_engine_rules) s.chunkingConfig.parserEngineRules = cc.parser_engine_rules
  }
  if (o.parser_engine_rules) s.chunkingConfig.parserEngineRules = o.parser_engine_rules
  if (o.enable_multimodel != null) s.multimodalConfig.enabled = o.enable_multimodel
  if (o.vlm_config) {
    if (o.vlm_config.enabled != null) s.multimodalConfig.enabled = o.vlm_config.enabled
    if (o.vlm_config.model_id != null) s.multimodalConfig.vllmModelId = o.vlm_config.model_id
    if (o.vlm_config.description_language != null) s.multimodalConfig.descriptionLanguage = o.vlm_config.description_language
    if (o.vlm_config.custom_instructions != null) s.multimodalConfig.customInstructions = o.vlm_config.custom_instructions
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
    if (qg.custom_instructions != null) s.questionGenerationConfig.customInstructions = qg.custom_instructions
  }
  const ec = o.extract_config
  if (ec) {
    if (ec.enabled != null) s.nodeExtractConfig.enabled = ec.enabled
    if (ec.text != null) s.nodeExtractConfig.text = ec.text
    if (ec.tags) s.nodeExtractConfig.tags = ec.tags
    if (ec.nodes) s.nodeExtractConfig.nodes = ec.nodes.map(n => ({ name: n.name, attributes: n.attributes || [] }))
    if (ec.relations) s.nodeExtractConfig.relations = ec.relations
    if (ec.custom_instructions != null) s.nodeExtractConfig.customInstructions = ec.custom_instructions
  }
  if (o.graph_enabled != null) s.graphEnabled = o.graph_enabled
  s.nodeExtractConfig.enabled = s.nodeExtractConfig.enabled && s.graphEnabled
  if (o.parser_engine_overrides && o.parser_engine_overrides.pdf_force_scanned === 'true') {
    s.pdfForceScanned = true
  } else {
    s.pdfForceScanned = false
  }
}

async function loadModels() {
  try {
    await chatResources.ensureModels()
    allModels.value = chatResources.allModels || []
  } catch {
    allModels.value = []
  }
}

async function loadSystemInfo() {
  try {
    await editorResources.ensureSystemInfo()
  } catch {
    // Graph section falls back to hidden when system info is unavailable.
  }
}

async function loadTags() {
  const kbId = props.kbInfo?.id
  availableTags.value = []
  tagsLoadFailed.value = false
  if (!kbId || props.mode === 'reparse') return

  tagsLoading.value = true
  try {
    const response: any = await listKnowledgeTags(kbId, { page: 1, page_size: 1000 })
    const tags = response?.data?.data || []
    availableTags.value = tags.map((tag: any) => ({
      id: String(tag.id),
      name: String(tag.name || ''),
    }))
  } catch {
    tagsLoadFailed.value = true
  } finally {
    tagsLoading.value = false
  }
}

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    localFiles.value = props.mode === 'file' ? [...(props.files || [])] : []
    localUrls.value = props.mode === 'file' ? [...(props.urls || [])] : []
    selectedTagIds.value = props.mode === 'reparse' ? [] : [...(props.tagIds || [])]
    localTargetFolder.value = props.mode === 'file' ? (props.targetFolder || '') : ''
    initFromKbInfo(props.kbInfo)
    if (props.mode === 'reparse') {
      applyOverridesToState(props.reparsePreview?.processOverrides)
    }
    activeSection.value = getDefaultSection()
    chunkingMoreOpen.value = false
    loadModels()
    loadSystemInfo()
    loadTags()
  },
)

watch(isGraphSectionAvailable, (available) => {
  if (!available && activeSection.value === 'graph') {
    activeSection.value = getDefaultSection()
  }
})

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

const handleParserEngineRulesUpdate = (rules: Array<{
  file_types: string[]
  engine: string
  xlsx_first_row_as_header?: boolean
}>) => {
  uiState.value.chunkingConfig.parserEngineRules = rules
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

const handleNodeExtractUpdate = (config: UploadUIState['nodeExtractConfig']) => {
  uiState.value.nodeExtractConfig = { ...config }
  uiState.value.graphEnabled = config.enabled
}

const validateBeforeConfirm = (): boolean => {
  if (hasImages.value) {
    if (!uiState.value.multimodalConfig.enabled || !uiState.value.multimodalConfig.vllmModelId) {
      MessagePlugin.warning(t('uploadConfirm.vlmModelRequired'))
      uiState.value.multimodalConfig.enabled = true
      goToSection('multimodal')
      return false
    }
  } else if (showMultimodalModelError.value) {
    MessagePlugin.warning(t('uploadConfirm.vlmModelSelectRequired'))
    goToSection('multimodal')
    return false
  }

  if (hasAudio.value) {
    if (!uiState.value.asrConfig.enabled || !uiState.value.asrConfig.modelId) {
      MessagePlugin.warning(t('uploadConfirm.asrModelRequired'))
      uiState.value.asrConfig.enabled = true
      goToSection('asr')
      return false
    }
  } else if (showAsrModelError.value) {
    MessagePlugin.warning(t('uploadConfirm.asrModelSelectRequired'))
    goToSection('asr')
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
    emit('confirm', {
      processConfig,
      mode: 'manual',
      tagIds: [...selectedTagIds.value],
      manual: { ...props.manualPreview, tagIds: [...selectedTagIds.value] },
    })
  } else if (props.mode === 'reparse' && props.reparsePreview) {
    emit('confirm', { processConfig, mode: 'reparse', reparse: { ...props.reparsePreview } })
  } else {
    emit('confirm', {
      processConfig,
      mode: 'file',
      tagIds: [...selectedTagIds.value],
      files: [...localFiles.value],
      urls: [...localUrls.value],
      targetFolder: localTargetFolder.value,
    })
  }
  emit('update:visible', false)
}
</script>

<style lang="less" scoped>
.upload-confirm-overlay {
  position: fixed;
  inset: 0;
  z-index: 3000;
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
  width: 92vw;
  max-width: 1160px;
  height: 85vh;
  max-height: 750px;
  overflow: hidden;
  border-radius: 12px;
  background: var(--td-bg-color-container);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.12);
}

.close-btn {
  position: absolute;
  top: 20px;
  right: 20px;
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
  background: var(--td-bg-color-settings-modal, var(--td-bg-color-secondarycontainer));
  border-right: 1px solid var(--td-component-stroke);
}

.sidebar-header,
.settings-sidebar-header {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  box-sizing: border-box;
  height: 56px;
  padding: 0 12px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.sidebar-header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  min-width: 0;
}

.sidebar-title {
  margin: 0;
  flex: 1;
  min-width: 0;
  padding-right: 0;
  font-size: 16px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--td-text-color-primary);
}

.sidebar-header-actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 6px;
}

.files-count {
  flex-shrink: 0;
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

.destination-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  margin: 0 8px;
  padding: 6px 8px;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  font-size: 12px;
  line-height: 18px;
}

.destination-icon {
  flex-shrink: 0;
  font-size: 14px;
  color: var(--td-text-color-placeholder);
}

.destination-label {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
}

.destination-path {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  direction: rtl;
  text-align: left;
  color: var(--td-text-color-primary);
}

.destination-clear {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: color 0.15s ease;

  &:hover {
    color: var(--td-text-color-secondary);
  }

  .t-icon {
    font-size: 14px;
  }
}

.files-list-wrap {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  padding: 6px 8px 12px;
  overflow: hidden;
}

.files-list {
  flex: 1;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  list-style: none;
}

.file-item {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 2px;
  padding: 6px 6px 6px 8px;
  border-radius: 6px;
  transition: background-color 0.15s ease;

  &:last-child {
    margin-bottom: 0;
  }

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.file-icon-wrap {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
}

.file-icon {
  font-size: 16px;
  color: var(--td-text-color-secondary);
}

.file-item:hover .file-icon {
  color: var(--td-brand-color);
}

.file-meta {
  flex: 1;
  min-width: 0;
}

.file-name {
  display: block;
  overflow: hidden;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.35;
  color: var(--td-text-color-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-size {
  display: block;
  margin-top: 1px;
  font-size: 11px;
  line-height: 1.3;
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-relative-dir {
  color: var(--td-text-color-secondary);
}

.file-remove {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: opacity 0.15s ease, color 0.15s ease, background-color 0.15s ease;
  opacity: 0.45;

  .file-item:hover &,
  &:focus-visible {
    opacity: 1;
  }

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-component);
  }
}

.files-empty {
  flex: 1;
  padding: 16px 8px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  text-align: center;
}

.manual-source-panel {
  flex: 1;
  min-height: 0;
  padding: 0;
  overflow-y: auto;
}

.manual-source-title {
  margin: 0;
  padding: 8px 10px;
  border-radius: 6px;
  background: var(--td-bg-color-container-hover);
  font-size: 13px;
  font-weight: 500;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.manual-source-meta {
  margin: 6px 2px 0;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}

.config-panel {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
}

.settings-sidebar {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  width: 216px;
  min-height: 0;
  background-color: var(--td-bg-color-settings-modal, var(--td-bg-color-secondarycontainer));
  border-right: 1px solid var(--td-component-stroke);
}

.settings-sidebar-header {
  width: 100%;
}

.settings-sidebar-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--td-text-color-primary);
}

.settings-nav {
  flex: 1;
  padding: 10px 6px 12px;
  overflow-y: auto;
  min-height: 0;
}

.nav-group-title {
  padding: 6px 14px 2px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.02em;

  .settings-nav > &:first-child {
    padding-top: 2px;
  }

  .settings-nav > &:not(:first-child) {
    padding-top: 8px;
  }
}

.nav-item {
  display: flex;
  align-items: flex-start;
  width: 100%;
  margin-bottom: 2px;
  padding: 9px 10px;
  border: none;
  border-radius: 6px;
  background: transparent;
  font-size: 14px;
  color: var(--td-text-color-primary);
  text-align: left;
  cursor: pointer;
  transition: all 0.2s ease;
  user-select: none;

  &:hover {
    background-color: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.active {
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
    font-weight: 500;

    .nav-label {
      color: var(--td-brand-color);
    }

    .nav-status {
      color: var(--td-text-color-secondary);
    }
  }

  &--issue .nav-label {
    color: var(--td-error-color);
  }
}

.nav-icon {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  margin-right: 8px;
  margin-top: 2px;
  font-size: 16px;
  color: inherit;
}

.nav-label-wrap {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.nav-label {
  overflow: hidden;
  font-size: 13px;
  font-weight: 500;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nav-status {
  overflow: hidden;
  font-size: 12px;
  line-height: 1.35;
  color: var(--td-text-color-placeholder);
  text-overflow: ellipsis;
  white-space: nowrap;

  &--muted {
    color: var(--td-text-color-placeholder);
  }

  &--warning {
    color: var(--td-warning-color);
  }

  &--error {
    color: var(--td-error-color);
  }
}

.nav-dot {
  flex-shrink: 0;
  width: 6px;
  height: 6px;
  margin-left: 6px;
  border-radius: 50%;
  background: var(--td-error-color);
}

.content-wrapper {
  flex: 1;
  min-width: 0;
  min-height: 0;
  padding: 22px 32px 28px;
  overflow-y: auto;
  background: var(--td-bg-color-container);
}

.upload-confirm-content {
  .setting-info {
    flex: 0 0 40%;
    max-width: 40%;
  }

  .setting-control {
    flex: 1 1 56%;
    max-width: 56%;
    min-width: 280px;
  }
}

.section-notice {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-bottom: 16px;
  padding: 10px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);

  .t-icon {
    flex-shrink: 0;
    margin-top: 1px;
    font-size: 16px;
    color: var(--td-brand-color);
  }
}

.section {
  margin-bottom: 32px;

  &:last-child {
    margin-bottom: 0;
  }
}

.section-content {
  .section-header {
    margin-bottom: 16px;
  }

  .section-title {
    margin: 0 0 6px;
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .section-desc {
    margin: 0;
    font-size: 14px;
    line-height: 22px;
    color: var(--td-text-color-placeholder);
  }
}

.kb-settings-block {
  width: 100%;

  .section-header {
    margin-bottom: 20px;
  }

  .section-title {
    margin: 0 0 6px;
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .section-desc {
    margin: 0;
    font-size: 14px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;

  &--more {
    margin-top: 4px;
  }
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 16px 0;
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
    display: block;
    margin-bottom: 4px;
    font-size: 15px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .desc {
    margin: 0;
    font-size: 13px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }

  .warn {
    margin: 4px 0 0;
    font-size: 12px;
    line-height: 1.4;
    color: var(--td-warning-color);
  }
}

.setting-control {
  display: flex;
  flex: 0 0 55%;
  max-width: 55%;
  align-items: center;
  justify-content: flex-end;

  &-full {
    flex: none;
    width: 100%;
    max-width: none;
    justify-content: flex-start;
  }

  &-inline {
    gap: 12px;
    justify-content: flex-end;
  }
}

.setting-row-vertical {
  flex-direction: column;
  gap: 12px;

  .setting-info,
  .setting-control {
    flex: none;
    width: 100%;
    max-width: none;
    padding-right: 0;
  }

  .setting-control {
    display: block;
  }
}

.required {
  margin-left: 2px;
  font-weight: 500;
  color: var(--td-error-color);
}

.field-hint {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);

  &--error {
    color: var(--td-error-color);
  }
}

.more-options-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-top: 4px;
  padding: 6px 0;
  border: none;
  background: transparent;
  color: var(--td-brand-color);
  font-size: 13px;
  cursor: pointer;

  .t-icon {
    transition: transform 0.18s ease;

    &.is-open {
      transform: rotate(180deg);
    }
  }
}

:deep(.t-input-number) {
  width: 100%;
}

.modal-footer {
  display: flex;
  flex-shrink: 0;
  justify-content: flex-end;
  gap: 12px;
  padding: 14px 20px;
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

@media (max-width: 800px) {
  .upload-confirm-container {
    flex-direction: column;
  }

  .files-panel {
    width: auto;
    max-height: 140px;
    border-right: none;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  .settings-sidebar {
    width: auto;
    border-right: none;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  .settings-nav {
    display: flex;
    flex-wrap: nowrap;
    gap: 4px;
    flex: none;
    padding: 8px;
    overflow-x: auto;
  }

  .nav-group-title {
    display: none;
  }

  .nav-item {
    flex: 0 0 auto;
    width: auto;
    margin-bottom: 0;
    white-space: nowrap;
  }

  .content-wrapper {
    padding: 16px;
  }

  .setting-info,
  .setting-control {
    flex: 1 1 100%;
    max-width: none;
  }

  .setting-row {
    flex-direction: column;
    gap: 12px;
  }
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
