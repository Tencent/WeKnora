<template>
  <div class="api-integration">
    <div v-if="loading" class="state-row">
      <t-loading size="small" />
      <span>{{ $t('integrations.api.loading') }}</span>
    </div>

    <t-alert v-else-if="error" theme="error" :message="error">
      <template #operation>
        <t-button size="small" @click="load">{{ $t('integrations.api.retry') }}</t-button>
      </template>
    </t-alert>

    <div v-else class="api-settings">
      <section class="settings-band">
        <div class="row">
          <div class="row-info">
            <label>{{ $t('integrations.api.baseUrl') }}</label>
            <p>{{ $t('integrations.api.baseUrlDesc') }}</p>
          </div>
          <div class="row-control copy-field">
            <t-input :model-value="apiBaseUrl" readonly class="mono-input" />
            <t-button variant="text" :title="$t('integrations.api.copy')" @click="copy(apiBaseUrl)">
              <t-icon name="file-copy" />
            </t-button>
          </div>
        </div>

        <div class="row">
          <div class="row-info">
            <label>{{ $t('integrations.api.apiKey') }}</label>
            <p>{{ $t('integrations.api.apiKeyDesc') }}</p>
          </div>
          <div class="row-control copy-field">
            <t-input :model-value="displayApiKey" readonly class="mono-input" />
            <t-button variant="text" @click="showApiKey = !showApiKey">
              <t-icon :name="showApiKey ? 'browse-off' : 'browse'" />
            </t-button>
            <t-button variant="text" :title="$t('integrations.api.copy')" @click="copy(apiKey)">
              <t-icon name="file-copy" />
            </t-button>
          </div>
        </div>
      </section>

      <section class="settings-band principal-section">
        <div class="principal-section__header">
          <label>{{ $t('integrations.api.principalMode') }}</label>
          <p>{{ $t('integrations.api.principalModeDesc') }}</p>
          <p class="principal-section__scope">{{ $t('integrations.api.principalScope') }}</p>
        </div>

        <t-radio-group v-model="form.mode" class="mode-radio">
          <t-radio-button value="tenant">{{ $t('integrations.api.modeTenant') }}</t-radio-button>
          <t-radio-button value="direct_header">{{ $t('integrations.api.modeDirect') }}</t-radio-button>
          <t-radio-button value="signed_token">{{ $t('integrations.api.modeSigned') }}</t-radio-button>
        </t-radio-group>

        <div v-if="form.mode !== 'tenant'" class="mode-detail">
          <t-alert
            v-if="form.mode === 'direct_header'"
            theme="warning"
            :title="$t('integrations.api.directWarning')"
            :message="$t('integrations.api.directWarningDetail')"
          />
          <p v-else-if="form.mode === 'signed_token'" class="mode-hint">
            <span class="mode-hint__title">{{ $t('integrations.api.signedRecommended') }}</span>
            {{ $t('integrations.api.signedFlowDetail') }}
          </p>

          <div v-if="form.mode === 'direct_header'" class="form-grid">
            <div class="form-item">
              <label class="form-item__label">{{ $t('integrations.api.directHeader') }}</label>
              <t-input v-model="form.direct_header_name" class="mono-input" />
            </div>
            <div class="form-item form-item--switch">
              <label class="form-item__label">{{ $t('integrations.api.requireDirectHeader') }}</label>
              <div class="form-item__control">
                <t-switch v-model="form.require_direct_header" size="small" />
                <span class="form-item__hint">{{ $t('integrations.api.requireDirectHeaderDesc') }}</span>
              </div>
            </div>
          </div>

          <div v-else-if="form.mode === 'signed_token'" class="form-grid">
            <div class="form-item">
              <label class="form-item__label">{{ $t('integrations.api.tokenHeader') }}</label>
              <t-input v-model="form.signed_token_header_name" class="mono-input" />
              <p class="form-item__hint">{{ $t('integrations.api.tokenHeaderDesc') }}</p>
            </div>
            <div class="form-item">
              <label class="form-item__label">{{ $t('integrations.api.hmacSecret') }}</label>
              <div class="copy-field">
                <t-input
                  v-model="secretInput"
                  type="password"
                  class="mono-input"
                  :placeholder="config?.has_hmac_secret ? $t('integrations.api.secretConfigured') : ''"
                />
                <t-button variant="text" :title="$t('integrations.api.generateSecret')" @click="generateSecret">
                  <t-icon name="refresh" />
                </t-button>
              </div>
              <p class="form-item__hint">{{ $t('integrations.api.hmacSecretDesc') }}</p>
            </div>
          </div>
        </div>

        <div class="examples">
          <t-tabs
            v-if="form.mode === 'signed_token'"
            v-model="exampleTab"
            class="snippet-tabs"
          >
            <t-tab-panel value="jwt" :label="$t('integrations.api.tokenSignExample')" />
            <t-tab-panel value="curl" :label="$t('integrations.api.requestExample')" />
          </t-tabs>
          <div class="code-panel">
            <div class="code-panel__toolbar">
              <span class="code-panel__label">{{ activeExampleLabel }}</span>
              <t-button size="small" variant="text" class="code-panel__copy" @click="copy(activeExampleText)">
                <template #icon><t-icon name="file-copy" /></template>
                {{ $t('integrations.api.copy') }}
              </t-button>
            </div>
            <pre class="code-panel__pre">{{ activeExampleText }}</pre>
          </div>
        </div>

        <div class="actions">
          <t-button theme="primary" :loading="saving" :disabled="!canSave" @click="save">
            {{ $t('integrations.api.save') }}
          </t-button>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { getCurrentUser } from '@/api/auth'
