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
      <p v-if="snapshot" class="snapshot-meta">
        <span>{{ t('mcpMetadata.toolCount', { count: snapshot.tools.length }) }}</span>
        <span v-if="snapshot.server_name">{{ snapshot.server_name }} {{ snapshot.server_version }}</span>
        <span>{{ t('mcpMetadata.syncedAt') }} {{ formatTime(snapshot.synced_at) }}</span>
        <t-tooltip
          v-if="!hasServerDocumentation"
          :content="t('mcpMetadata.noServerDocumentation')"
          placement="top"
          show-arrow
          :overlay-inner-style="{ maxWidth: '360px', whiteSpace: 'normal' }"
        >
          <button type="button" class="snapshot-meta__help" :aria-label="t('mcpMetadata.noServerDocumentation')">
            <t-icon name="help-circle" size="16px" />
          </button>
        </t-tooltip>
        <t-popup
          v-else
          v-model:visible="docsOpen"
          trigger="click"
          placement="bottom-left"
          attach="body"
          destroy-on-close
          overlay-class-name="mcp-server-docs-popup-overlay"
          :overlay-inner-style="{ padding: 0 }"
        >
          <button
            type="button"
            class="snapshot-meta__docs"
            :aria-expanded="docsOpen"
            :aria-label="t('mcpMetadata.serverDocumentation')"
          >
            <span>{{ t('mcpMetadata.serverDocumentation') }}</span>
            <t-icon name="chevron-down" size="14px" />
          </button>
          <template #content>
            <div class="server-docs-popup" @click.stop>
              <div class="server-docs-popup__title">{{ t('mcpMetadata.serverDocumentation') }}</div>
              <p v-if="snapshot.server_description">{{ snapshot.server_description }}</p>
              <pre v-if="snapshot.instructions">{{ snapshot.instructions }}</pre>
            </div>
          </template>
        </t-popup>
      </p>
    </div>
    <t-loading :loading="busy" size="small" :text="busy ? t('mcpMetadata.fetching') : ''">
      <div class="metadata-body">
        <p v-if="error" class="form-desc form-desc--error">{{ error }}</p>
        <p v-else-if="snapshot?.stale" class="form-desc form-desc--warn">{{ t('mcpMetadata.stale') }}</p>
        <p v-else-if="!busy && !snapshot" class="form-desc">{{ t('mcpMetadata.notSynced') }}</p>
        <template v-if="snapshot">
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
const docsOpen = ref(false)
const error = ref('')
let generation = 0
const busy = computed(() => loading.value || (refreshing.value && !snapshot.value))
const hasServerDocumentation = computed(() => !!(snapshot.value?.instructions || snapshot.value?.server_description))

function onPolicyBusy(busyPolicy: boolean) {
  policyBusy.value = busyPolicy
  emit('busy', busyPolicy || refreshing.value)
}

function setSnapshot(saved: MCPMetadata | null) {
  snapshot.value = saved
  docsOpen.value = false
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
  margin: 4px 0 0;
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
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-height: 48px;
}

.metadata-body .form-desc {
  margin: 0;
}

.snapshot-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 10px;
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
}

.snapshot-meta__help,
.snapshot-meta__docs {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
}

.snapshot-meta__docs {
  font: inherit;
  font-size: 12px;
  line-height: 1.5;
}

.snapshot-meta__help {
  cursor: help;
}

.snapshot-meta__help:hover,
.snapshot-meta__help:focus-visible,
.snapshot-meta__docs:hover,
.snapshot-meta__docs:focus-visible,
.snapshot-meta__docs[aria-expanded='true'] {
  color: var(--td-brand-color);
}
</style>

<!-- t-popup attaches to body; z-index must sit above SettingDrawer (2500). -->
<style lang="less">
.mcp-server-docs-popup-overlay {
  z-index: 3100 !important;

  .t-popup__content {
    padding: 0 !important;
    width: 400px;
    max-width: calc(100vw - 24px);
    border-radius: 12px !important;
    background: var(--td-bg-color-container) !important;
    border: 0.5px solid var(--td-component-stroke) !important;
    box-shadow:
      0 0 0 0.5px rgba(0, 0, 0, 0.03),
      0 2px 4px rgba(0, 0, 0, 0.04),
      0 8px 24px rgba(0, 0, 0, 0.1) !important;
  }

  .server-docs-popup {
    padding: 14px 16px 12px;
    max-height: min(70vh, 420px);
    overflow: auto;
  }

  .server-docs-popup__title {
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .server-docs-popup p,
  .server-docs-popup pre {
    margin: 8px 0 0;
    font: inherit;
    font-size: 12px;
    line-height: 1.6;
    color: var(--td-text-color-secondary);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
}

:root[theme-mode='dark'] .mcp-server-docs-popup-overlay .t-popup__content {
  background: rgba(36, 36, 36, 0.92) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
}
</style>
