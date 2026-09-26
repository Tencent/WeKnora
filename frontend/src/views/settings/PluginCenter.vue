<template>
  <div class="plugin-center">
    <div class="section-header">
      <div class="section-header__top">
        <div>
          <h2>{{ t('pluginCenter.title') }}</h2>
          <p class="section-description">{{ t('pluginCenter.description') }}</p>
        </div>
        <t-button v-if="canManage && own.allowed" variant="outline" @click="ownOpen = true">
          <template #icon><t-icon name="add" /></template>
          {{ t('pluginCenter.own.register') }}
        </t-button>
      </div>
    </div>

    <div class="plugin-center__tabs" role="tablist">
      <button
        v-for="k in (['installed', 'builtin'] as const)"
        :key="k"
        type="button"
        role="tab"
        class="plugin-center__tab"
        :class="{ 'plugin-center__tab--active': tab === k }"
        :aria-selected="tab === k"
        @click="tab = k"
      >
        {{ t(`pluginCenter.tabs.${k}`) }}<span class="plugin-center__tab-count">{{ k === 'installed' ? installed.length : builtins.length }}</span>
      </button>
    </div>

    <div class="plugin-center__toolbar">
      <t-input v-model="query" class="plugin-center__search" clearable :placeholder="t('pluginCenter.searchPlaceholder')">
        <template #prefix-icon><t-icon name="search" /></template>
      </t-input>
      <div class="option-chips">
        <button type="button" class="option-chip" :class="{ 'option-chip--active': category === '' }" @click="category = ''">
          {{ t('pluginCenter.allPoints') }}
        </button>
        <button
          v-for="c in CATEGORY_ORDER"
          :key="c"
          type="button"
          class="option-chip"
          :class="{ 'option-chip--active': category === c }"
          @click="category = c"
        >
          {{ t(`pluginCenter.categories.${c}`) }}
        </button>
      </div>
    </div>

    <div v-if="loading" class="plugin-center__state"><t-loading size="small" /></div>

    <template v-else-if="tab === 'installed'">
      <div v-if="visibleInstalled.length === 0" class="plugin-center__state">
        <t-empty :description="installed.length ? t('pluginCenter.empty') : t('pluginCenter.noneInstalled')" />
      </div>
      <div v-else class="plugin-grid">
        <div
          v-for="p in visibleInstalled"
          :key="p.manifest.id"
          class="plugin-card"
          :class="{ 'plugin-card--off': !p.enabled }"
          role="button"
          tabindex="0"
          @click="openDetail(p)"
          @keydown.enter="openDetail(p)"
        >
          <PluginBadge :manifest="p.manifest" />
          <div class="plugin-card__body">
            <div class="plugin-card__head">
              <span class="plugin-card__name">{{ nameOf(p) }}</span>
              <t-tag v-if="ownedIds.has(p.manifest.id)" size="small" variant="light" theme="primary">
                {{ t('pluginCenter.own.tag') }}
              </t-tag>
            </div>
            <div class="plugin-card__meta">{{ publisherOf(p) }} · v{{ p.manifest.version }}</div>
            <div v-if="descriptionOf(p)" class="plugin-card__desc">{{ descriptionOf(p) }}</div>
            <div class="plugin-card__foot">
              <span class="plugin-card__provides">{{ providesOf(p) }}</span>
              <span v-if="!p.enabled" class="plugin-card__off">{{ t('pluginCenter.off') }}</span>
              <span v-else-if="canManage && canConfigure(p.manifest)" class="plugin-card__link">{{ t('pluginCenter.configure') }}</span>
            </div>
          </div>
          <div class="plugin-card__switch" @click.stop @keydown.enter.stop>
            <t-tooltip :content="switchTooltip(p)" :disabled="!switchTooltip(p)">
              <t-switch
                size="small"
                :model-value="p.enabled"
                :disabled="!canManage || pending.has(p.manifest.id)"
                :loading="pending.has(p.manifest.id)"
                @update:model-value="(v: boolean) => toggle(p, v)"
              />
            </t-tooltip>
          </div>
        </div>
      </div>
    </template>

    <template v-else>
      <div v-if="builtinGroups.length === 0" class="plugin-center__state">
        <t-empty :description="t('pluginCenter.empty')" />
      </div>
      <section v-for="g in builtinGroups" :key="g.category" class="plugin-group">
        <h3 class="plugin-group__title">{{ t(`pluginCenter.categories.${g.category}`) }} · {{ g.plugins.length }}</h3>
        <div class="plugin-tiles">
          <div
            v-for="p in g.plugins"
            :key="p.manifest.id"
            class="plugin-tile"
            :class="{ 'plugin-tile--off': !p.enabled }"
            role="button"
            tabindex="0"
            @click="openDetail(p)"
            @keydown.enter="openDetail(p)"
          >
            <PluginBadge :manifest="p.manifest" size="sm" />
            <span class="plugin-tile__name">{{ nameOf(p) }}</span>
            <span v-if="p.manifest.required" class="plugin-tile__required">{{ t('pluginCenter.required') }}</span>
            <div v-else class="plugin-tile__switch" @click.stop @keydown.enter.stop>
              <t-switch
                size="small"
                :model-value="p.enabled"
                :disabled="!canManage || pending.has(p.manifest.id)"
                :loading="pending.has(p.manifest.id)"
                @update:model-value="(v: boolean) => toggle(p, v)"
              />
            </div>
          </div>
        </div>
      </section>
    </template>

    <PluginCenterDrawer
      v-model:visible="detailOpen"
      :plugin="selected"
      :owned="selectedOwned"
      :own-allowed="own.allowed"
      :can-manage="canManage"
      :pending="!!selected && pending.has(selected.manifest.id)"
      @toggle="(v: boolean) => selected && toggle(selected, v)"
      @update-package="ownOpen = true"
      @changed="afterOwnChange"
      @removed="onOwnRemoved"
    />
    <PluginInstallDrawer v-model:visible="ownOpen" scope="tenant" @installed="afterOwnChange" />
    <PluginSecretDialog v-model:secret="issuedSecret" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'