import {
  getAPIPrincipalConfig,
  updateAPIPrincipalConfig,
  type APIPrincipalConfig,
  type APIPrincipalMode,
} from '@/api/tenant'
import { getApiBaseUrl } from '@/utils/api-base'

const { t } = useI18n()

const loading = ref(true)
const saving = ref(false)
const error = ref('')
const tenantId = ref(0)
const apiKey = ref('')
const showApiKey = ref(false)
const config = ref<APIPrincipalConfig | null>(null)
const secretInput = ref('')
const exampleTab = ref<'jwt' | 'curl'>('curl')

const form = reactive({
  mode: 'tenant' as APIPrincipalMode,
  direct_header_name: 'X-External-User-ID',
  signed_token_header_name: 'X-External-User-Token',
  require_direct_header: false,
})

watch(() => form.mode, (mode) => {
  if (mode === 'signed_token') {
    exampleTab.value = 'curl'
  }
})

const apiBaseUrl = computed(() => {
  const configured = getApiBaseUrl().trim().replace(/\/$/, '')
  const origin = typeof window !== 'undefined' && window.location.origin !== 'null' ? window.location.origin : ''
  return `${configured || origin}/api/v1`
})

const displayApiKey = computed(() => {
  if (!apiKey.value) return ''
  if (showApiKey.value) return apiKey.value
  return '•'.repeat(apiKey.value.length)
})

const canSave = computed(() => {
  if (!tenantId.value) return false
  if (form.mode === 'signed_token') {
    return config.value?.has_hmac_secret === true || secretInput.value.trim() !== ''
  }
  return true
})

const tokenSignExample = computed(() => {
  const tid = tenantId.value || 10000
  const headerName = form.signed_token_header_name.trim() || 'X-External-User-Token'
  return `import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func signExternalUserToken(hmacSecret, externalUserID string, tenantID uint64) (string, error) {
	claims := jwt.MapClaims{
		"sub":       externalUserID, // e.g. "user_123"
		"tenant_id": float64(tenantID),
		"aud":       "weknora",
		"exp":       time.Now().Add(time.Hour).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
		SignedString([]byte(hmacSecret))
}

// Send on each WeKnora API request:
//   ${headerName}: <JWT from signExternalUserToken>
// Tenant ID for this workspace: ${tid}`
})

