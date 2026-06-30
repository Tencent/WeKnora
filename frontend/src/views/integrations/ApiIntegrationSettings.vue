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

        <t-radio-group v-model="form.mode" class="mode-radio" @change="handlePrincipalModeChange">
          <t-radio-button value="tenant">{{ $t('integrations.api.modeTenant') }}</t-radio-button>
          <t-radio-button value="direct_header">{{ $t('integrations.api.modeDirect') }}</t-radio-button>
          <t-radio-button value="signed_token">{{ $t('integrations.api.modeSigned') }}</t-radio-button>
        </t-radio-group>

        <div v-if="form.mode !== 'tenant'" class="mode-detail">
          <div
            v-if="form.mode === 'direct_header'"
            class="mode-callout mode-callout--warning"
          >
            <div class="mode-callout__body">
              <strong>{{ $t('integrations.api.directWarning') }}</strong>
              <p>{{ $t('integrations.api.directWarningDetail') }}</p>
            </div>
          </div>
          <div
            v-else-if="form.mode === 'signed_token'"
            class="mode-callout"
          >
            <div class="mode-callout__body">
              <strong>{{ $t('integrations.api.signedRecommended') }}</strong>
              <p>{{ $t('integrations.api.signedFlowDetail') }}</p>
            </div>
          </div>

          <div v-if="form.mode === 'direct_header'" class="principal-config">
            <div class="config-row">
              <div class="config-row__text">
                <label>{{ $t('integrations.api.directHeader') }}</label>
              </div>
              <code class="fixed-header-name">{{ directHeaderName }}</code>
            </div>
            <div class="config-row config-row--switch">
              <div class="config-row__text">
                <label>{{ $t('integrations.api.requireDirectHeader') }}</label>
                <p>{{ $t('integrations.api.requireDirectHeaderDesc') }}</p>
              </div>
              <div class="config-row__action">
                <t-switch v-model="form.require_direct_header" size="small" @change="handleRequireDirectHeaderChange" />
              </div>
            </div>
          </div>

          <div v-else-if="form.mode === 'signed_token'" class="principal-config">
            <div class="config-row">
              <div class="config-row__text">
                <label>{{ $t('integrations.api.tokenHeader') }}</label>
                <p>{{ $t('integrations.api.tokenHeaderDesc') }}</p>
              </div>
              <code class="fixed-header-name">{{ tokenHeaderName }}</code>
            </div>
            <div class="config-row config-row--secret">
              <div class="config-row__text">
                <label>{{ $t('integrations.api.hmacSecret') }}</label>
                <p>{{ $t('integrations.api.hmacSecretDesc') }}</p>
              </div>
              <div class="secret-control">
                <t-input
                  v-model="secretInput"
                  :type="showHMACSecret ? 'text' : 'password'"
                  class="mono-input"
                  :placeholder="config?.has_hmac_secret ? $t('integrations.api.secretConfigured') : ''"
                  @blur="triggerAutoSave"
                />
                <t-button size="small" variant="text" @click="showHMACSecret = !showHMACSecret">
                  <t-icon :name="showHMACSecret ? 'browse-off' : 'browse'" />
                </t-button>
                <t-button
                  size="small"
                  variant="text"
                  :title="$t('integrations.api.copy')"
                  :disabled="!secretInput.trim()"
                  @click="copy(secretInput)"
                >
                  <t-icon name="file-copy" />
                </t-button>
                <t-button
                  size="small"
                  variant="text"
                  theme="danger"
                  :title="$t('integrations.api.generateSecret')"
                  :loading="saving"
                  @click="confirmGenerateSecret"
                >
                  <t-icon name="refresh" />
                </t-button>
              </div>
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

        <div class="playground-entry">
          <div class="playground-entry__info">
            <label>{{ $t('integrations.api.playgroundTitle') }}</label>
            <p>{{ $t('integrations.api.playgroundDesc') }}</p>
          </div>
          <t-button variant="outline" @click="openPlaygroundDrawer">
            <template #icon><t-icon name="code" /></template>
            {{ $t('integrations.api.playgroundOpen') }}
          </t-button>
        </div>
      </section>
    </div>

    <SettingDrawer
      v-model:visible="playgroundDrawerVisible"
      class="api-playground-drawer"
      :title="$t('integrations.api.playgroundTitle')"
      :description="$t('integrations.api.playgroundDrawerDesc')"
      icon="code"
      width="640px"
      :min-width="560"
      :max-width="960"
      storage-key="setting-drawer:width:api-playground"
      :confirm-text="$t('integrations.api.playgroundRun')"
      :confirm-loading="playground.running"
      :confirm-disabled="!canRunPlayground"
      @confirm="runPlayground"
      @cancel="handlePlaygroundDrawerCancel"
    >
      <template #footer-left>
        <t-button v-if="playground.running" variant="outline" @click="stopPlayground">
          <template #icon><t-icon name="close-circle" /></template>
          {{ $t('integrations.api.playgroundStop') }}
        </t-button>
        <span v-if="playgroundDisabledReason" class="footer-test-message">
          {{ playgroundDisabledReason }}
        </span>
      </template>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('integrations.api.playgroundSectionRequest') }}</h4>

        <div class="drawer-form-item">
          <label class="drawer-form-label">{{ $t('integrations.api.playgroundAgent') }}</label>
          <t-select
            v-model="playground.agent_id"
            :options="agentOptions"
            :loading="agentsLoading"
            filterable
            :placeholder="$t('integrations.api.playgroundAgentPlaceholder')"
          />
          <p v-if="agentsError" class="drawer-form-desc drawer-form-desc--error">{{ agentsError }}</p>
        </div>

        <div class="drawer-form-item">
          <label class="drawer-form-label">{{ $t('integrations.api.playgroundExternalUser') }}</label>
          <t-input
            v-model="playground.external_user_id"
            :disabled="form.mode === 'tenant'"
            class="mono-input"
            :placeholder="$t('integrations.api.playgroundExternalUserPlaceholder')"
          />
          <p class="drawer-form-desc">{{ externalUserHint }}</p>
        </div>

        <div class="drawer-form-item">
          <label class="drawer-form-label">{{ $t('integrations.api.playgroundQuestion') }}</label>
          <t-textarea
            v-model="playground.query"
            :autosize="{ minRows: 2, maxRows: 4 }"
            :placeholder="$t('integrations.api.playgroundQuestionPlaceholder')"
          />
        </div>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('integrations.api.playgroundSectionPreview') }}</h4>
        <div class="code-panel playground-preview">
          <div class="code-panel__toolbar">
            <span class="code-panel__label">{{ $t('integrations.api.playgroundRequestPreview') }}</span>
            <t-button size="small" variant="text" class="code-panel__copy" @click="copy(playgroundRequestPreview)">
              <template #icon><t-icon name="file-copy" /></template>
              {{ $t('integrations.api.copy') }}
            </t-button>
          </div>
          <pre class="code-panel__pre">{{ playgroundRequestPreview }}</pre>
        </div>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('integrations.api.playgroundSectionResult') }}</h4>
        <t-alert v-if="playground.error" theme="error" :message="playground.error" />

        <div v-if="hasPlaygroundResult" class="playground-results">
          <div v-if="form.mode === 'signed_token' && playground.signed_token" class="playground-step">
            <div class="playground-step__header">
              <span>{{ $t('integrations.api.playgroundGeneratedToken') }}</span>
              <t-button size="small" variant="text" @click="copy(playground.signed_token)">
                <template #icon><t-icon name="file-copy" /></template>
                {{ $t('integrations.api.copy') }}
              </t-button>
            </div>
            <pre>{{ playground.signed_token }}</pre>
          </div>

          <div class="playground-step">
            <div class="playground-step__header">
              <span>{{ $t('integrations.api.playgroundStepSession') }}</span>
              <t-tag size="small" :theme="playground.session_status === 'success' ? 'success' : 'default'" variant="light">
                {{ playground.session_status || '-' }}
              </t-tag>
            </div>
            <pre>{{ playground.session_response || '-' }}</pre>
          </div>

          <div class="playground-step">
            <div class="playground-step__header">
              <span>{{ $t('integrations.api.playgroundStepChat') }}</span>
              <t-tag size="small" :theme="playground.chat_status === 'success' ? 'success' : 'default'" variant="light">
                {{ playground.chat_status || '-' }}
              </t-tag>
            </div>
            <pre>{{ playground.stream_output || '-' }}</pre>
          </div>

          <div v-if="playground.final_answer" class="playground-step">
            <div class="playground-step__header">
              <span>{{ $t('integrations.api.playgroundFinalAnswer') }}</span>
            </div>
            <pre>{{ playground.final_answer }}</pre>
          </div>
        </div>
        <p v-else class="playground-empty">{{ $t('integrations.api.playgroundEmptyResult') }}</p>
      </section>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { getCurrentUser } from '@/api/auth'
