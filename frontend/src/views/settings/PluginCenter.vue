<template>
  <div class="plugin-center">
    <div class="section-header">
      <h2>{{ t('pluginCenter.title') }}</h2>
      <p class="section-description">{{ t('pluginCenter.description') }}</p>
    </div>

    <div class="plugin-center__toolbar">
      <div class="plugin-center__toolbar-row">
        <t-input v-model="query" class="plugin-center__search" clearable :placeholder="t('pluginCenter.searchPlaceholder')">
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-button v-if="canManage && own.allowed" variant="outline" @click="ownOpen = true">
          <template #icon><t-icon name="add" /></template>
          {{ t('pluginCenter.own.register') }}
        </t-button>
      </div>
      <div class="option-chips">
        <button type="button" class="option-chip" :class="{ 'option-chip--active': point === '' }" @click="point = ''">
          {{ t('pluginCenter.allPoints') }}
        </button>
        <button
          v-for="p in EXTENSION_POINTS"
          :key="p"
          type="button"
          class="option-chip"
          :class="{ 'option-chip--active': point === p }"
          @click="point = p"
        >
          {{ t(`pluginCenter.points.${p}`) }}
        </button>
      </div>
    </div>

    <div v-if="loading" class="plugin-center__state"><t-loading size="small" /></div>
    <div v-else-if="visible.length === 0" class="plugin-center__state">
      <t-empty :description="t('pluginCenter.empty')" />
    </div>
    <div v-else class="plugin-list">
      <div v-for="p in visible" :key="p.manifest.id" class="plugin-card" :class="{ 'plugin-card--off': !p.enabled }">
        <div class="plugin-card__badge" :class="{ 'plugin-card__badge--logo': !!pluginIconUrl(p.manifest) }">
          <img v-if="pluginIconUrl(p.manifest)" :src="pluginIconUrl(p.manifest)" alt="" class="plugin-card__badge-img" />
          <template v-else>{{ initial(p) }}</template>
        </div>
        <div class="plugin-card__body">
          <div class="plugin-card__title">
            <span class="plugin-card__name">{{ nameOf(p) }}</span>
            <t-tag v-if="p.manifest.builtin" size="small" variant="light">{{ t('pluginCenter.builtin') }}</t-tag>
            <t-tag v-else-if="ownedIds.has(p.manifest.id)" size="small" variant="light" theme="primary">
              {{ t('pluginCenter.own.tag') }}
            </t-tag>
            <t-tag v-else size="small" variant="light" theme="warning">{{ t('pluginCenter.installed') }}</t-tag>
            <t-tag v-if="p.manifest.required" size="small" variant="light" theme="primary">
              {{ t('pluginCenter.required') }}
            </t-tag>
          </div>
          <div class="plugin-card__meta">{{ p.manifest.id }} · v{{ p.manifest.version }}</div>
          <div v-if="descriptionOf(p)" class="plugin-card__desc">{{ descriptionOf(p) }}</div>
          <div class="plugin-card__contribs">
            <span v-for="s in contributionSummary(p.manifest)" :key="s.point" class="plugin-card__contrib">
              {{ t(`pluginCenter.points.${s.point}`) }} × {{ s.count }}
            </span>
          </div>
        </div>
        <div class="plugin-card__actions">
          <t-tooltip :content="switchTooltip(p)" :disabled="!switchTooltip(p)">
            <t-switch
              :model-value="p.enabled"
              :disabled="!canManage || p.manifest.required || pending.has(p.manifest.id)"
              :loading="pending.has(p.manifest.id)"
              @update:model-value="(v: boolean) => toggle(p, v)"
            />
          </t-tooltip>
          <t-button
            v-if="canManage && canConfigure(p.manifest)"
            size="small"
            variant="text"
            theme="primary"
            @click="openConfig(p)"
          >
            {{ t('pluginCenter.configure') }}
          </t-button>
          <t-dropdown
            v-if="canManage && ownedIds.has(p.manifest.id)"
            :options="ownActions"
            trigger="click"
            @click="(item: { value: OwnAction }) => onOwnAction(p.manifest.id, item.value)"
          >
            <t-button size="small" variant="text" theme="primary">{{ t('pluginCenter.own.manage') }}</t-button>
          </t-dropdown>
        </div>
      </div>
    </div>

    <PluginInstallDrawer v-model:visible="ownOpen" scope="tenant" @installed="onOwnInstalled" />
    <PluginSecretDialog v-model:secret="issuedSecret" />
    <t-dialog
      :visible="!!urlPlugin"
      :header="t('pluginCenter.own.urlTitle')"
      :confirm-btn="{ content: t('common.save'), loading: urlSaving, disabled: !isPackageUrl(urlDraft) }"
      @confirm="saveOwnUrl"
      @close="urlPlugin = ''"
      @cancel="urlPlugin = ''"
    >
      <p class="plugin-center__hint">{{ t('pluginAdmin.install.remoteUrlHint') }}</p>
      <t-input v-model="urlDraft" placeholder="https://plugins.example.com/acme-search" @enter="saveOwnUrl" />
    </t-dialog>

    <SettingDrawer
      v-model:visible="configOpen"
      :title="t('pluginCenter.configTitle', { name: configPlugin ? nameOf(configPlugin) : '' })"
      :description="t('pluginCenter.configDescription')"
      icon="setting"
      :confirm-loading="configSaving"
      :confirm-disabled="!configSchema || configSaving"
      :hide-footer="!configSchema"
      @confirm="saveConfig"
    >
      <div v-if="configLoading" class="plugin-center__state"><t-loading size="small" /></div>
      <template v-else>
        <section v-if="configSchema" class="setting-drawer__section">
          <SchemaForm v-model="configValues" :schema="configSchema" :errors="configErrors" />
        </section>
        <section v-if="configWebhooks.length" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('pluginCenter.webhooks') }}</h4>
          <p class="plugin-center__hint">{{ t('pluginCenter.webhooksHint') }}</p>
          <div v-for="hook in configWebhooks" :key="hook.id" class="plugin-center__webhook">
            <div class="plugin-center__webhook-name">{{ localizedText(hook.name, locale) }}</div>
            <p v-if="hook.description" class="plugin-center__hint">{{ localizedText(hook.description, locale) }}</p>
            <div class="plugin-center__webhook-url">
              <code>{{ webhookUrl(hook, origin) }}</code>
              <t-button size="small" variant="text" theme="primary"
                @click="copyWithToast(webhookUrl(hook, origin), 'pluginCenter.webhookCopied')">
                {{ t('pluginCenter.copy') }}
              </t-button>
            </div>
          </div>
        </section>
      </template>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { pluginIconUrl } from '@/extensions/pluginIcon'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'

