<template>
  <div class="tenant-info">
    <div class="section-header">
      <h2>{{ $t('tenant.title') }}</h2>
      <p class="section-description">{{ $t('tenant.sectionDescription') }}</p>
    </div>

    <!-- Loading state -->
    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>{{ $t('tenant.loadingInfo') }}</span>
    </div>

    <!-- Error state -->
    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="loadInfo">{{ $t('tenant.retry') }}</t-button>
        </template>
      </t-alert>
    </div>

    <!-- Content -->
    <div v-else class="settings-group">
      <!-- Tenant ID -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.idLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.idDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ tenantInfo?.id || '-' }}</span>
        </div>
      </div>

      <!-- Tenant name -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.nameLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.nameDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ tenantInfo?.name || '-' }}</span>
        </div>
      </div>

      <!-- Tenant description -->
      <div v-if="tenantInfo?.description" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.descriptionLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.descriptionDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ tenantInfo.description }}</span>
        </div>
      </div>

      <!-- Tenant business -->
      <div v-if="tenantInfo?.business" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.businessLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.businessDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ tenantInfo.business }}</span>
        </div>
      </div>

      <!-- Tenant status -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.statusLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.statusDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-tag 
            :theme="getStatusTheme(tenantInfo?.status)" 
            variant="light"
            size="small"
          >
            {{ getStatusText(tenantInfo?.status) }}
          </t-tag>
        </div>
      </div>

      <!-- Tenant creation time -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.createdAtLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.createdAtDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ formatDate(tenantInfo?.created_at) }}</span>
        </div>
      </div>

      <!-- Storage quota -->
      <div v-if="tenantInfo?.storage_quota !== undefined" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.storage.quotaLabel') }}</label>
          <p class="desc">{{ $t('tenant.storage.quotaDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ formatBytes(tenantInfo.storage_quota) }}</span>
        </div>
      </div>

      <!-- Used storage -->
      <div v-if="tenantInfo?.storage_quota !== undefined" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.storage.usedLabel') }}</label>
          <p class="desc">{{ $t('tenant.storage.usedDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ formatBytes(tenantInfo.storage_used || 0) }}</span>
        </div>
      </div>

      <!-- Storage usage -->
      <div v-if="tenantInfo?.storage_quota !== undefined" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.storage.usageLabel') }}</label>
          <p class="desc">{{ $t('tenant.storage.usageDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="usage-control">
            <span class="usage-text">{{ getUsagePercentage() }}%</span>
            <t-progress 
              :percentage="getUsagePercentage()" 
              :show-info="false" 
              size="small"
              :theme="getUsagePercentage() > 80 ? 'warning' : 'success'"
              style="flex: 1;"
            />
          </div>
        </div>
      </div>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getCurrentUser, type TenantInfo } from '@/api/auth'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()

// Reactive state
const tenantInfo = ref<TenantInfo | null>(null)
const loading = ref(true)
const error = ref('')

// Methods
const loadInfo = async () => {
  try {
    loading.value = true
    error.value = ''
    
    const userResponse = await getCurrentUser()
    
    if ((userResponse as any).success && userResponse.data) {
      tenantInfo.value = userResponse.data.tenant
    } else {
      error.value = userResponse.message || t('tenant.messages.fetchFailed')
    }
  } catch (err: any) {
    error.value = err?.message || t('tenant.messages.networkError')
  } finally {
    loading.value = false
  }
}

const getStatusText = (status: string | undefined) => {
  switch (status) {
    case 'active':
      return t('tenant.statusActive')
    case 'inactive':
      return t('tenant.statusInactive')
    case 'suspended':
      return t('tenant.statusSuspended')
    default:
      return t('tenant.statusUnknown')
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
  if (!dateStr) return t('tenant.unknown')
  
  try {
    const date = new Date(dateStr)
    const formatter = new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit'
    })
    return formatter.format(date)
  } catch {
    return t('tenant.formatError')
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
  return Math.min(Math.round(percentage * 100) / 100, 100)
}

// Lifecycle
onMounted(() => {
  loadInfo()
})
</script>

<style lang="less" scoped>
.tenant-info {
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

.loading-inline {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 40px 0;
  justify-content: center;
  color: var(--td-text-color-secondary);
  font-size: 14px;
}

.error-inline {
  padding: 20px 0;
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

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: center;

  .info-value {
    font-size: 14px;
    color: var(--td-text-color-primary);
    text-align: right;
    word-break: break-word;
  }
}

.usage-control {
//   width: 100%;
//   display: flex;
//   align-items: center;
//   gap: 12px;

  .usage-text {
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    min-width: 50px;
    text-align: right;
  }
}
</style>

