<template>
  <div class="ollama-settings">
    <div class="section-header">
      <h2>{{ $t('ollamaSettings.title') }}</h2>
      <p class="section-description">{{ $t('ollamaSettings.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- Ollama 服务状态 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('ollamaSettings.status.label') }}</label>
          <p class="desc">{{ $t('ollamaSettings.status.desc') }}</p>
        </div>
        <div class="setting-control">
          <div class="status-display">
            <t-tag 
              v-if="testing"
              theme="default"
              variant="light"
            >
              <t-icon name="loading" class="status-icon spinning" />
              {{ $t('ollamaSettings.status.testing') }}
            </t-tag>
            <t-tag 
              v-else-if="connectionStatus === true"
              theme="success"
              variant="light"
            >
              <t-icon name="check-circle-filled" />
              {{ $t('ollamaSettings.status.available') }}
            </t-tag>
            <t-tag 
              v-else-if="connectionStatus === false"
              theme="danger"
              variant="light"
            >
              <t-icon name="close-circle-filled" />
              {{ $t('ollamaSettings.status.unavailable') }}
            </t-tag>
            <t-tag 
              v-else
              theme="default"
              variant="light"
            >
              <t-icon name="help-circle" />
              {{ $t('ollamaSettings.status.untested') }}
            </t-tag>
            <t-button 
              size="small" 
              variant="outline"
              :loading="testing"
              @click="testConnection"
            >
              <template #icon>
                <t-icon name="refresh" />
              </template>
              {{ $t('ollamaSettings.status.retest') }}
            </t-button>
          </div>
        </div>
      </div>

      <!-- Ollama 服务地址 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('ollamaSettings.address.label') }}</label>
          <p class="desc">{{ $t('ollamaSettings.address.desc') }}</p>
        </div>
        <div class="setting-control">
          <div class="url-control-group">
            <t-input 
              v-model="localBaseUrl" 
              :placeholder="$t('ollamaSettings.address.placeholder')"
              disabled
              style="flex: 1;"
            />
          </div>
          <t-alert 
            v-if="connectionStatus === false"
            theme="warning"
            :message="$t('ollamaSettings.address.failed')"
            style="margin-top: 8px;"
          />
        </div>
      </div>

    </div>

    <!-- 下载新模型 -->
    <div v-if="connectionStatus === true" class="model-category-section">
      <div class="category-header">
        <div class="header-info">
          <h3>{{ $t('ollamaSettings.download.title') }}</h3>
          <p>
            {{ $t('ollamaSettings.download.descPrefix') }}
            <a href="https://ollama.com/search" target="_blank" rel="noopener noreferrer" class="model-link">
              {{ $t('ollamaSettings.download.browse') }}
              <t-icon name="link" class="link-icon" />
            </a>
          </p>
        </div>
      </div>
      
      <div class="download-content">
        <div class="input-group">
          <t-input 
            v-model="downloadModelName" 
            :placeholder="$t('ollamaSettings.download.placeholder')"
            style="flex: 1;"
          />
          <t-button 
            theme="primary"
            size="small"
            :loading="downloading"
            :disabled="!downloadModelName.trim()"
            @click="downloadModel"
          >
            {{ $t('ollamaSettings.download.download') }}
          </t-button>
        </div>
        
        <div v-if="downloadProgress > 0" class="download-progress">
          <div class="progress-info">
            <span>{{ $t('ollamaSettings.download.downloading', { name: downloadModelName }) }}</span>
            <span>{{ downloadProgress.toFixed(2) }}%</span>
          </div>
          <t-progress :percentage="downloadProgress" size="small" />
        </div>
      </div>
    </div>

    <!-- 已下载的模型 -->
    <div v-if="connectionStatus === true" class="model-category-section">
      <div class="category-header">
        <div class="header-info">
          <h3>{{ $t('ollamaSettings.installed.title') }}</h3>
          <p>{{ $t('ollamaSettings.installed.desc') }}</p>
        </div>
        <t-button 
          size="small" 
          variant="text"
          :loading="loadingModels"
          @click="refreshModels"
        >
          <template #icon>
            <t-icon name="refresh" />
          </template>
          {{ $t('common.refresh') }}
        </t-button>
      </div>
      
      <div v-if="loadingModels" class="loading-state">
        <t-loading size="small" />
        <span>{{ $t('common.loading') }}</span>
      </div>
      <div v-else-if="downloadedModels.length > 0" class="model-list-container">
        <div v-for="model in downloadedModels" :key="model.name" class="model-card">
          <div class="model-info">
            <div class="model-name">{{ model.name }}</div>
            <div class="model-meta">
              <span class="model-size">{{ formatSize(model.size) }}</span>
              <span class="model-modified">{{ formatDate(model.modified_at) }}</span>
            </div>
          </div>
        </div>
      </div>
      <div v-else class="empty-state">
        <p class="empty-text">{{ $t('ollamaSettings.installed.empty') }}</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useSettingsStore } from '@/stores/settings'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { checkOllamaStatus, listOllamaModels, downloadOllamaModel, getDownloadProgress, type OllamaModelInfo } from '@/api/initialization'

