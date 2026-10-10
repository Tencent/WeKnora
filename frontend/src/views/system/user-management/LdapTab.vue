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
          <span class="provider-card__title">{{ t('userManagement.ldap.enable') }}</span>
          <t-switch v-model="form.enabled" :disabled="saving" />
        </div>
        <p class="provider-card__hint">{{ t('userManagement.ldap.enableHint') }}</p>
      </section>

      <section class="provider-card">
        <h3 class="provider-section">{{ t('userManagement.ldap.sectionConnection') }}</h3>
        <div class="field-grid">
          <t-form-item :label="t('userManagement.ldap.host')">
            <t-input
              v-model="form.host"
              :disabled="saving"
              :placeholder="t('userManagement.ldap.hostPlaceholder')"
            />
            <p class="field-hint">{{ t('userManagement.ldap.hostHint') }}</p>
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.port')">
            <t-input-number v-model="form.port" :min="1" :max="65535" :disabled="saving" />
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.useTls')">
            <t-switch v-model="form.use_tls" :disabled="saving || form.start_tls" />
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.startTls')">
            <t-switch v-model="form.start_tls" :disabled="saving || form.use_tls" />
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.skipTlsVerify')">
            <t-switch v-model="form.skip_tls_verify" :disabled="saving" />
            <p class="field-hint">{{ t('userManagement.ldap.skipTlsVerifyHint') }}</p>
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.timeoutSeconds')">
            <t-input-number v-model="form.timeout_seconds" :min="0" :max="120" :disabled="saving" />
          </t-form-item>
        </div>
      </section>

      <section class="provider-card">
        <h3 class="provider-section">{{ t('userManagement.ldap.sectionBinding') }}</h3>
        <div class="field-grid">
          <t-form-item :label="t('userManagement.ldap.bindDn')">
            <t-input
              v-model="form.bind_dn"
              :disabled="saving"
              placeholder="CN=svc,OU=Service,DC=example,DC=com"
            />
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.bindPassword')">
            <t-input
              v-model="form.bind_password"
              type="password"
              autocomplete="new-password"
              :disabled="saving || form.clear_bind_password"
              :placeholder="
                view?.has_bind_password
                  ? t('userManagement.ldap.bindPasswordKeep')
                  : t('userManagement.ldap.bindPassword')
              "
            />
            <t-checkbox
              v-if="view?.has_bind_password"
              v-model="form.clear_bind_password"
              :disabled="saving"
            >
              {{ t('userManagement.ldap.bindPasswordClear') }}
            </t-checkbox>
          </t-form-item>
        </div>
      </section>

      <section class="provider-card">
        <h3 class="provider-section">{{ t('userManagement.ldap.sectionSearch') }}</h3>
        <div class="field-grid">
          <t-form-item :label="t('userManagement.ldap.baseDn')">
            <t-input v-model="form.base_dn" :disabled="saving" placeholder="DC=example,DC=com" />
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.userSearchBase')">
            <t-input
              v-model="form.user_search_base"
              :disabled="saving"
              placeholder="OU=Users,DC=example,DC=com"
            />
            <p class="field-hint">{{ t('userManagement.ldap.userSearchBaseHint') }}</p>
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.userFilter')" class="field-grid__full">
            <t-input v-model="form.user_filter" :disabled="saving" />
            <p class="field-hint">{{ t('userManagement.ldap.userFilterHint') }}</p>
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.userNameAttr')">
            <t-input v-model="form.user_name_attr" :disabled="saving" placeholder="sAMAccountName" />
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.userEmailAttr')">
            <t-input v-model="form.user_email_attr" :disabled="saving" placeholder="mail" />
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.userDisplayAttr')">
            <t-input v-model="form.user_display_attr" :disabled="saving" placeholder="displayName" />
          </t-form-item>
        </div>
      </section>

      <section class="provider-card">
        <h3 class="provider-section">{{ t('userManagement.ldap.sectionProvisioning') }}</h3>
        <div class="field-grid">
          <t-form-item :label="t('userManagement.ldap.autoCreateUser')">
            <t-switch v-model="form.auto_create_user" :disabled="saving" />
            <p class="field-hint">{{ t('userManagement.ldap.autoCreateUserHint') }}</p>
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.defaultTenantMode')">
            <t-input v-model="form.default_tenant_mode" :disabled="saving" />
            <p class="field-hint">{{ t('userManagement.ldap.defaultTenantModeHint') }}</p>
          </t-form-item>
        </div>
      </section>

      <!-- Probe panel. Without credentials this only exercises connectivity and
           the service bind; supplying them additionally verifies the user
           search and a real bind. -->
      <section class="provider-card">
        <h3 class="provider-section">{{ t('userManagement.ldap.testConnection') }}</h3>
        <div class="field-grid">
          <t-form-item :label="t('userManagement.ldap.testUsername')">
            <t-input
              v-model="probe.username"
              :disabled="testing"
              :placeholder="t('userManagement.ldap.testUsernamePlaceholder')"
            />
            <p class="field-hint">{{ t('userManagement.ldap.testWithCredentials') }}</p>
          </t-form-item>
          <t-form-item :label="t('userManagement.ldap.testPassword')">
            <t-input
              v-model="probe.password"
              type="password"
              autocomplete="new-password"
              :disabled="testing"
            />
          </t-form-item>
        </div>
      </section>

      <t-alert v-if="testResult" :theme="testResult.success ? 'success' : 'error'" class="source-alert">
        <template #message>
          <div class="probe">
            <div class="probe__title">
              {{ testResult.success ? t('userManagement.ldap.serviceOk') : t('userManagement.ldap.testConnection') }}
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

      <t-alert theme="info" :message="t('userManagement.ldap.ldapLoginNote')" class="source-alert" />

      <div class="provider-actions">
        <t-button variant="outline" :loading="testing" :disabled="saving" @click="runTest">
          {{ testing ? t('userManagement.ldap.testing') : t('userManagement.ldap.testConnection') }}
        </t-button>
        <t-button theme="primary" :loading="saving" @click="save">
          {{ saving ? t('userManagement.ldap.saving') : t('userManagement.ldap.save') }}
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
  getLDAPProvider,
  testLDAPProvider,
  updateLDAPProvider,
  type LDAPProviderView,
  type LDAPTestResponse,
} from '@/api/system/user-management'

