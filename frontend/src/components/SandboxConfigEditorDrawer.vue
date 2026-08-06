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
    <t-form label-align="top">
      <t-form-item :label="$t('settings.sandbox.configName')" :status="nameError ? 'error' : undefined"
        :tips="nameError || undefined">
        <t-input v-model="name" :placeholder="$t('settings.sandbox.configNamePlaceholder')" />
      </t-form-item>
      <t-form-item :label="$t('settings.sandbox.configDescription')">
        <t-input v-model="description" :placeholder="$t('settings.sandbox.configDescriptionPlaceholder')" />
      </t-form-item>

      <t-alert theme="info" class="identity-hint" :message="$t('settings.sandbox.identityFieldHint')" />

      <t-form-item :label="$t('settings.sandbox.backend')">
        <t-select v-model="backend" @change="invalidateCheck">
          <t-option v-for="opt in backendOptions" :key="opt" :value="opt" :label="backendLabel(opt)" />
        </t-select>
      </t-form-item>

      <!-- Cube -->
      <template v-if="backend === 'cube'">
        <t-form-item :label="$t('settings.sandbox.apiUrl')" :help="inheritedHint(cubeDefaults?.api_url)">
          <t-input v-model="cube.api_url" :placeholder="cubeDefaults?.api_url || 'http://127.0.0.1:33000'"
            @input="invalidateCheck" />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.proxyUrl')" :help="inheritedHint(cubeDefaults?.proxy_url)">
          <t-input v-model="cube.proxy_url" placeholder="http://127.0.0.1:80" @input="invalidateCheck" />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.sandboxDomain')" :help="inheritedHint(cubeDefaults?.sandbox_domain)">
          <t-input v-model="cube.sandbox_domain" placeholder="cube.app" />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.apiKey')"
          :help="inheritedHint(undefined, cubeDefaults?.api_key_configured)">
          <t-input v-model="cube.api_key" type="password" :placeholder="secretPlaceholder" />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.templateId')" :help="inheritedHint(cubeDefaults?.template_id)">
          <t-input v-model="cube.template_id" :placeholder="cubeDefaults?.template_id" @input="invalidateCheck" />
        </t-form-item>
        <div class="timeout-row">
          <t-form-item :label="$t('settings.sandbox.httpTimeout')" :help="inheritedHint(cubeDefaults?.http_timeout_sec)">
            <t-input-number v-model="cube.http_timeout_sec" :min="0" theme="column"
              :placeholder="String(cubeDefaults?.http_timeout_sec || '')" />
          </t-form-item>
          <t-form-item :label="$t('settings.sandbox.sandboxTtl')" :help="inheritedHint(cubeDefaults?.sandbox_ttl_seconds)">
            <t-input-number v-model="cube.cube_sandbox_ttl_seconds" :min="0" theme="column"
              :placeholder="String(cubeDefaults?.sandbox_ttl_seconds || '')" />
          </t-form-item>
          <t-form-item :label="$t('settings.sandbox.defaultTimeout')"
            :help="inheritedHint(defaults?.default_timeout_sec) || $t('settings.sandbox.defaultTimeoutHint')">
            <t-input-number v-model="defaultTimeoutSec" :min="0" theme="column"
              :placeholder="String(defaults?.default_timeout_sec || '')" />
          </t-form-item>
        </div>
      </template>

      <!-- E2B -->
      <template v-else-if="backend === 'e2b'">
        <t-form-item :label="$t('settings.sandbox.apiUrl')" :help="inheritedHint(e2bDefaults?.api_url)">
          <t-input v-model="e2b.api_url" :placeholder="e2bDefaults?.api_url || 'https://api.e2b.app'"
            @input="invalidateCheck" />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.sandboxDomain')" :help="inheritedHint(e2bDefaults?.sandbox_domain)">
          <t-input v-model="e2b.sandbox_domain" placeholder="e2b.app" />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.apiKey')"
          :help="inheritedHint(undefined, e2bDefaults?.api_key_configured)">
          <t-input v-model="e2b.api_key" type="password" :placeholder="secretPlaceholder" />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.templateId')" :help="inheritedHint(e2bDefaults?.template_id)">
          <t-input v-model="e2b.template_id" :placeholder="e2bDefaults?.template_id" @input="invalidateCheck" />
        </t-form-item>
        <div class="timeout-row">
          <t-form-item :label="$t('settings.sandbox.httpTimeout')" :help="inheritedHint(e2bDefaults?.http_timeout_sec)">
            <t-input-number v-model="e2b.http_timeout_sec" :min="0" theme="column"
              :placeholder="String(e2bDefaults?.http_timeout_sec || '')" />
          </t-form-item>
          <t-form-item :label="$t('settings.sandbox.sandboxTtl')" :help="inheritedHint(e2bDefaults?.sandbox_ttl_seconds)">
            <t-input-number v-model="e2b.e2b_sandbox_ttl_seconds" :min="0" theme="column"
              :placeholder="String(e2bDefaults?.sandbox_ttl_seconds || '')" />
          </t-form-item>
          <t-form-item :label="$t('settings.sandbox.defaultTimeout')"
            :help="inheritedHint(defaults?.default_timeout_sec) || $t('settings.sandbox.defaultTimeoutHint')">
            <t-input-number v-model="defaultTimeoutSec" :min="0" theme="column"
              :placeholder="String(defaults?.default_timeout_sec || '')" />
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

    <div v-if="checkResult" class="check-result">
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

    <!--
      The backend refuses identity changes while the config still owns sandboxes.
      It hands back what it counted, so the admin sees the scale of the problem
      and the two ways out - there is deliberately no release button, because
      releasing behind the admin's back would destroy live conversations.
    -->
    <div v-if="conflict" class="blocked">
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
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
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
const nameError = ref('')

const name = ref('')
const description = ref('')
const backend = ref('')
const defaultTimeoutSec = ref<number>(0)
const cube = reactive<SandboxCubeConfig>({})
const e2b = reactive<SandboxE2BConfig>({})
const envRows = ref<{ key: string; value: string }[]>([])

// Only remote backends have a control plane worth probing.
const isRemoteBackend = computed(() => backend.value === 'cube' || backend.value === 'e2b')

const backendLabel = (value: string) => t(`settings.sandbox.backends.${value}`)
const checkLabel = (probe: string) => t(`settings.sandbox.checks.${probe}`, probe)

const cubeDefaults = computed(() => props.defaults?.cube)
const e2bDefaults = computed(() => props.defaults?.e2b)

// Inherited values are shown as placeholders so an empty field reads as
// "inherits X" rather than "unset".
const inheritedHint = (value?: string | number, configured?: boolean) => {
  if (configured) return t('settings.sandbox.inheritedSecret')
  if (value === undefined || value === '' || value === 0) return ''
  return t('settings.sandbox.inheritedValue', { value: String(value) })
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
  defaultTimeoutSec.value = cfg.default_timeout_sec || 0
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
      return
    }
    MessagePlugin.error(e?.message || t('settings.sandbox.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function runCheck(deep: boolean) {
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

.blocked p {
  margin: 4px 0 0;
}
</style>
