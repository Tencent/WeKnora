<template>
  <t-drawer
    :visible="visible"
    :header="record ? $t('settings.sandbox.editTitle') : $t('settings.sandbox.createTitle')"
    size="620px"
    :confirm-btn="{ content: $t('common.save'), loading: saving }"
    :cancel-btn="{ content: $t('common.cancel') }"
    @confirm="save"
    @close="close"
    @update:visible="(v: boolean) => emit('update:visible', v)"
  >
    <!--
      Identity-change refusals must sit at the top: the form is long and the
      admin otherwise saves, sees nothing, and assumes the click did nothing.
    -->
    <div v-if="conflict" ref="conflictAlertRef" class="blocked blocked-top">
      <t-alert v-if="conflict.code === 'sandboxes_still_live'" theme="warning"
        :message="$t('settings.sandbox.sandboxesStillLive', { count: conflict.inventory?.sandbox_count ?? 0 })">
        <template #description>
          <p v-if="affectedSessionCount">{{ $t('settings.sandbox.affectedSessions', { count: affectedSessionCount }) }}</p>
          <p v-if="conflict.inventory?.agent_names?.length">
            {{ $t('settings.sandbox.affectedAgents', { names: conflict.inventory.agent_names.join('、') }) }}
          </p>
          <p>{{ $t('settings.sandbox.blockedHint') }}</p>
        </template>
      </t-alert>
      <t-alert v-else theme="warning" :message="$t('settings.sandbox.unverifiableBlocked')">
        <template #description>
          <p>{{ $t('settings.sandbox.unverifiableSaveHint') }}</p>
        </template>
      </t-alert>
    </div>

    <t-form label-align="top">
      <t-form-item :label="$t('settings.sandbox.configName')" :status="nameError ? 'error' : undefined"
        :tips="nameError || undefined">
        <t-input v-model="name" :placeholder="$t('settings.sandbox.configNamePlaceholder')" />
      </t-form-item>
      <t-form-item :label="$t('settings.sandbox.configDescription')">
        <t-input v-model="description" :placeholder="$t('settings.sandbox.configDescriptionPlaceholder')" />
      </t-form-item>

      <!--
        Only while editing: a config being created owns no sandboxes yet, so
        warning that these fields are locked would describe a restriction that
        does not apply.
      -->
      <t-alert v-if="record" theme="info" class="identity-hint"
        :message="$t('settings.sandbox.identityFieldHint')" />

      <t-form-item :label="$t('settings.sandbox.backend')">
        <t-select v-model="backend" @change="onBackendChange">
          <t-option v-for="opt in backendOptions" :key="opt" :value="opt" :label="backendLabel(opt)" />
        </t-select>
      </t-form-item>

      <!--
        Placeholders are format examples, never the deployment's own values: a
        named config inherits nothing, so showing what .env holds would suggest
        an empty field still works.
      -->
      <!-- Cube -->
      <template v-if="backend === 'cube'">
        <t-form-item :label="requiredLabel('apiUrl')" :status="fieldStatus('api_url')" :tips="fieldTip('api_url')">
          <t-input v-model="cube.api_url" placeholder="http://cube.example.com:33000"
            @input="onFieldInput('api_url')" />
        </t-form-item>
        <t-form-item :label="requiredLabel('proxyUrl')" :status="fieldStatus('proxy_url')"
          :tips="fieldTip('proxy_url')">
          <t-input v-model="cube.proxy_url" placeholder="http://cube.example.com:80"
            @input="onFieldInput('proxy_url')" />
        </t-form-item>
        <t-form-item :label="requiredLabel('sandboxDomain')" :status="fieldStatus('sandbox_domain')"
          :tips="fieldTip('sandbox_domain')">
          <t-input v-model="cube.sandbox_domain" placeholder="cube.app" @input="onFieldInput('sandbox_domain')" />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.apiKey')" :help="$t('settings.sandbox.cubeApiKeyOptional')">
          <t-input v-model="cube.api_key" type="password" :placeholder="secretPlaceholder" />
        </t-form-item>
        <t-form-item :label="requiredLabel('templateId')" :status="fieldStatus('template_id')"
          :tips="fieldTip('template_id')">
          <t-input v-model="cube.template_id" placeholder="tpl-xxxxxxxx" @input="onFieldInput('template_id')" />
        </t-form-item>
        <div class="timeout-row">
          <t-form-item :label="$t('settings.sandbox.httpTimeout')">
            <t-input-number v-model="cube.http_timeout_sec" :min="0" theme="column" placeholder="30" />
          </t-form-item>
          <t-form-item :label="$t('settings.sandbox.sandboxTtl')">
            <t-input-number v-model="cube.cube_sandbox_ttl_seconds" :min="0" theme="column" placeholder="1800" />
          </t-form-item>
          <t-form-item :label="$t('settings.sandbox.defaultTimeout')">
            <t-input-number v-model="defaultTimeoutSec" :min="0" theme="column"
              :placeholder="String(defaults?.default_timeout_sec || 60)" />
          </t-form-item>
        </div>
      </template>

      <!-- E2B -->
      <template v-else-if="backend === 'e2b'">
        <t-form-item :label="$t('settings.sandbox.apiUrl')" :help="$t('settings.sandbox.e2bApiUrlOptional')">
          <t-input v-model="e2b.api_url" placeholder="https://api.e2b.app" @input="invalidateCheck" />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.sandboxDomain')" :help="$t('settings.sandbox.e2bDomainOptional')">
          <t-input v-model="e2b.sandbox_domain" placeholder="e2b.app" />
        </t-form-item>
        <t-form-item :label="requiredLabel('apiKey')" :status="fieldStatus('api_key')" :tips="fieldTip('api_key')">
          <t-input v-model="e2b.api_key" type="password" :placeholder="secretPlaceholder"
            @input="onFieldInput('api_key')" />
        </t-form-item>
        <t-form-item :label="requiredLabel('templateId')" :status="fieldStatus('template_id')"
          :tips="fieldTip('template_id')">
          <t-input v-model="e2b.template_id" placeholder="xxxxxxxxxxxxxxxx"
            @input="onFieldInput('template_id')" />
        </t-form-item>
        <div class="timeout-row">
          <t-form-item :label="$t('settings.sandbox.httpTimeout')">
            <t-input-number v-model="e2b.http_timeout_sec" :min="0" theme="column" placeholder="30" />
          </t-form-item>
          <t-form-item :label="$t('settings.sandbox.sandboxTtl')">
            <t-input-number v-model="e2b.e2b_sandbox_ttl_seconds" :min="0" theme="column" placeholder="300" />
          </t-form-item>
          <t-form-item :label="$t('settings.sandbox.defaultTimeout')">
            <t-input-number v-model="defaultTimeoutSec" :min="0" theme="column"
              :placeholder="String(defaults?.default_timeout_sec || 60)" />
          </t-form-item>
        </div>
      </template>

      <!-- Environment variables: a map on the wire, editable rows in the UI. -->
      <t-form-item :label="$t('settings.sandbox.envVars')"
        :help="$t('settings.sandbox.envVarsHint')">
        <div class="env-rows">
          <div v-for="(row, index) in envRows" :key="index" class="env-row">
            <t-input v-model="row.key" :placeholder="$t('settings.sandbox.envKey')" style="width: 200px" />
            <t-input v-model="row.value" type="password" :placeholder="$t('settings.sandbox.envValue')"
              style="width: 220px" />
            <t-button variant="text" theme="danger" @click="envRows.splice(index, 1)">
              {{ $t('settings.sandbox.removeRow') }}
            </t-button>
          </div>
          <t-button variant="outline" size="small" @click="envRows.push({ key: '', value: '' })">
            {{ $t('settings.sandbox.addRow') }}
          </t-button>
        </div>
      </t-form-item>
    </t-form>

    <div v-if="isRemoteBackend" class="check-actions">
      <t-button variant="outline" :loading="checking" @click="runCheck(false)">
        {{ $t('settings.sandbox.testConnection') }}
      </t-button>
      <t-popconfirm :content="$t('settings.sandbox.deepCheckConfirm')" @confirm="runCheck(true)">
        <t-button variant="outline" :loading="checking">
          {{ $t('settings.sandbox.deepCheck') }}
        </t-button>
      </t-popconfirm>
    </div>

    <div v-if="checkResult" ref="checkResultRef" class="check-result">
      <t-alert :theme="checkResult.ok ? 'success' : 'error'"
        :message="checkResult.ok ? $t('settings.sandbox.checkPassed') : $t('settings.sandbox.checkFailed')" />
      <ul class="check-list">
        <li v-for="item in checkResult.checks" :key="item.name" class="check-item">
          <t-icon :name="item.ok === true ? 'check-circle-filled'
            : item.ok === false ? 'close-circle-filled' : 'minus-circle'"
            :class="item.ok === true ? 'ok' : item.ok === false ? 'err' : 'skip'" />
          <span class="check-name">{{ checkLabel(item.name) }}</span>
          <span v-if="item.latency_ms" class="check-latency">{{ item.latency_ms }} ms</span>
          <span v-if="item.message" class="check-message">{{ item.message }}</span>
        </li>
      </ul>
      <t-alert v-if="checkResult.capabilities && checkResult.capabilities.supports_volumes === false" theme="warning"
        :message="$t('settings.sandbox.noVolumeSupport')" />
    </div>

  </t-drawer>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  checkSandboxConfig,
  createSandboxConfig,
  parseSandboxConflict,
  updateSandboxConfigById,
  type SandboxCheckResult,
  type SandboxConfig,
  type SandboxConfigDefaults,
  type SandboxConfigRecord,
  type SandboxConflict,
  type SandboxCubeConfig,
  type SandboxE2BConfig,
  isNamedSandboxBackend,
  NAMED_SANDBOX_BACKEND_TYPES,
} from '@/api/system'

