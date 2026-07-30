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

        <template v-if="showDesktopPortSetting || showDesktopBindPublicSetting">
          <div v-if="showDesktopPortSetting" class="row">
            <div class="row-info">
              <label>{{ $t('tenant.api.desktopPortLabel') }}</label>
              <p>{{ $t('tenant.api.desktopPortDescription') }}</p>
            </div>
            <div class="row-control desktop-api-control">
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

          <div v-if="showDesktopBindPublicSetting" class="row">
            <div class="row-info">
              <label>{{ $t('tenant.api.desktopBindPublicLabel') }}</label>
              <p>{{ $t('tenant.api.desktopBindPublicDescription') }}</p>
            </div>
            <div class="row-control desktop-bind-public-control">
              <t-switch v-model="desktopBindPublicInput" @change="onDesktopBindPublicChange" />
            </div>
          </div>

          <div v-if="wailsApiLanBaseURL" class="row">
            <div class="row-info">
              <label>{{ $t('tenant.api.lanUrlLabel') }}</label>
              <p>{{ $t('tenant.api.lanUrlDescription') }}</p>
            </div>
            <div class="row-control copy-field">
              <t-input :model-value="wailsApiLanBaseURL" readonly class="mono-input" />
              <t-button variant="text" :title="$t('tenant.api.lanUrlCopyTitle')" @click="copy(wailsApiLanBaseURL)">
                <t-icon name="file-copy" />
              </t-button>
            </div>
          </div>

          <div v-if="showLanUrlUnavailableHint" class="row row--single">
            <t-alert theme="warning" :message="$t('tenant.api.lanUrlUnavailable')" />
          </div>
        </template>

        <div class="row row--doc">
          <div class="row-info">
            <label>{{ $t('tenant.api.docLabel') }}</label>
            <p>
              {{ $t('tenant.api.docDescription') }}
              <a class="doc-link" @click="openApiDoc">
                {{ $t('tenant.api.openDoc') }}
                <t-icon name="link" class="link-icon" />
              </a>
            </p>
          </div>
        </div>

        <div class="api-key-section">
          <div class="api-key-section__header">
            <div class="api-key-section__title">
              <label>{{ $t('integrations.api.apiKeys') }}</label>
              <p>{{ $t('integrations.api.apiKeysDesc') }}</p>
            </div>
            <t-button size="small" variant="outline" @click="openCreateAPIKeyDialog">
              <template #icon><t-icon name="add" /></template>
              {{ $t('integrations.api.createApiKey') }}
            </t-button>
          </div>
          <div class="api-key-section__body">
            <div class="api-key-list" :class="{ 'api-key-list--loading': apiKeysLoading }">
              <div v-if="apiKeysLoading" class="api-key-list__empty">
                <t-loading size="small" />
                <span>{{ $t('integrations.api.loading') }}</span>
              </div>
              <div v-else-if="apiKeys.length === 0" class="api-key-list__empty">
                {{ $t('integrations.api.noApiKeys') }}
              </div>
              <div v-else class="api-key-table-wrap">
                <table class="api-key-table">
                  <thead>
                    <tr>
                      <th>{{ $t('integrations.api.apiKeyName') }}</th>
                      <th>{{ $t('integrations.api.apiKeyValue') }}</th>
                      <th>{{ $t('integrations.api.apiKeyAccessMode') }}</th>
                      <th>{{ $t('integrations.api.apiKeyKnowledgeScope') }}</th>
                      <th>{{ $t('integrations.api.createdAt') }}</th>
                      <th class="api-key-table__actions-heading">{{ $t('integrations.api.actions') }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="key in apiKeys" :key="key.id">
                      <td>
                        <span class="api-key-name">{{ key.name }}</span>
                      </td>
                      <td>
                        <code class="api-key-fingerprint">{{ formatKeyMaskedValue(key) }}</code>
                      </td>
                      <td>
                        <div class="api-key-access-cell" :title="formatApiKeyCapabilitiesTitle(key)">
                          <span
                            v-if="key.full_access"
                            class="api-key-access-mode api-key-access-mode--full"
                          >
                            {{ formatApiKeyAccessModeLabel(key) }}
                          </span>
                          <div v-if="!key.full_access" class="api-key-capability-chips">
                            <span
                              v-for="label in keyCapabilityLabels(key)"
                              :key="label"
                              class="api-key-capability-chip"
                            >
                              {{ label }}
                            </span>
                          </div>
                        </div>
                      </td>
                      <td>
                        <span class="api-key-knowledge-scope">
                          {{ formatKeyKnowledgeScope(key.knowledge_base_ids) }}
                        </span>
                      </td>
                      <td>
                        <span class="api-key-created-at">{{ formatDate(key.created_at) }}</span>
                      </td>
                      <td>
                        <div class="api-key-table__actions">
                          <t-button
                            shape="square"
                            variant="text"
                            :title="$t('integrations.api.copy')"
                            @click="copy(key.api_key)"
                          >
                            <t-icon name="file-copy" />
                          </t-button>
                          <t-button
                            shape="square"
                            variant="text"
                            theme="danger"
                            :title="$t('integrations.api.deleteApiKey')"
                            @click="confirmDeleteAPIKey(key.id)"
                          >
                            <t-icon name="delete" />
                          </t-button>
                        </div>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
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
              <div class="secret-field">
                <div class="secret-control">
                  <t-input
                    v-model="secretInput"
                    :type="secretInputType"
                    class="mono-input secret-mono-input"
                    :placeholder="config?.has_hmac_secret && !secretInput.trim() ? $t('integrations.api.secretConfigured') : ''"
                    @blur="triggerAutoSave"
                  />
                  <t-button
                    v-if="secretInput.trim()"
                    size="small"
                    variant="text"
                    @click="showHMACSecret = !showHMACSecret"
                  >
                    <t-icon :name="showHMACSecret ? 'browse-off' : 'browse'" />
                  </t-button>
                  <t-button
                    v-if="secretInput.trim()"
                    size="small"
                    variant="text"
                    :title="$t('integrations.api.copy')"
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
                <p v-if="showSecretSavedHint" class="secret-saved-hint">
                  {{ $t('integrations.api.secretSavedCopyHint') }}
                </p>
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

    <SettingDrawer
      :visible="apiKeyDialogVisible"
      class="api-key-create-drawer"
      :title="$t('integrations.api.createApiKey')"
      :description="$t('integrations.api.createApiKeyDialogDesc')"
      icon="lock-on"
      width="560px"
      :min-width="480"
      :max-width="920"
      storage-key="setting-drawer:width:api-key-create"
      :close-on-overlay-click="false"
      :confirm-text="$t('integrations.api.createApiKey')"
      :confirm-loading="apiKeyCreating"
      @update:visible="(v: boolean) => apiKeyDialogVisible = v"
      @confirm="createScopedAPIKey"
    >
      <div class="api-key-dialog">
        <div class="api-key-dialog-row">
          <div class="api-key-dialog-row__label">
            <label>{{ $t('integrations.api.apiKeyName') }}</label>
          </div>
          <t-input
            v-model="apiKeyForm.name"
            :placeholder="$t('integrations.api.apiKeyNamePlaceholder')"
          />
        </div>

        <div class="api-key-dialog-row">
          <div class="api-key-dialog-row__label">
            <label>{{ $t('integrations.api.apiKeyAccessType') }}</label>
          </div>
          <t-radio-group v-model="apiKeyAccessMode" class="mode-radio api-key-access-type-radio">
            <t-radio-button value="scoped">{{ $t('integrations.api.apiKeyScopedAccess') }}</t-radio-button>
            <t-radio-button value="full">{{ $t('integrations.api.capabilityTenantFull') }}</t-radio-button>
          </t-radio-group>
          <p class="scope-hint">
            {{
              apiKeyFullAccessEnabled
                ? $t('integrations.api.capabilityTenantFullHint')
                : $t('integrations.api.apiKeyAccessTypeHint')
            }}
          </p>
        </div>

        <div v-if="!apiKeyFullAccessEnabled" class="api-key-dialog-row">
          <div class="api-key-dialog-row__label">
            <label>{{ $t('integrations.api.apiKeyCapabilities') }}</label>
          </div>
          <div v-if="!apiKeyFullAccessEnabled" class="api-key-capability-list">
            <div
              v-for="group in apiKeyCapabilityGroups"
              :key="group.key"
              class="api-key-capability-group"
            >
              <div class="api-key-capability-group__header">
                <span>{{ $t(group.labelKey) }}</span>
                <t-button
                  size="small"
                  variant="text"
                  @click="toggleCapabilityGroup(group, !capabilityGroupAllSelected(group))"
                >
                  {{
                    capabilityGroupAllSelected(group)
                      ? $t('integrations.api.apiKeyCapabilityClearGroup')
                      : $t('integrations.api.apiKeyCapabilitySelectGroup')
                  }}
                </t-button>
              </div>
              <div class="api-key-capability-group__items">
                <div
                  v-for="capability in group.capabilities"
                  :key="capability.value"
                  class="api-key-capability-item"
                >
                  <t-checkbox
                    :model-value="capabilitySelections[capability.value]"
                    @change="handleCapabilityChange(capability.value, $event)"
                  >
                    {{ $t(capability.labelKey) }}
                  </t-checkbox>
                  <p class="scope-hint">{{ $t(capability.hintKey) }}</p>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div v-if="apiKeyKnowledgeScopeApplies" class="api-key-dialog-row">
          <div class="api-key-dialog-row__label">
            <label>{{ $t('integrations.api.apiKeyKnowledgeScope') }}</label>
          </div>
          <t-select
            v-model="apiKeyForm.knowledge_base_ids"
            multiple
            filterable
            clearable
            :loading="knowledgeBasesLoading"
            :options="knowledgeBaseOptions"
            :placeholder="$t('integrations.api.apiKeyKnowledgeScopePlaceholder')"
          />
        </div>
      </div>
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
  createTenantAPIKey,
  deleteTenantAPIKey,
  createAPIPrincipalTestToken,
  getAPIPrincipalConfig,
  listTenantAPIKeys,
  updateAPIPrincipalConfig,
  type APIPrincipalConfig,
  type APIPrincipalMode,
  type TenantAPIKey,
  type TenantAPIKeyCapability,
} from '@/api/tenant'
import { listKnowledgeBases } from '@/api/knowledge-base'
import { getApiBaseUrl } from '@/utils/api-base'
import {
  DEFAULT_TENANT_API_KEY_CAPABILITIES,
  KB_SCOPED_API_KEY_CAPABILITIES,
  TENANT_API_KEY_CAPABILITIES,
  TENANT_API_KEY_CAPABILITY_GROUPS,
  type ApiKeyCapabilityGroup,
} from '@/config/apiKeyCapabilities'