import { listPlugins, setPluginEnabled, type TenantPlugin } from '@/api/plugin'
import type { InstalledPlugin } from '@/api/system/plugins'
import { listTenantPlugins, type TenantPluginListing } from '@/api/tenantPlugins'
import PluginBadge from '@/components/plugins/PluginBadge.vue'
import PluginSecretDialog from '@/components/plugins/PluginSecretDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { usePluginPagesStore } from '@/stores/pluginPages'
import { localizedText } from '@/utils/localizedText'
import PluginInstallDrawer from '@/views/system/plugins/PluginInstallDrawer.vue'

import PluginCenterDrawer from './plugins/PluginCenterDrawer.vue'
import {
  CATEGORY_ORDER,
  canConfigure,
  filterPlugins,
  primaryCategory,
  providedPoints,
  type PluginCategory,
} from './pluginCenterState'

// The workspace's plugins: installed ones as cards, the builtin capabilities
// as compact tiles grouped by what they are for. A card opens the plugin's
// details, configuration and webhooks; its switch turns it on or off here.
const { t, locale } = useI18n()
const authStore = useAuthStore()
const pluginPages = usePluginPagesStore()

const plugins = ref<TenantPlugin[]>([])
const loading = ref(false)
const query = ref('')
const category = ref<PluginCategory | ''>('')
const tab = ref<'installed' | 'builtin'>('installed')
const pending = ref(new Set<string>())

const canManage = computed(() => authStore.canAccessAllTenants || authStore.hasRole('admin'))

const installed = computed(() => plugins.value.filter((p) => !p.manifest.builtin))
const builtins = computed(() => plugins.value.filter((p) => p.manifest.builtin))
const filterOf = () => ({ query: query.value, category: category.value, locale: locale.value })
const visibleInstalled = computed(() => filterPlugins(installed.value, filterOf()))
const builtinGroups = computed(() => {
  const shown = filterPlugins(builtins.value, filterOf())
  return CATEGORY_ORDER
    .map((c) => ({ category: c, plugins: shown.filter((p) => primaryCategory(p.manifest) === c) }))
    .filter((g) => g.plugins.length > 0)
})

const nameOf = (p: TenantPlugin) => localizedText(p.manifest.name, locale.value) || p.manifest.id
const descriptionOf = (p: TenantPlugin) => localizedText(p.manifest.description, locale.value)
const publisherOf = (p: TenantPlugin) => p.manifest.publisher.name || p.manifest.publisher.id
const providesOf = (p: TenantPlugin) =>
  providedPoints(p.manifest).map((point) => t(`pluginCenter.points.${point}`)).join(' · ')

function switchTooltip(p: TenantPlugin): string {
  if (!canManage.value) return t('pluginCenter.adminOnly')
  return p.manifest.required ? t('pluginCenter.requiredHint') : ''
}

async function load() {
  loading.value = true
  try {
    const res = await listPlugins()
    plugins.value = res.data || []
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginCenter.loadFailed'))
  } finally {
    loading.value = false
  }
  if (canManage.value) void loadOwn()
}

