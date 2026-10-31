<template>
  <div class="plugin-settings">
    <div class="section-header">
      <h2>{{ $t('settings.plugins.title') }}</h2>
      <p class="section-description">{{ $t('settings.plugins.description') }}</p>
    </div>

    <div v-if="loading" class="loading-container">
      <t-loading :text="$t('common.loading')" />
    </div>

    <template v-else>
      <Teleport to="#toolbox-toolbar" defer>
        <t-input v-if="marketplaces.length > 0" v-model="query" class="list-search" :placeholder="$t('menu.search')"
          :aria-label="$t('menu.search')" clearable>
          <template #prefix-icon><t-icon name="search" size="16px" /></template>
        </t-input>
      </Teleport>

      <div v-if="marketplaces.length === 0" class="empty-state">
        <t-empty :description="$t(isAdmin ? 'settings.plugins.emptyDesc' : 'settings.plugins.emptyDescMember')" />
        <t-button v-if="isAdmin" theme="primary" @click="openAdd">{{ $t('settings.plugins.addMarketplace') }}</t-button>
      </div>

      <div v-else-if="filtered.length === 0" class="empty-state">
        <t-empty :description="$t('common.noResult')" />
        <t-button variant="outline" @click="query = ''">{{ $t('common.clear') }}</t-button>
      </div>

      <template v-else>
      <section v-for="mp in filtered" :key="mp.id" class="marketplace" :data-marketplace="mp.name">
        <header class="marketplace__header">
          <div class="marketplace__heading">
            <h3 class="marketplace__name">{{ mp.name }}</h3>
            <span v-if="mp.version" class="marketplace__meta">{{ mp.version }}</span>
          </div>
          <div v-if="isAdmin" class="marketplace__actions">
            <t-button size="small" variant="text" theme="danger" :disabled="marketplaceHasInstalls(marketplaces, mp.id)"
              :title="marketplaceHasInstalls(marketplaces, mp.id) ? $t('settings.plugins.deleteMarketplaceBlocked') : undefined"
              @click="askDeleteMarketplace(mp)">
              <template #icon><t-icon name="delete" /></template>
              {{ $t('settings.plugins.deleteMarketplace') }}
            </t-button>
          </div>
        </header>
        <p v-if="mp.description" class="marketplace__desc">{{ mp.description }}</p>

        <div class="plugin-grid">
          <article v-for="plugin in mp.plugins" :key="plugin.name" class="plugin-card"
            :class="`plugin-card--${pluginCardState(plugin)}`" :data-plugin="plugin.name">
            <div class="plugin-card__header">
              <div class="plugin-card__badge" aria-hidden="true"><t-icon name="app" size="14px" /></div>
              <h4 class="plugin-card__title" :title="plugin.name">{{ plugin.name }}</h4>
              <span v-if="plugin.installed_version || plugin.version" class="plugin-card__version">
                {{ plugin.installed_version || plugin.version }}
              </span>
              <span v-if="pluginHasNewerVersion(plugin)" class="plugin-card__update"
                :title="$t('settings.plugins.newerVersionHint', { latest: plugin.version, installed: plugin.installed_version })">
                {{ $t('settings.plugins.newerVersion', { latest: plugin.version }) }}
              </span>
            </div>
            <p v-if="plugin.description" class="plugin-card__desc" :title="plugin.description">
              {{ plugin.description }}
            </p>
            <div class="plugin-card__chips">
              <span v-if="plugin.skill_count > 0" class="plugin-card__chip">
                <t-icon :name="SKILL_ICON" size="12px" />{{ $t('settings.plugins.skillCount', { count: plugin.skill_count }) }}
              </span>
              <span v-if="plugin.mcp_count > 0" class="plugin-card__chip">
                <t-icon name="tools" size="12px" />{{ $t('settings.plugins.mcpCount', { count: plugin.mcp_count }) }}
              </span>
              <span v-if="plugin.requires_sandbox" class="plugin-card__chip plugin-card__chip--sandbox">
                {{ $t('settings.plugins.needsSandbox') }}
              </span>
            </div>
            <footer class="plugin-card__footer">
              <span class="plugin-card__status" :class="`is-${pluginCardState(plugin)}`" :title="plugin.error || undefined">
                <t-loading v-if="pluginStateIsTransient(plugin.status)" size="12px" />
                <t-icon v-else-if="plugin.status === 'failed'" name="error-circle" size="14px" />
                <span v-else class="plugin-card__status-dot" aria-hidden="true" />
                {{ $t(`settings.plugins.status.${pluginCardState(plugin)}`) }}
              </span>
              <t-button v-if="plugin.installed" size="small" variant="outline" @click="openDetail(mp, plugin)">
                {{ $t('settings.plugins.details') }}
              </t-button>
              <t-button v-else size="small" theme="primary" @click="openInstall(mp, plugin)">
                {{ $t('settings.plugins.install') }}
              </t-button>
            </footer>
          </article>
        </div>
      </section>
      </template>
    </template>

    <PluginMarketplaceDialog v-model:visible="addVisible" @added="onMarketplaceAdded" />
    <PluginInstallDialog v-model:visible="installVisible" :marketplace-id="target.marketplaceId"
      :plugin="target.plugin" @installed="onInstalled" />
    <PluginDetailDrawer v-model:visible="detailVisible" :marketplace-id="target.marketplaceId"
      :plugin-name="target.plugin?.name || ''" @changed="loadMarketplaces(true)" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  deletePluginMarketplace,
  listPluginMarketplaces,
  type MarketplacePlugin,
  type PluginInstallResult,
  type PluginMarketplace,
} from '@/api/plugin'
import { useAuthStore } from '@/stores/auth'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { matchesResourceQuery } from '@/utils/resourceListSearch'
import {
  installedPluginCount,
  marketplaceHasInstalls,
  pluginCardState,
  pluginHasNewerVersion,
  pluginStateIsTransient,
} from '@/utils/pluginMarketplace'
import { SKILL_ICON } from '@/types/mention'
import PluginMarketplaceDialog from './components/PluginMarketplaceDialog.vue'
import PluginInstallDialog from './components/PluginInstallDialog.vue'
import PluginDetailDrawer from './components/PluginDetailDrawer.vue'

