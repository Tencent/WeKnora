<template>
  <div class="sandbox-settings">
    <div class="section-header">
      <div class="section-header__top">
        <div>
          <h2>{{ $t('settings.sandbox.title') }}</h2>
          <p class="section-description">{{ $t('settings.sandbox.description') }}</p>
          <p class="section-description section-description--hint">{{ $t('settings.sandbox.namedBackendHint') }}</p>
        </div>
        <t-button theme="primary" variant="text" size="medium" class="defaults-trigger" @click="showDefaults = true">
          <template #icon><t-icon name="info-circle" /></template>
          {{ $t('settings.sandbox.viewDeployDefaults') }}
        </t-button>
      </div>
    </div>

    <div class="sandbox-tabs-row">
      <t-tabs v-model="activeType" class="sandbox-type-tabs">
        <t-tab-panel value="all" :label="`${$t('common.all')}(${records.length})`" />
        <t-tab-panel v-for="type in backendTypes" :key="type" :value="type"
          :label="`${backendLabel(type)}(${countByType(type)})`" />
      </t-tabs>
      <div class="sandbox-tabs-actions">
        <t-button v-if="!workspaceScriptsDisabled" variant="outline" theme="danger" size="medium"
          :loading="policySaving" @click="confirmDisableScripts">
          {{ $t('settings.sandbox.disableScripts') }}
        </t-button>
        <t-button v-else variant="outline" theme="primary" size="medium" :loading="policySaving"
          @click="enableScripts">
          {{ $t('settings.sandbox.enableScripts') }}
        </t-button>
      </div>
    </div>

    <t-loading :loading="loading" size="small" class="sandbox-list-loading">
      <div v-if="!loading" class="sandbox-grid">
        <div v-for="record in filteredRecords" :key="record.id" class="sandbox-card"
          :class="`sandbox-card--${record.sandbox_type}`">
          <div class="sandbox-card__badge">
            <t-icon :name="typeIcon(record.sandbox_type)" size="18px" />
          </div>
          <div class="sandbox-card__body">
            <div class="sandbox-card__header">
              <h3 class="sandbox-card__title">{{ record.name }}</h3>
              <div class="sandbox-card__actions" @click.stop>
                <t-dropdown :options="cardMenu(record)" trigger="click" attach="body" placement="bottom-right"
                  @click="(data: any) => onMenuAction(data.value, record)">
                  <t-button variant="text" shape="square" size="small" class="sandbox-card__action-btn">
                    <t-icon name="ellipsis" />
                  </t-button>
                </t-dropdown>
                <t-tooltip :content="$t('common.delete')" placement="top">
                  <t-button theme="danger" shape="square" variant="text" size="small"
                    class="sandbox-card__action-btn" @click.stop="confirmDelete(record)">
                    <template #icon><t-icon name="delete" /></template>
                  </t-button>
                </t-tooltip>
              </div>
            </div>
            <p class="sandbox-card__subtitle">
              <span>{{ backendLabel(record.sandbox_type) }}</span>
              <t-tag v-if="isLegacyRecord(record)" theme="warning" variant="light" size="small" class="legacy-tag">
                {{ $t('settings.sandbox.legacyConfig') }}
              </t-tag>
              <template v-if="endpointHost(record)">
                <span class="sandbox-card__sep">·</span>
                <span>{{ endpointHost(record) }}</span>
              </template>
            </p>
            <p v-if="record.description" class="sandbox-card__desc">{{ record.description }}</p>
          </div>
        </div>
        <button type="button" class="sandbox-card sandbox-card--add" @click="openCreate">
          <span class="sandbox-card--add__icon" aria-hidden="true"><t-icon name="add" /></span>
          <span class="sandbox-card--add__label">{{ $t('settings.sandbox.addConfig') }}</span>
        </button>
      </div>
      <p v-if="!loading && records.length === 0" class="sandbox-empty-hint">
        {{ $t('settings.sandbox.noConfigs') }}
      </p>
    </t-loading>

    <SandboxConfigEditorDrawer v-model:visible="showEditor" :record="editingRecord"
      :preset-type="activeType === 'all' ? '' : activeType" :defaults="defaults" @saved="load" />

    <t-dialog v-model:visible="showDefaults" :header="$t('settings.sandbox.deployDefaultsTitle')" width="560px"
      :cancel-btn="null" :confirm-btn="{ content: $t('common.close') }" @confirm="showDefaults = false">
      <p class="dialog-hint">{{ $t('settings.sandbox.deployDefaultsHint') }}</p>
      <ul class="defaults-list">
        <li>
          <span class="defaults-label">{{ $t('settings.sandbox.deployDefaultBackend') }}</span>
          <span>{{ defaults?.sandbox_type ? backendLabel(defaults.sandbox_type) : '-' }}</span>
        </li>
        <template v-for="provider in defaultProviders" :key="provider.type">
          <li class="defaults-group">{{ backendLabel(provider.type) }}</li>
          <li v-for="row in provider.rows" :key="row.label">
            <span class="defaults-label">{{ row.label }}</span>
            <span>{{ row.value }}</span>
          </li>
        </template>
      </ul>
    </t-dialog>

    <t-dialog v-model:visible="showInventory" :header="$t('settings.sandbox.inventoryTitle')" width="520px"
      :cancel-btn="null" :confirm-btn="{ content: $t('common.close') }" @confirm="showInventory = false">
      <t-loading :loading="inventoryLoading" size="small">
        <div v-if="inventory" class="inventory">
          <t-alert v-if="inventory.unverifiable" theme="warning"
            :message="$t('settings.sandbox.inventoryUnverifiableHint')" />
          <ul class="defaults-list">
            <li>
              <span class="defaults-label">{{ $t('settings.sandbox.sandboxCount') }}</span>
              <span>{{ inventory.unverifiable
                ? $t('settings.sandbox.sandboxCountUnknown')
                : inventory.sandbox_count }}</span>
            </li>
            <li v-if="inventory.session_ids?.length">
              <span class="defaults-label">{{ $t('settings.sandbox.affectedSessions', {
                count: inventory.session_ids.length }) }}</span>
            </li>
            <li v-if="inventory.agent_names?.length">
              <span class="defaults-label">{{ $t('settings.sandbox.affectedAgents', {
                names: inventory.agent_names.join('、') }) }}</span>
            </li>
          </ul>
        </div>
      </t-loading>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SandboxConfigEditorDrawer from '@/components/SandboxConfigEditorDrawer.vue'
