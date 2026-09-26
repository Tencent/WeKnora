<template>
  <div class="plugin-admin">
    <header class="section-header">
      <div class="section-header__row">
        <div>
          <h2>{{ t('pluginAdmin.title') }}</h2>
          <p class="section-description">{{ t('pluginAdmin.description') }}</p>
        </div>
        <t-button theme="primary" @click="installOpen = true">
          <template #icon><t-icon name="add" /></template>
          {{ t('pluginAdmin.installButton') }}
        </t-button>
      </div>
    </header>

    <div v-if="loading" class="plugin-admin__state"><t-loading size="small" /></div>
    <div v-else-if="plugins.length === 0" class="plugin-admin__state plugin-admin__state--empty">
      <t-empty :description="t('pluginAdmin.empty')" />
      <t-button variant="outline" @click="installOpen = true">
        <template #icon><t-icon name="add" /></template>
        {{ t('pluginAdmin.installButton') }}
      </t-button>
    </div>
    <div v-else class="plugin-list">
      <div
        v-for="p in plugins"
        :key="p.id"
        class="plugin-card"
        :class="{ 'plugin-card--off': p.desired_state === 'disabled' }"
        role="button"
        tabindex="0"
        @click="openDetail(p)"
        @keydown.enter="openDetail(p)"
      >
        <div class="plugin-card__badge" :class="{ 'plugin-card__badge--logo': !!pluginIconUrl(p.manifest) }">
          <img v-if="pluginIconUrl(p.manifest)" :src="pluginIconUrl(p.manifest)" alt="" class="plugin-card__badge-img" />
          <template v-else>{{ initial(p) }}</template>
        </div>
        <div class="plugin-card__body">
          <div class="plugin-card__title">
            <span class="plugin-card__name">{{ nameOf(p) }}</span>
            <t-tooltip :content="p.node?.error" :disabled="!p.node?.error">
              <t-tag size="small" variant="light" :theme="stateTheme(p)">
                {{ t(`pluginAdmin.state.${installedState(p)}`) }}
              </t-tag>
            </t-tooltip>
          </div>
          <div class="plugin-card__meta">{{ p.id }} · v{{ p.active_version }} · {{ runtimeLabel(p.runtime) }}</div>
          <div v-if="descriptionOf(p)" class="plugin-card__desc">{{ descriptionOf(p) }}</div>
          <div v-if="p.manifest" class="plugin-card__contribs">
            <span v-for="s in contributionSummary(p.manifest)" :key="s.point" class="plugin-card__contrib">
              {{ t(`pluginCenter.points.${s.point}`) }} × {{ s.count }}
            </span>
          </div>
        </div>
        <div class="plugin-card__actions" @click.stop @keydown.enter.stop>
          <t-tooltip :content="t('pluginAdmin.platformSwitch')">
            <t-switch
              :model-value="p.desired_state === 'enabled'"
              :loading="pending.has(p.id)"
              :disabled="pending.has(p.id)"
              @update:model-value="(v: boolean) => toggle(p, v)"
            />
          </t-tooltip>
        </div>
      </div>
    </div>

    <PluginInstallDrawer v-model:visible="installOpen" @installed="upsert" />
    <t-dialog
      v-model:visible="secretOpen"
      :header="t('pluginAdmin.secret.title')"
      :confirm-btn="{ content: t('pluginAdmin.secret.copy'), theme: 'primary' }"
      :cancel-btn="{ content: t('pluginAdmin.secret.done') }"
      :close-on-overlay-click="false"
      @confirm="copyWithToast(issuedSecret, 'pluginAdmin.secret.copied')"
      @closed="issuedSecret = ''"
    >
      <p>{{ t('pluginAdmin.secret.description') }}</p>
      <t-textarea :value="issuedSecret" readonly autosize />
    </t-dialog>
    <PluginDetailDrawer
      v-model:visible="detailOpen"
      :plugin="selected"
      @changed="upsert"
      @removed="remove"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { pluginIconUrl } from '@/extensions/pluginIcon'
import { MessagePlugin } from 'tdesign-vue-next'

import { listInstalledPlugins, setInstalledPluginEnabled, type InstalledPlugin } from '@/api/system/plugins'
import { copyWithToast } from '@/utils/clipboard'
import { localizedText } from '@/utils/localizedText'

