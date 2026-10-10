<template>
  <div class="provider-tab">
    <t-loading :loading="loading" size="small">
      <t-alert
        v-if="view"
        :theme="sourceTheme"
        :message="`${t(`userManagement.configSource.${view.source}`)} — ${t(`userManagement.configSourceHint.${view.source}`)}`"
        class="source-alert"
      />

      <section class="provider-card">
        <div class="provider-card__head">
          <span class="provider-card__title">{{ t('userManagement.oidc.enable') }}</span>
          <t-switch v-model="form.enabled" :disabled="saving" />
        </div>
        <p class="provider-card__hint">{{ t('userManagement.oidc.enableHint') }}</p>
      </section>

      <section class="provider-card">
        <h3 class="provider-section">{{ t('userManagement.oidc.sectionBasic') }}</h3>
        <div class="field-grid">
          <t-form-item :label="t('userManagement.oidc.providerDisplayName')">
            <t-input
              v-model="form.provider_display_name"
              :disabled="saving"
              :placeholder="t('userManagement.oidc.providerDisplayNamePlaceholder')"
            />
          </t-form-item>
          <t-form-item :label="t('userManagement.oidc.clientId')">
            <t-input v-model="form.client_id" :disabled="saving" placeholder="weknora" />
          </t-form-item>
          <t-form-item :label="t('userManagement.oidc.issuerUrl')" class="field-grid__full">
            <t-input
              v-model="form.issuer_url"
              :disabled="saving"
              :placeholder="t('userManagement.oidc.issuerUrlPlaceholder')"
            />
          </t-form-item>
          <t-form-item :label="t('userManagement.oidc.discoveryUrl')" class="field-grid__full">
            <t-input
              v-model="form.discovery_url"
              :disabled="saving"
              :placeholder="derivedDiscovery"
            />
            <p class="field-hint">{{ t('userManagement.oidc.discoveryUrlHint') }}</p>
          </t-form-item>
          <t-form-item :label="t('userManagement.oidc.clientSecret')" class="field-grid__full">
            <t-input
              v-model="form.client_secret"
              type="password"
              autocomplete="new-password"
              :disabled="saving || form.clear_secret"
              :placeholder="
                view?.has_secret
                  ? t('userManagement.oidc.clientSecretKeep')
                  : t('userManagement.oidc.clientSecret')
              "
            />
            <t-checkbox v-if="view?.has_secret" v-model="form.clear_secret" :disabled="saving">
              {{ t('userManagement.oidc.clientSecretClear') }}
            </t-checkbox>
          </t-form-item>
          <t-form-item :label="t('userManagement.oidc.scopes')" class="field-grid__full">
            <t-tag-input
              v-model="form.scopes"
              :disabled="saving"
              :placeholder="t('userManagement.oidc.scopesPlaceholder')"
              clearable
            />
          </t-form-item>
        </div>
      </section>

      <section class="provider-card">
        <h3 class="provider-section">{{ t('userManagement.oidc.sectionEndpoints') }}</h3>
        <p class="provider-card__hint">{{ t('userManagement.oidc.discoveryUrlHint') }}</p>
        <div class="field-grid">
          <t-form-item :label="t('userManagement.oidc.authorizationEndpoint')">
            <t-input v-model="form.authorization_endpoint" :disabled="saving" />
          </t-form-item>
          <t-form-item :label="t('userManagement.oidc.tokenEndpoint')">
            <t-input v-model="form.token_endpoint" :disabled="saving" />
          </t-form-item>
          <t-form-item :label="t('userManagement.oidc.userInfoEndpoint')">
            <t-input v-model="form.user_info_endpoint" :disabled="saving" />
          </t-form-item>
          <t-form-item :label="t('userManagement.oidc.jwksUri')">
            <t-input v-model="form.jwks_uri" :disabled="saving" />
          </t-form-item>
        </div>
      </section>

      <section class="provider-card">
        <h3 class="provider-section">{{ t('userManagement.oidc.sectionClaims') }}</h3>
        <div class="field-grid">
          <t-form-item :label="t('userManagement.oidc.usernameClaim')">
            <t-input v-model="form.username_claim" :disabled="saving" placeholder="name" />
          </t-form-item>
          <t-form-item :label="t('userManagement.oidc.emailClaim')">
            <t-input v-model="form.email_claim" :disabled="saving" placeholder="email" />
          </t-form-item>
        </div>
      </section>

      <!-- Legacy env vars are still reported even once a row exists, so an
           operator can see which values are shadowed rather than in effect. -->
      <t-alert
        v-if="envOverrideEntries.length"
        theme="info"
        class="source-alert"
        :message="view?.source === 'database'
          ? t('userManagement.oidc.envNoticeSaved')
          : t('userManagement.oidc.envNoticeUnsaved')"
      >
        <template #operation>
          <div class="env-list">
            <code v-for="[key, value] in envOverrideEntries" :key="key">{{ key }}={{ value }}</code>
          </div>
        </template>
      </t-alert>

      <t-alert
        v-if="testResult"
        :theme="testResult.success ? 'success' : 'error'"
        class="source-alert"
      >
        <template #message>
          <div class="probe">
            <div class="probe__title">
              {{ testResult.success ? t('userManagement.oidc.testSuccess') : t('userManagement.oidc.testFailed') }}
            </div>
            <div class="probe__message">{{ testResult.message }}</div>
            <dl v-if="probeRows.length" class="probe__list">
              <template v-for="row in probeRows" :key="row.label">
                <dt>{{ row.label }}</dt>
                <dd>{{ row.value }}</dd>
              </template>
            </dl>
            <ul v-if="testResult.warnings?.length" class="probe__warnings">
              <li v-for="w in testResult.warnings" :key="w">{{ w }}</li>
            </ul>
          </div>
        </template>
      </t-alert>

      <div class="provider-actions">
        <t-button variant="outline" :loading="testing" :disabled="saving" @click="runTest">
          {{ testing ? t('userManagement.oidc.testing') : t('userManagement.oidc.testConnection') }}
        </t-button>
        <t-button theme="primary" :loading="saving" @click="save">
          {{ saving ? t('userManagement.oidc.saving') : t('userManagement.oidc.save') }}
        </t-button>
      </div>
    </t-loading>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  getOIDCProvider,
  testOIDCProvider,
  updateOIDCProvider,
  type OIDCProviderView,
  type OIDCTestResponse,
} from '@/api/system/user-management'