const props = defineProps<{
  visible: boolean
  record: SandboxConfigRecord | null
  presetType?: string
  defaults: SandboxConfigDefaults | null
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'saved'): void
}>()

const { t } = useI18n()

// The backend echoes secrets as this placeholder; submitting it unchanged keeps
// the stored value, so the form never needs the real key.
const secretPlaceholder = '***'

const backendOptions = [...NAMED_SANDBOX_BACKEND_TYPES]

const saving = ref(false)
const checking = ref(false)
const checkResult = ref<SandboxCheckResult | null>(null)
const conflict = ref<SandboxConflict | null>(null)
const conflictAlertRef = ref<HTMLElement | null>(null)
const checkResultRef = ref<HTMLElement | null>(null)
const nameError = ref('')

const name = ref('')
const description = ref('')
const backend = ref('')
// undefined rather than 0 so the input renders empty and shows its placeholder,
// matching the HTTP timeout / TTL fields. A literal 0 would read as a real value.
const defaultTimeoutSec = ref<number | undefined>(undefined)
const cube = reactive<SandboxCubeConfig>({})
const e2b = reactive<SandboxE2BConfig>({})
const envRows = ref<{ key: string; value: string }[]>([])

// Only remote backends have a control plane worth probing.
const isRemoteBackend = computed(() => backend.value === 'cube' || backend.value === 'e2b')