import {
  checkSandboxConfig,
  deleteSandboxConfig,
  getSandboxConfigInventory,
  isNamedSandboxBackend,
  listSandboxConfigs,
  parseSandboxConflict,
  setSandboxWorkspacePolicy,
  type SandboxConfigDefaults,
  type SandboxConfigRecord,
  type SandboxInventory,
} from '@/api/system'

const { t } = useI18n()

const backendTypes = ['cube', 'e2b'] as const

const loading = ref(false)
const policySaving = ref(false)
const workspaceScriptsDisabled = ref(false)
const records = ref<SandboxConfigRecord[]>([])
const defaults = ref<SandboxConfigDefaults | null>(null)
const activeType = ref<string>('all')

const showEditor = ref(false)
const editingRecord = ref<SandboxConfigRecord | null>(null)
const showDefaults = ref(false)

const showInventory = ref(false)
const inventoryLoading = ref(false)
const inventory = ref<SandboxInventory | null>(null)

const backendLabel = (value: string) => t(`settings.sandbox.backends.${value}`)

const typeIcon = (type: string) => {
  if (type === 'docker' || type === 'local') return 'server'
  if (type === 'disabled') return 'minus-circle'
  return 'cloud'
}

const isLegacyRecord = (record: SandboxConfigRecord) => !isNamedSandboxBackend(record.sandbox_type)

const filteredRecords = computed(() => {
  const base = activeType.value === 'all'
    ? records.value
    : records.value.filter((r) => r.sandbox_type === activeType.value)
  return base
})

const countByType = (type: string) =>
  records.value.filter((r) => r.sandbox_type === type && isNamedSandboxBackend(r.sandbox_type)).length

const cardMenu = (record: SandboxConfigRecord) => {
  if (isLegacyRecord(record)) {
    return [{ content: t('common.delete'), value: 'delete-only' }]
  }
  return [
    { content: t('common.edit'), value: 'edit' },
    { content: t('settings.sandbox.testConnection'), value: 'check' },
    { content: t('settings.sandbox.viewSandboxes'), value: 'inventory' },
  ]
}

// The endpoint host is what tells two configs of the same backend apart at a
// glance, which is the whole point of allowing several of them.
function endpointHost(record: SandboxConfigRecord): string {
  const raw = record.config?.e2b?.api_url || record.config?.cube?.api_url || ''
  if (!raw) return ''
  try {
    return new URL(raw).host
  } catch {
    return raw
  }
}

const defaultProviders = computed(() => {
  const groups: { type: string; rows: { label: string; value: string }[] }[] = []
  const describe = (type: 'cube' | 'e2b') => {
    const provider = defaults.value?.[type]
    if (!provider) return
    const rows = [
      { label: t('settings.sandbox.apiUrl'), value: provider.api_url || '-' },
      ...(type === 'cube'
        ? [{ label: t('settings.sandbox.proxyUrl'), value: provider.proxy_url || '-' }]
        : []),
      { label: t('settings.sandbox.sandboxDomain'), value: provider.sandbox_domain || '-' },
      { label: t('settings.sandbox.templateId'), value: provider.template_id || '-' },
      {
        label: t('settings.sandbox.apiKey'),
        value: provider.api_key_configured
          ? t('settings.sandbox.apiKeyConfigured')
          : t('settings.sandbox.apiKeyNotConfigured'),
      },
    ]
    groups.push({ type, rows })
  }
  describe('cube')
  describe('e2b')
  return groups
})