const settingsStore = useSettingsStore()
const { t } = useI18n()

const localBaseUrl = ref(settingsStore.settings.ollamaConfig?.baseUrl ?? '')

const testing = ref(false)
const connectionStatus = ref<boolean | null>(null)
const loadingModels = ref(false)
const downloadedModels = ref<OllamaModelInfo[]>([])
const downloading = ref(false)
const downloadModelName = ref('')
const downloadProgress = ref(0)

// 测试连接
const testConnection = async () => {
  testing.value = true
  connectionStatus.value = null
  
  try {
    // 保存配置
    settingsStore.updateOllamaConfig({ baseUrl: localBaseUrl.value })
    
    // 调用真实 Ollama API 测试连接
    const result = await checkOllamaStatus()
    
    // 如果接口返回了 baseUrl 且与当前输入框的值不同，更新为接口返回的值
    if (result.baseUrl && result.baseUrl !== localBaseUrl.value) {
      localBaseUrl.value = result.baseUrl
      settingsStore.updateOllamaConfig({ baseUrl: result.baseUrl })
    }
    
    connectionStatus.value = result.available
    
    if (connectionStatus.value) {
      MessagePlugin.success(t('ollamaSettings.toasts.connected'))
      refreshModels()
    } else {
      MessagePlugin.error(result.error || t('ollamaSettings.toasts.connectFailed'))
    }
  } catch (error: any) {
    connectionStatus.value = false
    MessagePlugin.error(error.message || t('ollamaSettings.toasts.connectFailed'))
  } finally {
    testing.value = false
  }
}

// 刷新模型列表
const refreshModels = async () => {
  loadingModels.value = true
  
  try {
    // 调用真实 Ollama API 获取模型列表（现在返回完整的模型信息）
    const models = await listOllamaModels()
    downloadedModels.value = models
  } catch (error: any) {
    console.error('获取模型列表失败:', error)
    MessagePlugin.error(error.message || t('ollamaSettings.toasts.listFailed'))
  } finally {
    loadingModels.value = false
  }
}

// 格式化文件大小
const formatSize = (bytes: number): string => {
  if (!bytes || bytes === 0 || isNaN(bytes)) return '0 B'
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(2) + ' KB'
  if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(2) + ' MB'
  return (bytes / (1024 * 1024 * 1024)).toFixed(2) + ' GB'
}

// 格式化日期
const formatDate = (dateStr: string): string => {
  if (!dateStr) return '未知'
  
  const date = new Date(dateStr)
  // 检查日期是否有效
  if (isNaN(date.getTime())) return '未知'
  
  const now = new Date()
  const diff = now.getTime() - date.getTime()
  const days = Math.floor(diff / (1000 * 60 * 60 * 24))
  
  if (days === 0) return '今天'
  if (days === 1) return '昨天'
  if (days < 7) return `${days} 天前`
  if (days < 0) return date.toLocaleDateString('zh-CN')
  return date.toLocaleDateString('zh-CN')
}

// 下载模型
const downloadModel = async () => {
  if (!downloadModelName.value.trim()) return
  
  downloading.value = true
  downloadProgress.value = 0
  
  try {
    // 调用真实 Ollama API 下载模型
    const result = await downloadOllamaModel(downloadModelName.value)
    
    if (result.status === 'failed') {
      MessagePlugin.error(t('ollamaSettings.toasts.downloadFailed'))
      downloading.value = false
      downloadProgress.value = 0
      return
    }
    
    MessagePlugin.success(t('ollamaSettings.toasts.downloadStarted', { name: downloadModelName.value }))
    
    // 查询下载进度
    const taskId = result.taskId
    const progressInterval = setInterval(async () => {
      try {
        const task = await getDownloadProgress(taskId)
        downloadProgress.value = task.progress
        
        if (task.status === 'completed') {
          clearInterval(progressInterval)
          MessagePlugin.success(t('ollamaSettings.toasts.downloadCompleted', { name: downloadModelName.value }))
          downloadModelName.value = ''
          downloadProgress.value = 0
          downloading.value = false
          refreshModels()
        } else if (task.status === 'failed') {
          clearInterval(progressInterval)
          MessagePlugin.error(task.message || t('ollamaSettings.toasts.downloadFailed'))
          downloading.value = false
          downloadProgress.value = 0
        }
      } catch (error) {
        clearInterval(progressInterval)
        MessagePlugin.error(t('ollamaSettings.toasts.progressFailed'))
        downloading.value = false
        downloadProgress.value = 0
      }
    }, 1000)
  } catch (error: any) {
    console.error('下载失败:', error)
    MessagePlugin.error(error.message || t('ollamaSettings.toasts.downloadFailed'))
    downloading.value = false
    downloadProgress.value = 0
  }
}

