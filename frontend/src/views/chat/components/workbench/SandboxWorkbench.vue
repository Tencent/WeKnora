<template>
  <t-drawer
    :visible="true" class="sandbox-workbench" :size="overlay ? 'min(100vw, 640px)' : 'min(44vw, 600px)'"
    :show-overlay="overlay" :footer="false" :z-index="1400" attach="body" :close-on-esc-keydown="true"
    :close-on-overlay-click="true" @close="close"
  >
    <template #header>
      <div class="workbench-heading" data-sandbox-workbench><t-icon name="terminal" /><span>{{ t('workbench.title') }}</span></div>
      <t-button variant="text" shape="square" size="small" :title="t('common.close')" :aria-label="t('common.close')" @click="close">
        <template #icon><t-icon name="close" /></template>
      </t-button>
    </template>
    <div class="workbench-body" data-sandbox-workbench @dragover.prevent.stop @drop.prevent.stop>
      <div class="workbench-status">
        <t-loading v-if="loading" size="small" />
        <span v-else role="status">{{ stateLabel }}</span>
        <span v-if="status?.provider" class="workbench-provider">{{ status.provider }}</span>
        <t-button variant="text" shape="square" size="small" :loading="loading" :title="t('workbench.refresh')" :aria-label="t('workbench.refresh')" @click="refreshStatus">
          <template #icon><t-icon name="refresh" /></template>
        </t-button>
      </div>
      <div v-if="statusError" class="workbench-error" role="alert">{{ statusError }}</div>
      <div v-else-if="statusReason" class="workbench-error" role="status">{{ statusReason }}</div>
      <form v-if="status?.state === 'unbound'" class="workbench-binding" @submit.prevent="bind">
        <t-select v-model="configId" :placeholder="t('workbench.selectConfig')" :aria-label="t('workbench.selectConfig')" :loading="configsLoading" :disabled="binding || policyDisabled" :options="configOptions" />
        <t-button type="submit" :loading="binding" :disabled="!configId || policyDisabled">{{ t('workbench.bind') }}</t-button>
        <span v-if="!configsLoading && !configs.length" class="binding-note">{{ t('workbench.noConfigs') }}</span>
        <span v-if="policyDisabled" class="binding-note">{{ t('workbench.states.policy_denied') }}</span>
        <span v-if="configError" class="workbench-error" role="alert">{{ configError }}</span>
      </form>
      <details v-if="status?.limits && status.config_id" class="workbench-limits">
        <summary>{{ t('workbench.limits') }}</summary>
        <dl>
          <dt>{{ t('workbench.commandTimeout') }}</dt><dd>{{ status.limits.command_timeout_seconds }} s</dd>
          <dt>{{ t('workbench.sessionTimeout') }}</dt><dd>{{ status.limits.session_timeout_seconds }} s</dd>
          <dt>{{ t('workbench.cpuLimit') }}</dt><dd>{{ status.limits.cpu_seconds }} s</dd>
          <dt>{{ t('workbench.memoryLimit') }}</dt><dd>{{ formatWorkbenchBytes(status.limits.memory_bytes) }}</dd>
          <dt>{{ t('workbench.fileLimit') }}</dt><dd>{{ formatWorkbenchBytes(maxBytes) }}</dd>
        </dl>
      </details>
      <t-tabs v-model="tab" class="workbench-tabs">
        <t-tab-panel value="terminal" :label="t('workbench.terminal')" />
        <t-tab-panel value="files" :label="t('workbench.files')" />
        <t-tab-panel value="artifacts" :label="t('workbench.artifacts')" />
        <t-tab-panel value="audit" :label="t('workbench.audit')" />
      </t-tabs>
      <WorkbenchTerminal
        v-if="canUseTerminal" v-show="tab === 'terminal'" :api="api" :signal="scope.signal"
        :active="tab === 'terminal'" @changed="onTerminalChanged"
      />
      <div v-else-if="tab === 'terminal' && !loading" class="workbench-empty">{{ capabilityLabel('terminal') }}</div>
      <WorkbenchFiles
        v-if="canUseFiles" v-show="tab === 'files'" :api="api" :signal="scope.signal"
        :active="tab === 'files'" :revision="fileRevision" :max-bytes="maxBytes"
      />
      <div v-else-if="tab === 'files' && !loading" class="workbench-empty">{{ capabilityLabel('files') }}</div>
      <WorkbenchArtifacts :session-id="sessionId" :signal="scope.signal" :active="tab === 'artifacts'" :revision="recordRevision" :max-bytes="maxBytes" v-show="tab === 'artifacts'" />
      <WorkbenchAudit :api="api" :signal="scope.signal" :active="tab === 'audit'" :revision="recordRevision" v-show="tab === 'audit'" />
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { createWorkbenchApi, type WorkbenchStatus } from '@/api/sandbox-workbench'
import { listSandboxConfigs, type SandboxConfigRecord } from '@/api/system'
import { get, post, patch, del, postUpload } from '@/utils/request'
import { formatWorkbenchBytes, workbenchError, workbenchFileLimit } from '@/utils/sandboxWorkbench'
import WorkbenchTerminal from './WorkbenchTerminal.vue'
import WorkbenchFiles from './WorkbenchFiles.vue'
import WorkbenchArtifacts from './WorkbenchArtifacts.vue'
import WorkbenchAudit from './WorkbenchAudit.vue'