const { t } = useI18n()

const view = ref<OIDCProviderView | null>(null)
const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const testResult = ref<OIDCTestResponse | null>(null)

const form = reactive({
  enabled: false,
  issuer_url: '',
  discovery_url: '',
  provider_display_name: '',
  client_id: '',
  client_secret: '',
  clear_secret: false,
  authorization_endpoint: '',
  token_endpoint: '',
  user_info_endpoint: '',
  jwks_uri: '',
  scopes: [] as string[],
  username_claim: '',
  email_claim: '',
})

const sourceTheme = computed(() => {
  switch (view.value?.source) {
    case 'database':
      return 'success'
    case 'environment':
      return 'warning'
    default:
      return 'info'
  }
})

/** Mirrors the backend's derivation so the placeholder shows the real target. */
const derivedDiscovery = computed(() => {
  const issuer = form.issuer_url.trim().replace(/\/+$/, '')
  return issuer ? `${issuer}/.well-known/openid-configuration` : 'https://…/.well-known/openid-configuration'
})

const envOverrideEntries = computed(() => Object.entries(view.value?.env_overrides ?? {}))

const probeRows = computed(() => {
  const r = testResult.value
  if (!r) return []
  return (
    [
      { label: t('userManagement.oidc.issuerUrl'), value: r.issuer },
      { label: t('userManagement.oidc.authorizationEndpoint'), value: r.authorization_endpoint },
      { label: t('userManagement.oidc.tokenEndpoint'), value: r.token_endpoint },
      { label: t('userManagement.oidc.userInfoEndpoint'), value: r.user_info_endpoint },
      { label: t('userManagement.oidc.jwksUri'), value: r.jwks_uri },
    ] as Array<{ label: string; value?: string }>
  ).filter((row) => !!row.value)
})

