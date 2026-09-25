<template>
  <main class="toolbox-page">
    <header class="toolbox-header" style="--wails-draggable: drag">
      <div class="toolbox-title-row" style="--wails-draggable: drag">
        <h2 style="--wails-draggable: drag">
          <ResourceIcon type="toolbox" :size="24" />
          {{ t('toolbox.title') }}
        </h2>
        <t-button v-if="selectedItem && 'action' in selectedItem" variant="text" theme="default" size="small"
          class="toolbox-action" style="--wails-draggable: no-drag" @click="panel?.openAdd?.()">
          <template #icon><t-icon :name="selectedItem.icon" size="16px" /></template>
          {{ t(selectedItem.action) }}
        </t-button>
      </div>
      <p class="toolbox-subtitle" style="--wails-draggable: drag">
        {{ t(selectedItem?.description ?? 'toolbox.description') }}
        <t-tooltip v-if="selectedItem && 'help' in selectedItem" :content="t(selectedItem.help)" placement="bottom"
          overlay-class-name="skill-settings__help-tooltip">
          <t-icon name="help-circle" class="toolbox-help" :aria-label="t(selectedItem.help)" />
        </t-tooltip>
      </p>
    </header>

    <template v-if="visibleItems.length">
      <div class="toolbox-tabs" role="tablist" :aria-label="t('toolbox.title')">
        <button v-for="item in visibleItems" :key="item.key" type="button" role="tab" class="toolbox-tab"
          :aria-selected="selectedItem?.key === item.key" @click="select(item.key)">
          <BrowserIcon v-if="item.key === 'browserconnection'" width="18" height="18" />
          <t-icon v-else :name="item.icon" size="18px" />
          <span>{{ t(item.title) }}</span>
          <span v-if="item.key === 'browserconnection' && browserStatus" class="toolbox-tab-status"
            :class="`is-${browserStatus}`">
            <i aria-hidden="true" />{{ t(`localBrowser.${browserStatus}`) }}
          </span>
          <span v-else-if="item.key !== 'browserconnection' && counts[item.key] !== undefined"
            class="toolbox-tab-count">{{ counts[item.key] }}</span>
        </button>
      </div>

      <section v-if="selectedItem" class="toolbox-main" role="tabpanel">
        <div class="toolbox-panel">
          <SkillSettings v-if="selectedItem.key === 'skills'" ref="panel" :key="sandboxId"
            :initial-sandbox-id="sandboxId" @count="counts.skills = $event" />
          <McpSettings v-else-if="selectedItem.key === 'mcp'" ref="panel" @count="counts.mcp = $event" />
          <BrowserConnectionSettings v-else-if="selectedItem.key === 'browserconnection'" />
        </div>
      </section>
    </template>

    <EmptyState v-else icon="tools" :title="t('toolbox.unavailable')" />
  </main>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useBrowserConnectionStore } from '@/stores/browserConnection'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { listSkillCatalog } from '@/api/skill'
import { listMCPServices } from '@/api/mcp-service'
import {
  TOOLBOX_ITEMS,
  canAccessToolboxSection,
  toolboxLocation,
  type ToolboxSection,
} from '@/config/toolbox'
import ResourceIcon from '@/components/icons/ResourceIcon.vue'
import BrowserIcon from '@/components/icons/BrowserIcon.vue'
import EmptyState from '@/components/EmptyState.vue'
import SkillSettings from '@/views/settings/SkillSettings.vue'
import McpSettings from '@/views/settings/McpSettings.vue'
import BrowserConnectionSettings from '@/views/settings/BrowserConnectionSettings.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const authStore = useAuthStore()
const browserConnection = useBrowserConnectionStore()
const capabilities = useDeploymentCapabilitiesStore()
const panel = ref<{ openAdd?: () => void } | null>(null)
const counts = reactive<Partial<Record<ToolboxSection, number>>>({})
const requestedSection = computed(() => typeof route.params.section === 'string' ? route.params.section : '')
const sandboxId = computed(() => typeof route.query.sandboxId === 'string' ? route.query.sandboxId : '')
const visibleItems = computed(() => TOOLBOX_ITEMS.filter((item) => canAccessToolboxSection(item.key, {
  currentTenantRole: authStore.currentTenantRole,
  canAccessAllTenants: authStore.canAccessAllTenants,
  hasRole: (role) => authStore.hasRole(role),
  isSupported: (capability) => capabilities.isSupported(capability),
})))
const selectedItem = computed(() => visibleItems.value.find((item) => item.key === requestedSection.value))

