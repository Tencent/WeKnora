<template>
  <div class="platform-api-keys">
    <header class="section-header">
      <h2>{{ t('platformApiKeys.title') }}</h2>
      <p class="section-description">{{ t('platformApiKeys.description') }}</p>
    </header>

    <t-alert theme="warning" :message="t('platformApiKeys.securityNotice')" class="security-alert">
      <template #operation>
        <t-button size="small" variant="outline" @click="openCreate">
          <template #icon><t-icon name="add" /></template>
          {{ t('platformApiKeys.create') }}
        </t-button>
      </template>
    </t-alert>

    <section class="keys-section">
      <div v-if="loading" class="keys-state">
        <t-loading size="small" />
        <span>{{ t('platformApiKeys.loading') }}</span>
      </div>
      <div v-else-if="keys.length === 0" class="keys-state keys-state--empty">
        <span>{{ t('platformApiKeys.empty') }}</span>
        <t-button size="small" variant="outline" @click="openCreate">
          <template #icon><t-icon name="add" /></template>
          {{ t('platformApiKeys.create') }}
        </t-button>
      </div>
      <div v-else class="api-key-table-wrap">
        <table class="api-key-table">
          <thead>
            <tr>
              <th>{{ t('platformApiKeys.name') }}</th>
              <th>{{ t('platformApiKeys.key') }}</th>
              <th>{{ t('platformApiKeys.capability') }}</th>
              <th>{{ t('platformApiKeys.lastUsed') }}</th>
              <th>{{ t('platformApiKeys.createdAt') }}</th>
              <th class="api-key-table__actions-heading">{{ t('platformApiKeys.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="key in keys" :key="key.id">
              <td>
                <span class="api-key-name">{{ key.name }}</span>
              </td>
              <td>
                <code class="api-key-fingerprint">{{ key.api_key }}</code>
              </td>
              <td class="api-key-table__capability-cell">
                <div class="api-key-capability-inline">
                  <span
                    v-for="chip in visibleCapabilityChips(key)"
                    :key="chip.id"
                    class="api-key-capability-chip"
                  >
                    {{ chip.label }}
                  </span>
                  <t-popup
                    v-if="hiddenCapabilityCount(key) > 0"
                    trigger="click"
                    placement="bottom-left"
                    destroy-on-close
                    overlay-class-name="platform-api-key-capability-popup-overlay"
                  >
                    <button
                      type="button"
                      class="api-key-capability-chip api-key-capability-chip--more"
                      :aria-label="t('platformApiKeys.viewAllCapabilities')"
                    >
                      {{ t('platformApiKeys.capabilityMore', { count: hiddenCapabilityCount(key) }) }}
                    </button>
                    <template #content>
                      <div class="api-key-capability-popup">
                        <div class="api-key-capability-popup__title">{{ t('platformApiKeys.capability') }}</div>
                        <div
                          v-for="group in capabilityGroupsForKey(key)"
                          :key="group.key"
                          class="api-key-capability-block"
                        >
                          <div class="api-key-capability-block__title">{{ group.label }}</div>
                          <div class="api-key-capability-block__chips">
                            <span
                              v-for="label in group.labels"
                              :key="label"
                              class="api-key-capability-chip"
                            >
                              {{ label }}
                            </span>
                          </div>
                        </div>
                      </div>
                    </template>
                  </t-popup>
                </div>
              </td>
              <td>
                <span class="api-key-meta">{{ formatDate(key.last_used_at) }}</span>
              </td>
              <td>
                <time class="api-key-date" :datetime="key.created_at">{{ formatDate(key.created_at) }}</time>
              </td>
              <td>
                <div class="api-key-table__actions">
                  <t-popconfirm
                    :content="t('platformApiKeys.deleteConfirm', { name: key.name })"
                    :confirm-btn="{ content: t('common.delete'), theme: 'danger' }"
                    :cancel-btn="{ content: t('common.cancel') }"
                    placement="bottom-right"
                    @confirm="deleteKey(key)"
                  >
                    <t-button
                      shape="square"
                      variant="text"
                      theme="danger"
                      :title="t('common.delete')"
                      @click.stop
                    >
                      <t-icon name="delete" />
                    </t-button>
                  </t-popconfirm>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <SettingDrawer
      :visible="drawerVisible"
      class="api-key-create-drawer"
      :title="t('platformApiKeys.create')"
      :description="t('platformApiKeys.createDescription')"
      icon="secured"
      width="560px"
      :min-width="480"
      :max-width="920"
      storage-key="setting-drawer:width:platform-api-key-create"
      :close-on-overlay-click="false"
      :confirm-text="t('platformApiKeys.create')"
      :confirm-loading="creating"
      @update:visible="drawerVisible = $event"
      @confirm="createKey"
    >
      <div class="api-key-dialog">
        <div class="api-key-dialog-row">
          <div class="api-key-dialog-row__label">
            <label>{{ t('platformApiKeys.name') }}</label>
          </div>
          <t-input v-model="form.name" :placeholder="t('platformApiKeys.namePlaceholder')" />
        </div>

        <div class="api-key-dialog-row">
          <div class="api-key-dialog-row__label">
            <label>{{ t('platformApiKeys.capability') }}</label>
          </div>
          <p class="scope-hint">{{ t('platformApiKeys.capabilityHint') }}</p>
          <div class="api-key-capability-list">
            <div
              v-for="group in PLATFORM_API_KEY_CAPABILITY_GROUPS"
              :key="group.key"
              class="api-key-capability-group"
            >
              <div class="api-key-capability-group__header">
                <span>{{ t(group.labelKey) }}</span>
                <t-button
                  size="small"
                  variant="text"
                  @click="toggleGroup(group.capabilities.map(item => item.value))"
                >
                  {{
                    groupSelected(group.capabilities.map(item => item.value))
                      ? t('integrations.api.apiKeyCapabilityClearGroup')
                      : t('integrations.api.apiKeyCapabilitySelectGroup')
                  }}
                </t-button>
              </div>
              <div class="api-key-capability-group__items">
                <div
                  v-for="item in group.capabilities"
                  :key="item.value"
                  class="api-key-capability-item"
                >
                  <t-checkbox v-model="selected[item.value]">{{ t(item.labelKey) }}</t-checkbox>
                  <p class="scope-hint">{{ t(item.hintKey) }}</p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </SettingDrawer>

    <t-dialog
      v-model:visible="tokenVisible"
      :header="t('platformApiKeys.createdTitle')"
      :confirm-btn="{ content: t('platformApiKeys.copy'), theme: 'primary' }"
      :cancel-btn="null"
      :close-on-overlay-click="false"
      @confirm="copyToken"
    >
      <p>{{ t('platformApiKeys.createdDescription') }}</p>
      <t-textarea :value="createdToken" readonly autosize />
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import type { TenantAPIKey, TenantAPIKeyCapability } from '@/api/tenant'
import { createPlatformAPIKey, deletePlatformAPIKey, listPlatformAPIKeys } from '@/api/system'
import {
  PLATFORM_API_KEY_CAPABILITY_GROUPS,
  SYSTEM_API_KEY_CAPABILITIES,
  TENANT_API_KEY_CAPABILITIES,
} from '@/config/apiKeyCapabilities'

const { t } = useI18n()
const allCapabilities = [...SYSTEM_API_KEY_CAPABILITIES, ...TENANT_API_KEY_CAPABILITIES]
const keys = ref<TenantAPIKey[]>([])
const loading = ref(false)
const creating = ref(false)
const drawerVisible = ref(false)
const tokenVisible = ref(false)
const createdToken = ref('')
const form = reactive({ name: '' })
const selected = reactive<Record<TenantAPIKeyCapability, boolean>>(
  allCapabilities.reduce((result, capability) => {
    result[capability] = false
    return result
  }, {} as Record<TenantAPIKeyCapability, boolean>),
)

const VISIBLE_CAPABILITY_CHIP_COUNT = 4

type CapabilityChipView = {
  id: TenantAPIKeyCapability
  label: string
}

function capabilityChipsForKey(key: TenantAPIKey): CapabilityChipView[] {
  const chips: CapabilityChipView[] = []
  for (const group of PLATFORM_API_KEY_CAPABILITY_GROUPS) {
    for (const item of group.capabilities) {
      if (key.capabilities?.includes(item.value)) {
        chips.push({ id: item.value, label: t(item.labelKey) })
      }
    }
  }
  return chips
}

function visibleCapabilityChips(key: TenantAPIKey) {
  return capabilityChipsForKey(key).slice(0, VISIBLE_CAPABILITY_CHIP_COUNT)
}

function hiddenCapabilityCount(key: TenantAPIKey) {
  const total = capabilityChipsForKey(key).length
  return Math.max(0, total - VISIBLE_CAPABILITY_CHIP_COUNT)
}

function capabilityGroupsForKey(key: TenantAPIKey) {
  return PLATFORM_API_KEY_CAPABILITY_GROUPS
    .map(group => {
      const labels = group.capabilities
        .filter(item => key.capabilities?.includes(item.value))
        .map(item => t(item.labelKey))
      if (labels.length === 0) return null
      return { key: group.key, label: t(group.labelKey), labels }
    })
    .filter((group): group is { key: string; label: string; labels: string[] } => group !== null)
}

function formatDate(value?: string) {
  if (!value) return t('platformApiKeys.never')
  const date = new Date(value)
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function resetForm() {
  form.name = ''
  allCapabilities.forEach(capability => { selected[capability] = false })
}

function openCreate() {
  resetForm()
  drawerVisible.value = true
}

function groupSelected(capabilities: TenantAPIKeyCapability[]) {
  return capabilities.every(capability => selected[capability])
}

function toggleGroup(capabilities: TenantAPIKeyCapability[]) {
  const next = !groupSelected(capabilities)
  capabilities.forEach(capability => { selected[capability] = next })
}

async function reload() {
  loading.value = true
  try {
    const response = await listPlatformAPIKeys()
    keys.value = response.data ?? []
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('platformApiKeys.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function createKey() {
  const capabilities = allCapabilities.filter(capability => selected[capability])
  if (!form.name.trim()) {
    MessagePlugin.warning(t('platformApiKeys.nameRequired'))
    return
  }
  if (capabilities.length === 0) {
    MessagePlugin.warning(t('platformApiKeys.capabilityRequired'))
    return
  }
  creating.value = true
  try {
    const response = await createPlatformAPIKey({ name: form.name.trim(), capabilities })
    createdToken.value = response.data?.token ?? ''
    drawerVisible.value = false
    tokenVisible.value = true
    await reload()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('platformApiKeys.createFailed'))
  } finally {
    creating.value = false
  }
}

async function deleteKey(key: TenantAPIKey) {
  try {
    await deletePlatformAPIKey(key.id)
    MessagePlugin.success(t('platformApiKeys.deleteSuccess'))
    await reload()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('platformApiKeys.deleteFailed'))
  }
}

async function copyToken() {
  await navigator.clipboard.writeText(createdToken.value)
  MessagePlugin.success(t('platformApiKeys.copySuccess'))
  tokenVisible.value = false
}

onMounted(reload)
</script>

<style scoped>
.platform-api-keys {
  width: 100%;
}

.section-header {
  margin-bottom: 20px;
}

.section-header h2 {
  margin: 0 0 8px;
  font-size: 20px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.section-description {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 14px;
  line-height: 1.5;
}

.security-alert {
  margin-bottom: 20px;
}

.keys-section {
  border: 1px solid var(--td-component-border);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  overflow: hidden;
}

.keys-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 120px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.keys-state--empty {
  flex-direction: column;
  gap: 12px;
}

.api-key-table-wrap {
  width: 100%;
  overflow-x: auto;
}

.api-key-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.api-key-table th,
.api-key-table td {
  padding: 13px 14px;
  border-bottom: 1px solid var(--td-component-stroke);
  text-align: left;
  vertical-align: middle;
}

.api-key-table th {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-weight: 500;
  line-height: 1.4;
}

.api-key-table td {
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.45;
}

.api-key-table th:nth-child(1),
.api-key-table td:nth-child(1) {
  width: 11%;
}

.api-key-table th:nth-child(2),
.api-key-table td:nth-child(2) {
  width: 17%;
}

.api-key-table th:nth-child(3),
.api-key-table td:nth-child(3) {
  width: auto;
}

.api-key-table th:nth-child(4),
.api-key-table td:nth-child(4) {
  width: 80px;
}

.api-key-table th:nth-child(5),
.api-key-table td:nth-child(5) {
  width: 128px;
}

.api-key-table th:nth-child(6),
.api-key-table td:nth-child(6) {
  width: 52px;
}

.api-key-table__capability-cell {
  vertical-align: middle;
}

.api-key-capability-inline {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 5px;
}

.api-key-capability-chip--more {
  border: 1px dashed color-mix(in srgb, var(--td-success-color) 35%, transparent);
  background: transparent;
  cursor: pointer;
}

.api-key-capability-popup {
  width: 320px;
  max-width: min(360px, 88vw);
  max-height: 360px;
  overflow: auto;
  padding: 12px 14px;
}

.api-key-capability-popup__title {
  margin-bottom: 10px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
  line-height: 1.4;
}

.api-key-table tbody tr:last-child td {
  border-bottom: none;
}

.api-key-table__actions-heading {
  text-align: right !important;
}

.api-key-table__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
}

.api-key-capability-block + .api-key-capability-block {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed var(--td-component-stroke);
}

.api-key-capability-block__title {
  margin-bottom: 6px;
  color: var(--td-text-color-primary);
  font-size: 12px;
  font-weight: 600;
  line-height: 1.4;
}

.api-key-capability-block__chips {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.api-key-capability-chip {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 8px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--td-success-color) 10%, var(--td-bg-color-container));
  color: var(--td-success-color);
  font-size: 12px;
  font-weight: 500;
  line-height: 20px;
  white-space: nowrap;
}

.api-key-name {
  display: block;
  min-width: 0;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.api-key-fingerprint {
  display: inline-block;
  max-width: 100%;
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family-mono);
  font-size: 12px;
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: top;
  white-space: nowrap;
}

.api-key-meta {
  display: block;
  min-width: 0;
  font-size: 12px;
  line-height: 1.4;
  white-space: nowrap;
}

.api-key-date {
  display: block;
  font-family: var(--app-font-family-mono);
  font-size: 12px;
  line-height: 1.4;
  white-space: nowrap;
  color: var(--td-text-color-secondary);
}

.api-key-dialog {
  display: flex;
  flex-direction: column;
  gap: 0;
  padding: 0;
  border-bottom: 1px solid var(--td-component-stroke);
}

.api-key-dialog-row {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.api-key-dialog-row:first-child {
  padding-top: 0;
}

.api-key-dialog-row:last-child {
  border-bottom: none;
}

.api-key-dialog-row__label label {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--td-text-color-primary);
  font-size: 14px;
  font-weight: 600;
  line-height: 1.45;
}

.api-key-dialog-row__label label::before {
  content: '';
  flex-shrink: 0;
  width: 3px;
  height: 14px;
  border-radius: 2px;
  background: var(--td-brand-color);
}

.api-key-dialog-row :deep(.t-input) {
  border-radius: 4px;
  background-color: var(--td-bg-color-secondarycontainer);
  border-color: transparent;
  box-shadow: none !important;
}

.api-key-dialog-row :deep(.t-input:hover),
.api-key-dialog-row :deep(.t-input.t-is-focused) {
  border-color: var(--td-component-border);
  background-color: var(--td-bg-color-container);
}

.scope-hint {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 18px;
}

.api-key-capability-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.api-key-capability-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 0 2px;
}

.api-key-capability-group + .api-key-capability-group {
  border-top: 1px solid var(--td-component-stroke);
}

.api-key-capability-group__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 24px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.api-key-capability-group__items {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.api-key-capability-item .scope-hint {
  margin: 2px 0 0 24px;
}
</style>
