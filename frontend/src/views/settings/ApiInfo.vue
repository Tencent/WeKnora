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
              style="width: 100%; font-family: var(--app-font-family-mono); font-size: 12px;"
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
            <t-button
              size="small"
              variant="text"
              theme="danger"
              :loading="resetting"
              :title="$t('tenant.api.resetTitle')"
              @click="confirmResetApiKey"
            >
              <t-icon name="refresh" />
            </t-button>
          </div>
        </div>
      </div>

      <!-- API base URL -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.urlLabel') }}</label>
          <p class="desc">{{ $t('tenant.api.urlDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="api-key-control">
            <t-input
              :model-value="apiBaseUrlDisplay"
              readonly
              type="text"
              style="width: 100%; font-family: var(--app-font-family-mono); font-size: 12px;"
            />
            <t-button
              size="small"
              variant="text"
              @click="copyApiUrl"
              :title="$t('tenant.api.copyUrlTitle')"
            >
              <t-icon name="file-copy" />
            </t-button>
          </div>
        </div>
      </div>

      <!-- Desktop (Wails): fixed local API port + optional LAN/public listen -->
      <template v-if="showDesktopPortSetting || showDesktopBindPublicSetting">
        <div v-if="showDesktopPortSetting" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('tenant.api.desktopPortLabel') }}</label>
            <p class="desc">{{ $t('tenant.api.desktopPortDescription') }}</p>
          </div>
          <div class="setting-control">
            <div class="api-key-control">
              <div class="desktop-port-input-wrap">
                <t-input-number
                  v-model="desktopPortInput"
                  :min="0"
                  :max="65535"
                  theme="normal"
                />
              </div>
              <t-button size="small" variant="text" @click="saveDesktopPort">
                {{ $t('tenant.api.desktopPortSave') }}
              </t-button>
            </div>
          </div>
        </div>

        <div v-if="showDesktopBindPublicSetting" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('tenant.api.desktopBindPublicLabel') }}</label>
            <p class="desc">{{ $t('tenant.api.desktopBindPublicDescription') }}</p>
          </div>
          <div class="setting-control desktop-bind-public-control">
            <t-switch v-model="desktopBindPublicInput" @change="onDesktopBindPublicChange" />
          </div>
        </div>

        <div v-if="wailsApiLanBaseURL" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('tenant.api.lanUrlLabel') }}</label>
            <p class="desc">{{ $t('tenant.api.lanUrlDescription') }}</p>
          </div>
          <div class="setting-control">
            <div class="api-key-control">
              <t-input
                :model-value="wailsApiLanBaseURL"
                readonly
                type="text"
                style="width: 100%; font-family: var(--app-font-family-mono); font-size: 12px;"
              />
              <t-button
                size="small"
                variant="text"
                @click="copyLanApiUrl"
                :title="$t('tenant.api.lanUrlCopyTitle')"
              >
                <t-icon name="file-copy" />
              </t-button>
            </div>
          </div>
        </div>

        <div v-if="showLanUrlUnavailableHint" class="setting-row lan-url-hint-row">
          <t-alert theme="warning" :message="$t('tenant.api.lanUrlUnavailable')" />
        </div>
      </template>

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
import { resetTenantApiKey } from '@/api/tenant'
import { getApiBaseUrl } from '@/utils/api-base'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()

// Reactive state
const tenantInfo = ref<TenantInfo | null>(null)
const userInfo = ref<UserInfo | null>(null)
const loading = ref(true)
const error = ref('')
const showApiKey = ref(false)
const resetting = ref(false)
/** WeKnora Lite (Wails): real API origin is loopback + dynamic port, not window.location.origin */
const wailsApiBaseURL = ref<string | null>(null)
const showDesktopPortSetting = ref(false)
const showDesktopBindPublicSetting = ref(false)
const desktopPortInput = ref<number | undefined>(0)
const desktopBindPublicInput = ref(false)
const wailsApiLanBaseURL = ref<string | null>(null)
const desktopListenPublicActive = ref(false)

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

const showLanUrlUnavailableHint = computed(
  () =>
    showDesktopBindPublicSetting.value &&
    desktopListenPublicActive.value &&
    !wailsApiLanBaseURL.value
)

const apiBaseUrlDisplay = computed(() => {
  if (wailsApiBaseURL.value) {
    return wailsApiBaseURL.value
  }
  const configured = getApiBaseUrl().trim().replace(/\/$/, '')
  let origin = typeof window !== 'undefined' ? window.location.origin : ''
  if (!origin || origin === 'null') {
    origin = ''
  }
  const base = configured || origin
  return `${base}/api/v1`
})