const browserStatus = computed(() => {
  if (!browserConnection.loaded || !browserConnection.enabled) return ''
  if (browserConnection.connected) return 'connected'
  return browserConnection.device ? 'offline' : 'notPaired'
})

const select = (section: ToolboxSection) => {
  if (section === requestedSection.value) return
  void router.replace(toolboxLocation(section))
}

// The bare /toolbox URL, and tools lost to a role or workspace switch, land on
// the first tool the user can still open.
watch([selectedItem, visibleItems], () => {
  const fallback = visibleItems.value[0]
  if (!selectedItem.value && fallback) void router.replace(toolboxLocation(fallback.key))
}, { immediate: true })

// Tab badges for tools that are not open; the open panel keeps its own badge current.
const summaries: Record<ToolboxSection, () => Promise<void>> = {
  skills: async () => { counts.skills = (await listSkillCatalog())?.data?.length ?? 0 },
  mcp: async () => { counts.mcp = (await listMCPServices()).length },
  browserconnection: async () => { if (!browserConnection.loaded) await browserConnection.refresh() },
}
watch(() => visibleItems.value.map((item) => item.key), (keys, previous = []) => {
  for (const key of keys) {
    if (!previous.includes(key)) summaries[key]().catch(() => {})
  }
}, { immediate: true })
</script>

<style scoped lang="less">
.toolbox-page {
  flex: 1;
  min-width: 0;
  height: 100%;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  margin: 0 16px 0 0;
  padding: 20px 28px 0;
  color: var(--td-text-color-primary);
}

.toolbox-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 12px;
  flex-shrink: 0;

  h2 {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    font-family: var(--app-font-family);
    font-size: var(--app-text-4xl);
    font-weight: 600;
    line-height: 32px;
  }
}

.toolbox-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--app-space-4);
}

.toolbox-action.t-button {
  flex-shrink: 0;
  min-height: 32px;
  padding: 0 12px;
  gap: 6px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  transition: background var(--app-motion-base);

  &:hover:not(:disabled) {
    border-color: var(--td-component-stroke);
    background: var(--td-bg-color-container-hover);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 2px;
  }

  :deep(.t-icon) {
    color: var(--td-brand-color);
  }
}

.toolbox-subtitle {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-base);
  line-height: 20px;
}

.toolbox-help {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);
  cursor: help;

  &:hover {
    color: var(--td-text-color-secondary);
  }
}

.toolbox-tabs {
  flex-shrink: 0;
  display: flex;
  gap: 32px;
  overflow-x: auto;
  border-bottom: 1px solid var(--td-component-stroke);
  scrollbar-width: none;
}

.toolbox-tab {
  position: relative;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 48px;
  padding: 0 2px;
  border: 0;
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-lg);
  font-weight: 500;
  cursor: pointer;
  transition: color var(--app-motion-fast) ease;

  &::after {
    content: '';
    position: absolute;
    left: 0;
    right: 0;
    bottom: -1px;
    height: 2px;
    border-radius: var(--app-radius-pill);
    background: transparent;
    transition: background var(--app-motion-fast) ease;
  }

  &:hover {
    color: var(--td-text-color-primary);
  }

  &[aria-selected='true'] {
    color: var(--td-text-color-primary);

    > .t-icon, > svg {
      color: var(--td-brand-color);
    }

    &::after {
      background: var(--td-brand-color);
    }

    .toolbox-tab-count {
      background: var(--td-brand-color-light);
      color: var(--td-brand-color);
    }
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: -2px;
    border-radius: var(--app-radius-sm);
  }
}

.toolbox-tab-count {
  min-width: 20px;
  height: 20px;
  box-sizing: border-box;
  padding: 0 6px;
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  font-weight: 500;
  line-height: 20px;
  text-align: center;
  font-variant-numeric: tabular-nums;
}

.toolbox-tab-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
  font-weight: 400;

  i {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: currentColor;
  }

  &.is-connected {
    color: var(--td-success-color);
  }

  &.is-offline {
    color: var(--td-warning-color);
  }
}

.toolbox-main {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: var(--app-space-5) 0 var(--app-space-8);
}

.toolbox-panel {
  // Panels are shared with the settings dialog; here the tab and page header
  // already carry their title and description.
  > :deep(* > .section-header) {
    display: none;
  }
}

@media (max-width: 768px) {
  .toolbox-page {
    margin: 0;
    padding: 16px 16px 0;
  }

  .toolbox-tabs {
    gap: 20px;
  }
}
</style>