const backendLabel = (value: string) => t(`settings.sandbox.backends.${value}`)
const checkLabel = (probe: string) => t(`settings.sandbox.checks.${probe}`, probe)

// Mirrors sandbox.MissingRequiredFields on the server. Duplicated on purpose:
// the server stays the authority, this only spares the admin a round-trip and
// points at the offending input instead of showing one combined message.
const REQUIRED_FIELDS: Record<string, string[]> = {
  cube: ['api_url', 'proxy_url', 'sandbox_domain', 'template_id'],
  e2b: ['api_key', 'template_id'],
}

const fieldErrors = ref<Record<string, string>>({})

const fieldStatus = (field: string) => (fieldErrors.value[field] ? 'error' : undefined)
const fieldTip = (field: string) => fieldErrors.value[field] || undefined

const requiredLabel = (labelKey: string) => `${t(`settings.sandbox.${labelKey}`)} *`

// Clearing on input rather than re-validating keeps the error from flickering
// back while the admin is still halfway through typing a URL.
function onFieldInput(field: string) {
  delete fieldErrors.value[field]
  invalidateCheck()
}

function validateRequiredFields(): boolean {
  const required = REQUIRED_FIELDS[backend.value] || []
  const values = (backend.value === 'cube' ? cube : e2b) as Record<string, unknown>
  const errors: Record<string, string> = {}
  for (const field of required) {
    const value = values[field]
    if (typeof value !== 'string' || value.trim() === '') {
      errors[field] = t('settings.sandbox.fieldRequired')
    }
  }
  fieldErrors.value = errors
  return Object.keys(errors).length === 0
}

const affectedSessionCount = computed(() => conflict.value?.inventory?.session_ids?.length || 0)

function defaultBackendType(): string {
  const fromRecord = props.record?.config?.sandbox_type || props.presetType || ''
  if (isNamedSandboxBackend(fromRecord)) return fromRecord
  const fromDeploy = props.defaults?.sandbox_type || ''
  if (isNamedSandboxBackend(fromDeploy)) return fromDeploy
  return 'cube'
}

