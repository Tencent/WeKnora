<template>
  <section class="workbench-records">
    <div class="workbench-toolbar">
      <span class="workbench-ellipsis">{{ t('workbench.audit') }}</span>
      <t-button variant="text" shape="square" size="small" :loading="loading" :title="t('workbench.refresh')" :aria-label="t('workbench.refresh')" @click="refresh">
        <template #icon><t-icon name="refresh" /></template>
      </t-button>
    </div>
    <div v-if="error" class="workbench-error" role="alert">{{ error }}</div>
    <div v-if="loading" class="workbench-empty"><t-loading size="small" /></div>
    <div v-else-if="!rows.length && !error" class="workbench-empty">{{ t('workbench.noAudit') }}</div>
    <ul v-else class="workbench-list">
      <li v-for="row in rows" :key="row.id" class="audit-row">
        <div class="audit-heading"><span>{{ row.action }}</span><t-tag size="small" variant="light" :theme="row.outcome === 'denied' || row.outcome === 'failed' ? 'danger' : 'default'">{{ row.outcome }}</t-tag></div>
        <pre v-if="typeof auditDetails(row).command === 'string'">{{ auditDetails(row).command }}</pre>
        <div class="audit-meta">
          <time :datetime="row.created_at">{{ formatDate(row.created_at) }}</time>
          <span v-if="auditDetails(row).duration_ms != null">{{ auditDetails(row).duration_ms }} ms</span>
          <span v-if="auditDetails(row).exit_code != null">{{ t('workbench.exitCode', { code: auditDetails(row).exit_code }) }}</span>
        </div>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { WorkbenchApi } from '@/api/sandbox-workbench'
import type { AuditLog } from '@/api/tenant/audit-log'
import { auditDetails, workbenchError } from '@/utils/sandboxWorkbench'

const props = defineProps<{ api: WorkbenchApi; signal: AbortSignal; active: boolean; revision: number }>()
const { t, locale } = useI18n()
const rows = ref<AuditLog[]>([])
const loading = ref(false)
const error = ref('')
let request = 0
let alive = true
const current = () => alive && !props.signal.aborted
function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale.value)
}
async function refresh() {
  const version = ++request
  loading.value = true
  error.value = ''
  try {
    const result = await props.api.audit()
    if (current() && version === request) rows.value = result
  } catch (value) { if (current() && version === request) error.value = workbenchError(value) || t('workbench.requestFailed') }
  finally { if (current() && version === request) loading.value = false }
}
watch(() => [props.active, props.revision], () => { if (props.active) void refresh() }, { immediate: true })
onBeforeUnmount(() => { alive = false; ++request })
</script>

<style scoped lang="less">
.audit-row { padding: 12px; border-bottom: 1px solid var(--td-component-stroke); }
.audit-heading { display: flex; justify-content: space-between; gap: 8px; overflow-wrap: anywhere; }
.audit-row pre { white-space: pre-wrap; overflow-wrap: anywhere; font: 12px/1.5 ui-monospace, monospace; margin: 8px 0; }
.audit-meta { display: flex; flex-wrap: wrap; gap: 8px; font-size: 12px; color: var(--td-text-color-placeholder); margin-top: 6px; }
</style>
