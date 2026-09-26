<template>
  <div class="plugin-admin">
    <header class="section-header">
      <div class="section-header__top">
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
    <template v-else>
      <div class="plugin-admin__toolbar">
        <t-input v-model="query" class="plugin-admin__search" clearable :placeholder="t('pluginAdmin.searchPlaceholder')">
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <div class="option-chips">
          <button
            v-for="f in ADMIN_FILTERS"
            :key="f"
            type="button"
            class="option-chip"
            :class="{ 'option-chip--active': filter === f }"
            :aria-pressed="filter === f"
            @click="filter = f"
          >
            {{ t(`pluginAdmin.filters.${f}`) }}<span class="option-chip__count">{{ counts[f] }}</span>
          </button>
        </div>
      </div>

      <div class="plugin-table">
        <table>
          <thead>
            <tr>
              <th>{{ t('pluginAdmin.columns.plugin') }}</th>
              <th>{{ t('pluginAdmin.columns.version') }}</th>
              <th>{{ t('pluginAdmin.columns.runtime') }}</th>
              <th>{{ t('pluginAdmin.columns.trust') }}</th>
              <th>{{ t('pluginAdmin.columns.audience') }}</th>
              <th>{{ t('pluginAdmin.columns.state') }}</th>
              <th class="plugin-table__menu-col" />
            </tr>
          </thead>
          <tbody>
            <tr v-if="visible.length === 0">
              <td colspan="7" class="plugin-table__empty">{{ t('pluginAdmin.noMatch') }}</td>
            </tr>
            <tr
              v-for="p in visible"
              :key="p.id"
              class="plugin-row"
              :class="{ 'plugin-row--off': p.desired_state === 'disabled' }"
              tabindex="0"
              @click="openDetail(p)"
              @keydown.enter="openDetail(p)"
            >
              <td>
                <div class="plugin-row__plugin">
                  <PluginBadge :manifest="p.manifest" size="md" />
                  <div class="plugin-row__names">
                    <div class="plugin-row__name">{{ nameOf(p) }}</div>
                    <div class="plugin-row__id">{{ p.id }}</div>
                  </div>
                </div>
              </td>
              <td class="plugin-row__num">v{{ p.active_version }}</td>
              <td>{{ runtimeLabel(p.runtime) }}</td>
              <td>
                <span class="trust" :class="`trust--${activeTrust(p)}`">{{ t(`pluginAdmin.trust.${activeTrust(p)}`) }}</span>
              </td>
              <td>{{ audienceLabel(p) }}</td>
              <td>
                <div class="plugin-row__state">
                  <t-tooltip :content="p.node?.error" :disabled="!p.node?.error">
                    <span class="state" :class="`state--${installedState(p)}`">{{ t(`pluginAdmin.state.${installedState(p)}`) }}</span>
                  </t-tooltip>
                  <t-tooltip v-if="egressUnenforced(p.manifest, p.egress)" :content="t('pluginAdmin.egress.unenforcedHint')">
                    <span class="egress-flag">{{ t('pluginAdmin.egress.unenforced') }}</span>
                  </t-tooltip>
                </div>
              </td>
              <td class="plugin-table__menu-col" @click.stop @keydown.enter.stop>
                <t-dropdown trigger="click" placement="bottom-right" attach="body" :min-column-width="120">
                  <t-button variant="text" shape="square" size="small" :loading="pending.has(p.id)" :aria-label="t('pluginAdmin.menu.more')">
                    <template #icon><t-icon name="ellipsis" /></template>
                  </t-button>
                  <template #dropdown>
                    <t-dropdown-menu>
                      <t-dropdown-item @click="openDetail(p)">{{ t('pluginAdmin.menu.detail') }}</t-dropdown-item>
                      <t-dropdown-item @click="toggle(p, p.desired_state !== 'enabled')">
                        {{ p.desired_state === 'enabled' ? t('pluginAdmin.menu.disable') : t('pluginAdmin.menu.enable') }}
                      </t-dropdown-item>
                    </t-dropdown-menu>
                  </template>
                </t-dropdown>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <PluginInstallDrawer v-model:visible="installOpen" @installed="upsert" />
    <PluginSecretDialog v-model:secret="issuedSecret" />
    <PluginDetailDrawer
      v-model:visible="detailOpen"
      :plugin="selected"
      :pending="!!selected && pending.has(selected.id)"
      @toggle="(v: boolean) => selected && toggle(selected, v)"
      @changed="upsert"
      @removed="remove"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'

import { listInstalledPlugins, setInstalledPluginEnabled, type InstalledPlugin } from '@/api/system/plugins'
import PluginBadge from '@/components/plugins/PluginBadge.vue'
import PluginSecretDialog from '@/components/plugins/PluginSecretDialog.vue'
import { usePluginPagesStore } from '@/stores/pluginPages'
import { localizedText } from '@/utils/localizedText'