import {
  getPluginConfig,
  listPluginWebhooks,
  listPlugins,
  setPluginEnabled,
  updatePluginConfig,
  type ExtensionPoint,
  type PluginWebhook,
  type TenantPlugin,
} from '@/api/plugin'
import SchemaForm from '@/components/schema-form/SchemaForm.vue'
import { pluginFormSource } from '@/components/schema-form/pluginSource'
import { provideSchemaFormSource } from '@/components/schema-form/source'
import { validateConfig, type ConfigSchema, type ConfigValue, type FieldError } from '@/components/schema-form/schema'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import PluginSecretDialog from '@/components/plugins/PluginSecretDialog.vue'
import type { InstalledPlugin } from '@/api/system/plugins'
import {
  listTenantPlugins,
  rotateTenantPluginSecret,
  setTenantPluginRemoteUrl,
  uninstallTenantPlugin,
  type TenantPluginListing,
} from '@/api/tenantPlugins'
import PluginInstallDrawer from '@/views/system/plugins/PluginInstallDrawer.vue'
import { isPackageUrl } from '@/views/system/pluginManagementState'
import { useAuthStore } from '@/stores/auth'
import { usePluginPagesStore } from '@/stores/pluginPages'
import { localizedText } from '@/utils/localizedText'
import { copyWithToast } from '@/utils/clipboard'

