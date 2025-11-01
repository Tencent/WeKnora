<template>
  <div class="tenant-info-container">
    <div class="tenant-header">
      <h2>{{ $t('tenant.systemInfo') }}</h2>
      <p class="tenant-subtitle">{{ $t('tenant.viewSystemInfo') }}</p>
    </div>

    <div class="tenant-content" v-if="!loading && !error">
      <!-- 系统信息卡片 -->
      <t-card class="info-card" :bordered="false">
        <template #header>
          <div class="card-title">{{ $t('tenant.systemInfo') }}</div>
        </template>
        <div class="info-content">
          <t-descriptions :column="1" layout="vertical">
            <t-descriptions-item :label="$t('tenant.version')">
              {{ systemInfo?.version || $t('tenant.unknown') }}
              <span v-if="systemInfo?.commit_id" class="commit-info">
                ({{ systemInfo.commit_id }})
              </span>
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.buildTime')" v-if="systemInfo?.build_time">
              {{ systemInfo.build_time }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.goVersion')" v-if="systemInfo?.go_version">
              {{ systemInfo.go_version }}
            </t-descriptions-item>
          </t-descriptions>
        </div>
      </t-card>

      <!-- 用户信息卡片 -->
      <t-card class="info-card" :bordered="false">
        <template #header>
          <div class="card-title">{{ $t('tenant.userInfo') }}</div>
        </template>
        <div class="info-content">
          <t-descriptions :column="1" layout="vertical">
            <t-descriptions-item :label="$t('tenant.userId')">
              {{ userInfo?.id }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.username')">
              {{ userInfo?.username }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.email')">
              {{ userInfo?.email }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.createdAt')">
              {{ formatDate(userInfo?.created_at) }}
            </t-descriptions-item>
          </t-descriptions>
        </div>
      </t-card>

      <!-- 租户信息卡片 -->
      <t-card class="info-card" :bordered="false">
        <template #header>
          <div class="card-title">{{ $t('tenant.tenantInfo') }}</div>
        </template>
        <div class="info-content">
          <t-descriptions :column="1" layout="vertical">
            <t-descriptions-item :label="$t('tenant.tenantId')">
              {{ tenantInfo?.id }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.tenantName')">
              {{ tenantInfo?.name }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.description')">
              {{ tenantInfo?.description || $t('tenant.noDescription') }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.business')">
              {{ tenantInfo?.business || $t('tenant.noBusiness') }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.status')">
              <t-tag
                :theme="getStatusTheme(tenantInfo?.status)"
                variant="light"
              >
                {{ getStatusText(tenantInfo?.status) }}
              </t-tag>
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.createdAt')">
              {{ formatDate(tenantInfo?.created_at) }}
            </t-descriptions-item>
          </t-descriptions>
        </div>
      </t-card>

      <!-- API Key 卡片 -->
      <t-card class="info-card" :bordered="false">
        <template #header>
          <div class="card-header-with-actions">
            <div class="card-title">{{ $t('tenant.apiKey') }}</div>
          </div>
        </template>
        <div class="api-key-content">
          <t-input 
            v-model="displayApiKey" 
            readonly 
            class="api-key-input"
            :type="showApiKey ? 'text' : 'password'"
          />
          <t-alert theme="warning" :close="false" class="api-warning">
            <template #icon>
              <t-icon name="error-circle" />
            </template>
            {{ $t('tenant.keepApiKeySafe') }}
          </t-alert>
        </div>
      </t-card>

      <!-- 存储信息卡片 -->
      <t-card 
        class="info-card" 
        :bordered="false"
        v-if="tenantInfo?.storage_quota !== undefined"
      >
        <template #header>
          <div class="card-title">{{ $t('tenant.storageInfo') }}</div>
        </template>
        <div class="storage-content">
          <t-descriptions :column="1" layout="vertical">
            <t-descriptions-item :label="$t('tenant.storageQuota')">
              {{ formatBytes(tenantInfo.storage_quota) }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.used')">
              {{ formatBytes(tenantInfo.storage_used || 0) }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('tenant.usage')">
              <div class="usage-info">
                <span class="usage-text">{{ getUsagePercentage() }}%</span>
                <t-progress 
                  :percentage="getUsagePercentage()" 
                  :show-info="false" 
                  size="medium"
                  :theme="getUsagePercentage() > 80 ? 'warning' : 'success'"
                />
              </div>
            </t-descriptions-item>
          </t-descriptions>
        </div>
      </t-card>

      <!-- API 开发文档卡片 -->
      <t-card class="info-card" :bordered="false">
        <template #header>
          <div class="card-title">{{ $t('tenant.apiDevDocs') }}</div>
        </template>
        <div class="doc-content">
          <p class="doc-description">{{ $t('tenant.useApiKey') }}</p>
          <t-space class="doc-actions">
            <t-button 
              theme="primary" 
              @click="openApiDoc"
            >
              <template #icon>
                <t-icon name="link" />
              </template>
              {{ $t('tenant.viewApiDoc') }}
            </t-button>
          </t-space>
          
        </div>
      </t-card>
    </div>

    <!-- 加载状态 -->
    <div v-if="loading" class="loading-container">
      <t-loading size="large" />
      <p class="loading-text">{{ $t('tenant.loadingAccountInfo') }}</p>
    </div>

    <!-- 错误状态 -->
    <div v-if="error" class="error-container">
      <t-result theme="error" :title="$t('tenant.loadFailed')" :description="error">
        <template #extra>
          <t-button theme="primary" @click="loadTenantInfo">{{ $t('tenant.retry') }}</t-button>
        </template>
      </t-result>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { getCurrentUser, type TenantInfo, type UserInfo } from '@/api/auth'
import { getSystemInfo, type SystemInfo } from '@/api/system'
const { t } = useI18n()

// 响应式数据
const tenantInfo = ref<TenantInfo | null>(null)
const userInfo = ref<UserInfo | null>(null)
const systemInfo = ref<SystemInfo | null>(null)
const loading = ref(true)
const error = ref('')
const showApiKey = ref(false)
const showApiExample = ref(false)

// API 基础 URL
const apiBaseUrl = window.location.origin

// 计算属性
const displayApiKey = computed(() => {
  if (!tenantInfo.value?.api_key) return ''
  return tenantInfo.value.api_key
})

// API示例代码
const apiExampleCode = computed(() => {
  return `curl -X GET "${apiBaseUrl}/api/v1/tenants/${tenantInfo.value?.id}" \\
  -H "Content-Type: application/json" \\
  -H "X-API-Key: ${tenantInfo.value?.api_key}"`
})

// 方法
const loadTenantInfo = async () => {
  try {
    loading.value = true
    error.value = ''
    
    // 并行获取用户信息和系统信息
    const [userResponse, systemResponse] = await Promise.all([
      getCurrentUser(),
      getSystemInfo().catch(() => ({ data: null })) // 系统信息获取失败不影响页面显示
    ])
    
    if (userResponse.success && userResponse.data) {
      userInfo.value = userResponse.data.user
      tenantInfo.value = userResponse.data.tenant
    } else {
      error.value = userResponse.message || '获取用户信息失败'
    }
    
    if (systemResponse.data) {
      systemInfo.value = systemResponse.data
    }
  } catch (err: any) {
    error.value = err.message || '网络错误，请稍后重试'
  } finally {
    loading.value = false
  }
}

const toggleApiKeyVisibility = () => {
  showApiKey.value = !showApiKey.value
}

const copyApiKey = async () => {
  if (!tenantInfo.value?.api_key) return
  
  try {
    await navigator.clipboard.writeText(tenantInfo.value.api_key)
    // 使用TDesign的消息组件
    import('tdesign-vue-next').then(({ MessagePlugin }) => {
      MessagePlugin.success('API Key 已复制到剪贴板')
    })
  } catch (err) {
    // 降级到传统方式
    const textArea = document.createElement('textarea')
    textArea.value = tenantInfo.value.api_key
    document.body.appendChild(textArea)
    textArea.select()
    document.execCommand('copy')
    document.body.removeChild(textArea)
    import('tdesign-vue-next').then(({ MessagePlugin }) => {
      MessagePlugin.success($t('tenant.apiKeyCopied'))
    })
  }
}

const openApiDoc = () => {
  window.open('https://github.com/Tencent/WeKnora/blob/main/docs/API.md', '_blank')
}

const getStatusText = (status: string | undefined) => {
  switch (status) {
    case 'active':
      return $t('tenant.statusActive')
    case 'inactive':
      return $t('tenant.statusInactive')
    case 'suspended':
      return $t('tenant.statusSuspended')
    default:
      return $t('tenant.statusUnknown')
  }
}

const getStatusTheme = (status: string | undefined) => {
  switch (status) {
    case 'active':
      return 'success'
    case 'inactive':
      return 'warning'
    case 'suspended':
      return 'danger'
    default:
      return 'default'
  }
}

const formatDate = (dateStr: string | undefined) => {
  if (!dateStr) return $t('tenant.unknown')

  try {
    const date = new Date(dateStr)
    return date.toLocaleString('ru-RU', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit'
    })
  } catch {
    return $t('tenant.formatError')
  }
}

const formatBytes = (bytes: number) => {
  if (bytes === 0) return '0 B'
  
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

const getUsagePercentage = () => {
  if (!tenantInfo.value?.storage_quota || tenantInfo.value.storage_quota === 0) {
    return 0
  }
  
  const used = tenantInfo.value.storage_used || 0
  const percentage = (used / tenantInfo.value.storage_quota) * 100
  return Math.min(Math.round(percentage * 100) / 100, 100) // 保留两位小数，最大100%
}

// 生命周期
onMounted(() => {
  loadTenantInfo()
})
</script>

<style lang="less" scoped>
.tenant-info-container {
  padding: 20px;
  background-color: #fff;
  margin: 0 20px 0 20px;
  height: calc(100vh);
  overflow-y: auto;
  box-sizing: border-box;
  flex: 1;
}

.tenant-header {
  margin-bottom: 20px;
  border-bottom: 1px solid #f0f0f0;
  padding-bottom: 16px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: #000000;
    margin: 0 0 8px 0;
  }

  .tenant-subtitle {
    font-size: 14px;
    color: #666666;
    margin: 0;
  }
}

.tenant-content {
  display: grid;
  gap: 20px;
  grid-template-columns: 1fr;
}

.info-card {
  margin-bottom: 20px;

  .card-title {
    font-size: 16px;
    font-weight: 600;
    color: #07C05F;
  }

  .card-header-with-actions {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }
}

.info-content,
.api-key-content,
.storage-content,
.doc-content {
  margin-top: 0;
}

.api-key-input {
  margin-bottom: 16px;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
}

.api-warning {
  margin-top: 16px;
}

.usage-info {
  display: flex;
  flex-direction: column;
  gap: 8px;

  .usage-text {
    font-weight: 500;
    color: #000000;
  }
}

.doc-description {
  margin-bottom: 16px;
  color: #666666;
  font-size: 14px;
}

.doc-actions {
  margin-bottom: 20px;
}

.api-example {
  margin-top: 20px;
  padding: 16px;
  background-color: #f8f9fa;
  border-radius: 6px;

  .example-header h4 {
    margin: 0 0 16px 0;
    font-size: 16px;
    font-weight: 600;
    color: #000000;
  }

  .code-textarea {
    margin-bottom: 16px;
    font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
  }

  .example-note {
    margin-top: 16px;
  }
}

.loading-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 24px;
  text-align: center;

  .loading-text {
    margin-top: 16px;
    color: #666666;
    font-size: 14px;
  }
}

.error-container {
  padding: 40px;
  text-align: center;
}

/* 响应式设计 */
@media (max-width: 768px) {
  .tenant-info-container {
    padding: 16px;
    margin: 10px;
    height: calc(100vh - 20px);
  }
  
  .tenant-header h2 {
    font-size: 18px;
  }
  
  .card-header-with-actions {
    flex-direction: column;
    align-items: flex-start !important;
    gap: 12px;
  }
  
  .commit-info {
    color: #666;
    font-size: 12px;
    margin-left: 8px;
  }

  .doc-actions {
    :deep(.t-space) {
      flex-direction: column;
      width: 100%;
      
      .t-button {
        width: 100%;
      }
    }
  }
}

/* 覆盖TDesign组件样式 */
:deep(.t-card) {
  border: 1px solid #e5e7eb;
}

:deep(.t-descriptions-item__label) {
  font-weight: 500;
  color: #374151;
}

:deep(.t-descriptions-item__content) {
  color: #000000;
}

:deep(.t-input__inner) {
  font-family: inherit;
}

:deep(.code-textarea .t-textarea__inner) {
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
  font-size: 13px;
  line-height: 1.4;
}
</style>
