<template>
  <div class="mcp-list-container">
    <ListSpaceSidebar
      v-model="spaceSelection"
      :count-all="services.length + sharedServices.length"
      :count-mine="localOrgServices.length"
      :count-by-org="sharedCountByOrg"
      :show-favorites="false"
      :show-recents="false"
    />
    <div class="mcp-list-content">
    <div class="section-header">
      <div class="section-header-row">
        <div>
          <h2>{{ $t('mcpSettings.title') }}</h2>
          <p class="section-description">
            {{ $t('mcpSettings.description') }}
          </p>
        </div>
        <t-button
          v-if="authStore.hasRole('admin') && !spaceSelectionOrgId"
          theme="primary"
          class="header-create-btn"
          @click="handleAdd"
        >
          <template #icon><add-icon /></template>
          {{ $t('mcpSettings.addService') }}
        </t-button>
      </div>
    </div>

    <div v-if="loading" class="loading-container">
      <t-loading :text="$t('common.loading')" />
    </div>

    <template v-else>
      <div class="list-section-header">
        <h3>{{ $t('mcpSettings.configuredServices') }}</h3>
        <p>{{ $t('mcpSettings.manageAndTest') }}</p>
      </div>

      <div
        v-if="displayServices.length === 0 && !authStore.hasRole('admin')"
        class="empty-state"
      >
        <t-empty :description="$t('mcpSettings.empty')" />
      </div>

      <div
        v-else-if="displayServices.length === 0 && authStore.hasRole('admin')"
        class="services-grid"
      >
        <button
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

      <div v-else class="services-grid">
        <!-- 与 ModelSettings / WebSearchSettings 同形的卡片：左侧 transport 徽章 +
             标题 / 副标题 / url 三段式。开关挂在标题行右侧，三点菜单 hover 才出现。
             SettingCard 当前没有其它消费者了，但保留组件供未来需要时复用。 -->
        <div
          v-for="service in displayServices"
          :key="service.id"
          class="service-card"
          :class="[
            `service-card--${service.transport_type || 'unknown'}`,
            {
              'service-card--builtin': service.is_builtin,
              'service-card--clickable': isServiceCardClickable(service),
            },
          ]"
          :role="isServiceCardClickable(service) ? 'button' : undefined"
          :tabindex="isServiceCardClickable(service) ? 0 : undefined"
          @click="onServiceCardClick($event, service)"
          @keydown.enter="onServiceCardClick($event, service)"
        >
          <div class="service-card__badge" :aria-label="getTransportTypeLabel(service.transport_type)">
            <t-icon :name="getTransportTypeIcon(service.transport_type)" size="18px" />
          </div>
          <div class="service-card__body">
            <div class="service-card__header">
              <h3 class="service-card__title" :title="service.name">{{ service.name }}</h3>
              <!-- 单一状态徽章：内置优先（builtin 永远启用、不可关），否则用 enabled。 -->
              <span
                v-if="service.is_builtin"
                class="service-card__pill service-card__pill--warning"
              >
                {{ $t('mcpSettings.builtin') }}
              </span>
              <span
                v-else
                class="service-card__status"
                :class="service.enabled ? 'service-card__status--on' : 'service-card__status--off'"
              >
                <span class="service-card__status-dot" />
                {{ service.enabled ? $t('common.on') : $t('common.off') }}
              </span>
              <div
                v-if="(service.is_builtin ? getBuiltinServiceOptions() : getServiceOptions(service)).length > 0"
                class="service-card__actions"
                @click.stop
              >
                <t-dropdown
                  :options="service.is_builtin ? getBuiltinServiceOptions() : getServiceOptions(service)"
                  placement="bottom-right"
                  attach="body"
                  trigger="click"
                  @click="(data: any) => handleMenuAction({ value: data.value }, service)"
                >
                  <t-button variant="text" shape="square" size="small" class="service-card__more">
                    <t-icon name="ellipsis" />
                  </t-button>
                </t-dropdown>
              </div>
            </div>
          </div>
        </article>
        <button
          v-if="authStore.hasRole('admin') && !spaceSelectionOrgId"
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
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { AddIcon } from 'tdesign-icons-vue-next'
import { useI18n } from 'vue-i18n'
import {
  listMCPServices,
  updateMCPService,
  deleteMCPService,
  type MCPService
} from '@/api/mcp-service'
import {
  listSharedMCPServices,
} from '@/api/organization'
import {
  clearExplicitAllOrgsScope,
  ensureStoredOrgUnitFromMembership,
  getStoredOrgUnitId,
  setStoredOrgUnitId,
} from '@/api/org-unit'
import McpServiceDialog from './components/McpServiceDialog.vue'
import ListSpaceSidebar from '@/components/ListSpaceSidebar.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { useAuthStore } from '@/stores/auth'
import { useListUrlState } from '@/composables/useListUrlState'
import {
  ORG_UNIT_CHANGED_EVENT,
} from '@/composables/useWorkspaceScopeLabel'

