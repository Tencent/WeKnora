<template>
  <div class="api-integration section">
    <div class="section-header">
      <h2>{{ $t('integrations.api.title') }}</h2>
      <p class="section-description">{{ $t('integrations.api.subtitle') }}</p>
    </div>

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

      <section class="settings-band">
        <div class="row row--stack">
          <div class="row-info">
            <label>{{ $t('integrations.api.principalMode') }}</label>
            <p>{{ $t('integrations.api.principalModeDesc') }}</p>
          </div>
          <div class="mode-panel">
            <t-radio-group v-model="form.mode">
              <t-radio-button value="tenant">{{ $t('integrations.api.modeTenant') }}</t-radio-button>
              <t-radio-button value="direct_header">{{ $t('integrations.api.modeDirect') }}</t-radio-button>
              <t-radio-button value="signed_token">{{ $t('integrations.api.modeSigned') }}</t-radio-button>
            </t-radio-group>

            <t-alert
              v-if="form.mode === 'direct_header'"
              theme="warning"
              :message="$t('integrations.api.directWarning')"
            />
            <t-alert
              v-if="form.mode === 'signed_token'"
              theme="success"
              :message="$t('integrations.api.signedRecommended')"
            />

            <div v-if="form.mode === 'direct_header'" class="field-grid">
              <label>{{ $t('integrations.api.directHeader') }}</label>
              <t-input v-model="form.direct_header_name" class="mono-input" />
            </div>

            <div v-if="form.mode === 'signed_token'" class="field-grid">
              <label>{{ $t('integrations.api.tokenHeader') }}</label>
              <t-input v-model="form.signed_token_header_name" class="mono-input" />
              <label>{{ $t('integrations.api.hmacSecret') }}</label>
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
            </div>

            <div class="example-box">
              <div class="example-title">{{ $t('integrations.api.requestExample') }}</div>
              <pre>{{ requestExample }}</pre>
            </div>

            <div class="actions">
              <t-button theme="primary" :loading="saving" :disabled="!canSave" @click="save">
                {{ $t('integrations.api.save') }}
              </t-button>
            </div>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
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

const form = reactive({
  mode: 'tenant' as APIPrincipalMode,
  direct_header_name: 'X-External-User-ID',
  signed_token_header_name: 'X-External-User-Token',
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

const requestExample = computed(() => {
  const lines = [
    `curl -X POST ${apiBaseUrl.value}/session/qa`,
    `  -H "X-API-Key: ${apiKey.value ? '<API_KEY>' : '<YOUR_API_KEY>'}"`,
    `  -H "Content-Type: application/json"`,
  ]
  if (form.mode === 'direct_header') {
    lines.push(`  -H "${form.direct_header_name || 'X-External-User-ID'}: user_123"`)
  }
  if (form.mode === 'signed_token') {
    lines.push(`  -H "${form.signed_token_header_name || 'X-External-User-Token'}: <HS256_JWT>"`)
  }
  lines.push(`  -d '{"query":"hello"}'`)
  return lines.join(' \\\n')
})

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

.row--stack {
  grid-template-columns: minmax(220px, 0.8fr) minmax(420px, 1fr);
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

.mode-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}

.field-grid {
  display: grid;
  grid-template-columns: 130px minmax(0, 1fr);
  gap: 10px 12px;
  align-items: center;

  label {
    color: var(--td-text-color-secondary);
    font-size: 13px;
  }
}

.example-box {
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-page);
  overflow: hidden;
}

.example-title {
  padding: 10px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

pre {
  margin: 0;
  padding: 12px;
  overflow: auto;
  font-family: var(--app-font-family-mono);
  font-size: 12px;
  line-height: 1.6;
}

.actions {
  display: flex;
  justify-content: flex-end;
}

.mono-input :deep(input) {
  font-family: var(--app-font-family-mono);
  font-size: 12px;
}

@media (max-width: 780px) {
  .row,
  .row--stack {
    grid-template-columns: 1fr;
  }

  .field-grid {
    grid-template-columns: 1fr;
  }
}
</style>