import { listAgents, BUILTIN_SMART_REASONING_ID, type CustomAgent } from '@/api/agent'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  createAPIPrincipalTestToken,
  getAPIPrincipalConfig,
  updateAPIPrincipalConfig,
  type APIPrincipalConfig,
  type APIPrincipalMode,
} from '@/api/tenant'
import { getApiBaseUrl } from '@/utils/api-base'

const { t } = useI18n()

const DEFAULT_DIRECT_HEADER_NAME = 'X-External-User-ID'
const DEFAULT_TOKEN_HEADER_NAME = 'X-External-User-Token'

const loading = ref(true)
const saving = ref(false)
const error = ref('')
const tenantId = ref(0)
const apiKey = ref('')
const showApiKey = ref(false)
const config = ref<APIPrincipalConfig | null>(null)
const secretInput = ref('')
const savedHMACSecret = ref('')
const exampleTab = ref<'jwt' | 'curl'>('curl')
const agents = ref<CustomAgent[]>([])
const agentsLoading = ref(false)
const agentsError = ref('')
const playgroundDrawerVisible = ref(false)
const playgroundController = ref<AbortController | null>(null)
const showHMACSecret = ref(false)

const form = reactive({
  mode: 'tenant' as APIPrincipalMode,
  direct_header_name: DEFAULT_DIRECT_HEADER_NAME,
  signed_token_header_name: DEFAULT_TOKEN_HEADER_NAME,
  require_direct_header: false,
})