import {
  EXTENSION_POINTS,
  canConfigure,
  contributionSummary,
  filterPlugins,
  hasTenantConfig,
  hasWebhooks,
  webhookUrl,
} from './pluginCenterState'

// Lists the plugins this deployment knows — builtins included — and lets an
// admin turn them off for the workspace. A disabled plugin's integrations drop
// out of the type lists; existing instances keep working.
const { t, locale } = useI18n()
const authStore = useAuthStore()

const plugins = ref<TenantPlugin[]>([])
const loading = ref(false)
const query = ref('')
const point = ref<ExtensionPoint | ''>('')
const pending = ref(new Set<string>())

const canManage = computed(() => authStore.canAccessAllTenants || authStore.hasRole('admin'))

const visible = computed(() =>
  filterPlugins(plugins.value, { query: query.value, point: point.value, locale: locale.value }),
)

const nameOf = (p: TenantPlugin) => localizedText(p.manifest.name, locale.value)
const descriptionOf = (p: TenantPlugin) => localizedText(p.manifest.description, locale.value)
const initial = (p: TenantPlugin) => (nameOf(p).trim().charAt(0) || '?').toUpperCase()

function switchTooltip(p: TenantPlugin): string {
  if (p.manifest.required) return t('pluginCenter.requiredHint')
  if (!canManage.value) return t('pluginCenter.adminOnly')
  return ''
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
const urlPlugin = ref('')
const urlDraft = ref('')
const urlSaving = ref(false)
type OwnAction = 'update' | 'url' | 'rotate' | 'remove'
const ownActions = computed(() => [
  ...(own.value.allowed ? [{ content: t('pluginCenter.own.update'), value: 'update' as OwnAction }] : []),
  { content: t('pluginCenter.own.editUrl'), value: 'url' as OwnAction },
  { content: t('pluginAdmin.detail.rotateSecret'), value: 'rotate' as OwnAction },
  { content: t('pluginCenter.own.remove'), value: 'remove' as OwnAction, theme: 'error' },
])

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

function onOwnInstalled(view: InstalledPlugin) {
  afterOwnChange(view)
}

function onOwnAction(id: string, action: OwnAction) {
  const p = own.value.plugins.find((x) => x.id === id)
  switch (action) {
    case 'update':
      ownOpen.value = true
      return
    case 'url':
      urlPlugin.value = id
      urlDraft.value = p?.remote_url ?? ''
      return
    case 'rotate': {
      const dlg = DialogPlugin.confirm({
        header: t('pluginAdmin.detail.rotateSecret'),
        body: t('pluginAdmin.detail.rotateConfirm'),
        onConfirm: async () => {
          dlg.hide()
          try {
            afterOwnChange((await rotateTenantPluginSecret(id)).data)
          } catch (e: any) {
            MessagePlugin.error(e?.message || t('pluginAdmin.detail.rotateFailed'))
          }
        },
      })
      return
    }
    case 'remove': {
      const dlg = DialogPlugin.confirm({
        header: t('pluginCenter.own.remove'),
        body: t('pluginCenter.own.removeConfirm'),
        theme: 'danger',
        onConfirm: async () => {
          dlg.hide()
          try {
            await uninstallTenantPlugin(id)
            MessagePlugin.success(t('pluginCenter.own.removed'))
            afterOwnChange()
          } catch (e: any) {
            MessagePlugin.error(e?.message || t('pluginCenter.own.removeFailed'))
          }
        },
      })
    }
  }
}

async function saveOwnUrl() {
  if (!urlPlugin.value || !isPackageUrl(urlDraft.value)) return
  urlSaving.value = true
  try {
    afterOwnChange((await setTenantPluginRemoteUrl(urlPlugin.value, urlDraft.value.trim())).data)
    urlPlugin.value = ''
    MessagePlugin.success(t('pluginAdmin.detail.urlSaved'))
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginAdmin.detail.urlSaveFailed'))
  } finally {
    urlSaving.value = false
  }
}

const pluginPages = usePluginPagesStore()

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

// Workspace configuration of an installed plugin, such as its API key.
const configOpen = ref(false)
const configPlugin = ref<TenantPlugin | null>(null)
const configSchema = ref<ConfigSchema | null>(null)
const configValues = ref<ConfigValue>({})
const configErrors = ref<FieldError[]>([])
const configLoading = ref(false)
const configSaving = ref(false)
const configWebhooks = ref<PluginWebhook[]>([])

// x-options / x-oauth fields of the workspace configuration ask the plugin.
provideSchemaFormSource(pluginFormSource({
  target: () => (configPlugin.value ? { pluginId: configPlugin.value.manifest.id, scope: 'tenant' } : undefined),
  values: () => configValues.value,
}))
const origin = window.location.origin

async function openConfig(p: TenantPlugin) {
  configPlugin.value = p
  configSchema.value = null
  configValues.value = {}
  configErrors.value = []
  configWebhooks.value = []
  configOpen.value = true
  configLoading.value = true
  try {
    const [config, hooks] = await Promise.all([
      hasTenantConfig(p.manifest) ? getPluginConfig(p.manifest.id) : null,
      hasWebhooks(p.manifest) ? listPluginWebhooks(p.manifest.id) : null,
    ])
    if (config) {
      configSchema.value = config.data.schema
      configValues.value = config.data.values ?? {}
    }
    configWebhooks.value = hooks?.data ?? []
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginCenter.configLoadFailed'))
  } finally {
    configLoading.value = false
  }
}

async function saveConfig() {
  const p = configPlugin.value
  if (!p || !configSchema.value) return
  configErrors.value = validateConfig(configSchema.value, configValues.value)
  if (configErrors.value.length) return
  configSaving.value = true
  try {
    const res = await updatePluginConfig(p.manifest.id, configValues.value)
    configValues.value = res.data.values ?? {}
    MessagePlugin.success(t('pluginCenter.configSaved'))
    configOpen.value = false
  } catch (e: any) {
    const details = e?.error?.details
    if (Array.isArray(details)) configErrors.value = details
    MessagePlugin.error(e?.message || t('pluginCenter.configSaveFailed'))
  } finally {
    configSaving.value = false
  }
}

onMounted(load)
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/provider-card.less';
@import (reference) '@/components/css/settings-section.less';

.plugin-center {
  width: 100%;
}

.section-header {
  .settings-section-header();
}

.plugin-center__toolbar {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-bottom: 16px;
}

.plugin-center__search {
  max-width: 360px;
}

.plugin-center__toolbar-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

// Segmented point filter, same look as the IM channel mode switch.
.option-chips {
  display: inline-flex;
  flex-wrap: wrap;
  align-self: flex-start;
  gap: 4px;
  padding: 3px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
}

.option-chip {
  border: none;
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  line-height: 1.3;
  padding: 5px 10px;
  border-radius: var(--app-radius-sm);
  cursor: pointer;
  transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;
  white-space: nowrap;

  &:hover {
    color: var(--td-text-color-primary);
  }

  &--active {
    background: var(--td-bg-color-container);
    color: var(--td-brand-color);
    font-weight: 500;
    box-shadow: var(--td-shadow-1);
  }
}

.plugin-center__hint {
  margin: 0 0 8px;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.plugin-center__webhook {
  display: flex;
  flex-direction: column;
  gap: 4px;

  & + & {
    margin-top: 12px;
  }
}

.plugin-center__webhook-name {
  font-size: var(--app-text-sm);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.plugin-center__webhook-url {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;

  code {
    flex: 1;
    min-width: 0;
    padding: 6px 8px;
    font-size: var(--app-text-xs);
    word-break: break-all;
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
    border-radius: var(--app-radius-sm);
  }
}

.plugin-center__state {
  display: flex;
  justify-content: center;
  padding: 32px 0;
}

.plugin-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
}

.plugin-card {
  .provider-card();
  align-items: flex-start;

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

.plugin-card__actions {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
  flex: none;
}

.plugin-card__contrib {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
  border-radius: var(--app-radius-xs);
  padding: 1px 6px;
}
</style>
