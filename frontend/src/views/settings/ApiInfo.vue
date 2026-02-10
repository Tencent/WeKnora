<template>
  <div class="api-info">
    <div class="section-header">
      <h2>{{ $t('tenant.api.title') }}</h2>
      <p class="section-description">{{ $t('tenant.api.description') }}</p>
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
      <!-- API Key -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.keyLabel') }}</label>
          <p class="desc">{{ $t('tenant.api.keyDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="api-key-control">
            <t-input 
              v-model="displayApiKey" 
              readonly 
              type="text"
              style="width: 100%; font-family: monospace; font-size: 12px;"
            />
            <t-button 
              size="small" 
              variant="text"
              @click="showApiKey = !showApiKey"
            >
              <t-icon :name="showApiKey ? 'browse-off' : 'browse'" />
            </t-button>
            <t-button 
              size="small" 
              variant="text"
              @click="copyApiKey"
              :title="$t('tenant.api.copyTitle')"
            >
              <t-icon name="file-copy" />
            </t-button>
          </div>
        </div>
      </div>

      <!-- API docs -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.docLabel') }}</label>
          <p class="desc">
            {{ $t('tenant.api.docDescription') }}
            <a @click="openApiDoc" class="doc-link">
              {{ $t('tenant.api.openDoc') }}
              <t-icon name="link" class="link-icon" />
            </a>
          </p>
        </div>
      </div>

      <!-- User info -->
      <div class="info-section-title">{{ $t('tenant.api.userSectionTitle') }}</div>

      <!-- User ID -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.userIdLabel') }}</label>
          <p class="desc">{{ $t('tenant.api.userIdDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ userInfo?.id || '-' }}</span>
        </div>
      </div>

      <!-- Username -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.usernameLabel') }}</label>
          <p class="desc">{{ $t('tenant.api.usernameDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ userInfo?.username || '-' }}</span>
        </div>
      </div>

      <!-- Email -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.emailLabel') }}</label>
          <p class="desc">{{ $t('tenant.api.emailDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ userInfo?.email || '-' }}</span>
        </div>
      </div>

      <!-- Created at -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.createdAtLabel') }}</label>
          <p class="desc">{{ $t('tenant.api.createdAtDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ formatDate(userInfo?.created_at) }}</span>
        </div>
      </div>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { getCurrentUser, type TenantInfo, type UserInfo } from '@/api/auth'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()

// Reactive state
const tenantInfo = ref<TenantInfo | null>(null)
const userInfo = ref<UserInfo | null>(null)
const loading = ref(true)
const error = ref('')
const showApiKey = ref(false)

// Computed
const displayApiKey = computed(() => {
  if (!tenantInfo.value?.api_key) return ''
  if (showApiKey.value) {
    return tenantInfo.value.api_key
  }
  let masked = ''
  for (let i = 0; i < tenantInfo.value.api_key.length; i++) {
    masked += '•'
  }
  return masked
})

// Methods
const loadInfo = async () => {
  try {
    loading.value = true
    error.value = ''
    
    const userResponse = await getCurrentUser()
    
    if ((userResponse as any).success && userResponse.data) {
      userInfo.value = userResponse.data.user
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

const openApiDoc = () => {
  window.open('https://github.com/Tencent/WeKnora/blob/main/docs/api/README.md', '_blank')
}

const fallbackCopyText = (text: string) => {
  const textArea = document.createElement('textarea')
  textArea.value = text
  textArea.style.position = 'fixed'
  textArea.style.opacity = '0'
  document.body.appendChild(textArea)
  textArea.select()
  document.execCommand('copy')
  document.body.removeChild(textArea)
}

const copyApiKey = async () => {
  if (!tenantInfo.value?.api_key) {
    MessagePlugin.warning(t('tenant.api.noKey'))
    return
  }
  
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(tenantInfo.value.api_key)
    } else {
      fallbackCopyText(tenantInfo.value.api_key)
    }
    MessagePlugin.success(t('tenant.api.copySuccess'))
  } catch (err) {
    fallbackCopyText(tenantInfo.value.api_key)
    MessagePlugin.success(t('tenant.api.copySuccess'))
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

// Lifecycle
onMounted(() => {
  loadInfo()
})
</script>

<style lang="less" scoped>
.api-info {
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

.loading-inline {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 40px 0;
  justify-content: center;
  color: #666666;
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
  border-bottom: 1px solid #e5e7eb;

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

  .doc-link {
    color: #07C05F;
    text-decoration: none;
    font-weight: 500;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    cursor: pointer;
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

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: center;

  .info-value {
    font-size: 14px;
    color: #333333;
    text-align: right;
    word-break: break-word;
  }
}

.api-key-control {
  width: 100%;
  display: flex;
  gap: 8px;
  align-items: center;
}

.info-section-title {
  font-size: 14px;
  font-weight: 600;
  color: #333333;
  margin-top: 24px;
  margin-bottom: 12px;

  &:first-child {
    margin-top: 0;
  }
}
</style>