// 初始化 Ollama 服务地址
const initOllamaBaseUrl = async () => {
  try {
    const result = await checkOllamaStatus()
    // 如果接口返回了 baseUrl，优先使用接口返回的值
    if (result.baseUrl) {
      localBaseUrl.value = result.baseUrl
      // 如果 store 中没有保存过，也保存到 store 中
      if (!settingsStore.settings.ollamaConfig?.baseUrl) {
        settingsStore.updateOllamaConfig({ baseUrl: result.baseUrl })
      }
    } else if (!localBaseUrl.value) {
      // 如果接口没返回且 store 中也没有，使用默认值
      localBaseUrl.value = 'http://localhost:11434'
    }
    
    // 直接使用初始化时获取的状态，避免重复调用
      connectionStatus.value = result.available
      if (result.available) {
        refreshModels()
    }
    
    return result
  } catch (error) {
    console.error('初始化 Ollama 地址失败:', error)
    // 如果获取失败，使用默认值或 store 中的值
    if (!localBaseUrl.value) {
      localBaseUrl.value = 'http://localhost:11434'
    }
    return null
  }
}

// 组件挂载时自动检查连接
onMounted(async () => {
  // 初始化服务地址，如果启用则直接使用返回的状态，避免重复调用
  await initOllamaBaseUrl()
})
</script>

<style lang="less" scoped>
.ollama-settings {
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
  padding-right: 32px;

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
    line-height: 1.6;
  }
}

.setting-control {
  flex-shrink: 0;
  min-width: 360px;
  max-width: 360px;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
}

.status-display {
  display: flex;
  align-items: center;
  gap: 12px;

  .status-icon.spinning {
    animation: spin 1s linear infinite;
  }
}

.url-control-group {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 8px;
}

.model-category-section {
  margin-top: 32px;
  margin-bottom: 32px;
  padding-top: 32px;
  border-top: 1px solid #e5e7eb;

  &:first-of-type {
    margin-top: 24px;
    padding-top: 24px;
  }

  &:last-child {
    margin-bottom: 0;
  }
}

.category-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 24px;

  .header-info {
    flex: 1;

    h3 {
      font-size: 17px;
      font-weight: 600;
      color: #333333;
      margin: 0 0 6px 0;
    }

    p {
      font-size: 13px;
      color: #999999;
      margin: 0;
      line-height: 1.5;
    }

    .model-link {
      color: #07C05F;
      text-decoration: none;
      font-weight: 500;
      display: inline-flex;
      align-items: center;
      gap: 4px;
      transition: all 0.2s ease;

      &:hover {
        color: #05a04f;
        text-decoration: underline;
      }

      .link-icon {
        font-size: 12px;
      }
    }
  }
}

.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 48px 0;
  color: #999999;
  font-size: 14px;
}

.model-list-container {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;

  @media (max-width: 768px) {
    grid-template-columns: 1fr;
  }
}

.model-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
  border: 1px solid #e5e7eb;
  border-radius: 6px;
  background: #fafafa;
  transition: all 0.2s;

  &:hover {
    border-color: #07C05F;
    background: #ffffff;
  }
}

.model-info {
  flex: 1;
  min-width: 0;

  .model-name {
    font-size: 14px;
    font-weight: 500;
    color: #333333;
    margin-bottom: 4px;
    font-family: monospace;
  }

  .model-meta {
    display: flex;
    gap: 12px;
    font-size: 12px;
    color: #666666;
  }
}

.download-content {
  display: flex;
  flex-direction: column;
  gap: 16px;

  .input-group {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .download-progress {
    padding: 16px;
    background: #f8f9fa;
    border-radius: 8px;
    border: 1px solid #e5e7eb;

    .progress-info {
      display: flex;
      justify-content: space-between;
      margin-bottom: 10px;
      font-size: 13px;
      color: #333333;
      font-weight: 500;
    }
  }
}

.empty-state {
  padding: 48px 0;
  text-align: center;

  .empty-text {
    font-size: 14px;
    color: #999999;
    margin: 0;
  }
}

@keyframes spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}
</style>