const props = defineProps<{ sessionId: string }>()
const emit = defineEmits<{ close: [] }>()
const { t, te } = useI18n()
const auth = useAuthStore()
const route = useRoute()
const scope = new AbortController()
const api = createWorkbenchApi(props.sessionId, { get, post, patch, del, postUpload }, scope.signal)
const status = shallowRef<WorkbenchStatus>()
const loading = ref(false)
const statusError = ref('')
const configs = ref<SandboxConfigRecord[]>([])
const configId = ref('')
const configError = ref('')
const configsLoading = ref(false)
const policyDisabled = ref(false)
const binding = ref(false)
const tab = ref('terminal')
const fileRevision = ref(0)
const recordRevision = ref(0)
const media = window.matchMedia('(max-width: 1199px)')
const overlay = ref(media.matches)
const canUseTerminal = computed(() => !!status.value?.available && !!status.value.config_id && status.value.capabilities.terminal)
const canUseFiles = computed(() => !!status.value?.available && !!status.value.config_id && status.value.capabilities.files)
const maxBytes = computed(() => workbenchFileLimit(status.value?.limits.max_file_bytes))
const configOptions = computed(() => configs.value.map(config => ({ value: config.id, label: `${config.name} (${config.sandbox_type})` })))
const stateLabel = computed(() => {
  if (!status.value) return t('workbench.states.unavailable')
  const key = `workbench.states.${status.value.state}`
  return te(key) ? t(key) : status.value.state
})
const statusReason = computed(() => {
  const reason = status.value?.reason
  if (!reason) return ''
  const key = `workbench.states.${reason}`
  return te(key) ? t(key) : reason
})
let statusRequest = 0

function capabilityLabel(capability: 'terminal' | 'files') {
  return status.value?.available && status.value.config_id
    ? t(capability === 'terminal' ? 'workbench.terminalUnavailable' : 'workbench.filesUnavailable')
    : stateLabel.value
}

function dispose() { scope.abort() }
function close() { dispose(); emit('close') }
defineExpose({ dispose })

async function loadConfigs() {
  configsLoading.value = true
  configError.value = ''
  try {
    const result = await listSandboxConfigs({ signal: scope.signal })
    if (scope.signal.aborted) return
    configs.value = result.data || []
    policyDisabled.value = result.workspace_scripts_disabled === true
  } catch (error) { if (!scope.signal.aborted) configError.value = workbenchError(error) || t('workbench.requestFailed') }
  finally { if (!scope.signal.aborted) configsLoading.value = false }
}

async function refreshStatus() {
  if (scope.signal.aborted || binding.value) return
  const request = ++statusRequest
  loading.value = true
  statusError.value = ''
  try {
    const result = await api.status()
    if (scope.signal.aborted || request !== statusRequest) return
    status.value = result
    if (result.state === 'unbound') await loadConfigs()
  } catch (error) {
    if (!scope.signal.aborted && request === statusRequest) {
      status.value = undefined
      statusError.value = workbenchError(error) || t('workbench.requestFailed')
    }
  } finally { if (!scope.signal.aborted && request === statusRequest) loading.value = false }
}