function applyView(v: OIDCProviderView) {
  view.value = v
  form.enabled = v.enabled
  form.issuer_url = v.issuer_url || ''
  // Show the stored discovery URL only when it is explicitly configured;
  // a derived one belongs in the placeholder so the operator can tell the
  // difference between "set" and "computed".
  form.discovery_url = v.discovery_url && v.discovery_url !== derivedFrom(v.issuer_url) ? v.discovery_url : ''
  form.provider_display_name = v.provider_display_name || ''
  form.client_id = v.client_id || ''
  form.client_secret = ''
  form.clear_secret = false
  form.authorization_endpoint = v.authorization_endpoint || ''
  form.token_endpoint = v.token_endpoint || ''
  form.user_info_endpoint = v.user_info_endpoint || ''
  form.jwks_uri = v.jwks_uri || ''
  form.scopes = [...(v.scopes || [])]
  form.username_claim = v.username_claim || ''
  form.email_claim = v.email_claim || ''
}

function derivedFrom(issuer: string | undefined) {
  const trimmed = (issuer || '').trim().replace(/\/+$/, '')
  return trimmed ? `${trimmed}/.well-known/openid-configuration` : ''
}

async function load() {
  loading.value = true
  try {
    applyView(await getOIDCProvider())
  } catch (error) {
    MessagePlugin.error(
      error instanceof Error && error.message
        ? `${t('userManagement.oidc.loadFailed')}: ${error.message}`
        : t('userManagement.oidc.loadFailed'),
    )
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!form.issuer_url.trim() && !form.discovery_url.trim()) {
    MessagePlugin.warning(t('userManagement.oidc.requiredIssuer'))
    return
  }
  saving.value = true
  try {
    const updated = await updateOIDCProvider({
      enabled: form.enabled,
      issuer_url: form.issuer_url.trim(),
      discovery_url: form.discovery_url.trim(),
      provider_display_name: form.provider_display_name.trim(),
      client_id: form.client_id.trim(),
      // Omit to keep the stored secret; the server treats absent and empty
      // identically. clear_secret is the only way to actually drop it.
      client_secret: form.clear_secret || !form.client_secret ? undefined : form.client_secret,
      authorization_endpoint: form.authorization_endpoint.trim(),
      token_endpoint: form.token_endpoint.trim(),
      user_info_endpoint: form.user_info_endpoint.trim(),
      jwks_uri: form.jwks_uri.trim(),
      scopes: form.scopes,
      username_claim: form.username_claim.trim(),
      email_claim: form.email_claim.trim(),
      clear_secret: form.clear_secret,
    })
    applyView(updated)
    MessagePlugin.success(t('userManagement.oidc.saveSuccess'))
  } catch (error) {
    MessagePlugin.error(
      error instanceof Error && error.message
        ? `${t('userManagement.oidc.saveFailed')}: ${error.message}`
        : t('userManagement.oidc.saveFailed'),
    )
  } finally {
    saving.value = false
  }
}

async function runTest() {
  testing.value = true
  testResult.value = null
  try {
    testResult.value = await testOIDCProvider()
  } catch (error) {
    MessagePlugin.error(error instanceof Error && error.message ? error.message : String(error))
  } finally {
    testing.value = false
  }
}

onMounted(() => {
  void load()
})
</script>

<style scoped>
.provider-tab {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.source-alert {
  margin-bottom: 12px;
}

.provider-card {
  padding: 16px 18px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  margin-bottom: 16px;
}

.provider-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.provider-card__title {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.provider-card__hint {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.provider-section {
  margin: 0 0 14px;
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.field-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 0 20px;
}

.field-grid__full {
  grid-column: 1 / -1;
}

.field-hint {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.provider-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding-top: 4px;
}

.env-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-top: 6px;
  font-size: 12px;
}

.env-list code {
  word-break: break-all;
}

.probe__title {
  font-weight: 600;
}

.probe__message {
  margin-top: 2px;
  font-size: 12px;
}

.probe__list {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 2px 10px;
  margin: 8px 0 0;
  font-size: 12px;
}

.probe__list dt {
  color: var(--td-text-color-secondary);
}

.probe__list dd {
  margin: 0;
  word-break: break-all;
}

.probe__warnings {
  margin: 8px 0 0;
  padding-left: 18px;
  font-size: 12px;
}
</style>
