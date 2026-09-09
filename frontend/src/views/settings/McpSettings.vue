<template>
  <div class="mcp-settings">
    <div class="section-header">
      <h2>{{ $t('mcpSettings.title') }}</h2>
      <p class="section-description">
        {{ $t('mcpSettings.description') }}
      </p>
    </div>

    <div v-if="loading" class="loading-container">
      <t-loading :text="$t('common.loading')" />
    </div>

    <template v-else>
      <div v-if="services.length === 0 && !authStore.hasRole('admin')" class="empty-state">
        <t-empty :description="$t('mcpSettings.empty')" />
      </div>

      <div v-else class="services-grid">
        <article v-for="service in services" :key="service.id" class="service-card">
          <div class="service-card__main">
            <div class="service-card__body">
              <div class="service-card__header">
                <div class="service-card__badge" aria-hidden="true">
                  <t-icon name="tools" size="14px" />
                </div>
                <h3 class="service-card__title" :title="service.name">{{ service.name }}</h3>
                <span v-if="service.is_builtin" class="service-card__builtin">{{ $t('mcpSettings.builtin') }}</span>
                <div v-if="authStore.hasRole('admin')" class="service-card__actions">
                  <button type="button" class="service-card__icon-btn" :title="$t('common.edit')"
                    :aria-label="`${service.name} · ${$t('common.edit')}`" @click="handleEdit(service)">
                    <t-icon name="edit" size="14px" />
                  </button>
                  <button v-if="!service.is_builtin" type="button" class="service-card__icon-btn service-card__icon-btn--danger"
                    :disabled="togglingIds.has(service.id)" :title="$t('common.delete')"
                    :aria-label="`${service.name} · ${$t('common.delete')}`" @click="handleDelete(service)">
                    <t-icon name="delete" size="14px" />
                  </button>
                </div>
              </div>
              <p v-if="serviceUsage(service)" class="service-card__desc" :title="serviceUsage(service)">
                {{ serviceUsage(service).replace(/\s+/g, ' ') }}
              </p>
              <div v-else class="service-card__empty-usage">
                <button v-if="authStore.hasRole('admin') && !service.is_builtin" type="button"
                  class="service-card__add-usage" @click="handleEdit(service, 1)">
                  <t-icon name="add" size="14px" />
                  {{ $t('mcpSettings.addUsageInstructions') }}
                </button>
                <span v-else>{{ $t('mcpSettings.noUsageInstructions') }}</span>
              </div>
              <div class="service-card__footer">
                <div class="service-card__metadata">
                  <component :is="authStore.hasRole('admin') ? 'button' : 'span'" class="service-card__tools"
                    :type="authStore.hasRole('admin') ? 'button' : undefined"
                    :class="{ 'is-stale': service.catalog?.stale, 'is-missing': !service.catalog }"
                    :title="$t('mcpMetadata.toolsAndUsage')"
                    @click="authStore.hasRole('admin') && handleEdit(service, 1)">
                    <t-icon v-if="service.catalog?.stale" name="error-circle" size="14px" />
                    <span class="service-card__tools-label">
                      {{ service.catalog ? $t('mcpSettings.toolCount', { count: service.catalog.tool_count }) : $t('mcpSettings.toolsNotSynced') }}
                      <template v-if="service.catalog?.stale"> · {{ $t('mcpSettings.toolsStale') }}</template>
                    </span>
                    <t-icon v-if="authStore.hasRole('admin')" name="chevron-right" size="14px" />
                  </component>
                  <span class="service-card__type">{{ getTransportTypeLabel(service.transport_type) }}</span>
                </div>
                <component :is="authStore.hasRole('admin') && !service.is_builtin ? 'button' : 'span'"
                  class="service-card__status" :class="{ 'is-enabled': service.enabled || service.is_builtin }"
                  :type="authStore.hasRole('admin') && !service.is_builtin ? 'button' : undefined"
                  :role="authStore.hasRole('admin') && !service.is_builtin ? 'switch' : undefined"
                  :aria-checked="authStore.hasRole('admin') && !service.is_builtin ? service.enabled : undefined"
                  :aria-label="`${service.name} · ${$t('mcpServiceDialog.enableService')}`"
                  :disabled="togglingIds.has(service.id)"
                  :title="!service.is_builtin && authStore.hasRole('admin') ? $t(service.enabled ? 'common.off' : 'common.on') : undefined"
                  @click="handleToggleEnabled(service)">
                  <t-loading v-if="togglingIds.has(service.id)" size="12px" />
                  <span v-else class="service-card__status-dot" aria-hidden="true" />
                  {{ $t(service.enabled || service.is_builtin ? 'common.on' : 'common.off') }}
                </component>
              </div>
            </div>
          </div>
        </article>
        <button
          v-if="authStore.hasRole('admin')"
          type="button"
          class="service-card service-card--add"
          @click="handleAdd"
        >
          <span class="service-card--add__icon" aria-hidden="true">
            <add-icon />
          </span>
          <span class="service-card--add__label">{{ $t('mcpSettings.addService') }}</span>
        </button>
      </div>
    </template>

    <!-- Add/Edit Drawer -->
    <McpServiceDialog
      v-model:visible="dialogVisible"
      :service="currentService"
      :mode="dialogMode"
      :initial-step="dialogInitialStep"
      @success="handleDialogSuccess"
      @created="handleDialogCreated"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { AddIcon } from 'tdesign-icons-vue-next'
