<template>
  <section class="setting-drawer__section mcp-settings-group mcp-metadata">
    <div class="metadata-heading">
      <div>
        <h4 class="setting-drawer__section-title">{{ t('mcpMetadata.tools') }} <t-tag v-if="snapshot" size="small" theme="primary" variant="light">{{ snapshot.tools.length }}</t-tag></h4>
        <p class="form-desc">{{ t('mcpMetadata.cacheHint') }}</p>
      </div>
      <t-button size="small" theme="primary" variant="outline" :loading="refreshing" :disabled="loading || disabled || policyBusy" @click="refresh">
        <template #icon><t-icon name="refresh" /></template>
        {{ t(snapshot ? 'mcpMetadata.refresh' : 'mcpMetadata.fetch') }}
      </t-button>
    </div>
    <t-loading :loading="loading" size="small">
      <t-alert v-if="error" theme="error" :message="error" />
      <t-alert v-if="snapshot?.stale" theme="warning" :message="t('mcpMetadata.stale')" />
      <t-alert v-if="!loading && !snapshot && !error" theme="info" :message="t('mcpMetadata.notSynced')" />
      <template v-if="snapshot">
        <div class="snapshot-info">
          <t-tag :theme="snapshot.stale ? 'warning' : 'success'" variant="light">
            {{ t(snapshot.stale ? 'mcpMetadata.needsRefresh' : 'mcpMetadata.saved') }}
          </t-tag>
          <span>{{ snapshot.server_name }} {{ snapshot.server_version }}</span>
          <span>{{ t('mcpMetadata.syncedAt') }} {{ new Date(snapshot.synced_at).toLocaleString() }}</span>
        </div>
        <details v-if="snapshot.instructions || snapshot.server_description" class="server-documentation">
          <summary>{{ t('mcpMetadata.serverDocumentation') }}</summary>
          <p v-if="snapshot.server_description">{{ snapshot.server_description }}</p>
          <pre v-if="snapshot.instructions">{{ snapshot.instructions }}</pre>
        </details>
        <p class="form-desc">{{ t('mcpMetadata.policyHint') }}</p>
        <McpToolsList :tools="snapshot.tools" :service-id="snapshot.stale ? undefined : serviceId" @busy="onPolicyBusy" />
      </template>
    </t-loading>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getMCPMetadata, refreshMCPMetadata, type MCPMetadata } from '@/api/mcp-service'
import McpToolsList from './McpToolsList.vue'

const props = defineProps<{ serviceId: string; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'busy', value: boolean): void }>()
const { t } = useI18n()
const snapshot = ref<MCPMetadata | null>(null)
const loading = ref(false)
const refreshing = ref(false)
const policyBusy = ref(false)
function onPolicyBusy(busy: boolean) { policyBusy.value = busy; emit('busy', busy || refreshing.value) }
const error = ref('')
let generation = 0

function errorText(e: any) {
  return e?.response?.data?.error?.message || e?.message || t('mcpMetadata.failed')
}

watch(() => props.serviceId, async id => {
  const current = ++generation
  snapshot.value = null
  error.value = ''
  refreshing.value = false
  emit('busy', false)
  if (!id) return
  loading.value = true
  try {
    const saved = await getMCPMetadata(id)
    if (current === generation) snapshot.value = saved
  } catch (e) {
    if (current === generation) error.value = errorText(e)
  } finally {
    if (current === generation) loading.value = false
  }
}, { immediate: true })

async function refresh() {
  if (refreshing.value || props.disabled || policyBusy.value) return
  const current = generation
  refreshing.value = true
  error.value = ''
  emit('busy', true)
  try {
    const saved = await refreshMCPMetadata(props.serviceId)
    if (current === generation) snapshot.value = saved
  } catch (e) {
    if (current === generation) error.value = errorText(e)
  } finally {
    if (current === generation) { refreshing.value = false; emit('busy', false) }
  }
}

onBeforeUnmount(() => { generation++; emit('busy', false) })
</script>

<style scoped lang="less">
.metadata-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.metadata-heading h4 { margin: 0; }
.form-desc { margin: 5px 0 0; font-size: 12px; line-height: 1.5; color: var(--td-text-color-placeholder); }
.snapshot-info { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; color: var(--td-text-color-secondary); font-size: 12px; margin: 0 0 12px; }
.server-documentation { margin: 0 0 12px; padding: 10px 12px; border: 1px solid var(--td-component-stroke); border-radius: 6px; background: var(--td-bg-color-secondarycontainer); font-size: 12px; line-height: 1.5; }
.server-documentation summary { cursor: pointer; font-size: 13px; font-weight: 500; color: var(--td-text-color-secondary); }
.server-documentation pre, .server-documentation p { white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; max-height: 260px; overflow: auto; }
.mcp-metadata :deep(.t-alert) { margin-top: 12px; }
</style>