interface SharedMCPServiceItem {
  mcp_service?: MCPService
  organization_id?: string
  org_name?: string
  permission?: string
  share_id?: string
  source_tenant_id?: number
}

const { t } = useI18n()
const authStore = useAuthStore()
const confirmDelete = useConfirmDelete()

const defaultScope: 'all' | 'mine' =
  authStore.hasRole('admin') || authStore.isSystemAdmin ? 'mine' : 'all'
const { scope: spaceSelection } = useListUrlState({
  defaultScope,
  defaultCreator: 'all',
})

const RESERVED_SCOPES = new Set(['all', 'mine', 'all-orgs'])
const spaceSelectionOrgId = computed(() => {
  const scope = spaceSelection.value
  return !!scope && !RESERVED_SCOPES.has(scope)
})

const services = ref<MCPService[]>([])
const sharedServices = ref<SharedMCPServiceItem[]>([])
const loading = ref(false)
const dialogVisible = ref(false)
const dialogMode = ref<'add' | 'edit'>('add')
const currentService = ref<MCPService | null>(null)
const dialogInitialStep = ref<0 | 1>(0)
const togglingIds = ref(new Set<string>())
const serviceUsage = (service: MCPService) => service.usage_instructions?.trim() || service.description?.trim() || ''

function isLocalOrgUnitService(service: MCPService): boolean {
  if (service.is_builtin) return true
  const activeUnit = getStoredOrgUnitId().trim()
  // 未选组织（超管「所有」）时不过滤，保持整树可见。
  if (!activeUnit) return true
  return (service.org_unit_id?.trim() || '') === activeUnit
}

const localOrgServices = computed(() =>
  services.value.filter(isLocalOrgUnitService),
)

const sharedCountByOrg = computed<Record<string, number>>(() => {
  const counts: Record<string, number> = {}
  sharedServices.value.forEach((item) => {
    const orgId = item.organization_id
    if (!orgId) return
    counts[orgId] = (counts[orgId] || 0) + 1
  })
  return counts
})

const displayServices = computed<MCPService[]>(() => {
  if (spaceSelection.value === 'mine' || spaceSelection.value === 'all-orgs') {
    return localOrgServices.value
  }
  if (spaceSelectionOrgId.value) {
    return sharedServices.value
      .filter((item) => item.organization_id === spaceSelection.value)
      .map((item) => item.mcp_service)
      .filter((svc): svc is MCPService => !!svc)
  }
  // 「全部」：本空间服务 + 共享给我的（按 id 去重，本空间优先）
  const seen = new Set<string>()
  const merged: MCPService[] = []
  services.value.forEach((svc) => {
    seen.add(svc.id)
    merged.push(svc)
  })
  sharedServices.value.forEach((item) => {
    const svc = item.mcp_service
    if (!svc || seen.has(svc.id)) return
    seen.add(svc.id)
    merged.push({ ...svc, can_write: false })
  })
  return merged
})

// Load MCP services
const loadServices = async () => {
  loading.value = true
  try {
    const [owned, sharedRes] = await Promise.all([
      listMCPServices(),
      listSharedMCPServices(),
    ])
    services.value = Array.isArray(owned) ? owned : []
    if (sharedRes.success && Array.isArray(sharedRes.data)) {
      sharedServices.value = sharedRes.data as SharedMCPServiceItem[]
    } else {
      sharedServices.value = []
    }
  } catch (error) {
    MessagePlugin.error(t('mcpSettings.toasts.loadFailed'))
    console.error('Failed to load MCP services:', error)
  } finally {
    loading.value = false
  }
}