// The workspace's own remote plugins, when the platform allows them.
const own = ref<TenantPluginListing>({ allowed: false, plugins: [] })
const ownedIds = computed(() => new Set(own.value.plugins.map((p) => p.id)))
const ownOpen = ref(false)
const issuedSecret = ref('')

async function loadOwn() {
  try {
    const res = await listTenantPlugins()
    own.value = res.data ?? { allowed: false, plugins: [] }
  } catch {
    own.value = { allowed: false, plugins: [] }
  }
}

function afterOwnChange(view?: InstalledPlugin) {
  if (view?.issuedSecret) issuedSecret.value = view.issuedSecret
  void load()
  void pluginPages.ensure(true).catch(() => {})
}

function onOwnRemoved() {
  detailOpen.value = false
  afterOwnChange()
}

// Details of one plugin.
const detailOpen = ref(false)
const selectedId = ref('')
const selected = computed(() => plugins.value.find((p) => p.manifest.id === selectedId.value) ?? null)
const selectedOwned = computed(() => own.value.plugins.find((p) => p.id === selectedId.value))

function openDetail(p: TenantPlugin) {
  selectedId.value = p.manifest.id
  detailOpen.value = true
}

async function toggle(p: TenantPlugin, enabled: boolean) {
  const id = p.manifest.id
  pending.value = new Set([...pending.value, id])
  try {
    const res = await setPluginEnabled(id, enabled)
    const updated = res.data
    plugins.value = plugins.value.map((x) => (x.manifest.id === id ? { ...x, ...updated } : x))
    // The plugin's pages, settings sections and tabs come and go with it.
    void pluginPages.ensure(true).catch(() => {})
    MessagePlugin.success(enabled ? t('pluginCenter.enabledToast') : t('pluginCenter.disabledToast'))
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginCenter.saveFailed'))
  } finally {
    const next = new Set(pending.value)
    next.delete(id)
    pending.value = next
  }
}

onMounted(load)
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/provider-card.less';
@import (reference) '@/components/css/settings-section.less';
@import (reference) '@/components/css/option-chips.less';

.plugin-center {
  width: 100%;
}

.section-header {
  .settings-section-header();
  margin-bottom: 20px;
}

.plugin-center__tabs {
  display: flex;
  gap: 24px;
  margin-bottom: 16px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.plugin-center__tab {
  margin-bottom: -1px;
  padding: 0 0 10px;
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  font: inherit;
  font-size: var(--app-text-base);
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-primary);
  }

  &--active {
    color: var(--td-text-color-primary);
    font-weight: 600;
    border-bottom-color: var(--td-brand-color);
  }
}

.plugin-center__tab-count {
  margin-left: 6px;
  font-size: var(--app-text-sm);
  font-weight: 400;
  color: var(--td-text-color-placeholder);
}

.plugin-center__toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
}

.plugin-center__search {
  width: 220px;
}

.option-chips {
  .option-chips();
}

.option-chip {
  .option-chip();
}

.plugin-center__state {
  display: flex;
  justify-content: center;
  padding: 48px 0;
}

.plugin-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 320px), 1fr));
  gap: 12px;
}

.plugin-card {
  .provider-card();
  .provider-card-interactive();
  min-height: 104px;

  &--off {
    .plugin-badge,
    .plugin-card__name,
    .plugin-card__desc {
      opacity: 0.55;
    }
  }
}

.plugin-card__body {
  .provider-card-body();
  align-self: stretch;
}

.plugin-card__head {
  .provider-card-header();
  padding-right: 44px;
}

.plugin-card__name {
  .provider-card-title();
  flex: 0 1 auto;
}

.plugin-card__meta {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.plugin-card__desc {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  font-size: var(--app-text-md);
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.plugin-card__foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: auto;
  padding-top: 6px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.plugin-card__provides {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.plugin-card__link {
  color: var(--td-brand-color);
}

.plugin-card__switch {
  position: absolute;
  top: 14px;
  right: 16px;
}

.plugin-group {
  margin-bottom: 20px;
}

.plugin-group__title {
  margin: 0 0 8px;
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-secondary);
}

.plugin-tiles {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 210px), 1fr));
  gap: 8px;
}

.plugin-tile {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  padding: 8px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  cursor: pointer;
  transition: border-color var(--app-motion-fast) ease;

  &:hover {
    border-color: var(--td-brand-color-3);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 2px;
  }

  &--off .plugin-badge,
  &--off .plugin-tile__name {
    opacity: 0.5;
  }
}

.plugin-tile__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
}

.plugin-tile__required {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}
</style>