const { t } = useI18n()

const DEFAULT_DIRECT_HEADER_NAME = 'X-External-User-ID'
const DEFAULT_TOKEN_HEADER_NAME = 'X-External-User-Token'

const loading = ref(true)
const saving = ref(false)
const error = ref('')
const tenantId = ref(0)
const apiKey = ref('')
const config = ref<APIPrincipalConfig | null>(null)
const apiKeys = ref<TenantAPIKey[]>([])
const apiKeysLoading = ref(false)
const apiKeyDialogVisible = ref(false)
const apiKeyCreating = ref(false)
const knowledgeBasesLoading = ref(false)
const knowledgeBases = ref<Array<{ id: string; name: string }>>([])
const secretInput = ref('')
/** Plaintext of the last secret successfully saved in this page session. */
const lastSavedSecretInput = ref('')
const exampleTab = ref<'jwt' | 'curl'>('curl')
const agents = ref<CustomAgent[]>([])
const agentsLoading = ref(false)
const agentsError = ref('')
const playgroundDrawerVisible = ref(false)
const playgroundController = ref<AbortController | null>(null)
const showHMACSecret = ref(false)
const wailsApiBaseURL = ref<string | null>(null)
const wailsApiLanBaseURL = ref<string | null>(null)
const showDesktopPortSetting = ref(false)
const showDesktopBindPublicSetting = ref(false)
const desktopPortInput = ref<number | undefined>(0)
const desktopBindPublicInput = ref(false)
const desktopListenPublicActive = ref(false)

