<template>
  <div class="plugin-service">
    <h2 class="section-title">{{ t('settings.pluginService') }}</h2>
    <div class="desc-row">
      <p class="section-desc">{{ t('settings.pluginServiceDesc') }}</p>
      <div class="rescan-actions">
        <t-tooltip :content="pendingTooltip" placement="top">
          <span class="pending-dot" :class="hasPending ? 'is-pending' : 'is-ok'"></span>
        </t-tooltip>
        <t-button
          :loading="rescanning"
          @click="onRescan"
          class="rescan-btn"
        >
          <template #icon><t-icon name="refresh" /></template>
          {{ t('settings.refreshPlugins') }}
        </t-button>
      </div>
    </div>

    <div v-if="plugins.length" class="plugin-grid">
      <div v-for="p in plugins" :key="p.id" class="plugin-card">
        <div class="plugin-card__head">
          <span class="plugin-card__name" :title="p.id">{{ p.name }}</span>
          <div class="plugin-card__head-actions">
            <t-tooltip :content="t('settings.pluginRestart')" placement="top">
              <t-button
                theme="default"
                variant="outline"
                shape="square"
                size="small"
                :loading="restartingId === p.id"
                :disabled="restartingId !== null && restartingId !== p.id"
                @click="onRestart(p)"
              >
                <template #icon><t-icon name="refresh" /></template>
              </t-button>
            </t-tooltip>
            <span class="plugin-card__badge" :class="`state--${p.state}`">{{ stateLabel(p.state) }}</span>
          </div>
        </div>
        <div class="plugin-card__meta">
          <span class="plugin-card__id" :title="p.id">{{ p.id }}</span>
          <span class="plugin-card__version">v{{ p.version }}</span>
        </div>
        <div class="plugin-card__trust">
          <span class="plugin-card__trust-label">{{ t('settings.pluginTrustLevel') }}</span>
          <t-select
            :value="p.trust_level"
            :options="trustOptions(p)"
            size="small"
            class="plugin-card__select"
            @change="(v: any) => onTrustChange(p, v)"
          />
        </div>
      </div>
    </div>
    <div v-else-if="!loading" class="plugin-empty">
      {{ t('settings.pluginEmpty') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  rescanPlugins,
  listPlugins,
  setPluginTrust,
  restartPlugin,
  type RescanReport,
  type PluginListEntry,
  type PluginTrustLevel,
} from '@/api/plugin'

const { t } = useI18n()
const rescanning = ref(false)
const loading = ref(false)
const restartingId = ref<string | null>(null)
const plugins = ref<PluginListEntry[]>([])

const hasPending = computed(() => plugins.value.some((p) => p.pending))
const pendingNames = computed(() => plugins.value.filter((p) => p.pending).map((p) => p.name || p.id))
const pendingTooltip = computed(() =>
  hasPending.value
    ? t('settings.pluginPendingTip', { names: pendingNames.value.join('、') })
    : t('settings.pluginAllEffective'),
)

function summarize(report: RescanReport): string {
  const lines: string[] = []
  if (report.added?.length) lines.push(t('settings.rescanSummaryAdded', { count: report.added.length }) + `: ${report.added.join(', ')}`)
  if (report.skipped?.length) lines.push(t('settings.rescanSummarySkipped', { count: report.skipped.length }) + `: ${report.skipped.join(', ')}`)
  if (report.changed?.length) lines.push(t('settings.rescanSummaryChanged', { count: report.changed.length }) + `: ${report.changed.join(', ')}`)
  if (report.errors?.length) lines.push(t('settings.rescanSummaryErrors', { count: report.errors.length }) + `:\n${report.errors.join('\n')}`)
  if (!lines.length) return t('settings.rescanSummaryEmpty')
  return lines.join('\n')
}

function trustOptions(p: PluginListEntry) {
  const isOCI = p.entrypoint.startsWith('docker://')
  return [
    { label: t('settings.pluginTrustOffline'), value: 'offline' },
    { label: t('settings.pluginTrustTrusted'), value: 'trusted' },
    { label: t('settings.pluginTrustIsolated'), value: 'isolated', disabled: !isOCI },
  ]
}

function stateLabel(state: string): string {
  const key = `settings.pluginState_${state}`
  const label = t(key)
  return label === key ? state : label
}

async function loadPlugins() {
  loading.value = true
  try {
    plugins.value = await listPlugins()
  } catch (e: any) {
    plugins.value = []
    MessagePlugin.error(e?.message || t('settings.refreshPluginsFailed'))
  } finally {
    loading.value = false
  }
}

async function onRescan() {
  if (rescanning.value) return
  rescanning.value = true
  try {
    const report = await rescanPlugins()
    MessagePlugin.success(summarize(report))
    await loadPlugins()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.refreshPluginsFailed'))
  } finally {
    rescanning.value = false
  }
}

async function onTrustChange(p: PluginListEntry, level: PluginTrustLevel) {
  try {
    await setPluginTrust(p.id, level)
    // Optimistic update: the select reflects the new level immediately and the
    // pending dot turns amber, because the saved trust differs from the level
    // the plugin was loaded with until the next rescan applies it.
    p.trust_level = level
    p.pending = true
    MessagePlugin.success(t('settings.pluginTrustSaved'))
    // Re-sync from the backend so trust_level and pending reflect the
    // authoritative server-side values.
    await loadPlugins()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.pluginTrustSaveFailed'))
  }
}

function onRestart(p: PluginListEntry) {
  const dialog = DialogPlugin.confirm({
    header: t('settings.pluginRestartConfirmTitle'),
    body: t('settings.pluginRestartConfirmBody', { name: p.name || p.id }),
    confirmBtn: { content: t('settings.pluginRestart'), theme: 'warning' },
    onConfirm: async () => {
      dialog.destroy()
      restartingId.value = p.id
      try {
        await restartPlugin(p.id)
        MessagePlugin.success(t('settings.pluginRestarted', { name: p.name || p.id }))
      } catch (e: any) {
        MessagePlugin.error(e?.message || t('settings.pluginRestartFailed'))
      } finally {
        restartingId.value = null
        await loadPlugins()
      }
    },
    onCancel: () => dialog.destroy(),
  })
}

onMounted(loadPlugins)
</script>

<style lang="less" scoped>
.plugin-service {
  .section-title {
    font-size: 18px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }
  .desc-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 24px;
  }
  .section-desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    line-height: 1.6;
    margin: 0;
    max-width: 520px;
  }
  .rescan-actions {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-shrink: 0;
  }
  .pending-dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    display: inline-block;
    cursor: default;
    &.is-ok {
      background: var(--td-success-color);
    }
    &.is-pending {
      background: var(--td-warning-color);
    }
  }
  .rescan-btn {
    min-width: 160px;
    flex-shrink: 0;
  }
  .plugin-grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 16px;
    margin-top: 24px;
  }
  .plugin-card {
    border: 1px solid var(--td-component-stroke);
    border-radius: 8px;
    padding: 16px;
    background: var(--td-bg-color-container);
    transition: border-color 0.2s;
    &:hover {
      border-color: var(--td-brand-color);
    }
  }
  .plugin-card__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .plugin-card__head-actions {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
  }
  .plugin-card__name {
    font-size: 15px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .plugin-card__badge {
    flex-shrink: 0;
    font-size: 12px;
    padding: 2px 8px;
    border-radius: 10px;
    &.state--running {
      color: var(--td-success-color);
      background: var(--td-success-color-1);
    }
    &.state--failed, &.state--unhealthy {
      color: var(--td-error-color);
      background: var(--td-error-color-1);
    }
    &.state--stopped, &.state--degraded, &.state--discovered, &.state--starting, &.state--draining {
      color: var(--td-text-color-secondary);
      background: var(--td-bg-color-component);
    }
  }
  .plugin-card__meta {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 6px;
  }
  .plugin-card__id {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .plugin-card__version {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    flex-shrink: 0;
  }
  .plugin-card__trust {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-top: 14px;
    padding-top: 12px;
    border-top: 1px solid var(--td-component-stroke);
  }
  .plugin-card__trust-label {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    flex-shrink: 0;
  }
  .plugin-card__select {
    min-width: 180px;
  }
  .plugin-empty {
    margin-top: 24px;
    padding: 32px;
    text-align: center;
    font-size: 13px;
    color: var(--td-text-color-placeholder);
    border: 1px dashed var(--td-component-stroke);
    border-radius: 8px;
  }
}
</style>