type WeKnoraDesktopWindow = Window & {
  __WEKNORA_API_BASE__?: string
  __WEKNORA_API_LAN_BASE__?: string
  go?: {
    main?: {
      App?: {
        GetAPIBaseURL?: () => Promise<string> | string
        GetAPILanBaseURL?: () => Promise<string> | string
        GetDesktopHTTPPortSetting?: () => Promise<number> | number
        GetDesktopHTTPBindPublicSetting?: () => Promise<boolean> | boolean
        GetDesktopListenPublicActive?: () => Promise<boolean> | boolean
        SetDesktopHTTPPortSetting?: (port: number) => Promise<void> | void
        SetDesktopHTTPBindPublicSetting?: (v: boolean) => Promise<void> | void
      }
    }
  }
}

async function tryLoadWailsApiBaseURL() {
  const win = window as WeKnoraDesktopWindow
  for (let i = 0; i < 40; i++) {
    const injected = win.__WEKNORA_API_BASE__
    if (typeof injected === 'string' && injected.trim()) {
      wailsApiBaseURL.value = injected.trim().replace(/\/$/, '')
      await tryLoadWailsLanHints(win)
      return
    }
    const fn = win.go?.main?.App?.GetAPIBaseURL
    if (typeof fn === 'function') {
      try {
        const raw = await Promise.resolve(fn())
        if (typeof raw === 'string' && raw.trim()) {
          wailsApiBaseURL.value = raw.trim().replace(/\/$/, '')
        }
      } catch {
        /* binding error */
      }
      await tryLoadWailsLanHints(win)
      return
    }
    await new Promise((r) => setTimeout(r, 50))
  }
  await tryLoadWailsLanHints(win)
}

async function tryLoadWailsLanHints(win: WeKnoraDesktopWindow) {
  const injectedLan = win.__WEKNORA_API_LAN_BASE__
  if (typeof injectedLan === 'string' && injectedLan.trim()) {
    wailsApiLanBaseURL.value = injectedLan.trim().replace(/\/$/, '')
  }
  const fnLan = win.go?.main?.App?.GetAPILanBaseURL
  if (typeof fnLan === 'function' && !wailsApiLanBaseURL.value) {
    try {
      const raw = await Promise.resolve(fnLan())
      if (typeof raw === 'string' && raw.trim()) {
        wailsApiLanBaseURL.value = raw.trim().replace(/\/$/, '')
      }
    } catch {
      /* binding error */
    }
  }
  const fnAct = win.go?.main?.App?.GetDesktopListenPublicActive
  if (typeof fnAct === 'function') {
    try {
      desktopListenPublicActive.value = !!(await Promise.resolve(fnAct()))
    } catch {
      desktopListenPublicActive.value = false
    }
  }
}

function desktopPortBindingsAvailable(win: WeKnoraDesktopWindow) {
  const app = win.go?.main?.App
  return typeof app?.GetDesktopHTTPPortSetting === 'function' && typeof app?.SetDesktopHTTPPortSetting === 'function'
}

function desktopBindPublicBindingsAvailable(win: WeKnoraDesktopWindow) {
  const app = win.go?.main?.App
  return (
    typeof app?.GetDesktopHTTPBindPublicSetting === 'function' &&
    typeof app?.SetDesktopHTTPBindPublicSetting === 'function'
  )
}

async function loadDesktopApiPrefs() {
  const win = window as WeKnoraDesktopWindow
  if (desktopPortBindingsAvailable(win)) {
    showDesktopPortSetting.value = true
    try {
      const p = await Promise.resolve(win.go!.main!.App!.GetDesktopHTTPPortSetting!())
      desktopPortInput.value = typeof p === 'number' ? p : 0
    } catch {
      desktopPortInput.value = 0
    }
  }
  if (desktopBindPublicBindingsAvailable(win)) {
    showDesktopBindPublicSetting.value = true
    try {
      const b = await Promise.resolve(win.go!.main!.App!.GetDesktopHTTPBindPublicSetting!())
      desktopBindPublicInput.value = !!b
    } catch {
      desktopBindPublicInput.value = false
    }
  }
}