const form = reactive({
  mode: 'tenant' as APIPrincipalMode,
  direct_header_name: DEFAULT_DIRECT_HEADER_NAME,
  signed_token_header_name: DEFAULT_TOKEN_HEADER_NAME,
  require_direct_header: false,
})

const API_KEY_CAPABILITIES = TENANT_API_KEY_CAPABILITIES
const DEFAULT_API_KEY_CAPABILITIES = DEFAULT_TENANT_API_KEY_CAPABILITIES
const KB_SCOPED_CAPABILITIES = KB_SCOPED_API_KEY_CAPABILITIES
const apiKeyCapabilityGroups = TENANT_API_KEY_CAPABILITY_GROUPS

const capabilitySelections = reactive<Record<TenantAPIKeyCapability, boolean>>(
  API_KEY_CAPABILITIES.reduce((acc, capability) => {
    acc[capability] = DEFAULT_API_KEY_CAPABILITIES.has(capability)
    return acc
  }, {} as Record<TenantAPIKeyCapability, boolean>),
)

const apiKeyForm = reactive({
  name: '',
  knowledge_base_ids: [] as string[],
  // Tenant-full keys already cover every capability. Scoped keys default to
  // retrieval + chat + agent reads so a fresh integration can ask questions
  // and present an agent picker immediately.
  tenant_full_enabled: false,
})