type PlaygroundStatus = '' | 'running' | 'success' | 'failed' | 'stopped'

const playground = reactive({
  agent_id: '',
  query: 'hello',
  external_user_id: 'user_123',
  signed_token: '',
  running: false,
  session_status: '' as PlaygroundStatus,
  chat_status: '' as PlaygroundStatus,
  session_response: '',
  stream_output: '',
  final_answer: '',
  error: '',
})

watch(() => form.mode, (mode) => {
  if (mode === 'signed_token') {
    exampleTab.value = 'curl'
  }
})

watch(playgroundDrawerVisible, (visible) => {
  if (!visible) {
    stopPlayground()
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

const tokenHeaderName = computed(() => DEFAULT_TOKEN_HEADER_NAME)

const directHeaderName = computed(() => DEFAULT_DIRECT_HEADER_NAME)

const canAutoSave = computed(() => {
  if (!tenantId.value) return false
  if (form.mode === 'signed_token') {
    return secretInput.value.trim() !== ''
  }
  return true
})

const agentOptions = computed(() => agents.value.map((agent) => ({
  label: `${agent.name}${agent.is_builtin ? ` · ${t('integrations.api.playgroundBuiltin')}` : ''}`,
  value: agent.id,
})))

const hasUnsavedPrincipalChanges = computed(() => {
  const cfg = config.value
  if (!cfg) return false
  return (
    form.mode !== cfg.mode
    || form.require_direct_header !== cfg.require_direct_header
    || secretInput.value.trim() !== savedHMACSecret.value.trim()
  )
})

const externalUserHint = computed(() => {
  if (form.mode === 'tenant') return t('integrations.api.playgroundTenantModeHint')
  if (form.mode === 'direct_header') {
    return t('integrations.api.playgroundDirectModeHint', { headerName: directHeaderName.value })
  }
  return t('integrations.api.playgroundSignedModeHint', { headerName: tokenHeaderName.value })
})

const playgroundRequestPreview = computed(() => {
  const body = {
    query: playground.query || '<query>',
    agent_enabled: true,
    agent_id: playground.agent_id || '<agent_id>',
    channel: 'api',
  }
  const headers = buildPlaygroundHeaders(true)
  return [
    'POST /api/v1/sessions',
    JSON.stringify({ headers: headers.sessionHeaders, body: {} }, null, 2),
    '',
    'POST /api/v1/agent-chat/<session_id>',
    JSON.stringify({ headers: headers.chatHeaders, body }, null, 2),
  ].join('\n')
})

const playgroundDisabledReason = computed(() => {
  if (playground.running) return ''
  if (!apiKey.value) return t('integrations.api.playgroundNeedApiKey')
  if (!playground.agent_id) return t('integrations.api.playgroundNeedAgent')
  if (!playground.query.trim()) return t('integrations.api.playgroundNeedQuestion')
  if (form.mode === 'signed_token' && !playground.external_user_id.trim()) {
    return t('integrations.api.playgroundNeedExternalUser')
  }
  return ''
})

const canRunPlayground = computed(() => !playground.running && !playgroundDisabledReason.value)

const hasPlaygroundResult = computed(() => Boolean(
  playground.signed_token || playground.session_response || playground.stream_output || playground.final_answer,
))

const tokenSignExample = computed(() => {
  const tid = tenantId.value || 10000
  const headerName = tokenHeaderName.value
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
  if (form.mode === 'direct_header') {
    principalHeaders.push(`  -H "${directHeaderName.value}: user_123"`)
  }
  if (form.mode === 'signed_token') {
    principalHeaders.push(`  -H "${tokenHeaderName.value}: ${t('integrations.api.requestExampleJwtPlaceholder')}"`)
  }
  const commonHeaders = [apiKeyHeader, contentType, ...principalHeaders].join(' \\\n')
  const agentID = playground.agent_id || BUILTIN_SMART_REASONING_ID

  const lines: string[] = []
  if (form.mode === 'signed_token') {
    lines.push(
      t('integrations.api.signedRequestStep0', { tenantId: tenantId.value || '<tenant_id>' }),
      t('integrations.api.signedRequestStep0Hint', { headerName: tokenHeaderName.value }),
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
    `  -d '{"query":"hello","agent_enabled":true,"agent_id":"${agentID}","channel":"api"}'`,
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
    const [userResp] = await Promise.all([
      getCurrentUser(),
      loadAgents(),
    ])
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
    form.direct_header_name = DEFAULT_DIRECT_HEADER_NAME
    form.signed_token_header_name = DEFAULT_TOKEN_HEADER_NAME
    form.require_direct_header = cfgResp.data.require_direct_header === true
    savedHMACSecret.value = cfgResp.data.hmac_secret || ''
    secretInput.value = savedHMACSecret.value
    ensurePlaygroundAgent()
  } catch (err: any) {
    error.value = err?.message || t('integrations.api.loadFailed')
  } finally {
    loading.value = false
  }
}

async function loadAgents() {
  agentsLoading.value = true
  agentsError.value = ''
  try {
    const resp = await listAgents({ creator: 'all' }) as any
    agents.value = Array.isArray(resp?.data) ? resp.data : []
    ensurePlaygroundAgent()
  } catch (err: any) {
    agentsError.value = err?.message || t('integrations.api.playgroundAgentsLoadFailed')
  } finally {
    agentsLoading.value = false
  }
}

function ensurePlaygroundAgent() {
  if (playground.agent_id && agents.value.some((agent) => agent.id === playground.agent_id)) return
  const smartReasoning = agents.value.find((agent) => agent.id === BUILTIN_SMART_REASONING_ID)
  playground.agent_id = smartReasoning?.id || agents.value[0]?.id || ''
}

function openPlaygroundDrawer() {
  ensurePlaygroundAgent()
  playgroundDrawerVisible.value = true
}

function handlePlaygroundDrawerCancel() {
  stopPlayground()
  playgroundDrawerVisible.value = false
}

function handlePrincipalModeChange(mode: APIPrincipalMode) {
  form.mode = mode
  void saveIfNeeded()
}

function handleRequireDirectHeaderChange(checked: boolean) {
  form.require_direct_header = checked
  void saveIfNeeded()
}

function triggerAutoSave() {
  void saveIfNeeded()
}

function confirmGenerateSecret() {
  const dialog = DialogPlugin.confirm({
    header: t('integrations.api.hmacSecretResetConfirmTitle'),
    body: t('integrations.api.hmacSecretResetConfirmBody'),
    confirmBtn: { content: t('integrations.api.hmacSecretResetConfirmOk'), theme: 'danger' },
    cancelBtn: t('integrations.api.hmacSecretResetConfirmCancel'),
    onConfirm: async () => {
      await generateSecret()
      dialog.destroy()
    },
    onClose: () => dialog.destroy(),
  })
}

async function generateSecret() {
  const bytes = new Uint8Array(32)
  window.crypto.getRandomValues(bytes)
  secretInput.value = btoa(String.fromCharCode(...bytes))
  showHMACSecret.value = true
  await saveIfNeeded({ showSuccess: true })
}

async function saveIfNeeded(options: { showSuccess?: boolean } = {}) {
  if (!hasUnsavedPrincipalChanges.value) return true
  if (!canAutoSave.value) {
    MessagePlugin.error(t('integrations.api.autoSaveNeedSecret'))
    return false
  }
  saving.value = true
  try {
    const payload: Parameters<typeof updateAPIPrincipalConfig>[1] = {
      mode: form.mode,
      direct_header_name: DEFAULT_DIRECT_HEADER_NAME,
      signed_token_header_name: DEFAULT_TOKEN_HEADER_NAME,
      require_direct_header: form.require_direct_header,
    }
    if (secretInput.value.trim() !== savedHMACSecret.value.trim()) {
      payload.hmac_secret = secretInput.value.trim()
    }
    const resp = await updateAPIPrincipalConfig(tenantId.value, payload)
    if (!resp.success || !resp.data) {
      throw new Error(resp.message || t('integrations.api.saveFailed'))
    }
    config.value = resp.data
    savedHMACSecret.value = resp.data.hmac_secret || payload.hmac_secret || ''
    secretInput.value = savedHMACSecret.value
    if (options.showSuccess) {
      MessagePlugin.success(t('integrations.api.saveSuccess'))
    }
    return true
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.api.saveFailed'))
    return false
  } finally {
    saving.value = false
  }
}

async function copy(text: string) {
  if (!text) return
  await navigator.clipboard.writeText(text)
  MessagePlugin.success(t('integrations.api.copySuccess'))
}

function buildPlaygroundHeaders(maskSecrets: boolean) {
  const commonHeaders: Record<string, string> = {
    Accept: 'application/json',
    'Content-Type': 'application/json',
    'X-API-Key': maskSecrets ? '<API_KEY>' : apiKey.value,
  }
  if (form.mode === 'direct_header' && playground.external_user_id.trim()) {
    commonHeaders[directHeaderName.value] = playground.external_user_id.trim()
  }
  if (form.mode === 'signed_token') {
    commonHeaders[tokenHeaderName.value] = maskSecrets ? '<JWT>' : playground.signed_token.trim()
  }
  return {
    sessionHeaders: commonHeaders,
    chatHeaders: { ...commonHeaders, Accept: 'text/event-stream' },
  }
}

function compactText(text: string, max = 12000) {
  if (text.length <= max) return text
  return `${text.slice(0, max)}\n...`
}

function formatJSON(value: unknown) {
  try {
    return compactText(JSON.stringify(value, null, 2))
  } catch {
    return String(value)
  }
}

function extractAnswerFromSSE(raw: string) {
  const chunks: string[] = []
  raw.split('\n').forEach((line) => {
    if (!line.startsWith('data:')) return
    const payload = line.slice(5).trim()
    if (!payload || payload === '[DONE]') return
    try {
      const parsed = JSON.parse(payload)
      const type = parsed?.response_type || parsed?.type
      const content = parsed?.content
      if (type === 'answer' && typeof content === 'string') {
        chunks.push(content)
      }
    } catch {
      // Keep raw stream visible even when an event is not JSON.
    }
  })
  return chunks.join('')
}

async function readResponseBody(resp: Response) {
  const text = await resp.text()
  if (!text) return ''
  try {
    return formatJSON(JSON.parse(text))
  } catch {
    return compactText(text)
  }
}

async function ensurePlaygroundSignedToken() {
  if (form.mode !== 'signed_token') return
  if (!tenantId.value) throw new Error(t('integrations.api.loadFailed'))
  const resp = await createAPIPrincipalTestToken(tenantId.value, {
    external_user_id: playground.external_user_id.trim(),
    expires_in_seconds: 900,
  })
  if (!resp.success || !resp.data?.token) {
    throw new Error(resp.message || t('integrations.api.playgroundMintTokenFailed'))
  }
  playground.signed_token = resp.data.token
}

async function runPlayground() {
  if (!canRunPlayground.value) return
  const controller = new AbortController()
  playgroundController.value = controller
  playground.running = true
  playground.error = ''
  playground.session_status = 'running'
  playground.chat_status = ''
  playground.session_response = ''
  playground.stream_output = ''
  playground.final_answer = ''
  playground.signed_token = ''

  const startedAt = performance.now()
  try {
    await ensurePlaygroundSignedToken()
    const headers = buildPlaygroundHeaders(false).sessionHeaders
    const sessionResp = await fetch(`${apiBaseUrl.value}/sessions`, {
      method: 'POST',
      headers,
      body: '{}',
      signal: controller.signal,
      credentials: 'omit',
    })
    const sessionRaw = await sessionResp.text()
    let sessionPayload: any = null
    try {
      sessionPayload = sessionRaw ? JSON.parse(sessionRaw) : null
    } catch {
      sessionPayload = null
    }
    playground.session_response = sessionPayload ? formatJSON(sessionPayload) : compactText(sessionRaw)
    if (!sessionResp.ok || sessionPayload?.success === false) {
      playground.session_status = 'failed'
      throw new Error(sessionPayload?.message || sessionPayload?.error?.message || `HTTP ${sessionResp.status}`)
    }
    playground.session_status = 'success'
    const sessionID = sessionPayload?.data?.id || sessionPayload?.data?.ID
    if (!sessionID) {
      throw new Error(t('integrations.api.playgroundMissingSessionId'))
    }

    playground.chat_status = 'running'
    const chatResp = await fetch(`${apiBaseUrl.value}/agent-chat/${encodeURIComponent(sessionID)}`, {
      method: 'POST',
      headers: buildPlaygroundHeaders(false).chatHeaders,
      body: JSON.stringify({
        query: playground.query.trim(),
        agent_enabled: true,
        agent_id: playground.agent_id,
        channel: 'api',
      }),
      signal: controller.signal,
      credentials: 'omit',
    })
    if (!chatResp.ok) {
      playground.chat_status = 'failed'
      const body = await readResponseBody(chatResp)
      playground.stream_output = body
      throw new Error(body || `HTTP ${chatResp.status}`)
    }
    if (!chatResp.body) {
      throw new Error(t('integrations.api.playgroundNoStream'))
    }

    const reader = chatResp.body.getReader()
    const decoder = new TextDecoder()
    let raw = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      raw += decoder.decode(value, { stream: true })
      playground.stream_output = compactText(raw)
      playground.final_answer = extractAnswerFromSSE(raw)
    }
    raw += decoder.decode()
    playground.stream_output = compactText(raw)
    playground.final_answer = extractAnswerFromSSE(raw)
    playground.chat_status = 'success'
    MessagePlugin.success(t('integrations.api.playgroundSuccess', {
      ms: Math.round(performance.now() - startedAt),
    }))
  } catch (err: any) {
    const aborted = err?.name === 'AbortError'
    if (aborted) {
      if (playground.session_status === 'running') playground.session_status = 'stopped'
      if (playground.chat_status === 'running') playground.chat_status = 'stopped'
      playground.error = t('integrations.api.playgroundStopped')
    } else {
      if (playground.session_status === 'running') playground.session_status = 'failed'
      if (playground.chat_status === 'running') playground.chat_status = 'failed'
      playground.error = err?.message || t('integrations.api.playgroundFailed')
    }
  } finally {
    playground.running = false
    playgroundController.value = null
  }
}

function stopPlayground() {
  playgroundController.value?.abort()
}

onMounted(load)
onBeforeUnmount(stopPlayground)
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
  gap: 12px;
  width: 100%;
  max-width: 760px;
}

.mode-callout {
  position: relative;
  padding: 12px 14px;
  border-radius: 8px;
  border: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
  overflow: hidden;

  &--warning {
    border-color: var(--td-warning-color-3);
    background: var(--td-warning-color-1);
  }

  &__body {
    display: block;

    strong {
      display: block;
      margin-bottom: 5px;
      color: var(--td-text-color-primary);
      font-size: 13px;
      font-weight: 600;
      line-height: 1.4;
    }

    margin: 0;
    p {
      margin: 0;
      color: var(--td-text-color-secondary);
      font-size: 12px;
      line-height: 1.6;
    }
  }
}

.principal-config {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  overflow: hidden;
}

.config-row {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) auto;
  align-items: center;
  gap: 16px;
  padding: 14px 16px;

  & + & {
    border-top: 1px solid var(--td-component-stroke);
  }

  &__text {
    min-width: 0;

    label {
      display: block;
      color: var(--td-text-color-primary);
      font-size: 13px;
      font-weight: 600;
      line-height: 1.4;
    }

    p {
      margin: 5px 0 0;
      color: var(--td-text-color-placeholder);
      font-size: 12px;
      line-height: 1.5;
    }
  }

  &__action {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    min-width: 52px;
  }

  &--switch {
    align-items: flex-start;
  }

  &--secret {
    grid-template-columns: minmax(220px, 0.55fr) minmax(360px, 1fr);
    align-items: center;
  }
}

.secret-control {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;

  .mono-input {
    flex: 1 1 auto;
    min-width: 0;
  }

  :deep(.t-button) {
    flex: 0 0 auto;
  }
}

@media (max-width: 640px) {
  .config-row {
    grid-template-columns: 1fr;
    align-items: stretch;
    gap: 10px;

    &__action {
      justify-content: flex-start;
    }
  }

  .fixed-header-name {
    width: 100%;
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

.mono-input :deep(input) {
  font-family: var(--app-font-family-mono);
  font-size: 12px;
}

.fixed-header-name {
  width: fit-content;
  max-width: 100%;
  padding: 7px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family-mono);
  font-size: 12px;
  line-height: 18px;
  overflow-wrap: anywhere;
}

.mono-textarea :deep(.t-textarea__inner) {
  font-family: var(--app-font-family-mono);
  font-size: 12px;
}

.playground-entry {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);

  &__info {
    min-width: 0;

    label {
      display: block;
      margin-bottom: 4px;
      color: var(--td-text-color-primary);
      font-size: 13px;
      font-weight: 500;
    }

    p {
      margin: 0;
      color: var(--td-text-color-secondary);
      font-size: 12px;
      line-height: 1.5;
    }
  }
}

.playground-preview {
  width: 100%;
}

.drawer-form-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.drawer-form-label {
  display: block;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 500;
  line-height: 1.4;
}

.drawer-form-desc {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.5;

  &--error {
    color: var(--td-error-color);
  }
}

.footer-test-message {
  min-width: 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.4;
}

.playground-empty {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.6;
}

.playground-results {
  display: grid;
  grid-template-columns: 1fr;
  gap: 12px;
}

.playground-step {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  overflow: hidden;

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-container);
    color: var(--td-text-color-secondary);
    font-size: 12px;
    font-weight: 500;
  }

  pre {
    max-height: 240px;
    margin: 0;
    padding: 10px 12px;
    overflow: auto;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family-mono);
    font-size: 12px;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
  }
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

  .playground-entry {
    flex-direction: column;
    align-items: stretch;
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
