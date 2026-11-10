<template>
  <div class="chat-history-settings">
    <div class="section-header">
      <h2>{{ t('chatHistorySettings.title') }}</h2>
      <p class="section-description">{{ t('chatHistorySettings.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- 启用开关 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('chatHistorySettings.enableLabel') }}</label>
          <p class="desc">{{ t('chatHistorySettings.enableDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="localEnabled"
            :disabled="controlsDisabled"
            @change="handleEnabledChange"
          />
        </div>
      </div>

      <!-- Embedding 模型选择 -->
      <div v-if="localEnabled || localEmbeddingModelId" class="setting-row">
        <div class="setting-info">
          <label>{{ t('chatHistorySettings.embeddingModelLabel') }}</label>
          <p class="desc">{{ t('chatHistorySettings.embeddingModelDescription') }}</p>
          <p v-if="modelLocked" class="desc warning-text">
            {{ t('chatHistorySettings.embeddingModelLocked') }}
          </p>
        </div>
        <div class="setting-control" style="min-width: 280px;">
          <ModelSelector
            model-type="Embedding"
            :selected-model-id="localEmbeddingModelId"
            :disabled="modelLocked || controlsDisabled"
            @update:selected-model-id="handleModelChange"
          />
        </div>
      </div>
    </div>

    <!-- 统计信息 -->
    <div class="stats-section">
      <h3 class="stats-title">{{ t('chatHistorySettings.statsTitle') }}</h3>
      <div v-if="stats && stats.knowledge_base_id" class="stats-grid">
        <div class="stat-card">
          <div class="stat-value">{{ stats.indexed_message_count }}</div>
          <div class="stat-label">{{ t('chatHistorySettings.statsIndexedMessages') }}</div>
        </div>
      </div>
      <div v-else class="stats-empty">
        <p class="stats-empty-title">{{ t('chatHistorySettings.statsNotConfigured') }}</p>
        <p class="stats-empty-desc">{{ t('chatHistorySettings.statsNotConfiguredDesc') }}</p>
      </div>
    </div>

    <div v-if="stats?.knowledge_base_id" class="setting-row clear-index-row">
      <div class="setting-info">
        <label>{{ t('chatHistorySettings.clearIndexLabel') }}</label>
        <p class="desc">{{ t('chatHistorySettings.clearIndexDescription') }}</p>
      </div>
      <div class="setting-control">
        <t-button theme="danger" variant="outline" :disabled="controlsDisabled" @click="requestClearIndex">
          {{ t('chatHistorySettings.clearIndexButton') }}
        </t-button>
      </div>
    </div>

    <t-dialog
      v-model:visible="clearDialogVisible"
      theme="warning"
      :header="t('chatHistorySettings.clearIndexLabel')"
      :body="t('chatHistorySettings.clearIndexConfirm')"
      :confirm-btn="{ content: t('chatHistorySettings.clearIndexButton'), theme: 'danger', loading: isClearing }"
      :cancel-btn="{ content: t('common.cancel'), disabled: isClearing }"
      :close-btn="!isClearing"
      :close-on-overlay-click="false"
      :close-on-esc-keydown="!isClearing"
      @confirm="clearIndex"
      @cancel="closeClearDialog"
      @close="closeClearDialog"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import ModelSelector from '@/components/ModelSelector.vue'
import {
  getTenantChatHistoryConfig,
  updateTenantChatHistoryConfig,
  getChatHistoryKBStats,
  clearChatHistoryIndex,
  type ChatHistoryConfig,
  type ChatHistoryKBStats,
} from '@/api/chat-history'

const { t } = useI18n()
const authStore = useAuthStore()
const canEdit = computed(() => authStore.canAccessAllTenants || authStore.hasRole('admin'))

// Local state
const localEnabled = ref(false)
const localEmbeddingModelId = ref('')
const isInitializing = ref(true)
const initialConfig = ref<ChatHistoryConfig | null>(null)
const stats = ref<ChatHistoryKBStats | null>(null)
const isSaving = ref(false)
const isClearing = ref(false)
const clearDialogVisible = ref(false)
let mounted = true
let saveTimer: number | null = null

// Whether the embedding model is locked (has indexed messages — cannot change)
const modelLocked = ref(true)
const controlsDisabled = computed(() =>
  !canEdit.value || !initialConfig.value || isInitializing.value ||
  isSaving.value || isClearing.value || clearDialogVisible.value,
)

const applyConfig = (config: ChatHistoryConfig) => {
  initialConfig.value = { ...config }
  localEnabled.value = config.enabled || false
  localEmbeddingModelId.value = config.embedding_model_id || ''
}

const applyStats = (data: ChatHistoryKBStats) => {
  stats.value = data
  modelLocked.value = data.has_indexed_messages === true
}

// Load tenant config
const loadConfig = async () => {
  try {
    const response = await getTenantChatHistoryConfig()
    if (mounted && response.data) applyConfig(response.data)
  } catch (error: any) {
    console.error('Failed to load chat history config:', error)
    if (mounted) MessagePlugin.error(t('chatHistorySettings.toasts.loadFailed'))
  }
}

// Load stats
const loadStats = async () => {
  try {
    const response = await getChatHistoryKBStats()
    if (mounted && response.data) applyStats(response.data)
  } catch (error: any) {
    console.error('Failed to load chat history stats:', error)
    // A failed count must not be interpreted as an empty index.
    if (mounted) {
      modelLocked.value = true
      MessagePlugin.error(t('chatHistorySettings.toasts.loadFailed'))
    }
  }
}

// Check if config changed
const hasConfigChanged = (): boolean => {
  if (!initialConfig.value) return true
  const initial = initialConfig.value
  if (localEnabled.value !== initial.enabled) return true
  if (localEmbeddingModelId.value !== initial.embedding_model_id) return true
  return false
}

// Save config
const saveConfig = async () => {
  if (controlsDisabled.value || !hasConfigChanged()) return

  isSaving.value = true
  let saved = false
  try {
    const config: ChatHistoryConfig = {
      enabled: localEnabled.value,
      embedding_model_id: localEmbeddingModelId.value,
    }

    const response = await updateTenantChatHistoryConfig(config)
    if (!mounted) return

    // Update initial config from response (includes auto-managed knowledge_base_id)
    if (response.data) {
      initialConfig.value = {
        enabled: response.data.enabled || false,
        embedding_model_id: response.data.embedding_model_id || '',
      }
    } else {
      initialConfig.value = { ...config }
    }

    saved = true
    MessagePlugin.success(t('chatHistorySettings.toasts.saveSuccess'))
    await loadStats()
  } catch (error: any) {
    console.error('Failed to save chat history config:', error)
    const errorMessage = error?.message || 'Unknown error'
    if (mounted) MessagePlugin.error(t('chatHistorySettings.toasts.saveFailed', { message: errorMessage }))
  } finally {
    isSaving.value = false
    if (mounted && saved && hasConfigChanged()) debouncedSave()
  }
}

// Debounced save
const debouncedSave = () => {
  if (controlsDisabled.value) return
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = window.setTimeout(() => {
    saveTimer = null
    saveConfig().catch(() => {})
  }, 500)
}

// Handlers
const handleEnabledChange = () => debouncedSave()
const handleModelChange = (modelId: string) => {
  if (controlsDisabled.value || modelLocked.value) return
  localEmbeddingModelId.value = modelId
  debouncedSave()
}

const requestClearIndex = () => {
  if (controlsDisabled.value || !stats.value?.knowledge_base_id) return
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = null
  clearDialogVisible.value = true
}

const closeClearDialog = () => {
  if (isClearing.value) return
  clearDialogVisible.value = false
  if (hasConfigChanged()) debouncedSave()
}

const clearIndex = async () => {
  if (!canEdit.value || !clearDialogVisible.value || isClearing.value || isSaving.value) return
  isClearing.value = true
  try {
    const response = await clearChatHistoryIndex()
    if (!mounted) return
    applyStats(response.data)
    applyConfig({
      enabled: response.data.enabled,
      embedding_model_id: response.data.embedding_model_id || '',
      knowledge_base_id: response.data.knowledge_base_id,
    })
    clearDialogVisible.value = false
    MessagePlugin.success(t('chatHistorySettings.toasts.clearSuccess'))
  } catch (error: any) {
    if (!mounted) return
    MessagePlugin.error(t('chatHistorySettings.toasts.clearFailed', { message: error?.message || '' }))
    // Cleanup may have paused indexing or removed some entries before failing.
    // Refresh that persisted state instead of restoring a stale enabled draft.
    await Promise.all([loadConfig(), loadStats()])
  } finally {
    isClearing.value = false
  }
}

// Init
onMounted(async () => {
  await Promise.all([loadConfig(), loadStats()])
  if (mounted) isInitializing.value = false
})

onBeforeUnmount(() => {
  mounted = false
  if (saveTimer) clearTimeout(saveTimer)
})
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/settings-section.less';

.chat-history-settings {
  width: 100%;
}

.section-header {
  .settings-section-header();
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  .setting-row();
}

.setting-info {
  .setting-info();
}

.warning-text {
  color: var(--td-warning-color) !important;
  margin-top: 4px !important;
}

.setting-control {
  .setting-control();
}

.clear-index-row {
  margin-top: 20px;
  flex-wrap: wrap;
  gap: 16px;

  .setting-info {
    flex-basis: 320px;
  }
}

// Stats section
.stats-section {
  margin-top: 32px;
  padding-top: 24px;
  border-top: 1px solid var(--td-component-stroke);
}

.stats-title {
  font-size: var(--app-text-xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 16px 0;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 16px;
}

.stat-card {
  background: var(--td-bg-color-secondarycontainer);
  border-radius: var(--app-radius-md);
  padding: 20px;
  text-align: center;
}

.stat-value {
  font-size: 28px;
  font-weight: 700;
  color: var(--td-brand-color);
  margin-bottom: 4px;
}

.stat-label {
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
}

.stats-empty {
  background: var(--td-bg-color-secondarycontainer);
  border-radius: var(--app-radius-md);
  padding: 24px;
  text-align: center;
}

.stats-empty-title {
  font-size: var(--app-text-base);
  font-weight: 500;
  color: var(--td-text-color-secondary);
  margin: 0 0 4px 0;
}

.stats-empty-desc {
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
  margin: 0;
}
</style>