const apiKeyFullAccessEnabled = computed(() => apiKeyForm.tenant_full_enabled)
const apiKeyAccessMode = computed<'scoped' | 'full'>({
  get: () => (apiKeyForm.tenant_full_enabled ? 'full' : 'scoped'),
  set: (value) => {
    apiKeyForm.tenant_full_enabled = value === 'full'
  },
})
const selectedCapabilityValues = computed(() => API_KEY_CAPABILITIES.filter((capability) => capabilitySelections[capability]))
const apiKeyKnowledgeScopeApplies = computed(() => (
  !apiKeyFullAccessEnabled.value
  && selectedCapabilityValues.value.some((capability) => KB_SCOPED_CAPABILITIES.has(capability))
))

watch(() => apiKeyForm.tenant_full_enabled, (enabled) => {
  if (enabled) {
    apiKeyForm.knowledge_base_ids = []
  }
})

watch(apiKeyKnowledgeScopeApplies, (applies) => {
  if (!applies) {
    apiKeyForm.knowledge_base_ids = []
  }
})

function selectedCapabilities(): TenantAPIKeyCapability[] {
  return selectedCapabilityValues.value
}

function setCapabilitySelected(capability: TenantAPIKeyCapability, selected: boolean) {
  capabilitySelections[capability] = selected
}

function handleCapabilityChange(capability: TenantAPIKeyCapability, checked: unknown) {
  setCapabilitySelected(capability, Boolean(checked))
}

function capabilityGroupAllSelected(group: ApiKeyCapabilityGroup): boolean {
  return group.capabilities.every((capability) => capabilitySelections[capability.value])
}

function toggleCapabilityGroup(group: ApiKeyCapabilityGroup, selected: boolean) {
  group.capabilities.forEach((capability) => {
    capabilitySelections[capability.value] = selected
  })
}

// Full-access keys already cover every capability, so no capability badges for them.
function keyCapabilityLabels(key: TenantAPIKey): string[] {
  if (key.full_access) return []
  const labels: Partial<Record<TenantAPIKeyCapability, string>> = {
    retrieve: t('integrations.api.capabilityRetrieve'),
    chat: t('integrations.api.capabilityChat'),
    read_agents: t('integrations.api.capabilityReadAgents'),
    ingest: t('integrations.api.capabilityIngest'),
    manage_kbs: t('integrations.api.capabilityManageKbs'),
    manage_agents: t('integrations.api.capabilityManageAgents'),
    message_history: t('integrations.api.capabilityMessageHistory'),
    manage_models: t('integrations.api.capabilityManageModels'),
    manage_mcp_services: t('integrations.api.capabilityManageMcpServices'),
    manage_datasources: t('integrations.api.capabilityManageDatasources'),
    manage_channels: t('integrations.api.capabilityManageChannels'),
    manage_vector_stores: t('integrations.api.capabilityManageVectorStores'),
    manage_storage_backends: t('integrations.api.capabilityManageStorageBackends'),
    manage_web_search: t('integrations.api.capabilityManageWebSearch'),
    run_evaluations: t('integrations.api.capabilityRunEvaluations'),
    manage_members: t('integrations.api.capabilityManageMembers'),
    manage_spaces: t('integrations.api.capabilityManageSpaces'),
    manage_tenant_settings: t('integrations.api.capabilityManageTenantSettings'),
  }
  return (key.capabilities ?? [])
    .map((c) => labels[c])
    .filter((label): label is string => Boolean(label))
}