const requestExample = computed(() => {
  const apiKeyHeader = `  -H "X-API-Key: ${apiKey.value ? '<API_KEY>' : '<YOUR_API_KEY>'}"`
  const contentType = '  -H "Content-Type: application/json"'
  const principalHeaders: string[] = []
  const tokenHeaderName = form.signed_token_header_name.trim() || 'X-External-User-Token'
  if (form.mode === 'direct_header') {
    principalHeaders.push(`  -H "${form.direct_header_name || 'X-External-User-ID'}: user_123"`)
  }
  if (form.mode === 'signed_token') {
    principalHeaders.push(`  -H "${tokenHeaderName}: ${t('integrations.api.requestExampleJwtPlaceholder')}"`)
  }
  const commonHeaders = [apiKeyHeader, contentType, ...principalHeaders].join(' \\\n')

  const lines: string[] = []
  if (form.mode === 'signed_token') {
    lines.push(
      t('integrations.api.signedRequestStep0', { tenantId: tenantId.value || '<tenant_id>' }),
      t('integrations.api.signedRequestStep0Hint', { headerName: tokenHeaderName }),
      '',
    )
  }
  lines.push(
    t('integrations.api.requestExampleCreateSession'),
    `curl -X POST ${apiBaseUrl.value}/sessions \\`,
    commonHeaders,
    `  -d '{}'`,
    '',
    t('integrations.api.requestExampleAgentChat'),
    `curl -N -X POST ${apiBaseUrl.value}/agent-chat/<session_id> \\`,
    commonHeaders,
    `  -d '{"query":"hello"}'`,
  )
  return lines.join('\n')
})

const activeExampleText = computed(() => (
  form.mode === 'signed_token' && exampleTab.value === 'jwt'
    ? tokenSignExample.value
    : requestExample.value
))

const activeExampleLabel = computed(() => (
  form.mode === 'signed_token' && exampleTab.value === 'jwt'
    ? t('integrations.api.tokenSignExample')
    : t('integrations.api.requestExample')
))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const userResp = await getCurrentUser()
    const tenant = (userResp as any)?.data?.tenant
    if (!tenant?.id) {
      throw new Error(t('integrations.api.loadFailed'))
    }
    tenantId.value = Number(tenant.id)
    apiKey.value = tenant.api_key || ''

    const cfgResp = await getAPIPrincipalConfig(tenantId.value)
    if (!cfgResp.success || !cfgResp.data) {
      throw new Error(cfgResp.message || t('integrations.api.loadFailed'))
    }
    config.value = cfgResp.data
    form.mode = cfgResp.data.mode || 'tenant'
    form.direct_header_name = cfgResp.data.direct_header_name || 'X-External-User-ID'
    form.signed_token_header_name = cfgResp.data.signed_token_header_name || 'X-External-User-Token'
    form.require_direct_header = cfgResp.data.require_direct_header === true
    secretInput.value = ''
  } catch (err: any) {
    error.value = err?.message || t('integrations.api.loadFailed')
  } finally {
    loading.value = false
  }
}

function generateSecret() {
  const bytes = new Uint8Array(32)
  window.crypto.getRandomValues(bytes)
  secretInput.value = btoa(String.fromCharCode(...bytes))
}

