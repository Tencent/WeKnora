<template>
  <section class="setting-drawer__section mcp-settings-group mcp-metadata">
    <div class="section-title-block">
      <div class="metadata-heading">
        <h4 class="setting-drawer__section-title">{{ t('mcpMetadata.tools') }}</h4>
        <t-button
          v-if="!busy || snapshot"
          variant="text"
          size="small"
          theme="primary"
          :loading="refreshing"
          :disabled="loading || disabled || policyBusy"
          @click="refresh"
        >
          <template #icon><t-icon name="refresh" /></template>
          {{ t(snapshot ? 'mcpMetadata.refresh' : 'mcpMetadata.fetch') }}
        </t-button>
      </div>
      <p class="form-desc">{{ t('mcpMetadata.cacheHint') }}</p>
    </div>
    <t-loading :loading="busy" size="small" :text="busy ? t('mcpMetadata.fetching') : ''">
      <div class="metadata-body">
        <p v-if="error" class="form-desc form-desc--error">{{ error }}</p>
        <p v-else-if="snapshot?.stale" class="form-desc form-desc--warn">{{ t('mcpMetadata.stale') }}</p>
        <p v-else-if="!busy && !snapshot" class="form-desc">{{ t('mcpMetadata.notSynced') }}</p>
        <template v-if="snapshot">
          <p class="snapshot-meta">
            <span>{{ t('mcpMetadata.toolCount', { count: snapshot.tools.length }) }}</span>
            <span v-if="snapshot.server_name">{{ snapshot.server_name }} {{ snapshot.server_version }}</span>
            <span>{{ t('mcpMetadata.syncedAt') }} {{ formatTime(snapshot.synced_at) }}</span>
          </p>
          <details v-if="snapshot.instructions || snapshot.server_description" class="server-documentation">
            <summary>{{ t('mcpMetadata.serverDocumentation') }}</summary>
            <p v-if="snapshot.server_description">{{ snapshot.server_description }}</p>
            <pre v-if="snapshot.instructions">{{ snapshot.instructions }}</pre>
          </details>
          <p class="form-desc">{{ t('mcpMetadata.policyHint') }}</p>
          <McpToolsList :tools="snapshot.tools" :service-id="snapshot.stale ? undefined : serviceId" @busy="onPolicyBusy" />
        </template>
      </div>
    </t-loading>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getMCPMetadata, refreshMCPMetadata, type MCPMetadata } from '@/api/mcp-service'
import McpToolsList from './McpToolsList.vue'

const props = defineProps<{ serviceId: string; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'busy', value: boolean): void; (e: 'synced', value: boolean): void }>()
const { t } = useI18n()
const snapshot = ref<MCPMetadata | null>(null)
const loading = ref(false)
const refreshing = ref(false)
const policyBusy = ref(false)
const error = ref('')
let generation = 0
const busy = computed(() => loading.value || (refreshing.value && !snapshot.value))

function onPolicyBusy(busyPolicy: boolean) {
  policyBusy.value = busyPolicy
  emit('busy', busyPolicy || refreshing.value)
}

function setSnapshot(saved: MCPMetadata | null) {
  snapshot.value = saved
  emit('synced', !!(saved && !saved.stale))
}

function errorText(e: any) {
  return e?.response?.data?.error?.message || e?.message || t('mcpMetadata.failed')
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

async function refreshFrom(current: number) {
  if (refreshing.value || props.disabled || policyBusy.value) return
  refreshing.value = true
  error.value = ''
  emit('busy', true)
  try {
    const saved = await refreshMCPMetadata(props.serviceId)
    if (current === generation) setSnapshot(saved)
  } catch (e) {
    if (current === generation) error.value = errorText(e)
  } finally {
    if (current === generation) {
      refreshing.value = false
      emit('busy', policyBusy.value)
    }
  }
}

watch(() => props.serviceId, async id => {
  const current = ++generation
  setSnapshot(null)
  error.value = ''
  refreshing.value = false
  emit('busy', false)
  if (!id) return
  loading.value = true
  try {
    const saved = await getMCPMetadata(id)
    if (current !== generation) return
    if (!saved && !props.disabled) {
      loading.value = false
      await refreshFrom(current)
      return
    }
    setSnapshot(saved)
  } catch (e) {
    if (current === generation) error.value = errorText(e)
  } finally {
    if (current === generation) loading.value = false
  }
}, { immediate: true })

function refresh() {
  void refreshFrom(generation)
}

onBeforeUnmount(() => { generation++; emit('busy', false); emit('synced', false) })
</script>

<style scoped lang="less">
.metadata-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.metadata-heading h4 {
  margin: 0;
}

.form-desc {
  margin: 5px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);

  &--error {
    color: var(--td-error-color);
  }

  &--warn {
    color: var(--td-warning-color);
  }
}

.metadata-body {
  min-height: 48px;
}

.snapshot-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 12px;
  margin: 10px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.server-documentation {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.server-documentation summary {
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.server-documentation pre,
.server-documentation p {
  margin: 8px 0 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: inherit;
  max-height: 260px;
  overflow: auto;
}
</style>