function formatApiKeyCapabilitiesTitle(key: TenantAPIKey): string {
  if (key.full_access) return t('integrations.api.capabilityTenantFull')
  const labels = keyCapabilityLabels(key)
  return labels.length > 0 ? labels.join(' / ') : t('integrations.api.apiKeyScopedAccess')
}

function formatApiKeyAccessModeLabel(key: TenantAPIKey): string {
  return key.full_access
    ? t('integrations.api.capabilityTenantFull')
    : t('integrations.api.apiKeyScopedAccess')
}

type PlaygroundStatus = '' | 'running' | 'success' | 'failed' | 'stopped'

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
  if (wailsApiBaseURL.value) {
    return wailsApiBaseURL.value
  }
  const configured = getApiBaseUrl().trim().replace(/\/$/, '')
  const origin = typeof window !== 'undefined' && window.location.origin !== 'null' ? window.location.origin : ''
  return `${configured || origin}/api/v1`
})

const showLanUrlUnavailableHint = computed(() => (
  showDesktopBindPublicSetting.value
  && desktopListenPublicActive.value
  && !wailsApiLanBaseURL.value
))

const tokenHeaderName = computed(() => DEFAULT_TOKEN_HEADER_NAME)

const directHeaderName = computed(() => DEFAULT_DIRECT_HEADER_NAME)

const secretInputType = computed(() => {
  if (!secretInput.value.trim()) return 'text'
  return showHMACSecret.value ? 'text' : 'password'
})

const canAutoSave = computed(() => {
  if (!tenantId.value) return false
  if (form.mode === 'signed_token') {
    // Either a secret is already stored server-side, or the user has just
    // typed a new one. The plaintext secret is never returned by the API,
    // so we rely on the has_hmac_secret presence flag.
    return config.value?.has_hmac_secret === true || secretInput.value.trim() !== ''
  }
  return true
})

const agentOptions = computed(() => agents.value.map((agent) => ({
  label: `${agent.name}${agent.is_builtin ? ` · ${t('integrations.api.playgroundBuiltin')}` : ''}`,
  value: agent.id,
})))

const knowledgeBaseOptions = computed(() => knowledgeBases.value.map((kb) => ({
  label: kb.name || kb.id,
  value: kb.id,
})))

const hasUnsavedSecretChange = computed(() => {
  const trimmed = secretInput.value.trim()
  if (!trimmed) return false
  return trimmed !== lastSavedSecretInput.value
})

const showSecretSavedHint = computed(() => {
  const trimmed = secretInput.value.trim()
  return trimmed !== '' && trimmed === lastSavedSecretInput.value
})