async function save() {
  if (!tenantId.value) return
  saving.value = true
  try {
    const payload: Parameters<typeof updateAPIPrincipalConfig>[1] = {
      mode: form.mode,
      direct_header_name: form.direct_header_name.trim(),
      signed_token_header_name: form.signed_token_header_name.trim(),
      require_direct_header: form.require_direct_header,
    }
    if (secretInput.value.trim()) {
      payload.hmac_secret = secretInput.value.trim()
    }
    const resp = await updateAPIPrincipalConfig(tenantId.value, payload)
    if (!resp.success || !resp.data) {
      throw new Error(resp.message || t('integrations.api.saveFailed'))
    }
    config.value = resp.data
    secretInput.value = ''
    MessagePlugin.success(t('integrations.api.saveSuccess'))
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.api.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function copy(text: string) {
  if (!text) return
  await navigator.clipboard.writeText(text)
  MessagePlugin.success(t('integrations.api.copySuccess'))
}

onMounted(load)
</script>

<style scoped lang="less">
.api-integration {
  width: 100%;
}

.state-row {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  min-height: 160px;
  color: var(--td-text-color-secondary);
}

.api-settings,
.settings-band {
  display: flex;
  flex-direction: column;
}

.settings-band {
  border-top: 1px solid var(--td-component-stroke);
}

.row {
  display: grid;
  grid-template-columns: minmax(220px, 0.8fr) minmax(320px, 1fr);
  gap: 24px;
  padding: 20px 0;
  border-bottom: 1px solid var(--td-component-stroke);
}

.principal-section {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: 20px 0;
}

.principal-section__header {
  label {
    display: block;
    margin-bottom: 6px;
    color: var(--td-text-color-primary);
    font-size: 15px;
    font-weight: 600;
  }

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.55;
  }
}

.principal-section__scope {
  margin-top: 6px !important;
  color: var(--td-text-color-placeholder) !important;
  font-size: 12px !important;
}

.mode-radio {
  width: fit-content;
  max-width: 100%;
}

.mode-detail {
  display: flex;
  flex-direction: column;
  gap: 16px;
  width: 100%;
  max-width: 640px;
}

.mode-hint {
  margin: 0;
  padding: 10px 12px;
  border-radius: 8px;
  border: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.55;

  &__title {
    display: block;
    margin-bottom: 4px;
    color: var(--td-text-color-primary);
    font-weight: 500;
  }
}

.form-grid {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;

  &__label {
    color: var(--td-text-color-primary);
    font-size: 13px;
    font-weight: 500;
  }

  &__control {
    display: flex;
    align-items: flex-start;
    gap: 10px;
  }

  &__hint {
    margin: 0;
    color: var(--td-text-color-placeholder);
    font-size: 12px;
    line-height: 1.5;
  }

  &--switch .form-item__control {
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
  }
}

.examples {
  width: 100%;
}

.snippet-tabs {
  margin-bottom: 8px;

  :deep(.t-tabs__nav) {
    min-height: 36px;
  }

  :deep(.t-tabs__nav-item) {
    font-size: 13px;
    height: 36px;
    line-height: 36px;
    color: var(--td-text-color-secondary);
  }

  :deep(.t-tabs__nav-item.t-is-active) {
    color: var(--td-text-color-primary);
    font-weight: 500;
  }

  :deep(.t-tabs__bar) {
    background: var(--td-brand-color);
  }
}

.code-panel {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  overflow: hidden;

  &__toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-container);
  }

  &__label {
    font-size: 12px;
    font-weight: 500;
    color: var(--td-text-color-secondary);
  }

  &__copy {
    flex-shrink: 0;

    :deep(.t-button__text) {
      display: inline-flex;
      align-items: center;
    }

    :deep(.t-icon) {
      display: inline-flex;
      align-items: center;
    }
  }

  &__pre {
    margin: 0;
    padding: 10px 12px;
    overflow: auto;
    font-family: var(--app-font-family-mono);
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-primary);
    background: transparent;
  }
}

.actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 4px;
  padding-top: 16px;
  border-top: 1px solid var(--td-component-stroke);
}

.mono-input :deep(input) {
  font-family: var(--app-font-family-mono);
  font-size: 12px;
}

@media (max-width: 780px) {
  .row {
    grid-template-columns: 1fr;
  }

  .mode-radio {
    width: 100%;

    :deep(.t-radio-group) {
      display: flex;
      width: 100%;
    }

    :deep(.t-radio-button) {
      flex: 1 1 0;
      min-width: 0;
    }
  }

  .mode-detail {
    max-width: none;
  }
}

.row-info {
  label {
    display: block;
    margin-bottom: 4px;
    color: var(--td-text-color-primary);
    font-size: 15px;
    font-weight: 600;
  }

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.5;
  }
}

.row-control,
.copy-field {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
</style>
