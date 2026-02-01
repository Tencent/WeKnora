<template>
  <div class="agent-settings">
    <div v-if="activeSection === 'modes'">
      <div class="section-header">
        <h2>{{ $t('settings.conversationStrategy') }}</h2>
        <p class="section-description">{{ $t('conversationSettings.description') }}</p>
        <div class="global-config-notice">
          <t-icon name="info-circle" />
          <span>这些是全局默认配置，新建智能体时会继承这些设置。您也可以在智能体列表中单独配置每个智能体。</span>
        </div>
      </div>

      <t-tabs v-model="activeTab" class="conversation-tabs">
      <!-- Agent 模式设置 Tab -->
      <t-tab-panel value="agent" :label="$t('conversationSettings.agentMode')">
        <div class="tab-content">
          <!-- Agent 状态显示 -->
          <div class="agent-status-row">
        <div class="status-label">
          <label>{{ $t('agentSettings.status.label') }}</label>
        </div>
        <div class="status-control">
          <div class="status-badge" :class="{ ready: isAgentReady }">
            <t-icon 
              v-if="isAgentReady" 
              name="check-circle-filled" 
              class="status-icon"
            />
            <t-icon 
              v-else 
              name="error-circle-filled" 
              class="status-icon"
            />
            <span class="status-text">
              {{ isAgentReady ? $t('agentSettings.status.ready') : $t('agentSettings.status.notReady') }}
            </span>
          </div>
          <span v-if="!isAgentReady" class="status-hint">
            {{ agentStatusMessage }}
            <t-link v-if="needsModelConfig" @click="handleGoToModelSettings" theme="primary">
              {{ $t('agentSettings.status.goConfigureModels') }}
            </t-link>
          </span>
          <p v-if="!isAgentReady" class="status-tip">
            <t-icon name="info-circle" class="tip-icon" />
            {{ $t('agentSettings.status.hint') }}
          </p>
        </div>
      </div>

          <!-- 模型推荐提示 -->
          <div class="model-recommendation-box">
            <div class="recommendation-header">
              <t-icon name="info-circle" class="recommendation-icon" />
              <span class="recommendation-title">{{ $t('agentSettings.modelRecommendation.title') }}</span>
            </div>
            <div class="recommendation-content">
              <p>{{ $t('agentSettings.modelRecommendation.content') }}</p>
            </div>
          </div>

          <div class="settings-group">

      <!-- 最大迭代次数 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agentSettings.maxIterations.label') }}</label>
          <p class="desc">{{ $t('agentSettings.maxIterations.desc') }}</p>
        </div>
        <div class="setting-control">
          <div class="slider-with-value">
          <t-slider 
            v-model="localMaxIterations" 
            :min="1" 
            :max="30" 
            :step="1"
            :marks="{ 1: '1', 5: '5', 10: '10', 15: '15', 20: '20', 25: '25', 30: '30' }"
            @change="handleMaxIterationsChangeDebounced"
              style="width: 200px;"
          />
            <span class="value-display">{{ localMaxIterations }}</span>
          </div>
        </div>
      </div>

      <!-- 温度参数 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agentSettings.temperature.label') }}</label>
          <p class="desc">{{ $t('agentSettings.temperature.desc') }}</p>
        </div>
        <div class="setting-control">
          <div class="slider-with-value">
          <t-slider 
            v-model="localTemperature" 
            :min="0" 
            :max="1" 
            :step="0.1"
            :marks="{ 0: '0', 0.5: '0.5', 1: '1' }"
            @change="handleTemperatureChange"
              style="width: 200px;"
          />
            <span class="value-display">{{ localTemperature.toFixed(1) }}</span>
          </div>
        </div>
      </div>

      <!-- 允许的工具 -->
      <div class="setting-row vertical">
        <div class="setting-info">
          <label>{{ $t('agentSettings.allowedTools.label') }}</label>
          <p class="desc">{{ $t('agentSettings.allowedTools.desc') }}</p>
        </div>
        <div class="setting-control full-width allowed-tools-display">
          <div v-if="displayAllowedTools.length" class="allowed-tool-list">
            <div
              v-for="tool in displayAllowedTools"
              :key="tool.name"
              class="allowed-tool-chip"
            >
              <span class="allowed-tool-label">{{ tool.label }}</span>
              <span
                v-if="tool.description"
                class="allowed-tool-desc"
              >
                {{ tool.description }}
              </span>
            </div>
          </div>
          <p v-else class="allowed-tools-empty">
            {{ $t('agentSettings.allowedTools.empty') }}
          </p>
        </div>
      </div>

      <!-- 系统 Prompt -->
      <div class="setting-row vertical">
        <div class="setting-info">
          <label>{{ $t('agentSettings.systemPrompt.label') }}</label>
          <p class="desc">{{ $t('agentSettings.systemPrompt.desc') }}</p>
          <div class="placeholder-hint">
            <p class="hint-title">{{ $t('agentSettings.systemPrompt.availablePlaceholders') }}</p>
            <ul class="placeholder-list">
              <li v-for="placeholder in availablePlaceholders" :key="placeholder.name">
                <code v-html="`{{${placeholder.name}}}`"></code> - {{ placeholder.label }}（{{ placeholder.description }}）
              </li>
            </ul>
            <p class="hint-tip">{{ $t('agentSettings.systemPrompt.hintPrefix') }} <code>&#123;&#123;</code> {{ $t('agentSettings.systemPrompt.hintSuffix') }}</p>
          </div>
        </div>
        <div class="setting-control full-width" style="position: relative;">
          <div class="prompt-header">
            <t-button
              theme="default"
              variant="outline"
              size="small"
              @click="handleResetToDefault"
              :loading="isResettingPrompt"
            >
              {{ $t('common.resetToDefault') }}
            </t-button>
          </div>
          <p class="prompt-tab-hint">
            {{ $t('agentSettings.systemPrompt.tabHint') }}（留空则使用系统默认，使用 <code v-pre>{{web_search_status}}</code> 占位符动态控制网络搜索行为）
          </p>
          <div class="system-prompt-tabs">
            <div class="prompt-textarea-wrapper textarea-with-template">
              <t-textarea
                ref="promptTextareaRef"
                v-model="localSystemPrompt"
                :autosize="{ minRows: 15, maxRows: 30 }"
                :placeholder="$t('agentSettings.systemPrompt.placeholder')"
                @blur="handleSystemPromptChange"
                @input="handlePromptInput"
                @keydown="handlePromptKeydown"
                style="width: 100%; font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace; font-size: 13px;"
              />
              <PromptTemplateSelector 
                type="systemPrompt" 
                position="corner"
                :hasKnowledgeBase="true"
                @select="handleAgentSystemPromptTemplateSelect"
              />
            </div>
          </div>
          <!-- 占位符提示下拉框 -->
          <teleport to="body">
            <div
              v-if="showPlaceholderPopup && filteredPlaceholders.length > 0"
              class="placeholder-popup-wrapper"
              :style="popupStyle"
            >
              <div class="placeholder-popup">
              <div
                v-for="(placeholder, index) in filteredPlaceholders"
                :key="placeholder.name"
                class="placeholder-item"
                :class="{ active: selectedPlaceholderIndex === index }"
                @mousedown.prevent="insertPlaceholder(placeholder.name)"
                @mouseenter="selectedPlaceholderIndex = index"
              >
                  <div class="placeholder-name">
                    <code v-html="`{{${placeholder.name}}}`"></code>
                  </div>
                  <div class="placeholder-desc">{{ placeholder.description }}</div>
                </div>
              </div>
            </div>
          </teleport>
        </div>
      </div>
        </div>
      </div>
      </t-tab-panel>

      <!-- 普通模式设置 Tab -->
      <t-tab-panel value="normal" :label="$t('conversationSettings.normalMode')">
        <div class="tab-content">
          <div class="settings-group">
            <!-- System Prompt（普通模式，自定义开关） -->
            <div class="setting-row vertical">
              <div class="setting-info">
                <label>{{ $t('conversationSettings.systemPrompt.label') }}</label>
                <p class="desc">{{ $t('conversationSettings.systemPrompt.desc') }}（留空则使用系统默认）</p>
              </div>
              <div class="setting-control full-width">
                <div class="prompt-textarea-wrapper textarea-with-template">
                  <t-textarea
                    v-model="localSystemPromptNormal"
                    :autosize="{ minRows: 10, maxRows: 20 }"
                    :placeholder="$t('conversationSettings.systemPrompt.placeholder')"
                    @blur="handleSystemPromptNormalChange"
                    style="width: 100%; font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace; font-size: 13px;"
                  />
                  <PromptTemplateSelector 
                    type="systemPrompt" 
                    position="corner"
                    :hasKnowledgeBase="true"
                    @select="handleNormalSystemPromptTemplateSelect"
                  />
                </div>
              </div>
            </div>

            <!-- Context Template（普通模式） -->
            <div class="setting-row vertical">
              <div class="setting-info">
                <label>{{ $t('conversationSettings.contextTemplate.label') }}</label>
                <p class="desc">{{ $t('conversationSettings.contextTemplate.desc') }}（留空则使用系统默认）</p>
              </div>
              <div class="setting-control full-width">
                <div class="prompt-textarea-wrapper textarea-with-template">
                  <t-textarea
                    v-model="localContextTemplate"
                    :autosize="{ minRows: 15, maxRows: 30 }"
                    :placeholder="$t('conversationSettings.contextTemplate.placeholder')"
                    @blur="handleContextTemplateChange"
                    style="width: 100%; font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace; font-size: 13px;"
                  />
                  <PromptTemplateSelector 
                    type="contextTemplate" 
                    position="corner"
                    :hasKnowledgeBase="true"
                    @select="handleContextTemplateTemplateSelect"
                  />
                </div>
              </div>
            </div>
          </div>
        </div>
      </t-tab-panel>
    </t-tabs>
    </div>

    <div v-else-if="activeSection === 'models'" class="section-block" data-conversation-section="models">
      <div class="section-header">
        <h2>{{ $t('conversationSettings.menus.models') }}</h2>
        <p class="section-description">{{ $t('conversationSettings.models.description') }}</p>
      </div>

      <div class="settings-group">
        <!-- 默认大模型（对话/总结模型） -->
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.models.chatGroupLabel') }}</label>
            <p class="desc">{{ $t('conversationSettings.models.chatGroupDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-select
              v-model="localSummaryModelId"
              :loading="loadingModels"
              filterable
              :placeholder="$t('conversationSettings.models.chatModel.placeholder')"
              style="width: 320px;"
              @focus="loadAllModels"
              @change="handleConversationSummaryModelChange"
            >
              <t-option
                v-for="model in chatModels"
                :key="model.id"
                :value="model.id"
                :label="model.name"
              />
              <t-option value="__add_model__" class="add-model-option">
                <div class="model-option add">
                  <t-icon name="add" class="add-icon" />
                  <span class="model-name">{{ $t('agentSettings.model.addChat') }}</span>
                </div>
              </t-option>
            </t-select>
          </div>
        </div>

        <!-- 默认 ReRank 模型 -->
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.models.rerankGroupLabel') }}</label>
            <p class="desc">{{ $t('conversationSettings.models.rerankGroupDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-select
              v-model="localConversationRerankModelId"
              :loading="loadingModels"
              filterable
              :placeholder="$t('conversationSettings.models.rerankModel.placeholder')"
              style="width: 320px;"
              @focus="loadAllModels"
              @change="handleConversationRerankModelChange"
            >
              <t-option
                v-for="model in rerankModels"
                :key="model.id"
                :value="model.id"
                :label="model.name"
              />
              <t-option value="__add_model__" class="add-model-option">
                <div class="model-option add">
                  <t-icon name="add" class="add-icon" />
                  <span class="model-name">{{ $t('agentSettings.model.addRerank') }}</span>
                </div>
              </t-option>
            </t-select>
          </div>
        </div>
      </div>
    </div>

    <div v-else-if="activeSection === 'thresholds'" class="section-block">
      <div class="section-header">
        <h2>{{ $t('conversationSettings.menus.thresholds') }}</h2>
        <p class="section-description">{{ $t('conversationSettings.thresholds.description') }}</p>
      </div>

      <div class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.maxRounds.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.maxRounds.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              v-model="localMaxRounds"
              :min="1"
              :max="50"
              @change="handleMaxRoundsChange"
            />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.embeddingTopK.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.embeddingTopK.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              v-model="localEmbeddingTopK"
              :min="1"
              :max="50"
              @change="handleEmbeddingTopKChange"
            />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.keywordThreshold.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.keywordThreshold.desc') }}</p>
          </div>
          <div class="setting-control slider-with-value">
            <t-slider
              v-model="localKeywordThreshold"
              :min="0"
              :max="1"
              :step="0.05"
              style="width: 240px;"
              @change="handleKeywordThresholdChange"
            />
            <span class="value-display">{{ localKeywordThreshold.toFixed(2) }}</span>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.vectorThreshold.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.vectorThreshold.desc') }}</p>
          </div>
          <div class="setting-control slider-with-value">
            <t-slider
              v-model="localVectorThreshold"
              :min="0"
              :max="1"
              :step="0.05"
              style="width: 240px;"
              @change="handleVectorThresholdChange"
            />
            <span class="value-display">{{ localVectorThreshold.toFixed(2) }}</span>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.rerankTopK.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.rerankTopK.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              v-model="localRerankTopK"
              :min="1"
              :max="20"
              @change="handleRerankTopKChange"
            />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.rerankThreshold.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.rerankThreshold.desc') }}</p>
          </div>
          <div class="setting-control slider-with-value">
            <t-slider
              v-model="localRerankThreshold"
              :min="0"
              :max="1"
              :step="0.05"
              style="width: 240px;"
              @change="handleRerankThresholdChange"
            />
            <span class="value-display">{{ localRerankThreshold.toFixed(2) }}</span>
          </div>
        </div>

      </div>
    </div>

    <div v-else-if="activeSection === 'advanced'" class="section-block">
      <div class="section-header">
        <h2>{{ $t('conversationSettings.menus.advanced') }}</h2>
        <p class="section-description">{{ $t('conversationSettings.advanced.description') }}</p>
      </div>

      <div class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.enableQueryExpansion.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.enableQueryExpansion.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              v-model="localEnableQueryExpansion"
              :label="[$t('common.off'), $t('common.on')]"
              @change="handleEnableQueryExpansionChange"
            />
          </div>
        </div>
        <!-- 开启问题改写 -->
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.enableRewrite.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.enableRewrite.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              v-model="localEnableRewrite"
              :label="[$t('common.off'), $t('common.on')]"
              @change="handleEnableRewriteChange"
            />
          </div>
        </div>

        <!-- 改写 Prompt：仅在开启改写时展示 -->
        <div v-if="localEnableRewrite" class="setting-row vertical">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.rewritePrompt.system') }}</label>
            <p class="desc">{{ $t('conversationSettings.rewritePrompt.desc') }}</p>
          </div>
          <div class="setting-control full-width">
            <div class="textarea-with-template">
              <t-textarea
                v-model="localRewritePromptSystem"
                :autosize="{ minRows: 8, maxRows: 16 }"
                @blur="handleRewritePromptSystemChange"
              />
              <PromptTemplateSelector 
                type="rewriteSystem" 
                position="corner"
                @select="handleRewriteSystemTemplateSelect"
              />
            </div>
          </div>
        </div>

        <div v-if="localEnableRewrite" class="setting-row vertical">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.rewritePrompt.user') }}</label>
            <p class="desc">{{ $t('conversationSettings.rewritePrompt.userDesc') }}</p>
          </div>
          <div class="setting-control full-width">
            <div class="textarea-with-template">
              <t-textarea
                v-model="localRewritePromptUser"
                :autosize="{ minRows: 8, maxRows: 16 }"
                @blur="handleRewritePromptUserChange"
              />
              <PromptTemplateSelector 
                type="rewriteUser" 
                position="corner"
                @select="handleRewriteUserTemplateSelect"
              />
            </div>
          </div>
        </div>

        <!-- 兜底策略 -->
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.fallbackStrategy.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.fallbackStrategy.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-radio-group v-model="localFallbackStrategy" @change="handleFallbackStrategyChange">
              <t-radio value="fixed">{{ $t('conversationSettings.fallbackStrategy.fixed') }}</t-radio>
              <t-radio value="model">{{ $t('conversationSettings.fallbackStrategy.model') }}</t-radio>
            </t-radio-group>
          </div>
        </div>

        <!-- 固定兜底回复：仅在选择固定回复时展示 -->
        <div v-if="localFallbackStrategy === 'fixed'" class="setting-row vertical">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.fallbackResponse.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.fallbackResponse.desc') }}</p>
          </div>
          <div class="setting-control full-width">
            <div class="textarea-with-template">
              <t-textarea
                v-model="localFallbackResponse"
                :autosize="{ minRows: 3, maxRows: 6 }"
                @blur="handleFallbackResponseChange"
              />
              <PromptTemplateSelector 
                type="fallback" 
                position="corner"
                @select="handleFallbackResponseTemplateSelect"
              />
            </div>
          </div>
        </div>

        <!-- 兜底 Prompt：仅在选择"交给模型继续生成"时展示 -->
        <div v-else-if="localFallbackStrategy === 'model'" class="setting-row vertical">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.fallbackPrompt.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.fallbackPrompt.desc') }}</p>
          </div>
          <div class="setting-control full-width">
            <div class="textarea-with-template">
              <t-textarea
                v-model="localFallbackPrompt"
                :autosize="{ minRows: 8, maxRows: 16 }"
                @blur="handleFallbackPromptChange"
              />
              <PromptTemplateSelector 
                type="fallback" 
                position="corner"
                @select="handleFallbackPromptTemplateSelect"
              />
            </div>
          </div>
        </div>

        <!-- 普通模式生成参数：Temperature -->
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.temperature.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.temperature.desc') }}</p>
          </div>
          <div class="setting-control">
            <div class="slider-with-value">
              <t-slider 
                v-model="localTemperatureNormal" 
                :min="0" 
                :max="1" 
                :step="0.1"
                :marks="{ 0: '0', 0.5: '0.5', 1: '1' }"
                @change="handleTemperatureNormalChange"
                style="width: 200px;"
              />
              <span class="value-display">{{ localTemperatureNormal.toFixed(1) }}</span>
            </div>
          </div>
        </div>

        <!-- 普通模式生成参数：Max Tokens -->
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('conversationSettings.maxTokens.label') }}</label>
            <p class="desc">{{ $t('conversationSettings.maxTokens.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              v-model="localMaxCompletionTokens"
              :min="1"
              :max="100000"
              :step="100"
              @change="handleMaxCompletionTokensChange"
              style="width: 200px;"
            />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, watch, computed, nextTick } from 'vue'
import type { Ref } from 'vue'
import { useRouter } from 'vue-router'
import { useSettingsStore } from '@/stores/settings'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { listModels, type ModelConfig } from '@/api/model'
import { getAgentConfig, updateAgentConfig, getConversationConfig, updateConversationConfig, type AgentConfig, type ConversationConfig, type ToolDefinition, type PlaceholderDefinition } from '@/api/system'
import PromptTemplateSelector from '@/components/PromptTemplateSelector.vue'

const props = defineProps<{
  // 来自外部设置弹窗的子菜单 key: 'modes' | 'models' | 'thresholds' | 'advanced'
  activeSubSection?: string
}>()

// 当前子页面（模式、模型、阈值、高级）
const activeSection = computed(() => props.activeSubSection || 'modes')

const settingsStore = useSettingsStore()
const router = useRouter()
const { t } = useI18n()

// Tab 状态
const activeTab = ref('agent')

const getDefaultConversationConfig = (): ConversationConfig => ({
  prompt: '',
  context_template: '',
  temperature: 0.3,
  max_completion_tokens: 2048,
  max_rounds: 5,
  embedding_top_k: 10,
  keyword_threshold: 0.3,
  vector_threshold: 0.5,
  rerank_top_k: 5,
  rerank_threshold: 0.5,
  enable_rewrite: true,
  enable_query_expansion: true,
  fallback_strategy: 'fixed',
  fallback_response: '',
  fallback_prompt: '',
  summary_model_id: '',
  rerank_model_id: '',
  rewrite_prompt_system: '',
  rewrite_prompt_user: '',
})

const normalizeConversationConfig = (config?: Partial<ConversationConfig>): ConversationConfig => ({
  ...getDefaultConversationConfig(),
  ...config,
})

const conversationConfig = ref<ConversationConfig>(getDefaultConversationConfig())
const conversationConfigLoaded = ref(false)
const conversationSaving = ref(false)

// Agent 模式本地状态
const localMaxIterations = ref(5)
const localTemperature = ref(0.7)
const localAllowedTools = ref<string[]>([])

// 统一系统提示词
const localSystemPrompt = ref('')
let savedSystemPrompt = ''

// 普通模式本地状态
const localContextTemplate = ref('')
const localSystemPromptNormal = ref('')
const localTemperatureNormal = ref(0.3)
const localMaxCompletionTokens = ref(2048)
let savedContextTemplate = ''
let savedSystemPromptNormal = ''
let savedTemperatureNormal = 0.3
let savedMaxCompletionTokens = 2048

const localMaxRounds = ref(5)
const localEmbeddingTopK = ref(10)
const localKeywordThreshold = ref(0.3)
const localVectorThreshold = ref(0.5)
const localRerankTopK = ref(5)
const localRerankThreshold = ref(0.5)
const localEnableRewrite = ref(true)
const localEnableQueryExpansion = ref(true)
const localFallbackStrategy = ref<'fixed' | 'model'>('fixed')
const localFallbackResponse = ref('')
const localFallbackPrompt = ref('')
const localRewritePromptSystem = ref('')
const localRewritePromptUser = ref('')
const localSummaryModelId = ref('')
const localConversationRerankModelId = ref('')

const syncConversationLocals = () => {
  const cfg = conversationConfig.value
  localContextTemplate.value = cfg.context_template ?? ''
  savedContextTemplate = localContextTemplate.value
  localSystemPromptNormal.value = cfg.prompt ?? ''
  savedSystemPromptNormal = localSystemPromptNormal.value
  localTemperatureNormal.value = cfg.temperature ?? 0.3
  savedTemperatureNormal = localTemperatureNormal.value
  localMaxCompletionTokens.value = cfg.max_completion_tokens ?? 2048
  savedMaxCompletionTokens = localMaxCompletionTokens.value

  localMaxRounds.value = cfg.max_rounds ?? 5
  localEmbeddingTopK.value = cfg.embedding_top_k ?? 10
  localKeywordThreshold.value = cfg.keyword_threshold ?? 0.3
  localVectorThreshold.value = cfg.vector_threshold ?? 0.5
  localRerankTopK.value = cfg.rerank_top_k ?? 5
  localRerankThreshold.value = cfg.rerank_threshold ?? 0.5
  localEnableRewrite.value = cfg.enable_rewrite ?? true
  localEnableQueryExpansion.value = cfg.enable_query_expansion ?? true
  localFallbackStrategy.value = (cfg.fallback_strategy as 'fixed' | 'model') || 'fixed'
  localFallbackResponse.value = cfg.fallback_response ?? ''
  localFallbackPrompt.value = cfg.fallback_prompt ?? ''
  localRewritePromptSystem.value = cfg.rewrite_prompt_system ?? ''
  localRewritePromptUser.value = cfg.rewrite_prompt_user ?? ''
  localSummaryModelId.value = cfg.summary_model_id ?? ''
  localConversationRerankModelId.value = cfg.rerank_model_id ?? ''

  settingsStore.updateConversationModels({
    summaryModelId: localSummaryModelId.value || '',
    rerankModelId: localConversationRerankModelId.value || '',
  })
}

const saveConversationConfig = async (partial: Partial<ConversationConfig>, toastMessage?: string) => {
  if (!conversationConfigLoaded.value) return

  const payload = normalizeConversationConfig({
    ...conversationConfig.value,
    ...partial,
  })

  try {
    conversationSaving.value = true
    const res = await updateConversationConfig(payload)
    conversationConfig.value = normalizeConversationConfig(res.data ?? payload)
    syncConversationLocals()
    if (toastMessage) {
      MessagePlugin.success(toastMessage)
    }
  } catch (error) {
    console.error('保存对话配置失败:', error)
    MessagePlugin.error(getErrorMessage(error))
    throw error
  } finally {
    conversationSaving.value = false
  }
}

// 计算 Agent 是否就绪
const isAgentReady = computed(() => {
  return (
    localAllowedTools.value.length > 0 &&
    localSummaryModelId.value &&
    localSummaryModelId.value.trim() !== '' &&
    localConversationRerankModelId.value &&
    localConversationRerankModelId.value.trim() !== ''
  )
})

const buildAgentConfigPayload = (overrides: Partial<AgentConfig> = {}): AgentConfig => ({
  max_iterations: localMaxIterations.value,
  reflection_enabled: false,
  allowed_tools: localAllowedTools.value,
  temperature: localTemperature.value,
  system_prompt: localSystemPrompt.value,
  ...overrides,
})

// 是否缺少模型配置
const needsModelConfig = computed(() => {
  return (
    (!localSummaryModelId.value || localSummaryModelId.value.trim() === '') ||
    (!localConversationRerankModelId.value || localConversationRerankModelId.value.trim() === '')
  )
})

// Agent 状态提示消息
const agentStatusMessage = computed(() => {
  const missing: string[] = []
  
  if (localAllowedTools.value.length === 0) {
    missing.push(t('agentSettings.status.missingAllowedTools'))
  }
  
  if (!localSummaryModelId.value || localSummaryModelId.value.trim() === '') {
    missing.push(t('agentSettings.status.missingSummaryModel'))
  }
  
  if (!localConversationRerankModelId.value || localConversationRerankModelId.value.trim() === '') {
    missing.push(t('agentSettings.status.missingRerankModel'))
  }
  
  if (missing.length === 0) {
    return ''
  }
  
  return t('agentSettings.status.pleaseConfigure', { items: missing.join('、') })
})

// 跳转到模型配置
const handleGoToModelSettings = () => {
  router.push('/platform/settings')

  setTimeout(() => {
    const event = new CustomEvent('settings-nav', {
      detail: { section: 'agent', subsection: 'models' }
    })
    window.dispatchEvent(event)

    setTimeout(() => {
      const sectionEl = document.querySelector('[data-conversation-section="models"]')
      if (sectionEl) {
        sectionEl.scrollIntoView({ behavior: 'smooth', block: 'start' })
      }
    }, 150)
  }, 100)
}

// 模型列表状态
const chatModels = ref<ModelConfig[]>([])
const rerankModels = ref<ModelConfig[]>([])
const loadingModels = ref(false)

// 可用工具列表
const availableTools = ref<ToolDefinition[]>([])
// 可用占位符列表
const availablePlaceholders = ref<PlaceholderDefinition[]>([])
const displayAllowedTools = computed(() => {
  return localAllowedTools.value.map(name => {
    const detail = availableTools.value.find(tool => tool.name === name)
    return {
      name,
      label: detail?.label || name,
      description: detail?.description || ''
    }
  })
})

// 配置加载状态
const loadingConfig = ref(false)
const configLoaded = ref(false) // 防止重复加载
const isInitializing = ref(true) // 标记是否正在初始化，防止初始化时触发保存

// 恢复默认 Prompt 的加载状态
const isResettingPrompt = ref(false)

// 占位符提示相关状态
const promptTextareaRef = ref<any>(null)
const showPlaceholderPopup = ref(false)
const selectedPlaceholderIndex = ref(0)
let placeholderPopupTimer: any = null
const placeholderPrefix = ref('') // 当前输入的前缀，用于过滤
const popupStyle = ref({ top: '0px', left: '0px' }) // 提示框位置

// 设置 textarea 原生事件监听器
const setupTextareaEventListeners = () => {
  nextTick(() => {
    const textarea = getTextareaElement()
    if (textarea) {
      // 添加原生 keydown 事件监听（使用 capture 阶段，确保优先处理）
      textarea.addEventListener('keydown', (e: KeyboardEvent) => {
        // 如果正在显示占位符提示，优先处理占位符相关的按键
        if (showPlaceholderPopup.value && filteredPlaceholders.value.length > 0) {
          if (e.key === 'ArrowDown') {
            // 下箭头选择下一个
            e.preventDefault()
            e.stopPropagation()
            e.stopImmediatePropagation()
            if (selectedPlaceholderIndex.value < filteredPlaceholders.value.length - 1) {
              selectedPlaceholderIndex.value++
            } else {
              selectedPlaceholderIndex.value = 0 // 循环到第一个
            }
            return
          } else if (e.key === 'ArrowUp') {
            // 上箭头选择上一个
            e.preventDefault()
            e.stopPropagation()
            e.stopImmediatePropagation()
            if (selectedPlaceholderIndex.value > 0) {
              selectedPlaceholderIndex.value--
            } else {
              selectedPlaceholderIndex.value = filteredPlaceholders.value.length - 1 // 循环到最后一个
            }
            return
          } else if (e.key === 'Enter') {
            // Enter 键插入选中的占位符
            e.preventDefault()
            e.stopPropagation()
            e.stopImmediatePropagation()
            const selected = filteredPlaceholders.value[selectedPlaceholderIndex.value]
            if (selected) {
              insertPlaceholder(selected.name)
            }
            return
          } else if (e.key === 'Escape') {
            // ESC 键关闭提示
            e.preventDefault()
            e.stopPropagation()
            e.stopImmediatePropagation()
            showPlaceholderPopup.value = false
            placeholderPrefix.value = ''
            return
          }
        }
        
        // 如果按下的是 { 键
        if (e.key === '{') {
          // 清除之前的定时器
          if (placeholderPopupTimer) {
            clearTimeout(placeholderPopupTimer)
          }
          
          // 延迟检查，等待输入完成（连续输入两个 {）
          placeholderPopupTimer = setTimeout(() => {
            checkAndShowPlaceholderPopup()
          }, 150)
        }
      }, true) // 使用 capture 阶段
      
      // 添加原生 input 事件监听（作为备用）
      textarea.addEventListener('input', () => {
        if (placeholderPopupTimer) {
          clearTimeout(placeholderPopupTimer)
        }
        placeholderPopupTimer = setTimeout(() => {
          checkAndShowPlaceholderPopup()
        }, 50)
      })
    }
  })
}

// 获取 textarea 元素的辅助函数
const getTextareaElement = (): HTMLTextAreaElement | null => {
  if (promptTextareaRef.value) {
    if (promptTextareaRef.value.$el) {
      return promptTextareaRef.value.$el.querySelector('textarea')
    } else if (promptTextareaRef.value instanceof HTMLTextAreaElement) {
      return promptTextareaRef.value
    }
  }
  
  // 如果还是找不到，尝试通过 DOM 查找
  const wrapper = document.querySelector('.setting-control.full-width')
  return wrapper?.querySelector('textarea') || null
}

const getActivePromptRef = () => {
  return activeTab.value === 'normal' ? localSystemPromptNormal : localSystemPrompt
}

// 初始化加载
onMounted(async () => {
  // 防止重复加载
  if (configLoaded.value) return
  
  loadingConfig.value = true
  configLoaded.value = true
  isInitializing.value = true
  
  try {
    // 从后台加载配置
    const res = await getAgentConfig()
    const config = res.data
    
    // 更新本地状态（在初始化期间，不会触发保存）
    localMaxIterations.value = config.max_iterations
    lastSavedValue = config.max_iterations // 初始化时记录已保存的值
    localTemperature.value = config.temperature
    localAllowedTools.value = config.allowed_tools || []
    const systemPrompt = config.system_prompt || ''
    localSystemPrompt.value = systemPrompt
    savedSystemPrompt = systemPrompt
    availableTools.value = config.available_tools || []
    availablePlaceholders.value = config.available_placeholders || []
    
    // 调试信息
    console.log('加载的占位符列表:', availablePlaceholders.value)
    
    // 统一加载所有模型（只调用一次API）
      await loadAllModels()
    
    // 同步到store（只更新本地存储，不触发API保存）
    // 注意：不自动设置 isAgentEnabled，保持用户之前的选择
    // enabled 状态应该由用户手动控制，而不是根据配置自动设置
    settingsStore.updateAgentConfig({
      maxIterations: config.max_iterations,
      temperature: config.temperature,
      allowedTools: config.allowed_tools || [],
      system_prompt: systemPrompt,
    })

    // 加载普通模式配置
    if (!conversationConfigLoaded.value) {
      try {
        const convRes = await getConversationConfig()
        conversationConfig.value = normalizeConversationConfig(convRes.data)
        conversationConfigLoaded.value = true
        syncConversationLocals()
      } catch (error) {
        console.error('加载普通模式配置失败:', error)
        // 使用默认值
        conversationConfigLoaded.value = true
      }
    }
    
    // 等待下一个 tick，确保所有响应式更新完成
    await nextTick()
    // 再等待一帧，确保所有事件监听器都已设置好
    requestAnimationFrame(() => {
      // 初始化完成，现在可以允许保存操作
      isInitializing.value = false
      
      // 设置原生事件监听器（作为备用方案）
      setupTextareaEventListeners()
    })
  } catch (error) {
    console.error('加载Agent配置失败:', error)
    MessagePlugin.error('加载Agent配置失败')
    configLoaded.value = false // 加载失败时重置标记，允许重试
    
    // 失败时从store加载
    localMaxIterations.value = settingsStore.agentConfig.maxIterations
    localTemperature.value = settingsStore.agentConfig.temperature
  } finally {
    loadingConfig.value = false
    isInitializing.value = false // 确保初始化完成，即使失败也要允许后续操作
  }
})

// 错误码到错误消息的映射
const getErrorMessage = (error: any): string => {
  const errorCode = error?.response?.data?.error?.code
  const errorMessage = error?.response?.data?.error?.message
  
  switch (errorCode) {
    case 2100:
      return t('agentSettings.errors.selectThinkingModel')
    case 2101:
      return t('agentSettings.errors.selectAtLeastOneTool')
    case 2102:
      return t('agentSettings.errors.iterationsRange')
    case 2103:
      return t('agentSettings.errors.temperatureRange')
    case 1010:
      return errorMessage || t('agentSettings.errors.validationFailed')
    default:
      return errorMessage || t('common.saveFailed')
  }
}

// 防抖定时器
let maxIterationsDebounceTimer: any = null
// 上次保存的值，用于避免重复保存相同值
let lastSavedValue: number | null = null

// 处理最大迭代次数变化（防抖版本，点击和拖动都使用这个）
const handleMaxIterationsChangeDebounced = (value: number) => {
  // 如果正在初始化，不触发保存
  if (isInitializing.value) return
  
  // 确保 value 是数字类型
  const numValue = typeof value === 'number' ? value : Number(value)
  if (isNaN(numValue)) {
    console.error('Invalid max_iterations value:', value)
    return
  }
  
  // 如果值没有变化，不保存
  if (lastSavedValue === numValue) {
    return
  }
  
  // 清除之前的定时器
  if (maxIterationsDebounceTimer) {
    clearTimeout(maxIterationsDebounceTimer)
}

  // 设置新的定时器，300ms 后保存（减少延迟，提升响应速度）
  maxIterationsDebounceTimer = setTimeout(async () => {
    // 再次检查值是否变化（可能在等待期间值又变了）
    if (lastSavedValue === numValue) {
      maxIterationsDebounceTimer = null
      return
    }
  
  try {
    const config = buildAgentConfigPayload({ max_iterations: numValue })
    await updateAgentConfig(config)
      settingsStore.updateAgentConfig({ maxIterations: numValue })
      lastSavedValue = numValue // 记录已保存的值
    MessagePlugin.success(t('agentSettings.toasts.iterationsSaved'))
  } catch (error) {
    console.error('保存失败:', error)
    MessagePlugin.error(getErrorMessage(error))
    } finally {
      maxIterationsDebounceTimer = null
  }
  }, 300)
}

// 统一加载所有模型（只调用一次API）
const loadAllModels = async () => {
  if (chatModels.value.length > 0 && rerankModels.value.length > 0) return // 已经加载过
  
  loadingModels.value = true
  try {
    const allModels = await listModels()
    // 按类型过滤，避免重复调用
    chatModels.value = allModels.filter(m => m.type === 'KnowledgeQA')
    rerankModels.value = allModels.filter(m => m.type === 'Rerank')
  } catch (error) {
    console.error('加载模型列表失败:', error)
    MessagePlugin.error('加载模型列表失败')
  } finally {
    loadingModels.value = false
  }
}

// 加载对话模型列表（已废弃，使用 loadAllModels）
const loadChatModels = async () => {
  await loadAllModels()
}

// 加载 Rerank 模型列表（已废弃，使用 loadAllModels）
const loadRerankModels = async () => {
  await loadAllModels()
}

// 处理温度参数变化
const handleTemperatureChange = async (value: number) => {
  // 如果正在初始化，不触发保存
  if (isInitializing.value) return
  
  try {
    const config = buildAgentConfigPayload({ temperature: value })
    await updateAgentConfig(config)
    settingsStore.updateAgentConfig({ temperature: value })
    MessagePlugin.success(t('agentSettings.toasts.temperatureSaved'))
  } catch (error) {
    console.error('保存失败:', error)
    MessagePlugin.error(getErrorMessage(error))
  }
}

// 处理系统 Prompt 键盘事件（作为备用，主要逻辑在原生事件监听器中）
const handlePromptKeydown = (e: KeyboardEvent) => {
  // 如果正在显示占位符提示，且输入的是字母、数字或下划线，实时更新过滤
  if (showPlaceholderPopup.value && /^[a-zA-Z0-9_]$/.test(e.key)) {
    // 延迟检查，等待字符输入完成
    if (placeholderPopupTimer) {
      clearTimeout(placeholderPopupTimer)
    }
    placeholderPopupTimer = setTimeout(() => {
      checkAndShowPlaceholderPopup()
    }, 50)
  }
}

// 过滤后的占位符列表（根据前缀匹配）
const filteredPlaceholders = computed(() => {
  if (!placeholderPrefix.value) {
    return availablePlaceholders.value
  }
  
  const prefix = placeholderPrefix.value.toLowerCase()
  return availablePlaceholders.value.filter(p => 
    p.name.toLowerCase().startsWith(prefix)
  )
})

// 计算光标在 textarea 中的像素位置
const calculateCursorPosition = (textarea: HTMLTextAreaElement) => {
  const cursorPos = textarea.selectionStart
  const activePromptValue = getActivePromptRef().value
  const textBeforeCursor = activePromptValue.substring(0, cursorPos)
  
  // 获取 textarea 的样式和位置
  const style = window.getComputedStyle(textarea)
  const textareaRect = textarea.getBoundingClientRect()
  
  // 计算行数和当前行的文本
  const lines = textBeforeCursor.split('\n')
  const currentLine = lines.length - 1
  const lineText = lines[currentLine] || ''
  
  // 获取行高
  const lineHeight = parseFloat(style.lineHeight) || parseFloat(style.fontSize) * 1.2
  
  // 获取 padding
  const paddingTop = parseFloat(style.paddingTop) || 0
  const paddingLeft = parseFloat(style.paddingLeft) || 0
  
  // 使用 canvas 测量当前行的文本宽度（更准确）
  const canvas = document.createElement('canvas')
  const context = canvas.getContext('2d')
  let textWidth = 0
  
  if (context) {
    context.font = `${style.fontSize} ${style.fontFamily}`
    textWidth = context.measureText(lineText).width
  } else {
    // 回退方案：使用等宽字体估算（Monaco/Menlo 是等宽字体）
    const charWidth = parseFloat(style.fontSize) * 0.6 // 等宽字体字符宽度约为字体大小的 0.6 倍
    textWidth = lineText.length * charWidth
  }
  
  // 计算光标位置的 top（考虑滚动）
  const scrollTop = textarea.scrollTop
  const top = textareaRect.top + paddingTop + (currentLine * lineHeight) - scrollTop + lineHeight + 4
  
  // 计算光标位置的 left（考虑滚动）
  const scrollLeft = textarea.scrollLeft
  const left = textareaRect.left + paddingLeft + textWidth - scrollLeft
  
  return { top, left }
}

// 检查并显示占位符提示
const checkAndShowPlaceholderPopup = () => {
  const textarea = getTextareaElement()
  
  if (!textarea) {
    return
  }
  
  const cursorPos = textarea.selectionStart
  const textBeforeCursor = getActivePromptRef().value.substring(0, cursorPos)
  
  // 检查是否输入了 {{（从光标位置向前查找最近的 {{）
  // 需要找到光标前最近的 {{，且中间没有 }}
  let lastOpenPos = -1
  for (let i = cursorPos - 1; i >= 0; i--) {
    if (i > 0 && textBeforeCursor[i - 1] === '{' && textBeforeCursor[i] === '{') {
      // 找到了 {{
      const textAfterOpen = textBeforeCursor.substring(i + 1)
      // 检查是否已经包含 }}（说明占位符已完成）
      if (!textAfterOpen.includes('}}')) {
        lastOpenPos = i - 1
        break
      }
    }
  }
  
  if (lastOpenPos === -1) {
    // 没有找到有效的 {{，隐藏提示
    showPlaceholderPopup.value = false
    placeholderPrefix.value = ''
    return
  }
  
  // 获取 {{ 之后到光标位置的内容作为前缀
  const textAfterOpen = textBeforeCursor.substring(lastOpenPos + 2)
  
  // 更新前缀
  placeholderPrefix.value = textAfterOpen
  
  // 根据前缀过滤占位符
  const filtered = filteredPlaceholders.value
  
  if (filtered.length > 0) {
    // 有匹配的占位符，显示提示
    // 计算光标位置
    nextTick(() => {
      const position = calculateCursorPosition(textarea)
      popupStyle.value = {
        top: `${position.top}px`,
        left: `${position.left}px`
      }
      showPlaceholderPopup.value = true
      // 重置选中索引为第一个（默认选择第一个）
      selectedPlaceholderIndex.value = 0
    })
  } else {
    // 没有匹配的占位符，隐藏提示
    showPlaceholderPopup.value = false
  }
}

// 处理系统 Prompt 输入
const handlePromptInput = () => {
  // 清除之前的定时器
  if (placeholderPopupTimer) {
    clearTimeout(placeholderPopupTimer)
  }
  
  // 延迟检查，避免频繁触发
  placeholderPopupTimer = setTimeout(() => {
    checkAndShowPlaceholderPopup()
  }, 50)
}

// 插入占位符
const insertPlaceholder = (placeholderName: string) => {
  const textarea = getTextareaElement()
  if (!textarea) {
    return
  }
  
  // 先关闭提示，避免触发 blur 事件
  showPlaceholderPopup.value = false
  placeholderPrefix.value = ''
  selectedPlaceholderIndex.value = 0
  
  // 延迟执行，确保提示框已关闭
  nextTick(() => {
    const cursorPos = textarea.selectionStart
    const promptRef = getActivePromptRef()
    const currentValue = promptRef.value
    const textBeforeCursor = currentValue.substring(0, cursorPos)
    const textAfterCursor = currentValue.substring(cursorPos)
    
    // 找到最后一个 {{ 的位置
    const lastOpenPos = textBeforeCursor.lastIndexOf('{{')
    if (lastOpenPos === -1) {
      // 如果没有找到 {{，直接插入完整的占位符
      const placeholder = `{{${placeholderName}}}`
      promptRef.value = textBeforeCursor + placeholder + textAfterCursor
      // 设置光标位置
      nextTick(() => {
        const newPos = cursorPos + placeholder.length
        textarea.setSelectionRange(newPos, newPos)
        textarea.focus()
      })
    } else {
      // 替换 {{ 到光标位置的内容为完整的占位符
      const beforePlaceholder = textBeforeCursor.substring(0, lastOpenPos)
      const placeholder = `{{${placeholderName}}}`
      promptRef.value = beforePlaceholder + placeholder + textAfterCursor
      // 设置光标位置
      nextTick(() => {
        const newPos = lastOpenPos + placeholder.length
        textarea.setSelectionRange(newPos, newPos)
        textarea.focus()
      })
    }
  })
}

// 恢复默认 Prompt
const handleResetToDefault = async () => {
  const confirmDialog = DialogPlugin.confirm({
    header: t('agentSettings.reset.header'),
    body: t('agentSettings.reset.body'),
    confirmBtn: t('common.confirm'),
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      try {
        isResettingPrompt.value = true
        
        // 通过设置 system_prompt 为空字符串来获取默认值
        // 后端在字段为空时会返回默认值
        const tempConfig = buildAgentConfigPayload({
          system_prompt: '',
        })
        
        await updateAgentConfig(tempConfig)
        
        // 重新加载配置以获取默认 Prompt 的完整内容
        const res = await getAgentConfig()
        const defaultPrompt = res.data.system_prompt || ''
        
        // 设置为默认 Prompt 的内容
        localSystemPrompt.value = defaultPrompt
        savedSystemPrompt = defaultPrompt
        
        MessagePlugin.success(t('agentSettings.toasts.resetToDefault'))
        confirmDialog.hide()
      } catch (error) {
        console.error('恢复默认 Prompt 失败:', error)
        MessagePlugin.error(getErrorMessage(error))
      } finally {
        isResettingPrompt.value = false
      }
    }
  })
}

// 处理系统 Prompt 变化
const handleSystemPromptChange = async (e?: FocusEvent) => {
  // 如果点击的是占位符提示框，不触发保存
  if (e?.relatedTarget) {
    const target = e.relatedTarget as HTMLElement
    if (target.closest('.placeholder-popup-wrapper')) {
      return
    }
  }
  
  // 延迟检查，避免点击占位符时立即触发
  await nextTick()
  
  // 如果占位符提示框还在显示，说明用户点击了占位符，不触发保存
  if (showPlaceholderPopup.value) {
    return
  }
  
  // 隐藏占位符提示
  placeholderPrefix.value = ''
  
  // 如果正在初始化，不触发保存
  if (isInitializing.value) return

  // 检查内容是否变化
  if (localSystemPrompt.value === savedSystemPrompt) {
    return // 内容没变，不调用接口
  }
  
  try {
    const config = buildAgentConfigPayload()
    await updateAgentConfig(config)
    savedSystemPrompt = localSystemPrompt.value // 更新已保存的值
    MessagePlugin.success(t('agentSettings.toasts.systemPromptSaved'))
  } catch (error) {
    console.error('保存系统 Prompt 失败:', error)
    MessagePlugin.error(getErrorMessage(error))
  }
}

// 监听 Agent 就绪状态变化，同步到 store
watch(isAgentReady, (newValue, oldValue) => {
  if (!isInitializing.value) {
    // 如果配置从"就绪"变为"未就绪"，且 Agent 当前是启用状态，自动关闭
    if (!newValue && oldValue && settingsStore.isAgentEnabled) {
      settingsStore.toggleAgent(false)
      MessagePlugin.warning(t('agentSettings.toasts.autoDisabled'))
    }
    // 注意：配置从"未就绪"变为"就绪"时，不自动启用（让用户自己决定是否启用）
  }
})

// 普通模式配置处理函数
const handleContextTemplateChange = async () => {
  if (!conversationConfigLoaded.value) return
  
  if (localContextTemplate.value === savedContextTemplate) {
    return
  }
  
  try {
    await saveConversationConfig(
      {
        context_template: localContextTemplate.value,
      },
      t('conversationSettings.toasts.contextTemplateSaved')
    )
    savedContextTemplate = localContextTemplate.value
  } catch (error) {
    console.error('保存Context Template失败:', error)
    MessagePlugin.error(getErrorMessage(error))
  }
}

const reloadConversationConfig = async () => {
  const convRes = await getConversationConfig()
  conversationConfig.value = normalizeConversationConfig(convRes.data)
  syncConversationLocals()
}

const handleSystemPromptNormalChange = async () => {
  if (!conversationConfigLoaded.value) return
  
  if (localSystemPromptNormal.value === savedSystemPromptNormal) {
    return
  }
  
  try {
    await saveConversationConfig(
      {
        prompt: localSystemPromptNormal.value,
      },
      t('conversationSettings.toasts.systemPromptSaved')
    )
    savedSystemPromptNormal = localSystemPromptNormal.value
  } catch (error) {
    console.error('保存System Prompt失败:', error)
    MessagePlugin.error(getErrorMessage(error))
  }
}

const handleTemperatureNormalChange = async (value: number) => {
  if (!conversationConfigLoaded.value) return
  if (value === savedTemperatureNormal) return
  
  try {
    await saveConversationConfig(
      { temperature: value },
      t('conversationSettings.toasts.temperatureSaved')
    )
    savedTemperatureNormal = value
  } catch (error) {
    console.error('保存Temperature失败:', error)
    MessagePlugin.error(getErrorMessage(error))
  }
}

const handleMaxCompletionTokensChange = async (value: number) => {
  if (!conversationConfigLoaded.value) return
  
  try {
    await saveConversationConfig(
      { max_completion_tokens: value },
      t('conversationSettings.toasts.maxTokensSaved')
    )
    savedMaxCompletionTokens = value
  } catch (error) {
    console.error('保存Max Tokens失败:', error)
    MessagePlugin.error(getErrorMessage(error))
  }
}

const handleMaxRoundsChange = async (value: number) => {
  try {
    await saveConversationConfig({ max_rounds: value }, t('conversationSettings.toasts.maxRoundsSaved'))
  } catch (error) {
    console.error('保存 max_rounds 失败:', error)
    localMaxRounds.value = conversationConfig.value.max_rounds
  }
}

const handleEmbeddingTopKChange = async (value: number) => {
  try {
    await saveConversationConfig({ embedding_top_k: value }, t('conversationSettings.toasts.embeddingSaved'))
  } catch (error) {
    console.error('保存 embedding_top_k 失败:', error)
    localEmbeddingTopK.value = conversationConfig.value.embedding_top_k
  }
}

const handleKeywordThresholdChange = async (value: number) => {
  try {
    await saveConversationConfig({ keyword_threshold: value }, t('conversationSettings.toasts.keywordThresholdSaved'))
  } catch (error) {
    console.error('保存 keyword_threshold 失败:', error)
    localKeywordThreshold.value = conversationConfig.value.keyword_threshold
  }
}

const handleVectorThresholdChange = async (value: number) => {
  try {
    await saveConversationConfig({ vector_threshold: value }, t('conversationSettings.toasts.vectorThresholdSaved'))
  } catch (error) {
    console.error('保存 vector_threshold 失败:', error)
    localVectorThreshold.value = conversationConfig.value.vector_threshold
  }
}

const handleRerankTopKChange = async (value: number) => {
  try {
    await saveConversationConfig({ rerank_top_k: value }, t('conversationSettings.toasts.rerankTopKSaved'))
  } catch (error) {
    console.error('保存 rerank_top_k 失败:', error)
    localRerankTopK.value = conversationConfig.value.rerank_top_k
  }
}

const handleRerankThresholdChange = async (value: number) => {
  try {
    await saveConversationConfig({ rerank_threshold: value }, t('conversationSettings.toasts.rerankThresholdSaved'))
  } catch (error) {
    console.error('保存 rerank_threshold 失败:', error)
    localRerankThreshold.value = conversationConfig.value.rerank_threshold
  }
}

const handleEnableRewriteChange = async (value: boolean) => {
  try {
    await saveConversationConfig({ enable_rewrite: value }, t('conversationSettings.toasts.enableRewriteSaved'))
  } catch (error) {
    console.error('保存 enable_rewrite 失败:', error)
    localEnableRewrite.value = conversationConfig.value.enable_rewrite
  }
}

const handleEnableQueryExpansionChange = async (value: boolean) => {
  try {
    await saveConversationConfig(
      { enable_query_expansion: value },
      t('conversationSettings.toasts.enableQueryExpansionSaved')
    )
  } catch (error) {
    console.error('保存 enable_query_expansion 失败:', error)
    localEnableQueryExpansion.value = conversationConfig.value.enable_query_expansion ?? true
  }
}

const handleFallbackStrategyChange = async (value: 'fixed' | 'model') => {
  try {
    await saveConversationConfig({ fallback_strategy: value }, t('conversationSettings.toasts.fallbackStrategySaved'))
  } catch (error) {
    console.error('保存 fallback_strategy 失败:', error)
    localFallbackStrategy.value = (conversationConfig.value.fallback_strategy as 'fixed' | 'model') || 'fixed'
  }
}

const handleFallbackResponseChange = async () => {
  if (localFallbackResponse.value === (conversationConfig.value.fallback_response ?? '')) return
  try {
    await saveConversationConfig({ fallback_response: localFallbackResponse.value }, t('conversationSettings.toasts.fallbackResponseSaved'))
  } catch (error) {
    console.error('保存 fallback_response 失败:', error)
    localFallbackResponse.value = conversationConfig.value.fallback_response ?? ''
  }
}

const handleRewritePromptSystemChange = async () => {
  if (localRewritePromptSystem.value === (conversationConfig.value.rewrite_prompt_system ?? '')) return
  try {
    await saveConversationConfig({ rewrite_prompt_system: localRewritePromptSystem.value }, t('conversationSettings.toasts.rewritePromptSystemSaved'))
  } catch (error) {
    console.error('保存 rewrite_prompt_system 失败:', error)
    localRewritePromptSystem.value = conversationConfig.value.rewrite_prompt_system ?? ''
  }
}

const handleRewritePromptUserChange = async () => {
  if (localRewritePromptUser.value === (conversationConfig.value.rewrite_prompt_user ?? '')) return
  try {
    await saveConversationConfig({ rewrite_prompt_user: localRewritePromptUser.value }, t('conversationSettings.toasts.rewritePromptUserSaved'))
  } catch (error) {
    console.error('保存 rewrite_prompt_user 失败:', error)
    localRewritePromptUser.value = conversationConfig.value.rewrite_prompt_user ?? ''
  }
}

const handleFallbackPromptChange = async () => {
  if (localFallbackPrompt.value === (conversationConfig.value.fallback_prompt ?? '')) return
  try {
    await saveConversationConfig({ fallback_prompt: localFallbackPrompt.value }, t('conversationSettings.toasts.fallbackPromptSaved'))
  } catch (error) {
    console.error('保存 fallback_prompt 失败:', error)
    localFallbackPrompt.value = conversationConfig.value.fallback_prompt ?? ''
  }
}

// 模板选择处理函数
const handleAgentSystemPromptTemplateSelect = (template: string) => {
  localSystemPrompt.value = template
}

const handleNormalSystemPromptTemplateSelect = (template: string) => {
  localSystemPromptNormal.value = template
}

const handleContextTemplateTemplateSelect = (template: string) => {
  localContextTemplate.value = template
}

const handleRewriteSystemTemplateSelect = (template: string) => {
  localRewritePromptSystem.value = template
}

const handleRewriteUserTemplateSelect = (template: string) => {
  localRewritePromptUser.value = template
}

const handleFallbackResponseTemplateSelect = (template: string) => {
  localFallbackResponse.value = template
}

const handleFallbackPromptTemplateSelect = (template: string) => {
  localFallbackPrompt.value = template
}

const navigateToModelSettings = (subsection: 'chat' | 'rerank') => {
  router.push('/platform/settings')

  setTimeout(() => {
    const event = new CustomEvent('settings-nav', {
      detail: { section: 'models', subsection },
    })
    window.dispatchEvent(event)

    setTimeout(() => {
      const selector = subsection === 'rerank' ? '[data-model-type="rerank"]' : '[data-model-type="chat"]'
      const element = document.querySelector(selector)
      if (element) {
        element.scrollIntoView({ behavior: 'smooth', block: 'start' })
      }
    }, 200)
  }, 100)
}

const handleConversationSummaryModelChange = async (value: string) => {
  if (value === '__add_model__') {
    localSummaryModelId.value = conversationConfig.value.summary_model_id ?? ''
    navigateToModelSettings('chat')
    return
  }

  try {
    await saveConversationConfig({ summary_model_id: value }, t('conversationSettings.toasts.chatModelSaved'))
  } catch (error) {
    console.error('保存 summary_model_id 失败:', error)
    localSummaryModelId.value = conversationConfig.value.summary_model_id ?? ''
  }
}

const handleConversationRerankModelChange = async (value: string) => {
  if (value === '__add_model__') {
    localConversationRerankModelId.value = conversationConfig.value.rerank_model_id ?? ''
    navigateToModelSettings('rerank')
    return
  }

  try {
    await saveConversationConfig({ rerank_model_id: value }, t('conversationSettings.toasts.rerankModelSaved'))
  } catch (error) {
    console.error('保存 rerank_model_id 失败:', error)
    localConversationRerankModelId.value = conversationConfig.value.rerank_model_id ?? ''
  }
}
</script>

<style lang="less" scoped>
.agent-settings {
  width: 100%;
}


.section-header {

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: #333333;
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: #666666;
    margin: 0 0 12px 0;
    line-height: 1.5;
  }

  .global-config-notice {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 12px 16px;
    background: #f0f9ff;
    border: 1px solid #bae0ff;
    border-radius: 8px;
    margin-bottom: 20px;
    color: #0958d9;
    font-size: 13px;
    line-height: 1.5;

    .t-icon {
      font-size: 16px;
      flex-shrink: 0;
      margin-top: 2px;
    }
  }
}

.agent-status-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 20px 0;
  border-bottom: 1px solid #e5e7eb;
  margin-top: 8px;

  .status-label {
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
  }

  .status-control {
    flex-shrink: 0;
    min-width: 280px;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 8px;

    .status-badge {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      padding: 4px 12px;
      border-radius: 4px;
      font-size: 14px;
      font-weight: 500;

      &.ready {
        background: #f0fdf4;
        color: #16a34a;
        
        .status-icon {
          color: #16a34a;
          font-size: 16px;
        }
      }

      &:not(.ready) {
        background: #fff7ed;
        color: #ea580c;
        
        .status-icon {
          color: #ea580c;
          font-size: 16px;
        }
      }

      .status-text {
        line-height: 1.4;
      }
    }

    .status-hint {
      font-size: 13px;
      color: #666666;
      text-align: right;
      line-height: 1.5;
      max-width: 280px;
    }

    .status-tip {
      margin: 8px 0 0 0;
      font-size: 12px;
      color: #999999;
      text-align: right;
      line-height: 1.5;
      max-width: 280px;
      display: flex;
      align-items: flex-start;
      gap: 4px;
      justify-content: flex-end;

      .tip-icon {
        font-size: 14px;
        color: #999999;
        flex-shrink: 0;
        margin-top: 2px;
      }
    }
  }
}

.model-recommendation-box {
  margin: 20px 0;
  background: #f0fdf6;
  border: 1px solid #d1fae5;
  border-left: 3px solid #07C05F;
  border-radius: 6px;
  padding: 16px;

  .recommendation-header {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;

    .recommendation-icon {
      font-size: 16px;
      color: #07C05F;
      flex-shrink: 0;
    }

    .recommendation-title {
      font-size: 14px;
      font-weight: 500;
      color: #059669;
    }
  }

  .recommendation-content {
    font-size: 13px;
    line-height: 1.6;
    color: #065f46;

    p {
      margin: 0;
    }
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

  &.vertical {
    flex-direction: column;
    align-items: flex-start;

    .setting-info {
      margin-bottom: 12px;
      max-width: 100%;
    }

    .setting-control.full-width {
      width: 100%;
    }
  }
}

.setting-info {
  flex: 1;
  max-width: 55%;
  word-break: keep-all;
  white-space: normal;

  .setting-info-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 4px;
    
    label {
      margin-bottom: 0;
    }
  }

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

  .hint-tip {
    margin: 8px 0 0 0;
    font-size: 12px;
    color: #999999;
    line-height: 1.5;
    display: flex;
    align-items: flex-start;
    gap: 4px;

    .tip-icon {
      font-size: 14px;
      color: #999999;
      flex-shrink: 0;
      margin-top: 2px;
    }
  }
}

.model-row {
  display: flex;
  flex-wrap: wrap;
  gap: 24px;
}

.model-column {
  min-width: 260px;
  flex: 1;
}

.model-column-label {
  font-size: 13px;
  font-weight: 500;
  color: #555;
  margin-bottom: 4px;
}

.model-column-desc {
  margin: 0 0 8px 0;
  font-size: 12px;
  color: #888;
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
}

.slider-with-value {
  display: flex;
  align-items: center;
  gap: 16px;
  justify-content: flex-end;

  .value-display {
    font-size: 14px;
    font-weight: 500;
    color: #333333;
    min-width: 40px;
    text-align: right;
  }
}

// 模型选择器样式
.model-option {
  display: flex;
  align-items: center;
  gap: 8px;
  
  .model-icon {
    font-size: 14px;
    color: #07C05F;
  }
  
  .add-icon {
    font-size: 14px;
    color: #07C05F;
  }
  
  .model-name {
    flex: 1;
    font-size: 13px;
  }
  
  &.add {
    .model-name {
      color: #07C05F;
      font-weight: 500;
    }
  }
}

.prompt-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  width: 100%;
}

.prompt-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
}

.prompt-toggle-label {
  font-size: 13px !important;
  color: #555;
}

.prompt-toggle :deep(.t-switch) {
  font-size: 0;
}

.prompt-toggle :deep(.t-switch__label),
.prompt-toggle :deep(.t-switch__content) {
  font-size: 12px !important;
  line-height: 18px;
  color: #666;
}

.prompt-toggle :deep(.t-switch__label--off),
.prompt-toggle :deep(.t-switch__content) {
  color: #fafafa !important;
}

.prompt-disabled-hint {
  margin: 0 0 8px;
  color: #666;
  font-size: 12px;
}

.prompt-tab-hint {
  margin: 0 0 12px;
  color: #666;
  font-size: 12px;
}

.system-prompt-tabs {
  width: 100%;
}

.allowed-tools-display {
  width: 100%;
}

.allowed-tool-list {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}

.allowed-tool-chip {
  background: #f5f7fa;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  padding: 10px 12px;
  min-width: 180px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.allowed-tool-label {
  font-size: 13px;
  font-weight: 600;
  color: #1d2129;
}

.allowed-tool-desc {
  font-size: 12px;
  color: #666;
  line-height: 1.4;
}

.allowed-tools-empty {
  margin: 0;
  font-size: 12px;
  color: #999;
}

.prompt-textarea-readonly {
  background-color: #fafafa;
}

.prompt-textarea-wrapper {
  width: 100%;
}

.textarea-with-template {
  position: relative;
  width: 100%;
}

.setting-control.full-width {
  display: flex;
  flex-direction: column;
  align-items: stretch;
}

.placeholder-hint {
  margin-top: 12px;
  padding: 12px;
  background: #f5f7fa;
  border-radius: 4px;
  font-size: 12px;
  line-height: 1.6;

  .hint-title {
    font-weight: 500;
    color: #333;
    margin: 0 0 8px 0;
  }

  .placeholder-list {
    margin: 8px 0;
    padding-left: 20px;
    color: #666;

    li {
      margin: 4px 0;

      code {
        background: #fff;
        padding: 2px 6px;
        border-radius: 3px;
        font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
        font-size: 11px;
        color: #e83e8c;
        border: 1px solid #e1e8ed;
      }
    }
  }

  .hint-tip {
    margin: 8px 0 0 0;
    color: #999;
    font-style: italic;
  }
}

.placeholder-popup-wrapper {
  position: fixed;
  z-index: 10001;
  pointer-events: auto;
}

.placeholder-popup {
  background: #fff;
  border: 1px solid #e5e7eb;
  border-radius: 4px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  max-width: 400px;
  max-height: 300px;
  overflow-y: auto;
  padding: 4px 0;
}

.placeholder-item {
  padding: 8px 12px;
  cursor: pointer;
  transition: background-color 0.2s;

  &:hover,
  &.active {
    background-color: #f5f7fa;
  }

  .placeholder-name {
    font-weight: 500;
    margin-bottom: 4px;

    code {
      background: #f5f7fa;
      padding: 2px 6px;
      border-radius: 3px;
      font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
      font-size: 12px;
      color: #e83e8c;
    }
  }

  .placeholder-desc {
    font-size: 12px;
    color: #666;
    line-height: 1.4;
  }
}

</style>