const hasUnsavedPrincipalChanges = computed(() => {
  const cfg = config.value
  if (!cfg) return false
  return (
    form.mode !== cfg.mode
    || form.require_direct_header !== cfg.require_direct_header
    || hasUnsavedSecretChange.value
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
    await Promise.all([
      loadAPIKeys(),
      loadKnowledgeBaseOptions(),
    ])

    const cfgResp = await getAPIPrincipalConfig(tenantId.value)
    if (!cfgResp.success || !cfgResp.data) {
      throw new Error(cfgResp.message || t('integrations.api.loadFailed'))
    }
    config.value = cfgResp.data
    form.mode = cfgResp.data.mode || 'tenant'
    form.direct_header_name = DEFAULT_DIRECT_HEADER_NAME
    form.signed_token_header_name = DEFAULT_TOKEN_HEADER_NAME
    form.require_direct_header = cfgResp.data.require_direct_header === true
    // The plaintext secret is never returned; start with an empty input and
    // rely on config.has_hmac_secret to reflect whether one is configured.
    secretInput.value = ''
    lastSavedSecretInput.value = ''
    ensurePlaygroundAgent()
  } catch (err: any) {
    error.value = err?.message || t('integrations.api.loadFailed')
  } finally {
    loading.value = false
  }
}

async function loadAPIKeys() {
  if (!tenantId.value) return
  apiKeysLoading.value = true
  try {
    const resp = await listTenantAPIKeys(tenantId.value)
    if (!resp.success) {
      throw new Error(resp.message || t('integrations.api.loadApiKeysFailed'))
    }
    apiKeys.value = resp.data || []
    if (!apiKey.value && apiKeys.value.length > 0) {
      apiKey.value = apiKeys.value[0].api_key || ''
    }
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.api.loadApiKeysFailed'))
  } finally {
    apiKeysLoading.value = false
  }
}

async function loadKnowledgeBaseOptions() {
  knowledgeBasesLoading.value = true
  try {
    const resp: any = await listKnowledgeBases({ creator: 'all' })
    const rows = Array.isArray(resp?.data) ? resp.data : []
    knowledgeBases.value = rows.map((item: any) => ({
      id: String(item.id),
      name: item.name || item.id,
    }))
  } catch {
    knowledgeBases.value = []
  } finally {
    knowledgeBasesLoading.value = false
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
    // Only send the secret when the user entered a new value; otherwise the
    // backend keeps the stored value untouched.
    const secretBeingSaved = hasUnsavedSecretChange.value ? secretInput.value.trim() : ''
    if (secretBeingSaved) {
      payload.hmac_secret = secretBeingSaved
    }
    const resp = await updateAPIPrincipalConfig(tenantId.value, payload)
    if (!resp.success || !resp.data) {
      throw new Error(resp.message || t('integrations.api.saveFailed'))
    }
    config.value = resp.data
    if (secretBeingSaved) {
      lastSavedSecretInput.value = secretBeingSaved
      showHMACSecret.value = true
    }
    if (options.showSuccess) {
      MessagePlugin.success(
        secretBeingSaved ? t('integrations.api.secretSavedCopyHint') : t('integrations.api.saveSuccess'),
      )
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
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text)
  } else {
    const textArea = document.createElement('textarea')
    textArea.value = text
    textArea.style.position = 'fixed'
    textArea.style.opacity = '0'
    document.body.appendChild(textArea)
    textArea.select()
    document.execCommand('copy')
    document.body.removeChild(textArea)
  }
  MessagePlugin.success(t('integrations.api.copySuccess'))
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
    await new Promise((resolve) => setTimeout(resolve, 50))
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
      const port = await Promise.resolve(win.go!.main!.App!.GetDesktopHTTPPortSetting!())
      desktopPortInput.value = typeof port === 'number' ? port : 0
    } catch {
      desktopPortInput.value = 0
    }
  }
  if (desktopBindPublicBindingsAvailable(win)) {
    showDesktopBindPublicSetting.value = true
    try {
      const bind = await Promise.resolve(win.go!.main!.App!.GetDesktopHTTPBindPublicSetting!())
      desktopBindPublicInput.value = !!bind
    } catch {
      desktopBindPublicInput.value = false
    }
  }
}

const onDesktopBindPublicChange = async (value: boolean) => {
  const next = value === true
  const fn = (window as WeKnoraDesktopWindow).go?.main?.App?.SetDesktopHTTPBindPublicSetting
  if (typeof fn !== 'function') return
  try {
    await Promise.resolve(fn(next))
    MessagePlugin.success(t('tenant.api.desktopBindPublicSaved'))
  } catch (err: unknown) {
    MessagePlugin.error(err instanceof Error ? err.message : t('tenant.api.desktopBindPublicSaveFailed'))
    desktopBindPublicInput.value = !next
  }
}

const saveDesktopPort = async () => {
  const value = desktopPortInput.value
  const port = typeof value === 'number' && !Number.isNaN(value) ? Math.floor(value) : 0
  if (port < 0 || port > 65535) {
    MessagePlugin.warning(t('tenant.api.desktopPortInvalid'))
    return
  }
  const fn = (window as WeKnoraDesktopWindow).go?.main?.App?.SetDesktopHTTPPortSetting
  if (typeof fn !== 'function') return
  try {
    await Promise.resolve(fn(port))
    MessagePlugin.success(t('tenant.api.desktopPortSaved'))
  } catch (err: unknown) {
    MessagePlugin.error(err instanceof Error ? err.message : t('tenant.api.desktopPortSaveFailed'))
  }
}

function openApiDoc() {
  window.open('https://github.com/Tencent/WeKnora/blob/main/docs/api/README.md', '_blank')
}

function openCreateAPIKeyDialog() {
  apiKeyForm.name = ''
  apiKeyForm.knowledge_base_ids = []
  apiKeyForm.tenant_full_enabled = false
  API_KEY_CAPABILITIES.forEach((capability) => {
    capabilitySelections[capability] = DEFAULT_API_KEY_CAPABILITIES.has(capability)
  })
  apiKeyDialogVisible.value = true
  void loadKnowledgeBaseOptions()
}