const emit = defineEmits<{ count: [value: number] }>()
const { t } = useI18n()
const authStore = useAuthStore()
const confirmDelete = useConfirmDelete()
const isAdmin = computed(() => authStore.canAccessAllTenants || authStore.hasRole('admin'))

const loading = ref(false)
const marketplaces = ref<PluginMarketplace[]>([])
const query = ref('')
const addVisible = ref(false)
const installVisible = ref(false)
const detailVisible = ref(false)
const target = reactive<{ marketplaceId: string; plugin: MarketplacePlugin | null }>({ marketplaceId: '', plugin: null })
let pollTimer: number | null = null
let unmounted = false

// A marketplace matches when its own name does or any of its plugins does;
// in the latter case only the matching plugins are shown.
const filtered = computed(() => marketplaces.value.flatMap((mp) => {
  if (!query.value.trim() || matchesResourceQuery({ name: mp.name, description: mp.description }, query.value)) return [mp]
  const plugins = mp.plugins.filter((p) => matchesResourceQuery(
    { name: p.name, description: [p.description, ...(p.keywords || [])].join(' ') }, query.value))
  return plugins.length ? [{ ...mp, plugins }] : []
}))

async function loadMarketplaces(silent = false) {
  if (!silent) loading.value = true
  try {
    const res = await listPluginMarketplaces()
    if (unmounted) return
    marketplaces.value = res?.data || []
    emit('count', installedPluginCount(marketplaces.value))
    schedulePoll()
  } catch (error: any) {
    if (!silent) MessagePlugin.error(error?.message || t('settings.plugins.toasts.loadFailed'))
  } finally {
    loading.value = false
  }
}

function schedulePoll() {
  if (pollTimer) window.clearTimeout(pollTimer)
  pollTimer = null
  if (unmounted) return
  const busy = marketplaces.value.some((mp) => mp.plugins.some((p) => pluginStateIsTransient(p.status)))
  if (busy) pollTimer = window.setTimeout(() => { void loadMarketplaces(true) }, 3000)
}

function openAdd() {
  if (isAdmin.value) addVisible.value = true
}

