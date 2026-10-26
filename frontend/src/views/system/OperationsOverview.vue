<template>
  <section class="operations" :aria-label="t('system.globalSettings.operations.title')">
    <header class="operations-header">
      <div>
        <span class="eyebrow">{{ t('system.globalSettings.operations.eyebrow') }}</span>
        <h3>{{ t('system.globalSettings.operations.title') }}</h3>
      </div>
      <span class="sample" :class="{ warning: status?.stale }">{{ sampleLabel }}</span>
    </header>
    <div class="capacity-grid">
      <article class="capacity primary">
        <span>{{ t('system.globalSettings.operations.im') }}</span>
        <strong>{{ im?.available ? `${im.active} / ${im.workers}` : '—' }}</strong>
        <p>{{ t('system.globalSettings.operations.processing') }} · {{ t('system.globalSettings.operations.waiting') }} {{ im?.available ? `${im.pending} / ${im.queue_limit}` : '—' }}</p>
        <p>{{ t('system.globalSettings.operations.instanceScope') }}</p>
      </article>
      <article class="capacity">
        <span>{{ t('system.globalSettings.operations.memory') }}</span>
        <strong>{{ bytes(status?.host?.memory_available_bytes) }}</strong>
        <p>{{ t('system.globalSettings.operations.total') }} {{ bytes(status?.host?.memory_total_bytes) }}</p>
        <p>{{ t('system.globalSettings.operations.cpu') }} {{ status?.host?.cpu_count ?? '—' }} · {{ t('system.globalSettings.operations.load') }} {{ status?.host?.load_1m?.toFixed(2) ?? '—' }}</p>
      </article>
      <article v-for="disk in status?.disks || []" :key="disk.name" class="capacity" :class="{ warning: disk.available && (disk.used_percent ?? 0) >= 80 }">
        <span>{{ disk.name }}</span>
        <strong>{{ disk.available && disk.used_percent != null ? `${disk.used_percent}%` : '—' }}</strong>
        <div class="meter" aria-hidden="true"><i :style="{ width: `${Math.max(0, Math.min(100, disk.used_percent ?? 0))}%` }" /></div>
        <p>{{ t('system.globalSettings.operations.available') }} {{ bytes(disk.available_bytes) }}</p>
      </article>
    </div>
    <p v-if="!status?.available" class="notice" role="status">{{ t('system.globalSettings.operations.notConnected') }}</p>
    <p v-else-if="status.stale" class="notice warning" role="status">{{ t('system.globalSettings.operations.stale') }}</p>
    <div v-if="status?.available" class="details-grid">
      <section v-if="status.backups?.length">
        <h4>{{ t('system.globalSettings.operations.backups') }}</h4>
        <div v-for="backup in status.backups" :key="backup.name" class="detail-row">
          <div>
            <b>{{ backup.name }}</b>
            <small>{{ t('system.globalSettings.operations.snapshots') }} {{ backup.snapshot_count }} · {{ date(backup.completed_at) }}</small>
          </div>
          <span class="badge" :class="{ warning: !backup.available || backup.stale }">
            {{ !backup.available ? t('system.globalSettings.operations.unknown') : backup.stale ? t('system.globalSettings.operations.overdue') : t('system.globalSettings.operations.recent') }}
          </span>
        </div>
      </section>
      <section v-if="status.logs?.length">
        <h4>{{ t('system.globalSettings.operations.logs') }}</h4>
        <div v-for="log in status.logs" :key="log.name" class="detail-row">
          <div>
            <b>{{ log.name }}</b>
            <small>{{ log.max_size ? `${log.max_size} × ${log.max_files || '—'}` : t('system.globalSettings.operations.retentionUnknown') }}</small>
          </div>
          <div class="log-values">
            <span>{{ bytes(log.bytes) }}</span>
            <span class="badge" :class="{ warning: logState(log).warning }">{{ logState(log).label }}</span>
          </div>
        </div>
      </section>
      <section v-if="status.services?.length" class="service-section">
        <h4>{{ t('system.globalSettings.operations.services') }}</h4>
        <div class="service-grid">
          <div v-for="service in status.services" :key="service.name" class="detail-row">
            <b>{{ service.name.replace(/\.service$/, '') }}</b>
            <span class="badge" :class="{ warning: serviceState(service).warning }">{{ serviceState(service).label }}</span>
          </div>
        </div>
      </section>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OperationsStatus, IMRuntimeStatus } from '@/api/system'