watch(spaceSelection, async (val, prev) => {
  if (val === 'all-orgs') {
    if (!authStore.isSystemAdmin) {
      spaceSelection.value = defaultScope
      return
    }
    if (getStoredOrgUnitId().trim()) {
      setStoredOrgUnitId('')
    }
    return
  }
  if (
    prev === 'all-orgs' &&
    authStore.isSystemAdmin &&
    !getStoredOrgUnitId().trim()
  ) {
    clearExplicitAllOrgsScope()
    await ensureStoredOrgUnitFromMembership({ allowAllOrgsDefault: false })
  }
})

const handleOrgUnitChanged = () => {
  void loadServices()
}

// Handle add button click
const handleAdd = () => {
  currentService.value = null
  dialogMode.value = 'add'
  dialogInitialStep.value = 0
  dialogVisible.value = true
}

const isServiceCardClickable = (service?: MCPService) => {
  if (!authStore.hasRole('admin')) return false
  // Ancestor-shared read-only MCP: no edit entry via card click.
  if (service && service.can_write === false) return false
  return true
}

const onServiceCardClick = (event: Event, service: MCPService) => {
  if (!isServiceCardClickable(service)) return
  if (event.type === 'keydown') {
    const ke = event as KeyboardEvent
    if (ke.key !== 'Enter' && ke.key !== ' ') return
    ke.preventDefault()
  }
  const target = event.target as HTMLElement | null
  if (target?.closest('.service-card__actions')) return
  handleEdit(service)
}

// Handle edit button click
const handleEdit = (service: MCPService) => {
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

// Get service options for dropdown menu. MCP service mutations are all
// Admin+ in the backend matrix, so non-Admins see an empty action menu.
// 测试连接已挪到编辑抽屉的 footer，不再放在外层菜单里 — 单一入口减少
// 用户疑惑（"为什么有两个测试入口，结果一样吗？"）。
const getServiceOptions = (service: MCPService) => {
  if (!authStore.hasRole('admin') || service.can_write === false) {
    return []
  }
  return [
    {
      content: service.enabled ? t('common.off') : t('common.on'),
      value: 'toggle',
    },
    { content: t('common.edit'), value: 'edit' },
    { content: t('common.delete'), value: 'delete', theme: 'error' as const }
  ]
}

// Builtin: 仅编辑（同样 Admin+ only）。内置服务测试也通过抽屉的 footer 触发，
// 不再在外层菜单露出"测试连接"项。
const getBuiltinServiceOptions = () => {
  if (!authStore.hasRole('admin')) {
    return []
  }
  return [
    { content: t('common.edit'), value: 'edit' }
  ]
}

// Handle menu action. 'test' has been removed from the menu — testing now
// lives only in the editor drawer. We keep the switch's case list narrow
// so a stray 'test' from somewhere else falls through harmlessly.
const handleMenuAction = (data: { value: string }, service: MCPService) => {
  switch (data.value) {
    case 'toggle':
      // Flip the local model and reuse the toggle path so the API call,
      // optimistic UI, and rollback-on-failure all stay in one place.
      service.enabled = !service.enabled
      handleToggleEnabled(service)
      break
    case 'edit':
      handleEdit(service)
      break
    case 'delete':
      handleDelete(service)
      break
  }
}

// Get transport type icon. 复用 tdesign 自带 icon name；新增 transport 时同步加。
const getTransportTypeIcon = (transportType: string) => {
  switch (transportType) {
    case 'sse':
      return 'cast'
    case 'http-streamable':
      return 'link'
    case 'stdio':
      return 'code'
    default:
      return 'tools'
  }
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
  void loadServices()
  window.addEventListener(ORG_UNIT_CHANGED_EVENT, handleOrgUnitChanged)
})

onUnmounted(() => {
  window.removeEventListener(ORG_UNIT_CHANGED_EVENT, handleOrgUnitChanged)
})
</script>

<style scoped lang="less">
.mcp-list-container {
  margin: 0;
  height: 100%;
  box-sizing: border-box;
  flex: 1;
  display: flex;
  position: relative;
  min-height: 0;
}

.mcp-list-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 20px 28px 32px;
  overflow: auto;
}

.section-header {
  margin-bottom: 28px;

  .section-header-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
  }

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

  .header-create-btn {
    flex-shrink: 0;
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