async function createScopedAPIKey() {
  if (!apiKeyForm.name.trim()) {
    MessagePlugin.error(t('integrations.api.apiKeyNameRequired'))
    return
  }
  apiKeyCreating.value = true
  try {
    const resp = await createTenantAPIKey(tenantId.value, {
      name: apiKeyForm.name.trim(),
      full_access: apiKeyFullAccessEnabled.value,
      // KB scoping only applies to capabilities that touch knowledge bases.
      knowledge_base_ids: apiKeyKnowledgeScopeApplies.value ? apiKeyForm.knowledge_base_ids : [],
      // Capabilities only matter below full access; full access already covers them all.
      capabilities: apiKeyFullAccessEnabled.value ? [] : selectedCapabilities(),
    })
    if (!resp.success || !resp.data?.api_key) {
      throw new Error(resp.message || t('integrations.api.createApiKeyFailed'))
    }
    apiKeyDialogVisible.value = false
    apiKey.value = resp.data.api_key
    MessagePlugin.success(t('integrations.api.apiKeyCreated'))
    await loadAPIKeys()
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('integrations.api.createApiKeyFailed'))
  } finally {
    apiKeyCreating.value = false
  }
}

function confirmDeleteAPIKey(id: number) {
  const dialog = DialogPlugin.confirm({
    header: t('integrations.api.deleteApiKey'),
    body: t('integrations.api.deleteApiKeyConfirm'),
    confirmBtn: { content: t('integrations.api.deleteApiKey'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      await deleteScopedAPIKey(id)
      dialog.destroy()
    },
    onClose: () => dialog.destroy(),
  })
}

async function deleteScopedAPIKey(id: number) {
  const resp = await deleteTenantAPIKey(tenantId.value, id)
  if (!resp.success) {
    MessagePlugin.error(resp.message || t('integrations.api.deleteApiKeyFailed'))
    return
  }
  MessagePlugin.success(t('integrations.api.deleteApiKeySuccess'))
  await loadAPIKeys()
}

function formatKeyKnowledgeScope(ids: string[] = []) {
  if (!ids.length) return t('integrations.api.allKnowledgeBases')
  const names = ids.map((id) => knowledgeBases.value.find((kb) => kb.id === id)?.name || id)
  return names.join(', ')
}

function formatKeyMaskedValue(key: TenantAPIKey) {
  const value = key.api_key || ''
  if (!value) return '-'
  return maskAPIKey(value)
}

function maskAPIKey(value: string) {
  if (value.length <= 12) return '*'.repeat(value.length)
  return `${value.slice(0, 8)}${'*'.repeat(8)}${value.slice(-6)}`
}

function formatDate(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString()
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

onMounted(async () => {
  await tryLoadWailsApiBaseURL()
  await loadDesktopApiPrefs()
  await load()
})
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

.row--single {
  display: block;
}

.row--doc {
  grid-template-columns: 1fr;
}

.doc-link {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--td-brand-color);
  cursor: pointer;
  text-decoration: none;

  &:hover {
    text-decoration: underline;
  }
}

.link-icon {
  font-size: 13px;
}

.desktop-api-control {
  display: flex;
  align-items: center;
  gap: 8px;
}

.desktop-port-input-wrap {
  flex: 1;
  min-width: 0;

  :deep(.t-input-number),
  :deep(.t-input__wrap) {
    width: 100%;
  }

  :deep(input) {
    font-family: var(--app-font-family-mono);
    font-size: 12px;
  }
}

.desktop-bind-public-control {
  justify-content: flex-end;
  padding-top: 4px;
}

.api-key-section {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 20px 0;
  border-bottom: 1px solid var(--td-component-stroke);
}

.api-key-section__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.api-key-section__title {
  min-width: 0;

  label {
    display: block;
    margin-bottom: 6px;
    color: var(--td-text-color-primary);
    font-size: 15px;
    font-weight: 600;
    line-height: 1.4;
  }

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.55;
  }
}

.api-key-section__body {
  min-width: 0;
}

.api-key-list {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  overflow: hidden;
}

.api-key-list__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 88px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.api-key-table-wrap {
  width: 100%;
  overflow-x: auto;
}