import { useI18n } from 'vue-i18n'
import {
  listMCPServices,
  updateMCPService,
  deleteMCPService,
  type MCPService
} from '@/api/mcp-service'
import McpServiceDialog from './components/McpServiceDialog.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const authStore = useAuthStore()
const confirmDelete = useConfirmDelete()

const services = ref<MCPService[]>([])
const loading = ref(false)
const dialogVisible = ref(false)
const dialogMode = ref<'add' | 'edit'>('add')
const currentService = ref<MCPService | null>(null)
const dialogInitialStep = ref<0 | 1>(0)
const togglingIds = ref(new Set<string>())
const serviceUsage = (service: MCPService) => service.usage_instructions?.trim() || service.description?.trim() || ''

// Load MCP services
const loadServices = async () => {
  loading.value = true
  try {
    services.value = await listMCPServices()
  } catch (error) {
    MessagePlugin.error(t('mcpSettings.toasts.loadFailed'))
    console.error('Failed to load MCP services:', error)
  } finally {
    loading.value = false
  }
}

// Handle add button click
const handleAdd = () => {
  currentService.value = null
  dialogMode.value = 'add'
  dialogInitialStep.value = 0
  dialogVisible.value = true
}

// Explicit buttons keep selecting card text separate from editing a service.
const handleEdit = (service: MCPService, initialStep: 0 | 1 = 0) => {
  if (!authStore.hasRole('admin')) return
  currentService.value = { ...service }
  dialogMode.value = 'edit'
  dialogInitialStep.value = initialStep
  dialogVisible.value = true
}

// Handle dialog success (edit-mode update): close + refresh.
const handleDialogSuccess = () => {
  dialogVisible.value = false
  loadServices()
}

// Handle first create: keep the drawer open and flip it to edit mode bound to
// the newly created service, so OAuth authorization and "test connection"
// (both of which need a saved service id) are usable right away. The list is
// refreshed in the background; we prefer the freshly-fetched record so the
// edit form sees server-side fields (e.g. credential metadata).
const handleDialogCreated = async (created: MCPService) => {
  await loadServices()
  const full = services.value.find((s) => s.id === created.id) || created
  currentService.value = { ...full }
  dialogMode.value = 'edit'
}

// Commit the visible state only after saving; reject duplicate toggles while pending.
const handleToggleEnabled = async (service: MCPService) => {
  if (!authStore.hasRole('admin') || service.is_builtin || !service.id || togglingIds.value.has(service.id)) return
  const enabled = !service.enabled
  togglingIds.value.add(service.id)
  try {
    await updateMCPService(service.id, { enabled })
    service.enabled = enabled
    MessagePlugin.success(enabled ? t('mcpSettings.toasts.enabled') : t('mcpSettings.toasts.disabled'))
  } catch (error) {
    MessagePlugin.error(t('mcpSettings.toasts.updateStateFailed'))
    console.error('Failed to update MCP service:', error)
  } finally {
    togglingIds.value.delete(service.id)
  }
}

// Handle delete button click
const handleDelete = (service: MCPService) => {
  if (!authStore.hasRole('admin') || service.is_builtin || !service.id || togglingIds.value.has(service.id)) return

  confirmDelete({
    body: t('mcpSettings.deleteConfirmBody', { name: service.name || t('mcpSettings.unnamed') }),
    onConfirm: async () => {
      try {
        await deleteMCPService(service.id)
        MessagePlugin.success(t('mcpSettings.toasts.deleted'))
        loadServices()
      } catch (error) {
        MessagePlugin.error(t('mcpSettings.toasts.deleteFailed'))
        console.error('Failed to delete MCP service:', error)
      }
    }
  })
}