const onDesktopBindPublicChange = async (value: boolean) => {
  const v = value === true
  const win = window as WeKnoraDesktopWindow
  const fn = win.go?.main?.App?.SetDesktopHTTPBindPublicSetting
  if (typeof fn !== 'function') return
  try {
    await Promise.resolve(fn(v))
    MessagePlugin.success(t('tenant.api.desktopBindPublicSaved'))
  } catch (err: unknown) {
    MessagePlugin.error(err instanceof Error ? err.message : t('tenant.api.desktopBindPublicSaveFailed'))
    desktopBindPublicInput.value = !v
  }
}

const saveDesktopPort = async () => {
  const v = desktopPortInput.value
  const port = typeof v === 'number' && !Number.isNaN(v) ? Math.floor(v) : 0
  if (port < 0 || port > 65535) {
    MessagePlugin.warning(t('tenant.api.desktopPortInvalid'))
    return
  }
  const win = window as WeKnoraDesktopWindow
  const fn = win.go?.main?.App?.SetDesktopHTTPPortSetting
  if (typeof fn !== 'function') return
  try {
    await Promise.resolve(fn(port))
    MessagePlugin.success(t('tenant.api.desktopPortSaved'))
  } catch (err: unknown) {
    MessagePlugin.error(err instanceof Error ? err.message : t('tenant.api.desktopPortSaveFailed'))
  }
}

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

const confirmResetApiKey = () => {
  if (!tenantInfo.value?.id) {
    MessagePlugin.warning(t('tenant.api.noKey'))
    return
  }
  const dialog = DialogPlugin.confirm({
    header: t('tenant.api.resetConfirmTitle'),
    body: t('tenant.api.resetConfirmBody'),
    confirmBtn: { content: t('tenant.api.resetConfirmOk'), theme: 'danger' },
    cancelBtn: t('tenant.api.resetConfirmCancel'),
    onConfirm: async () => {
      await performResetApiKey()
      dialog.destroy()
    },
    onClose: () => dialog.destroy(),
  })
}

const performResetApiKey = async () => {
  if (!tenantInfo.value?.id) return
  resetting.value = true
  try {
    const resp = await resetTenantApiKey(tenantInfo.value.id)
    if (resp.success && resp.data?.api_key) {
      tenantInfo.value = { ...tenantInfo.value, api_key: resp.data.api_key }
      showApiKey.value = true
      MessagePlugin.success(t('tenant.api.resetSuccess'))
    } else {
      MessagePlugin.error(resp.message || t('tenant.api.resetFailed'))
    }
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('tenant.api.resetFailed'))
  } finally {
    resetting.value = false
  }
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

const copyLanApiUrl = async () => {
  const text = wailsApiLanBaseURL.value
  if (!text) return
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text)
    } else {
      fallbackCopyText(text)
    }
    MessagePlugin.success(t('tenant.api.lanUrlCopySuccess'))
  } catch {
    fallbackCopyText(text)
    MessagePlugin.success(t('tenant.api.lanUrlCopySuccess'))
  }
}

const copyApiUrl = async () => {
  const text = apiBaseUrlDisplay.value
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text)
    } else {
      fallbackCopyText(text)
    }
    MessagePlugin.success(t('tenant.api.urlCopySuccess'))
  } catch {
    fallbackCopyText(text)
    MessagePlugin.success(t('tenant.api.urlCopySuccess'))
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
onMounted(async () => {
  await tryLoadWailsApiBaseURL()
  await loadDesktopApiPrefs()
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

  .doc-link {
    color: var(--td-brand-color);
    text-decoration: none;
    font-weight: 500;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    cursor: pointer;
    transition: all 0.2s ease;

    &:hover {
      color: var(--td-brand-color-active);
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
  align-items: flex-start;

  .info-value {
    font-size: 14px;
    color: var(--td-text-color-primary);
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
  color: var(--td-text-color-primary);
  margin-top: 24px;
  margin-bottom: 12px;

  &:first-child {
    margin-top: 0;
  }
}

/* 与 API Key / API 地址 行一致：输入区占满 flex 剩余宽度，文案按钮贴右 */
.desktop-port-input-wrap {
  flex: 1;
  min-width: 0;

  :deep(.t-input-number) {
    width: 100%;
  }

  :deep(.t-input__wrap) {
    width: 100%;
  }

  :deep(input) {
    font-family: var(--app-font-family-mono);
    font-size: 12px;
  }
}

.desktop-bind-public-control {
  padding-top: 4px;
}

.lan-url-hint-row {
  padding-top: 0;
  border-bottom: 1px solid var(--td-component-stroke);

  :deep(.t-alert) {
    width: 100%;
  }
}
</style>