import { contributionSummary } from '../settings/pluginCenterState'
import PluginDetailDrawer from './plugins/PluginDetailDrawer.vue'
import PluginInstallDrawer from './plugins/PluginInstallDrawer.vue'
import { installedState } from './pluginManagementState'

// Platform plugin management: what is installed, on which version, whether it
// loads. Installing makes a plugin available to every workspace; each
// workspace still turns it on in its own plugin center.
const { t, te, locale } = useI18n()
const runtimeLabel = (rt: string) => (te(`pluginAdmin.runtime.${rt}`) ? t(`pluginAdmin.runtime.${rt}`) : rt)

const plugins = ref<InstalledPlugin[]>([])
const loading = ref(false)
const pending = ref(new Set<string>())
const installOpen = ref(false)
const detailOpen = ref(false)
// A remote plugin's signing secret, shown once after it is issued.
const secretOpen = ref(false)
const issuedSecret = ref('')
const selectedId = ref('')
const selected = computed(() => plugins.value.find((p) => p.id === selectedId.value) ?? null)

const nameOf = (p: InstalledPlugin) => (p.manifest ? localizedText(p.manifest.name, locale.value) : p.id)
const descriptionOf = (p: InstalledPlugin) => (p.manifest ? localizedText(p.manifest.description, locale.value) : '')
const initial = (p: InstalledPlugin) => (nameOf(p).trim().charAt(0) || '?').toUpperCase()

function stateTheme(p: InstalledPlugin) {
  switch (installedState(p)) {
    case 'running':
      return 'success'
    case 'failed':
      return 'danger'
    case 'degraded':
      return 'warning'
    default:
      return 'default'
  }
}

async function load() {
  loading.value = true
  try {
    const res = await listInstalledPlugins()
    plugins.value = res.data || []
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginAdmin.loadFailed'))
  } finally {
    loading.value = false
  }
}

function upsert(received: InstalledPlugin) {
  const { issuedSecret: secret, ...p } = received
  if (secret) {
    issuedSecret.value = secret
    secretOpen.value = true
  }
  const i = plugins.value.findIndex((x) => x.id === p.id)
  if (i >= 0) plugins.value.splice(i, 1, p)
  else plugins.value = [...plugins.value, p].sort((a, b) => a.id.localeCompare(b.id))
}

function remove(id: string) {
  plugins.value = plugins.value.filter((p) => p.id !== id)
}

function openDetail(p: InstalledPlugin) {
  selectedId.value = p.id
  detailOpen.value = true
}

async function toggle(p: InstalledPlugin, enabled: boolean) {
  pending.value = new Set([...pending.value, p.id])
  try {
    const res = await setInstalledPluginEnabled(p.id, enabled)
    upsert(res.data)
    MessagePlugin.success(enabled ? t('pluginAdmin.enabledToast') : t('pluginAdmin.disabledToast'))
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginAdmin.saveFailed'))
  } finally {
    const next = new Set(pending.value)
    next.delete(p.id)
    pending.value = next
  }
}

onMounted(load)
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/provider-card.less';
@import (reference) '@/components/css/settings-section.less';

.plugin-admin {
  width: 100%;
}

.section-header {
  .settings-section-header();

  &__row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
  }
}

.plugin-admin__state {
  display: flex;
  justify-content: center;
  padding: 32px 0;

  &--empty {
    flex-direction: column;
    align-items: center;
    gap: 12px;
  }
}

.plugin-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 12px;
}

.plugin-card {
  .provider-card();
  align-items: flex-start;
  cursor: pointer;

  &--off {
    .plugin-card__badge,
    .plugin-card__body {
      opacity: 0.55;
    }
  }
}

.plugin-card__badge {
  .provider-card-badge();
  .provider-card-badge-color(#0052d9);
}

.plugin-card__badge-img {
  .provider-card-badge-img();
}

.plugin-card__body {
  .provider-card-body();
  gap: 4px;
}

.plugin-card__title {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.plugin-card__name {
  .provider-card-title();
}

.plugin-card__meta {
  font-family: var(--app-font-family-mono);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.plugin-card__desc {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}

.plugin-card__contribs {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 4px;
}

.plugin-card__contrib {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
  border-radius: var(--app-radius-xs);
  padding: 1px 6px;
}

.plugin-card__actions {
  flex: none;
}
</style>