import PluginDetailDrawer from './plugins/PluginDetailDrawer.vue'
import PluginInstallDrawer from './plugins/PluginInstallDrawer.vue'
import {
  ADMIN_FILTERS,
  activeTrust,
  audienceSummary,
  egressUnenforced,
  filterInstalled,
  installedState,
  matchesAdminFilter,
  type AdminFilter,
} from './pluginManagementState'

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
const issuedSecret = ref('')
const selectedId = ref('')
const selected = computed(() => plugins.value.find((p) => p.id === selectedId.value) ?? null)

const query = ref('')
const filter = ref<AdminFilter>('all')
const visible = computed(() => filterInstalled(plugins.value, { query: query.value, filter: filter.value, locale: locale.value }))
const counts = computed(
  () => Object.fromEntries(ADMIN_FILTERS.map((f) => [f, plugins.value.filter((p) => matchesAdminFilter(p, f)).length])) as Record<AdminFilter, number>,
)

const nameOf = (p: InstalledPlugin) => (p.manifest ? localizedText(p.manifest.name, locale.value) : p.id)

function audienceLabel(p: InstalledPlugin) {
  const a = audienceSummary(p)
  if (a.kind === 'owned') return t('pluginAdmin.ownedBy', { tenant: a.tenant })
  if (a.kind === 'all') return t('pluginAdmin.audience.all')
  return t('pluginAdmin.audience.count', { count: a.count })
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

// Installing, switching, upgrading or removing a plugin changes the pages,
// settings sections and tabs of the workspace the admin is in; reload them
// rather than keep the old ones (and an old version's files) until a reload.
const pluginPages = usePluginPagesStore()
const refreshPages = () => void pluginPages.ensure(true).catch(() => {})

function upsert(received: InstalledPlugin) {
  const { issuedSecret: secret, ...p } = received
  if (secret) issuedSecret.value = secret
  const i = plugins.value.findIndex((x) => x.id === p.id)
  if (i >= 0) plugins.value.splice(i, 1, p)
  else plugins.value = [...plugins.value, p]
  refreshPages()
}

function remove(id: string) {
  plugins.value = plugins.value.filter((p) => p.id !== id)
  refreshPages()
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
@import (reference) '@/components/css/settings-section.less';
@import (reference) '@/components/css/option-chips.less';

.plugin-admin {
  width: 100%;
}

.section-header {
  .settings-section-header();
  margin-bottom: 20px;

  // Clear of the settings dialog's close button.
  &__top {
    padding-right: 40px;
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

.plugin-admin__toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
}

.plugin-admin__search {
  width: 220px;
}

.option-chips {
  .option-chips();
}

.option-chip {
  .option-chip();

  &__count {
    .option-chip-count();
  }
}

.plugin-table {
  overflow-x: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--app-text-md);
  }

  th {
    padding: 10px 12px;
    text-align: left;
    font-weight: 400;
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
    white-space: nowrap;
  }

  td {
    padding: 10px 12px;
    border-top: 1px solid var(--td-component-stroke);
    color: var(--td-text-color-primary);
    white-space: nowrap;
  }

  th:first-child,
  td:first-child {
    padding-left: 16px;
  }

  &__menu-col {
    width: 48px;
    text-align: right;
  }

  &__empty {
    text-align: center;
    color: var(--td-text-color-placeholder);
  }
}

.plugin-row {
  cursor: pointer;
  transition: background var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: -2px;
  }

  &--off .plugin-badge,
  &--off .plugin-row__names {
    opacity: 0.5;
  }
}

.plugin-row__plugin {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.plugin-row__names {
  min-width: 0;
}

.plugin-row__name {
  font-weight: 500;
}

.plugin-row__id {
  margin-top: 1px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.plugin-row__num {
  font-variant-numeric: tabular-nums;
}

.plugin-row__state {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

// A plugin granted egress that some instance is not held to.
.egress-flag {
  padding: 1px 6px;
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-sm);
  color: var(--td-warning-color);
  background: var(--td-warning-color-1);
}

// Community is the default and stays quiet; official and verified stand out.
.trust {
  color: var(--td-text-color-placeholder);

  &--official,
  &--verified {
    padding: 1px 6px;
    border-radius: var(--app-radius-xs);
    font-size: var(--app-text-sm);
  }

  &--official {
    color: var(--td-success-color);
    background: var(--td-success-color-1);
  }

  &--verified {
    color: var(--td-brand-color);
    background: var(--td-brand-color-1);
  }
}

.state {
  display: inline-flex;
  align-items: center;
  gap: 6px;

  &::before {
    content: '';
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: currentColor;
  }

  &--running {
    color: var(--td-text-color-primary);

    &::before {
      background: var(--td-success-color);
    }
  }

  &--degraded {
    color: var(--td-warning-color);
  }

  &--failed {
    color: var(--td-error-color);
  }

  &--disabled,
  &--pending {
    color: var(--td-text-color-placeholder);
  }
}
</style>