// Get transport type label
const getTransportTypeLabel = (transportType: string) => {
  switch (transportType) {
    case 'sse':
      return 'SSE'
    case 'http-streamable':
      return 'HTTP Streamable'
    case 'stdio':
      return 'Stdio'
    default:
      return transportType
  }
}

onMounted(() => {
  loadServices()
})
</script>

<style scoped lang="less">
.mcp-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 28px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.6;
  }
}

.loading-container {
  padding: 40px 0;
  text-align: center;
}

.empty-state {
  padding: 80px 0;
  text-align: center;

  :deep(.t-empty__description) {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    margin-bottom: 16px;
  }
}

.services-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 320px), 1fr));
  gap: 10px;
  align-items: stretch;
}

.service-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  height: 100%;
  padding: 0;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);

  &--add {
    align-items: center;
    justify-content: center;
    gap: 6px;
    min-height: 88px;
    padding: 12px;
    border-style: dashed;
    background: transparent;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    font: inherit;
    text-align: center;
    transition: border-color 0.18s ease, background 0.18s ease;

    &:hover,
    &:focus-visible {
      color: var(--td-brand-color);
      border-color: var(--td-brand-color);
      background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);

      .service-card--add__icon {
        background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
        color: var(--td-brand-color);
      }
    }

    &__icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      border-radius: 8px;
      background: var(--td-bg-color-secondarycontainer);
      color: var(--td-text-color-secondary);
      font-size: 18px;
    }

    &__label {
      font-size: 13px;
      font-weight: 500;
      line-height: 1.4;
    }
  }
}

.service-card__main {
  display: flex;
  align-items: stretch;
  padding: 12px;
  min-width: 0;
  flex: 1;
}

.service-card__badge {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 7px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);

  :deep(.t-icon) {
    display: block;
    line-height: 1;
  }
}

.service-card__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.service-card__header {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  min-height: 28px;
}

.service-card__title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.service-card__builtin {
  flex-shrink: 0;
  font-size: 12px;
  line-height: 1.35;
  color: var(--td-text-color-placeholder);
}

.service-card__type {
  flex-shrink: 0;
  font-size: 11px;
  line-height: 18px;
  color: var(--td-text-color-placeholder);
}

.service-card__actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 2px;
}

.service-card__icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: none;
  color: var(--td-text-color-placeholder);
  cursor: pointer;

  :deep(.t-icon) {
    display: block;
    line-height: 1;
  }

  &:hover:not(:disabled) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &--danger:hover:not(:disabled) {
    color: var(--td-error-color);
    background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.4;
  }
}

.service-card__desc {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  margin: 0;
  overflow: hidden;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  overflow-wrap: anywhere;
}

.service-card__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: auto;
  padding-top: 0;
}

.service-card__empty-usage {
  display: flex;
  align-items: center;
  min-height: calc(2 * 12px * 1.5);
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.5;
}

.service-card__add-usage {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 0;
  border: 0;
  border-radius: 4px;
  background: none;
  color: inherit;
  font: inherit;
  cursor: pointer;

  &:hover { color: var(--td-brand-color); }
}

.service-card__metadata {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 8px;
  min-width: 0;
}

.service-card__tools {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  max-width: 100%;
  padding: 2px 6px;
  border: 0;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: 12px;
  line-height: 18px;
  text-align: left;

  :deep(.t-icon) { flex-shrink: 0; }

  &.is-stale {
    color: var(--td-warning-color);
    background: color-mix(in srgb, var(--td-warning-color) 10%, transparent);
  }

  &.is-missing {
    color: var(--td-text-color-placeholder);
  }
}

button.service-card__tools {
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.service-card__tools-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.service-card__status {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 4px;
  border: 0;
  border-radius: 6px;
  background: none;
  font: inherit;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-placeholder);

  &.is-enabled { color: var(--td-success-color); }
}

button.service-card__status {
  cursor: pointer;

  &:hover:not(:disabled) { background: var(--td-bg-color-container-hover); }
  &:disabled { cursor: wait; }
}

.service-card__status-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
}

.service-card button:focus-visible,
.service-card--add:focus-visible {
  outline: 2px solid var(--td-brand-color);
  outline-offset: -2px;
}
</style>