const props = defineProps<{ status?: OperationsStatus; im?: IMRuntimeStatus }>()
const { t, locale } = useI18n()
const sampleLabel = computed(() => props.status?.available
  ? date(props.status.collected_at)
  : t('system.globalSettings.operations.notUpdated'))

function bytes(value?: number | null): string {
  if (value == null || !Number.isFinite(value)) return '—'
  if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(1)} GiB`
  return `${(value / 1024 ** 2).toFixed(1)} MiB`
}

function date(value?: number): string {
  return value ? new Date(value * 1000).toLocaleString(locale.value, { hour12: false }) : '—'
}

function logState(log: NonNullable<OperationsStatus['logs']>[number]) {
  if (!log.status) return { label: t('system.globalSettings.operations.unknown'), warning: true }
  const warning = log.status !== 'running' || log.health === 'unhealthy'
  return { label: warning ? t('system.globalSettings.operations.failed') : t('system.globalSettings.operations.running'), warning }
}

function serviceState(service: NonNullable<OperationsStatus['services']>[number]) {
  if (!service.available) return { label: t('system.globalSettings.operations.unknown'), warning: true }
  const warning = service.result !== 'success' || service.exit_status !== 0 || (service.running_required && service.active !== 'active')
  return { label: warning ? t('system.globalSettings.operations.failed') : t('system.globalSettings.operations.normal'), warning }
}
</script>

<style scoped lang="less">
.operations {
  margin: 24px 0;
  padding: 24px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}
.operations-header { display: flex; justify-content: space-between; align-items: center; gap: 16px; margin-bottom: 20px; }
.eyebrow { font-size: var(--app-text-2xs); letter-spacing: .16em; color: var(--td-text-color-secondary); font-weight: 700; }
h3 { margin: 6px 0 0; font-size: var(--app-text-3xl); font-weight: 600; }
h4 { margin: 0 0 12px; font-size: var(--app-text-base); }
.sample { font-size: var(--app-text-sm); color: var(--td-text-color-secondary); font-variant-numeric: tabular-nums; }
.capacity-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; }
.capacity { padding: 18px; background: var(--td-bg-color-container); border: 1px solid var(--td-component-stroke); border-radius: var(--app-radius-md); }
.capacity > span { font-size: var(--app-text-sm); color: var(--td-text-color-secondary); }
.capacity strong { display: block; margin: 12px 0; font-size: var(--app-text-4xl); font-weight: 600; letter-spacing: -.04em; font-variant-numeric: tabular-nums; }
.capacity p { margin: 5px 0 0; font-size: var(--app-text-xs); color: var(--td-text-color-secondary); }
.primary { background: var(--td-brand-color); border-color: var(--td-brand-color); color: var(--td-text-color-anti); }
.primary > span, .primary p { color: var(--td-text-color-anti); }
.meter { height: 4px; background: var(--td-bg-color-secondarycontainer); border-radius: var(--app-radius-pill); overflow: hidden; }
.meter i { display: block; height: 100%; background: var(--td-success-color); }
.notice { margin: 16px 0 0; font-size: var(--app-text-md); color: var(--td-text-color-secondary); }
.warning { color: var(--td-warning-color); }
.capacity.warning .meter i { background: var(--td-warning-color); }
.log-values { display: flex; align-items: center; gap: 8px; }
.details-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; margin-top: 24px; }
.details-grid > section { min-width: 0; }
.detail-row { display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 10px 0; border-top: 1px solid var(--td-component-stroke); font-size: var(--app-text-sm); }
.detail-row b { font-weight: 500; overflow-wrap: anywhere; }
.detail-row small { display: block; margin-top: 5px; color: var(--td-text-color-secondary); font-size: var(--app-text-xs); }
.badge { flex-shrink: 0; padding: 4px 7px; background: var(--td-success-color-light); border-radius: var(--app-radius-xs); color: var(--td-success-color); font-size: var(--app-text-xs); }
.badge.warning { background: var(--td-warning-color-light); color: var(--td-warning-color); }
.service-section { grid-column: 1 / -1; }
.service-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 24px; }
@media (max-width: 1000px) { .capacity-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 600px) {
  .operations { padding: 16px; }
  .operations-header { align-items: flex-start; flex-direction: column; }
  .capacity-grid, .details-grid, .service-grid { grid-template-columns: 1fr; }
  .service-section { grid-column: auto; }
}
</style>
