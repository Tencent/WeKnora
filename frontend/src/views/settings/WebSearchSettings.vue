<template>
  <div class="websearch-settings">
    <div class="section-header">
      <h2>{{ t('webSearchSettings.title') }}</h2>
      <p class="section-description">{{ t('webSearchSettings.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- 搜索引擎提供商 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('webSearchSettings.providerLabel') }}</label>
          <p class="desc">{{ t('webSearchSettings.providerDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localProvider"
            :loading="loadingProviders"
            filterable
            :placeholder="t('webSearchSettings.providerPlaceholder')"
            @change="handleProviderChange"
            @focus="loadProviders"
            style="width: 280px;"
          >
            <t-option
              v-for="provider in providers"
              :key="provider.id"
              :value="provider.id"
              :label="provider.name"
            >
              <div class="provider-option-wrapper">
                <div class="provider-option">
                  <span class="provider-name">{{ provider.name }}</span>
                </div>
              </div>
            </t-option>
          </t-select>
        </div>
      </div>

      <!-- API 密钥 -->
      <div v-if="selectedProvider && selectedProvider.requires_api_key" class="setting-row">
        <div class="setting-info">
          <label>{{ t('webSearchSettings.apiKeyLabel') }}</label>
          <p class="desc">{{ t('webSearchSettings.apiKeyDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-input
            v-model="localAPIKey"
            type="password"
            :placeholder="t('webSearchSettings.apiKeyPlaceholder')"
            @change="handleAPIKeyChange"
            style="width: 400px;"
            :show-password="true"
          />
        </div>
      </div>

      <!-- 最大结果数 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('webSearchSettings.maxResultsLabel') }}</label>
          <p class="desc">{{ t('webSearchSettings.maxResultsDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="slider-with-value">
            <t-slider 
              v-model="localMaxResults" 
              :min="1" 
              :max="50" 
              :step="1"
              :marks="{ 1: '1', 10: '10', 20: '20', 30: '30', 40: '40', 50: '50' }"
              @change="handleMaxResultsChange"
              style="width: 200px;"
            />
            <span class="value-display">{{ localMaxResults }}</span>
          </div>
        </div>
      </div>

      <!-- 包含日期 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('webSearchSettings.includeDateLabel') }}</label>
          <p class="desc">{{ t('webSearchSettings.includeDateDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="localIncludeDate"
            @change="handleIncludeDateChange"
          />
        </div>
      </div>

      <!-- 压缩方法 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('webSearchSettings.compressionLabel') }}</label>
          <p class="desc">{{ t('webSearchSettings.compressionDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localCompressionMethod"
            @change="handleCompressionMethodChange"
            style="width: 280px;"
          >
            <t-option value="none" :label="t('webSearchSettings.compressionNone')">
              {{ t('webSearchSettings.compressionNone') }}
            </t-option>
            <t-option value="llm_summary" :label="t('webSearchSettings.compressionSummary')">
              {{ t('webSearchSettings.compressionSummary') }}
            </t-option>
          </t-select>
        </div>
      </div>

      <!-- 黑名单 -->
      <div class="setting-row vertical">
        <div class="setting-info">
          <label>{{ t('webSearchSettings.blacklistLabel') }}</label>
          <p class="desc">{{ t('webSearchSettings.blacklistDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-textarea
            v-model="localBlacklistText"
            :placeholder="t('webSearchSettings.blacklistPlaceholder')"
            :autosize="{ minRows: 4, maxRows: 8 }"
            @change="handleBlacklistChange"
            style="width: 500px;"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, nextTick } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { getWebSearchProviders, getTenantWebSearchConfig, updateTenantWebSearchConfig, type WebSearchProviderConfig, type WebSearchConfig } from '@/api/web-search'

const { t } = useI18n()

// 本地状态
const loadingProviders = ref(false)
const providers = ref<WebSearchProviderConfig[]>([])
const localProvider = ref<string>('')
const localAPIKey = ref<string>('')
const localMaxResults = ref<number>(5)
const localIncludeDate = ref<boolean>(true)
const localCompressionMethod = ref<string>('none')
const localBlacklistText = ref<string>('')
const isInitializing = ref(true) // 标记是否正在初始化，初始化期间不触发自动保存
const initialConfig = ref<WebSearchConfig | null>(null) // 保存初始配置，用于比较是否有变化

// 计算属性：当前选中的提供商
const selectedProvider = computed(() => {
  return providers.value.find(p => p.id === localProvider.value)
})

// 加载提供商列表
const loadProviders = async () => {
  if (providers.value.length > 0) {
    return // 已加载过
  }
  
  loadingProviders.value = true
  try {
    const response = await getWebSearchProviders()
    // request拦截器已经处理了响应，直接使用data字段
    if (response.data && Array.isArray(response.data)) {
      providers.value = response.data
    }
  } catch (error: any) {
    console.error('Failed to load web search providers:', error)
    const errorMessage = error?.message || t('webSearchSettings.errors.unknown')
    MessagePlugin.error(t('webSearchSettings.toasts.loadProvidersFailed', { message: errorMessage }))
  } finally {
    loadingProviders.value = false
  }
}

// 加载租户配置
const loadTenantConfig = async () => {
  try {
    const response = await getTenantWebSearchConfig()
    // request拦截器已经处理了响应，直接使用data字段
    if (response.data) {
      const config = response.data
      // 在设置初始值时，禁用自动保存
      isInitializing.value = true
      
      // 保存初始配置的副本（用于后续比较）
      const blacklist = (config.blacklist || []).join('\n')
      initialConfig.value = {
        provider: config.provider || '',
        api_key: config.api_key === '***' ? '***' : config.api_key || '',
        max_results: config.max_results || 5,
        include_date: config.include_date !== undefined ? config.include_date : true,
        compression_method: config.compression_method || 'none',
        blacklist: config.blacklist || []
      }
      
      // 设置本地状态值
      localProvider.value = config.provider || ''
      // API key 在响应中被隐藏，如果是 "***"，说明已配置但未返回实际值
      localAPIKey.value = config.api_key === '***' ? '***' : config.api_key || ''
      localMaxResults.value = config.max_results || 5
      localIncludeDate.value = config.include_date !== undefined ? config.include_date : true
      localCompressionMethod.value = config.compression_method || 'none'
      localBlacklistText.value = blacklist
      
      // 等待所有响应式更新完成后再启用自动保存
      await nextTick()
      await nextTick()
      // 使用 setTimeout 确保所有事件都已处理完毕
      setTimeout(() => {
        isInitializing.value = false
      }, 100)
    } else {
      // 如果没有配置数据，保存默认配置
      initialConfig.value = {
        provider: '',
        api_key: '',
        max_results: 5,
        include_date: true,
        compression_method: 'none',
        blacklist: []
      }
      await nextTick()
      setTimeout(() => {
        isInitializing.value = false
      }, 100)
    }
  } catch (error: any) {
    console.error('Failed to load tenant web search config:', error)
    // 如果配置不存在，使用默认值（不显示错误）
    initialConfig.value = {
      provider: '',
      api_key: '',
      max_results: 5,
      include_date: true,
      compression_method: 'none',
      blacklist: []
    }
    await nextTick()
    setTimeout(() => {
      isInitializing.value = false
    }, 100)
  }
}

// 检查配置是否有变化
const hasConfigChanged = (): boolean => {
  if (!initialConfig.value) {
    return true // 如果没有初始配置，认为有变化
  }
  
  const blacklist = localBlacklistText.value
    .split('\n')
    .map(line => line.trim())
    .filter(line => line.length > 0)
  
  const currentConfig: WebSearchConfig = {
    provider: localProvider.value,
    api_key: localAPIKey.value,
    max_results: localMaxResults.value,
    include_date: localIncludeDate.value,
    compression_method: localCompressionMethod.value,
    blacklist: blacklist
  }
  
  // 比较配置是否有变化（忽略 API key 的 '***' 占位符）
  const initial = initialConfig.value
  if (currentConfig.provider !== initial.provider) return true
  if (currentConfig.api_key !== initial.api_key && 
      !(currentConfig.api_key === '***' && initial.api_key === '***')) return true
  if (currentConfig.max_results !== initial.max_results) return true
  if (currentConfig.include_date !== initial.include_date) return true
  if (currentConfig.compression_method !== initial.compression_method) return true
  
  // 比较黑名单数组
  const currentBlacklist = blacklist.sort().join(',')
  const initialBlacklist = (initial.blacklist || []).sort().join(',')
  if (currentBlacklist !== initialBlacklist) return true
  
  return false
}

// 保存配置
const saveConfig = async () => {
  // 如果配置没有变化，不保存
  if (!hasConfigChanged()) {
    return
  }
  
  try {
    const blacklist = localBlacklistText.value
      .split('\n')
      .map(line => line.trim())
      .filter(line => line.length > 0)
    
    const config: WebSearchConfig = {
      provider: localProvider.value,
      api_key: localAPIKey.value,
      max_results: localMaxResults.value,
      include_date: localIncludeDate.value,
      compression_method: localCompressionMethod.value,
      blacklist: blacklist
    }
    
    await updateTenantWebSearchConfig(config)
    
    // 更新初始配置，避免重复保存
    initialConfig.value = {
      provider: config.provider,
      api_key: config.api_key,
      max_results: config.max_results,
      include_date: config.include_date,
      compression_method: config.compression_method,
      blacklist: [...config.blacklist]
    }
    
    MessagePlugin.success(t('webSearchSettings.toasts.saveSuccess'))
  } catch (error: any) {
    console.error('Failed to save web search config:', error)
    const errorMessage = error?.message || t('webSearchSettings.errors.unknown')
    MessagePlugin.error(t('webSearchSettings.toasts.saveFailed', { message: errorMessage }))
    throw error
  }
}

// 防抖保存
let saveTimer: number | null = null
const debouncedSave = () => {
  // 初始化期间不触发自动保存
  if (isInitializing.value) {
    return
  }
  if (saveTimer) {
    clearTimeout(saveTimer)
  }
  saveTimer = window.setTimeout(() => {
    saveConfig().catch(() => {
      // 错误已在 saveConfig 中处理
    })
  }, 500)
}

// 处理变化
const handleProviderChange = () => {
  debouncedSave()
}

const handleAPIKeyChange = () => {
  debouncedSave()
}

const handleMaxResultsChange = () => {
  debouncedSave()
}

const handleIncludeDateChange = () => {
  debouncedSave()
}

const handleCompressionMethodChange = () => {
  debouncedSave()
}

const handleBlacklistChange = () => {
  debouncedSave()
}

// 初始化
onMounted(async () => {
  isInitializing.value = true
  await loadProviders()
  await loadTenantConfig()
  // loadTenantConfig 内部已经处理了 isInitializing，这里不需要再设置
})
</script>

<style lang="less" scoped>
.websearch-settings {
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

  &.vertical {
    flex-direction: column;
    gap: 12px;

    .setting-control {
      width: 100%;
      max-width: 100%;
    }
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
  gap: 12px;
}

.value-display {
  min-width: 40px;
  text-align: right;
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.provider-option-wrapper {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 2px 0;
}

.provider-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.provider-name {
  font-weight: 500;
  font-size: 14px;
  color: var(--td-text-color-primary);
  flex-shrink: 0;
}

.provider-tags {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
  flex-shrink: 0;
}

.provider-desc {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 1.4;
  margin-top: 2px;
}

/* 修复下拉项描述与条目重叠：让选项支持多行自适应高度 */
:deep(.t-select-option) {
  height: auto;
  align-items: flex-start;
  padding-top: 6px;
  padding-bottom: 6px;
}

:deep(.t-select-option__content) {
  white-space: normal;
}

</style>
<style lang="less">
.t-select__dropdown .t-select-option {
  height: auto;
  align-items: flex-start;
  padding-top: 6px;
  padding-bottom: 6px;
}
.t-select__dropdown .t-select-option__content {
  white-space: normal;
}
.t-select__dropdown .provider-option-wrapper {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 2px 0;
}
</style>

