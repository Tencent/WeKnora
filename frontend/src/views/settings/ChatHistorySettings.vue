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
            @change="handleEnabledChange"
          />
        </div>
      </div>

      <!-- Embedding 模型选择 -->
      <div v-if="localEnabled" class="setting-row">
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
            :disabled="modelLocked"
            @update:selected-model-id="handleModelChange"
          />
        </div>
      </div>
    </div>

    <!-- 统计信息 -->
    <div class="stats-section">
      <h3 class="stats-title">{{ t('chatHistorySettings.statsTitle') }}</h3>
      <div v-if="stats && stats.enabled && stats.knowledge_base_id" class="stats-grid">
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
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, nextTick } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import ModelSelector from '@/components/ModelSelector.vue'
import {
  getTenantChatHistoryConfig,
  updateTenantChatHistoryConfig,
  getChatHistoryKBStats,
  type ChatHistoryConfig,
  type ChatHistoryKBStats,
} from '@/api/chat-history'

const { t } = useI18n()

// Local state
const localEnabled = ref(false)
const localEmbeddingModelId = ref('')
const isInitializing = ref(true)
const initialConfig = ref<ChatHistoryConfig | null>(null)
const stats = ref<ChatHistoryKBStats | null>(null)

// Whether the embedding model is locked (has indexed messages — cannot change)
const modelLocked = ref(false)

// Load tenant config
const loadConfig = async () => {
  try {
    const response = await getTenantChatHistoryConfig()
    if (response.data) {
      const config = response.data
      isInitializing.value = true

      initialConfig.value = {
        enabled: config.enabled || false,
        embedding_model_id: config.embedding_model_id || '',
      }

      localEnabled.value = config.enabled || false
      localEmbeddingModelId.value = config.embedding_model_id || ''

      await nextTick()
      await nextTick()
      setTimeout(() => { isInitializing.value = false }, 100)
    } else {
      initialConfig.value = {
        enabled: false,
        embedding_model_id: '',
      }
      await nextTick()
      setTimeout(() => { isInitializing.value = false }, 100)
    }
  } catch (error: any) {
    console.error('Failed to load chat history config:', error)
    initialConfig.value = {
      enabled: false,
      embedding_model_id: '',
    }
    await nextTick()
    setTimeout(() => { isInitializing.value = false }, 100)
  }
}

// Load stats
const loadStats = async () => {
  try {
    const response = await getChatHistoryKBStats()
    if (response.data) {
      stats.value = response.data
      // Lock model if there are indexed messages
      modelLocked.value = response.data.has_indexed_messages === true
    }
  } catch (error: any) {
    console.error('Failed to load chat history stats:', error)
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
  if (!hasConfigChanged()) return

  try {
    const config: ChatHistoryConfig = {
      enabled: localEnabled.value,
      embedding_model_id: localEmbeddingModelId.value,
    }

    const response = await updateTenantChatHistoryConfig(config)

    // Update initial config from response (includes auto-managed knowledge_base_id)
    if (response.data) {
      initialConfig.value = {
        enabled: response.data.enabled || false,
        embedding_model_id: response.data.embedding_model_id || '',
      }
    } else {
      initialConfig.value = { ...config }
    }

    MessagePlugin.success(t('chatHistorySettings.toasts.saveSuccess'))
    // Refresh stats after save
    loadStats()
  } catch (error: any) {
    console.error('Failed to save chat history config:', error)
    const errorMessage = error?.message || 'Unknown error'
    MessagePlugin.error(t('chatHistorySettings.toasts.saveFailed', { message: errorMessage }))
  }
}

// Debounced save
let saveTimer: number | null = null
const debouncedSave = () => {
  if (isInitializing.value) return
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = window.setTimeout(() => {
    saveConfig().catch(() => {})
  }, 500)
}

// Handlers
const handleEnabledChange = () => debouncedSave()
const handleModelChange = (modelId: string) => {
  localEmbeddingModelId.value = modelId
  debouncedSave()
}

// Init
onMounted(async () => {
  isInitializing.value = true
  await loadConfig()
  await loadStats()
})
</script>

<style lang="less" scoped>
.chat-history-settings {
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
  flex: 1;
  max-width: 65%;
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
}

.warning-text {
  color: var(--td-warning-color) !important;
  margin-top: 4px !important;
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
}

// Stats section
.stats-section {
  margin-top: 32px;
  padding-top: 24px;
  border-top: 1px solid var(--td-component-stroke);
}

.stats-title {
  font-size: 16px;
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
  border-radius: 8px;
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
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.stats-empty {
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
  padding: 24px;
  text-align: center;
}

.stats-empty-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  margin: 0 0 4px 0;
}

.stats-empty-desc {
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  margin: 0;
}
</style>