// Agent references never block deletion, but the admin has to see which agents
// will start failing — otherwise the breakage is discovered mid-conversation.
// The inventory is fetched first purely for that warning; the DELETE itself is
// still the authoritative check.
async function confirmDelete(record: SandboxConfigRecord) {
  let agents: string[] = []
  try {
    const res = await getSandboxConfigInventory(record.id)
    agents = res?.data?.agent_names || []
  } catch {
    // An unreachable backend must not stop the admin from trying to delete.
  }
  const body = agents.length
    ? t('settings.sandbox.confirmDeleteWithAgents', {
        name: record.name,
        agents: t('settings.sandbox.affectedAgents', { names: agents.join('、') }) + ' ',
      })
    : t('settings.sandbox.confirmDelete', { name: record.name })
  const dialog = DialogPlugin.confirm({
    header: t('common.confirmDelete'),
    body,
    theme: 'warning',
    confirmBtn: { content: t('common.delete'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      dialog.destroy()
      await removeRecord(record)
    },
  })
}

function openCreate() {
  editingRecord.value = null
  showEditor.value = true
}

async function load() {
  loading.value = true
  try {
    const res = await listSandboxConfigs()
    records.value = res?.data || []
    defaults.value = res?.defaults || null
    workspaceScriptsDisabled.value = res?.workspace_scripts_disabled === true
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.loadFailed'))
  } finally {
    loading.value = false
  }
}

function confirmDisableScripts() {
  const dialog = DialogPlugin.confirm({
    header: t('settings.sandbox.disableScripts'),
    body: t('settings.sandbox.disableScriptsConfirm'),
    theme: 'warning',
    confirmBtn: { content: t('settings.sandbox.disableScripts'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      dialog.destroy()
      await setScriptsDisabled(true)
    },
  })
}

async function enableScripts() {
  await setScriptsDisabled(false)
}

async function setScriptsDisabled(disabled: boolean) {
  policySaving.value = true
  try {
    const res = await setSandboxWorkspacePolicy(disabled)
    workspaceScriptsDisabled.value = res?.workspace_scripts_disabled === true
    MessagePlugin.success(
      disabled ? t('settings.sandbox.scriptsDisabled') : t('settings.sandbox.scriptsEnabled'),
    )
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.policySaveFailed'))
  } finally {
    policySaving.value = false
  }
}

async function onMenuAction(action: string, record: SandboxConfigRecord) {
  if (action === 'delete-only') {
    confirmDelete(record)
    return
  }
  if (action === 'edit') {
    editingRecord.value = record
    showEditor.value = true
    return
  }
  if (action === 'check') {
    await runQuickCheck(record)
    return
  }
  if (action === 'inventory') {
    await openInventory(record)
  }
}

// A card-level probe answers "is this backend still alive" without opening the
// form; the per-probe breakdown and the sandbox-consuming deep check stay in the
// editor, where the config being probed is on screen.
async function runQuickCheck(record: SandboxConfigRecord) {
  try {
    const res = await checkSandboxConfig({ config_id: record.id })
    const result = res?.data
    if (result?.ok) {
      MessagePlugin.success(t('settings.sandbox.checkPassed'))
      return
    }
    const failed = result?.checks?.find((item) => item.ok === false)
    MessagePlugin.error(failed?.message || t('settings.sandbox.checkFailed'))
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.checkFailed'))
  }
}

async function openInventory(record: SandboxConfigRecord) {
  showInventory.value = true
  inventoryLoading.value = true
  inventory.value = null
  try {
    const res = await getSandboxConfigInventory(record.id)
    inventory.value = res?.data || null
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.inventoryFailed'))
    showInventory.value = false
  } finally {
    inventoryLoading.value = false
  }
}

async function removeRecord(record: SandboxConfigRecord, force = false) {
  try {
    await deleteSandboxConfig(record.id, force)
    MessagePlugin.success(t('settings.sandbox.deleted'))
    await load()
  } catch (e: any) {
    const refusal = parseSandboxConflict(e)
    if (refusal?.code === 'sandboxes_still_live') {
      // Counted sandboxes are never overridable: forcing the row away would
      // leave paused instances nobody can reach, billing indefinitely.
      showStillLiveDialog(refusal.inventory)
      return
    }
    if (refusal?.code === 'sandbox_inventory_unverifiable') {
      confirmForceDelete(record)
      return
    }
    MessagePlugin.error(e?.message || t('settings.sandbox.deleteFailed'))
  }
}

function showStillLiveDialog(inv?: SandboxInventory) {
  const lines = [t('settings.sandbox.sandboxesStillLive', { count: inv?.sandbox_count ?? 0 })]
  if (inv?.session_ids?.length) {
    lines.push(t('settings.sandbox.affectedSessions', { count: inv.session_ids.length }))
  }
  lines.push(t('settings.sandbox.blockedHint'))
  const dialog = DialogPlugin.alert({
    header: t('settings.sandbox.inventoryTitle'),
    // A dialog body is plain text, so newlines would collapse.
    body: lines.join(' '),
    confirmBtn: t('common.close'),
    onConfirm: () => dialog.destroy(),
  })
}

// Deleting is the one action with no second way out: an endpoint whose DNS
// record is gone can never be verified again, so the row would be permanently
// undeletable. The admin takes that call explicitly.
function confirmForceDelete(record: SandboxConfigRecord) {
  const dialog = DialogPlugin.confirm({
    header: t('settings.sandbox.forceDeleteTitle'),
    body: t('settings.sandbox.forceDeleteConfirm'),
    theme: 'warning',
    confirmBtn: { content: t('settings.sandbox.forceDelete'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      dialog.destroy()
      await removeRecord(record, true)
    },
  })
}

onMounted(load)
</script>

<style lang="less" scoped>
.sandbox-settings {
  padding: 4px 0;
}

.section-header h2 {
  margin: 0 0 6px;
  font-size: 18px;
  font-weight: 600;
}

.section-description {
  margin: 0 0 20px;
  color: var(--td-text-color-secondary);
  font-size: 13px;

  &--hint {
    margin-top: -12px;
    margin-bottom: 20px;
  }
}

.section-header__top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
}

.defaults-trigger {
  --td-bg-color-container-hover: transparent;
  flex-shrink: 0;
  padding-left: 0;
  padding-right: 0;
  font-weight: 600;
}

.sandbox-list-loading {
  min-height: 120px;
}

.sandbox-tabs-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}

.sandbox-tabs-actions {
  flex-shrink: 0;
  padding-top: 4px;
}

.sandbox-type-tabs {
  flex: 1;
  min-width: 0;
  margin-bottom: 0;

  :deep(.t-tabs__nav-item) {
    font-size: 13px;
  }

  :deep(.t-tabs__content) {
    display: none;
  }
}

.sandbox-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;

  .sandbox-card--add {
    width: 100%;
    height: 100%;
  }
}