const { t } = useI18n()

const view = ref<LDAPProviderView | null>(null)
const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const testResult = ref<LDAPTestResponse | null>(null)

const form = reactive({
  enabled: false,
  host: '',
  port: 389,
  use_tls: false,
  start_tls: false,
  skip_tls_verify: false,
  bind_dn: '',
  bind_password: '',
  clear_bind_password: false,
  base_dn: '',
  user_search_base: '',
  user_filter: '',
  user_name_attr: '',
  user_email_attr: '',
  user_display_attr: '',
  auto_create_user: false,
  default_tenant_mode: '',
  timeout_seconds: 0,
})

const probe = reactive({ username: '', password: '' })

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

const probeRows = computed(() => {
  const r = testResult.value
  if (!r) return []
  const rows: Array<{ label: string; value?: string }> = []
  if (r.user_dn) rows.push({ label: 'DN', value: r.user_dn })
  if (r.detected_subject_attribute) {
    rows.push({
      label: t('userManagement.ldap.detectedSubjectAttribute'),
      value: r.detected_subject_attribute,
    })
  }
  for (const [key, value] of Object.entries(r.attributes ?? {})) {
    rows.push({ label: `${t('userManagement.ldap.attributes')}: ${key}`, value })
  }
  return rows
})

function applyView(v: LDAPProviderView) {
  view.value = v
  form.enabled = v.enabled
  form.host = v.host || ''
  form.port = v.port || 389
  form.use_tls = v.use_tls
  form.start_tls = v.start_tls
  form.skip_tls_verify = v.skip_tls_verify
  form.bind_dn = v.bind_dn || ''
  form.bind_password = ''
  form.clear_bind_password = false
  form.base_dn = v.base_dn || ''
  form.user_search_base = v.user_search_base || ''
  form.user_filter = v.user_filter || ''
  form.user_name_attr = v.user_name_attr || ''
  form.user_email_attr = v.user_email_attr || ''
  form.user_display_attr = v.user_display_attr || ''
  form.auto_create_user = v.auto_create_user
  form.default_tenant_mode = v.default_tenant_mode || ''
  form.timeout_seconds = v.timeout_seconds || 0
}

async function load() {
  loading.value = true
  try {
    applyView(await getLDAPProvider())
  } catch (error) {
    MessagePlugin.error(
      error instanceof Error && error.message
        ? `${t('userManagement.ldap.loadFailed')}: ${error.message}`
        : t('userManagement.ldap.loadFailed'),
    )
  } finally {
    loading.value = false
  }
}

/**
 * validate mirrors the server-side checks so the operator gets the message
 * inline instead of after a round-trip. The server remains authoritative.
 */
function validate(): boolean {
  if (!form.host.trim()) {
    MessagePlugin.warning(t('userManagement.ldap.requiredHost'))
    return false
  }
  if (!form.base_dn.trim()) {
    MessagePlugin.warning(t('userManagement.ldap.requiredBaseDn'))
    return false
  }
  if (form.use_tls && form.start_tls) {
    MessagePlugin.warning(t('userManagement.ldap.tlsConflict'))
    return false
  }
  return true
}

async function save() {
  if (!validate()) return
  saving.value = true
  try {
    const updated = await updateLDAPProvider({
      enabled: form.enabled,
      host: form.host.trim(),
      port: form.port,
      use_tls: form.use_tls,
      start_tls: form.start_tls,
      skip_tls_verify: form.skip_tls_verify,
      bind_dn: form.bind_dn.trim(),
      // Omit to keep the stored password; clear_bind_password is the only way
      // to actually drop it.
      bind_password:
        form.clear_bind_password || !form.bind_password ? undefined : form.bind_password,
      base_dn: form.base_dn.trim(),
      user_search_base: form.user_search_base.trim(),
      user_filter: form.user_filter.trim(),
      user_name_attr: form.user_name_attr.trim(),
      user_email_attr: form.user_email_attr.trim(),
      user_display_attr: form.user_display_attr.trim(),
      auto_create_user: form.auto_create_user,
      default_tenant_mode: form.default_tenant_mode.trim(),
      timeout_seconds: form.timeout_seconds,
      clear_bind_password: form.clear_bind_password,
    })
    applyView(updated)
    MessagePlugin.success(t('userManagement.ldap.saveSuccess'))
  } catch (error) {
    MessagePlugin.error(
      error instanceof Error && error.message
        ? `${t('userManagement.ldap.saveFailed')}: ${error.message}`
        : t('userManagement.ldap.saveFailed'),
    )
  } finally {
    saving.value = false
  }
}

async function runTest() {
  testing.value = true
  testResult.value = null
  try {
    testResult.value = await testLDAPProvider({
      username: probe.username.trim() || undefined,
      password: probe.password || undefined,
    })
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