async function bind() {
  if (!configId.value || binding.value || policyDisabled.value || scope.signal.aborted) return
  ++statusRequest
  binding.value = true
  configError.value = ''
  try {
    const result = await api.bind(configId.value)
    if (!scope.signal.aborted) { status.value = result; fileRevision.value++; recordRevision.value++ }
  } catch (error) { if (!scope.signal.aborted) configError.value = workbenchError(error) || t('workbench.requestFailed') }
  finally { if (!scope.signal.aborted) { binding.value = false; loading.value = false } }
}

function onTerminalChanged() {
  fileRevision.value++
  recordRevision.value++
}

function onMediaChange() { overlay.value = media.matches }
function onStorage(event: StorageEvent) {
  if (!event.key || ['weknora_token', 'weknora_user', 'weknora_tenant', 'weknora_selected_tenant_id'].includes(event.key)) close()
}
// Synchronous invalidation also closes a socket while a ticket request is in flight.
watch([() => props.sessionId, () => route.fullPath, () => auth.user?.id, () => auth.effectiveTenantId, () => auth.isLoggedIn], close, { flush: 'sync' })
onMounted(() => {
  media.addEventListener('change', onMediaChange)
  window.addEventListener('storage', onStorage)
  window.addEventListener('pagehide', close)
  void refreshStatus()
})
onBeforeUnmount(() => {
  dispose()
  media.removeEventListener('change', onMediaChange)
  window.removeEventListener('storage', onStorage)
  window.removeEventListener('pagehide', close)
})
</script>

<style lang="less">
.sandbox-workbench.t-drawer {
  .t-drawer__content-wrapper { display: flex; flex-direction: column; }
  .t-drawer__header { padding: 14px 16px; border-bottom: 1px solid var(--td-component-stroke); flex-shrink: 0; }
  .t-drawer__body { display: flex; flex-direction: column; padding: 0; min-height: 0; overflow: hidden; }
}
.sandbox-workbench {
  .workbench-body { display: flex; flex: 1; flex-direction: column; min-height: 0; min-width: 0; font-size: 13px; }
  .workbench-heading { display: flex; flex: 1; min-width: 0; gap: 8px; align-items: center; font-size: 15px; }
  .workbench-status { display: flex; align-items: center; gap: 8px; padding: 8px 12px; overflow-wrap: anywhere; }
  .workbench-status > .t-button { margin-left: auto; }
  .workbench-provider { color: var(--td-text-color-placeholder); }
  .workbench-binding { display: flex; flex-wrap: wrap; gap: 8px; padding: 8px 12px 12px; }
  .workbench-binding .t-select__wrap { flex: 1; min-width: 160px; }
  .binding-note { width: 100%; color: var(--td-text-color-secondary); }
  .workbench-limits { padding: 0 12px 8px; color: var(--td-text-color-secondary); font-size: 12px; }
  .workbench-limits summary { cursor: pointer; }
  .workbench-limits dl { display: grid; grid-template-columns: 1fr auto; gap: 4px 12px; }
  .workbench-limits dd { margin: 0; }
  .workbench-tabs { flex-shrink: 0; }
  .workbench-tabs .t-tabs__nav-item { padding: 0 12px; font-size: 13px; }
  .workbench-tabs .t-tabs__content { display: none; }
  @media (max-width: 480px) {
    .workbench-tabs .t-tabs__nav-item { padding: 0 8px; }
  }
  .workbench-toolbar { display: flex; align-items: center; gap: 8px; padding: 8px 12px; min-width: 0; flex-shrink: 0; }
  .workbench-ellipsis { min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .workbench-error { padding: 8px 12px; color: var(--td-error-color); overflow-wrap: anywhere; font-size: 13px; }
  .workbench-empty { padding: 32px 16px; color: var(--td-text-color-placeholder); text-align: center; }
  .workbench-list { list-style: none; margin: 0; padding: 0; flex: 1; min-height: 0; overflow: auto; }
  .workbench-records { display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0; }
  .document-preview { flex: 1; min-height: 0; }
  .document-preview .code-preview, .document-preview .html-iframe { border: 0; border-radius: 0; }
}
</style>