function reset() {
  const cfg: SandboxConfig = props.record?.config || {}
  name.value = props.record?.name || ''
  description.value = props.record?.description || ''
  backend.value = isNamedSandboxBackend(cfg.sandbox_type || '')
    ? cfg.sandbox_type!
    : defaultBackendType()
  defaultTimeoutSec.value = cfg.default_timeout_sec || undefined
  // Replace rather than merge: a reused reactive object would otherwise carry
  // the previously edited config's fields into the next one opened.
  Object.keys(cube).forEach((key) => delete (cube as Record<string, unknown>)[key])
  Object.keys(e2b).forEach((key) => delete (e2b as Record<string, unknown>)[key])
  Object.assign(cube, cfg.cube || {})
  Object.assign(e2b, cfg.e2b || {})
  envRows.value = Object.entries(cfg.env_vars || {}).map(([key, value]) => ({ key, value }))
  checkResult.value = null
  conflict.value = null
  nameError.value = ''
  fieldErrors.value = {}
}

watch(() => props.visible, (open) => {
  if (open) reset()
})

function collectPayload(): SandboxConfig {
  const envVars: Record<string, string> = {}
  for (const row of envRows.value) {
    const key = row.key.trim()
    if (key) envVars[key] = row.value
  }
  const payload: SandboxConfig = {
    sandbox_type: backend.value,
    default_timeout_sec: defaultTimeoutSec.value || undefined,
    env_vars: envVars,
  }
  // Send only the selected backend's block so an unused one cannot fail
  // validation (e.g. a stale private URL left in the other tab).
  if (backend.value === 'cube') payload.cube = { ...cube }
  if (backend.value === 'e2b') payload.e2b = { ...e2b }
  return payload
}

function close() {
  emit('update:visible', false)
}

async function save() {
  const trimmed = name.value.trim()
  if (!trimmed) {
    nameError.value = t('settings.sandbox.configNameRequired')
    return
  }
  nameError.value = ''
  if (!validateRequiredFields()) return
  saving.value = true
  conflict.value = null
  try {
    const payload = { name: trimmed, description: description.value, config: collectPayload() }
    if (props.record) {
      await updateSandboxConfigById(props.record.id, payload)
    } else {
      await createSandboxConfig(payload)
    }
    MessagePlugin.success(t('common.saveSuccess'))
    emit('saved')
    close()
  } catch (e: any) {
    const refusal = parseSandboxConflict(e)
    if (refusal) {
      // Keep the drawer open with the form intact: the admin has to act
      // elsewhere first, and retyping everything afterwards would be cruel.
      conflict.value = refusal
      await nextTick()
      conflictAlertRef.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      return
    }
    MessagePlugin.error(e?.message || t('settings.sandbox.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function runCheck(deep: boolean) {
  // The probe builds a real client, so an incomplete config would come back as
  // a generic client_build failure instead of naming the empty field.
  if (!validateRequiredFields()) return
  checking.value = true
  checkResult.value = null
  try {
    // config_id lets the backend resolve masked secrets against the stored row,
    // so an edited form can be probed without retyping the API key.
    const res = await checkSandboxConfig({
      config: collectPayload(),
      config_id: props.record?.id,
      deep,
    })
    checkResult.value = res?.data || null
    if (checkResult.value) {
      await nextTick()
      checkResultRef.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.checkFailed'))
  } finally {
    checking.value = false
  }
}

// A result that no longer matches the form is worse than none.
function invalidateCheck() {
  checkResult.value = null
}

// The two backends require different fields, so carrying errors across a switch
// would flag inputs the admin can no longer even see.
function onBackendChange() {
  fieldErrors.value = {}
  invalidateCheck()
}
</script>

<style scoped>
.identity-hint {
  margin-bottom: 16px;
}

.timeout-row {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 24px;
}

.timeout-row :deep(.t-form__item) {
  margin-bottom: 0;
}

.timeout-row :deep(.t-input-number) {
  width: 100%;
}

.env-rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: flex-start;
}

.env-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.check-actions {
  display: flex;
  gap: 12px;
  margin-top: 4px;
}

.check-result {
  margin-top: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.check-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.check-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.check-item .ok {
  color: var(--td-success-color);
}

.check-item .err {
  color: var(--td-error-color);
}

.check-item .skip {
  color: var(--td-text-color-placeholder);
}

.check-name {
  min-width: 140px;
}

.check-latency,
.check-message {
  color: var(--td-text-color-secondary);
}

.blocked {
  margin-top: 16px;
}

.blocked-top {
  margin-top: 0;
  margin-bottom: 16px;
}

.blocked p {
  margin: 4px 0 0;
}
</style>