.api-key-table {
  width: 100%;
  min-width: 960px;
  border-collapse: collapse;
  table-layout: fixed;

  th,
  td {
    padding: 13px 14px;
    border-bottom: 1px solid var(--td-component-stroke);
    text-align: left;
    vertical-align: middle;
  }

  th {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-placeholder);
    font-size: 12px;
    font-weight: 500;
    line-height: 1.4;
  }

  td {
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.45;
  }

  th:nth-child(1),
  td:nth-child(1) {
    width: 18%;
  }

  th:nth-child(2),
  td:nth-child(2) {
    width: 22%;
  }

  th:nth-child(3),
  td:nth-child(3) {
    width: 24%;
  }

  th:nth-child(4),
  td:nth-child(4) {
    width: 16%;
  }

  th:nth-child(5),
  td:nth-child(5) {
    width: 12%;
  }

  th:nth-child(6),
  td:nth-child(6) {
    width: 112px;
  }

  tbody tr:last-child td {
    border-bottom: none;
  }
}

.api-key-table__actions-heading {
  text-align: right !important;
}

.api-key-table__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
}

.api-key-name {
  display: block;
  min-width: 0;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.api-key-fingerprint {
  display: inline-block;
  max-width: 100%;
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family-mono);
  font-size: 12px;
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: top;
  white-space: nowrap;
}

.api-key-access-cell {
  display: flex;
  min-width: 0;
  flex-direction: column;
  align-items: flex-start;
  gap: 7px;
}

.api-key-access-mode {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  height: 24px;
  padding: 0 9px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  font-size: 12px;
  font-weight: 600;
  line-height: 22px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.api-key-access-mode--full {
  border-color: color-mix(in srgb, var(--td-brand-color) 28%, transparent);
  background: color-mix(in srgb, var(--td-brand-color) 10%, var(--td-bg-color-container));
  color: var(--td-brand-color);
}

.api-key-capability-chips {
  display: flex;
  max-width: 100%;
  flex-wrap: wrap;
  gap: 5px;
}

.api-key-capability-chip {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  height: 22px;
  padding: 0 8px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--td-success-color) 10%, var(--td-bg-color-container));
  color: var(--td-success-color);
  font-size: 12px;
  font-weight: 500;
  line-height: 20px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.api-key-capability-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.api-key-capability-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 0 2px;
}

.api-key-capability-group + .api-key-capability-group {
  border-top: 1px solid var(--td-component-stroke);
}

.api-key-capability-group__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 24px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.api-key-capability-group__items {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.api-key-capability-item .scope-hint {
  margin-top: 2px;
}

.api-key-knowledge-scope,
.api-key-created-at {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}


.api-key-dialog {
  display: flex;
  flex-direction: column;
  gap: 0;
  padding: 0;
  border-bottom: 1px solid var(--td-component-stroke);
}

.api-key-dialog-row {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);

  &:first-child {
    padding-top: 0;
  }

  &:last-child {
    border-bottom: none;
  }

  &__label {
    min-width: 0;

    label {
      display: flex;
      align-items: center;
      gap: 8px;
      color: var(--td-text-color-primary);
      font-size: 14px;
      font-weight: 600;
      line-height: 1.45;

      &::before {
        content: '';
        flex-shrink: 0;
        width: 3px;
        height: 14px;
        border-radius: 2px;
        background: var(--td-brand-color);
      }
    }

    p {
      margin: 2px 0 0;
      color: var(--td-text-color-placeholder);
      font-size: 12px;
      line-height: 1.5;
    }
  }

  :deep(.t-input),
  :deep(.t-select__wrap) {
    border-radius: 4px;
  }

  :deep(.t-input) {
    background-color: var(--td-bg-color-secondarycontainer);
    border-color: transparent;
    box-shadow: none !important;
  }

  :deep(.t-input:hover),
  :deep(.t-input.t-is-focused) {
    border-color: var(--td-component-border);
    background-color: var(--td-bg-color-container);
  }
}

.scope-hint {
  margin: 8px 0 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 18px;
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

.secret-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
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

  .secret-mono-input {
    :deep(.t-input__suffix) {
      display: none;
    }
  }

  :deep(.t-button) {
    flex: 0 0 auto;
  }
}

.secret-saved-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-warning-color);
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