function openInstall(mp: PluginMarketplace, plugin: MarketplacePlugin) {
  target.marketplaceId = mp.id
  target.plugin = plugin
  installVisible.value = true
}

function openDetail(mp: PluginMarketplace, plugin: MarketplacePlugin) {
  target.marketplaceId = mp.id
  target.plugin = plugin
  detailVisible.value = true
}

function onMarketplaceAdded() {
  addVisible.value = false
  void loadMarketplaces(true)
}

async function onInstalled(result: PluginInstallResult) {
  installVisible.value = false
  await loadMarketplaces(true)
  // The detail lists this install's skills and MCP services, including ones
  // still installing or waiting for OAuth.
  if (result.install_id) detailVisible.value = true
}

function askDeleteMarketplace(mp: PluginMarketplace) {
  confirmDelete({
    body: t('settings.plugins.deleteMarketplaceBody', { name: mp.name }),
    onConfirm: async () => {
      try {
        await deletePluginMarketplace(mp.id)
        MessagePlugin.success(t('settings.plugins.toasts.marketplaceDeleted'))
        await loadMarketplaces(true)
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('settings.plugins.toasts.marketplaceDeleteFailed'))
      }
    },
  })
}

onMounted(() => { void loadMarketplaces() })
onUnmounted(() => {
  unmounted = true
  if (pollTimer) window.clearTimeout(pollTimer)
})

defineExpose({ openAdd })
</script>

<style scoped lang="less">
@import (reference) '@/components/css/provider-card.less';
@import (reference) '@/components/css/settings-section.less';

.plugin-settings {
  width: 100%;
}

.section-header {
  .settings-section-header();
}

.loading-container {
  padding: 40px 0;
  text-align: center;
}

.empty-state {
  padding: 80px 0;
  text-align: center;

  :deep(.t-empty__description) {
    font-size: var(--app-text-base);
    color: var(--td-text-color-placeholder);
    margin-bottom: 16px;
  }
}

.marketplace {
  & + & {
    margin-top: 24px;
  }

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 8px;
  }

  &__heading {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 4px 10px;
    min-width: 0;
  }

  &__name {
    margin: 0;
    font-size: var(--app-text-md);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  &__meta {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
    overflow-wrap: anywhere;
  }

  &__actions {
    flex-shrink: 0;
    display: flex;
    gap: 4px;
  }

  &__desc {
    margin: 0 0 10px;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
  }
}

.plugin-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 300px), 1fr));
  gap: 10px;
}

.plugin-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  padding: 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  transition: border-color var(--app-motion-base) ease, box-shadow var(--app-motion-base) ease;

  &:hover {
    border-color: color-mix(in srgb, var(--td-brand-color) 40%, var(--td-component-stroke));
    box-shadow: 0 3px 12px color-mix(in srgb, var(--td-text-color-primary) 6%, transparent);
  }

  &--failed {
    border-color: color-mix(in srgb, var(--td-error-color) 40%, var(--td-component-stroke));
  }

  &__header {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }

  &__badge {
    .provider-card-badge(28px, 8px);
  }

  &__title {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: var(--app-text-base);
    font-weight: 600;
    line-height: 20px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__version {
    flex-shrink: 0;
    font-size: var(--app-text-xs);
    color: var(--td-text-color-placeholder);
  }

  &__update {
    flex-shrink: 0;
    padding: 0 6px;
    border-radius: var(--app-radius-pill);
    background: color-mix(in srgb, var(--td-warning-color) 12%, transparent);
    color: var(--td-warning-color);
    font-size: var(--app-text-xs);
    line-height: 18px;
  }

  &__desc {
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    margin: 0;
    overflow: hidden;
    font-size: var(--app-text-sm);
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }

  &__chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  &__chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 1px 6px;
    border-radius: var(--app-radius-sm);
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-xs);
    line-height: 18px;

    &--sandbox {
      background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
      color: var(--td-brand-color);
    }
  }

  &__footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-top: auto;
  }

  &__status {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);

    &.is-ready { color: var(--td-success-color); }
    &.is-installing { color: var(--td-brand-color); }
    &.is-needs_oauth { color: var(--td-warning-color); }
    &.is-failed { color: var(--td-error-color); }
  }

  &__status-dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: currentColor;
  }
}
</style>