.sandbox-card {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
  min-width: 0;

  &:hover {
    border-color: var(--td-brand-color-3, var(--td-brand-color));
    box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
  }

  &--add {
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 68px;
    border-style: dashed;
    background: transparent;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    font: inherit;
    text-align: center;

    &:hover,
    &:focus-visible {
      color: var(--td-brand-color);
      border-color: var(--td-brand-color);
      box-shadow: none;
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }

    &__icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      border-radius: 8px;
      background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
      color: var(--td-brand-color);
      font-size: 18px;
    }

    &__label {
      font-size: 13px;
      font-weight: 500;
      line-height: 1.4;
    }
  }
}

.sandbox-card__badge {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: 1px;
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}

.sandbox-card--e2b .sandbox-card__badge {
  background: rgba(98, 53, 187, 0.1);
  color: #6235BB;
}

.sandbox-card--docker .sandbox-card__badge,
.sandbox-card--local .sandbox-card__badge {
  background: rgba(17, 128, 83, 0.1);
  color: #118053;
}

.sandbox-card__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 2px;
}

.sandbox-card__header {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.sandbox-card__title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sandbox-card__subtitle,
.sandbox-card__desc {
  margin: 2px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sandbox-card__desc {
  color: var(--td-text-color-placeholder);
}

.sandbox-card__sep {
  margin: 0 4px;
  color: var(--td-text-color-placeholder);
}

.legacy-tag {
  margin-left: 6px;
  vertical-align: middle;
}

.sandbox-card__actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 2px;
}

.sandbox-card__action-btn {
  flex-shrink: 0;
  padding: 2px;
  opacity: 0;
  transition: opacity 0.15s ease;
}

.sandbox-card:hover .sandbox-card__action-btn,
.sandbox-card:focus-within .sandbox-card__action-btn {
  opacity: 1;
}

.sandbox-empty-hint {
  margin: 16px 0 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.dialog-hint {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.defaults-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
}

.defaults-group {
  margin-top: 8px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.defaults-label {
  display: inline-block;
  min-width: 120px;
  color: var(--td-text-color-secondary);
}

.inventory {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
</style>
