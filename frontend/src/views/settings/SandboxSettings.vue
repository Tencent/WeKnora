<template>
  <div class="sandbox-settings">
    <div class="section-header">
      <div class="section-header__top">
        <div>
          <div class="section-header__titlewrap">
            <h2>{{ $t('settings.sandbox.title') }}</h2>
            <!--
              The standard-template explainer used to be a permanent note block
              above the list, where it pushed the actual configs below the fold.
              It is reference material, so it belongs behind the title's hint.
            -->
            <t-popup placement="bottom-start" trigger="hover" :overlay-inner-style="{ maxWidth: '380px' }">
              <button type="button" class="hint-trigger"
                :aria-label="$t('settings.sandbox.weknoraTemplateTitle')">
                <t-icon name="info-circle" size="16px" />
              </button>
              <template #content>
                <div class="hint-popover">
                  <p class="hint-popover__title">{{ $t('settings.sandbox.weknoraTemplateTitle') }}</p>
                  <p class="hint-popover__text">{{ $t('settings.sandbox.weknoraTemplateOverview') }}</p>
                  <p class="hint-popover__text">{{ $t('settings.sandbox.namedBackendHint') }}</p>
                </div>
              </template>
            </t-popup>
          </div>
          <p class="section-description">{{ $t('settings.sandbox.description') }}</p>
        </div>
        <div class="header-actions">
          <a v-if="FEATURES.showGithubLinks" class="header-action-link" :href="sandboxGuideUrl" target="_blank" rel="noopener noreferrer">
            <t-icon name="help-circle" />
            {{ $t('settings.sandbox.viewClusterGuide') }}
          </a>
        </div>
      </div>
    </div>

    <!--
      Workspace-wide kill switch. It used to be a text button next to the type
      tabs, where it read as a filter action; as a labelled switch row it is
      clearly a persistent workspace setting instead.
    -->
    <div class="setting-row">
      <div class="setting-info">
        <label>{{ $t('settings.sandbox.scriptPolicyLabel') }}</label>
        <p class="desc">{{ $t('settings.sandbox.scriptPolicyDesc') }}</p>
      </div>
      <div class="setting-control">
        <!--
          Only switching execution off needs a confirmation, and binding :value
          one-way means the switch cannot flip until the change is accepted.
        -->
        <t-popconfirm v-if="!workspaceScriptsDisabled" theme="warning"
          :content="$t('settings.sandbox.disableScriptsConfirm')"
          :confirm-btn="{ content: $t('settings.sandbox.disableScripts'), theme: 'danger' }"
          :cancel-btn="{ content: $t('common.cancel') }" placement="left"
          @confirm="setScriptsDisabled(true)">
          <t-switch :value="true" :loading="policySaving" />
        </t-popconfirm>
        <t-switch v-else :value="false" :loading="policySaving" @change="setScriptsDisabled(false)" />
      </div>
    </div>

    <div class="sandbox-tabs-row">
      <t-tabs v-model="activeType" class="sandbox-type-tabs">
        <t-tab-panel value="all" :label="`${$t('common.all')}(${records.length})`" />
        <t-tab-panel v-for="type in backendTypes" :key="type" :value="type"
          :label="`${backendLabel(type)}(${countByType(type)})`" />
      </t-tabs>
    </div>

    <t-loading :loading="loading" size="small" class="sandbox-list-loading">
      <div v-if="!loading" class="sandbox-grid">
        <div v-for="record in filteredRecords" :key="record.id" class="sandbox-card"
          :class="[`sandbox-card--${record.sandbox_type}`, { 'sandbox-card--clickable': !isLegacyRecord(record) }]"
          :role="isLegacyRecord(record) ? undefined : 'button'"
          :tabindex="isLegacyRecord(record) ? undefined : 0"
          @click="openEdit(record)" @keydown.enter="openEdit(record)">
          <SandboxBackendBadge :type="record.sandbox_type" />
          <div class="sandbox-card__body">
            <div class="sandbox-card__header">
              <h3 class="sandbox-card__title">{{ record.name }}</h3>
              <div class="sandbox-card__actions" @click.stop>
                <t-dropdown v-if="!isLegacyRecord(record)" :options="cardMenu(record)" trigger="click" attach="body"
                  placement="bottom-right" @click="(data: any) => onMenuAction(data.value, record)">
                  <t-button variant="text" shape="square" size="small" class="sandbox-card__action-btn">
                    <t-icon name="ellipsis" />
                  </t-button>
                </t-dropdown>
                <t-popconfirm theme="warning" :content="deleteConfirmText(record)"
                  :confirm-btn="{ content: $t('common.delete'), theme: 'danger' }"
                  :cancel-btn="{ content: $t('common.cancel') }" placement="bottom-right"
                  @visible-change="(visible: boolean) => onDeleteConfirmOpen(visible, record)"
                  @confirm="removeRecord(record)">
                  <t-tooltip :content="$t('common.delete')" placement="top">
                    <t-button theme="danger" shape="square" variant="text" size="small"
                      class="sandbox-card__action-btn" @click.stop>
                      <template #icon><t-icon name="delete" /></template>
                    </t-button>
                  </t-tooltip>
                </t-popconfirm>
              </div>
            </div>
            <p class="sandbox-card__subtitle">
              <span>{{ backendLabel(record.sandbox_type) }}</span>
              <t-tag v-if="isLegacyRecord(record)" theme="warning" variant="light" size="small" class="legacy-tag">
                {{ $t('settings.sandbox.legacyConfig') }}
              </t-tag>
              <template v-if="targetSummary(record)">
                <span class="sandbox-card__sep">·</span>
                <span class="sandbox-card__target" :title="targetSummary(record)">{{ targetSummary(record) }}</span>
              </template>
            </p>
            <p v-if="record.description" class="sandbox-card__desc">{{ record.description }}</p>
            <!--
              The card used to spell out the raw template ID, which nobody can
              read or act on. What an admin needs when scanning the list is
              whether the config is complete and how it will behave at runtime.
            -->
            <ul v-if="cardFacts[record.id]?.length" class="sandbox-card__facts">
              <li v-for="fact in cardFacts[record.id]" :key="fact.key" class="sandbox-card__fact"
                :class="{ 'is-warning': fact.tone === 'warning' }" :title="fact.title">
                <t-icon :name="fact.icon" size="12px" />
                <span>{{ fact.text }}</span>
              </li>
            </ul>
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
      :preset-type="activeType === 'all' ? '' : activeType" @saved="load" />

    <!--
      Occupancy is a list of sessions and agents, not a one-line status, and it
      is also what explains a refused delete — so it gets a drawer rather than a
      toast or a cramped dialog.
    -->
    <SettingDrawer v-model:visible="showInventory" :title="$t('settings.sandbox.inventoryTitle')"
      :description="$t('settings.sandbox.inventoryDrawerDesc')" icon="server"
      width="480px" storage-key="setting-drawer:width:sandbox-inventory" hide-footer>
      <t-loading :loading="inventoryLoading" size="small">
        <section class="setting-drawer__section">
          <t-alert v-if="inventoryNotice === 'blocked'" theme="warning"
            :message="$t('settings.sandbox.sandboxesStillLive', { count: inventory?.sandbox_count ?? 0 })">
            <template #description>
              <p>{{ $t('settings.sandbox.blockedHint') }}</p>
            </template>
          </t-alert>
          <t-alert v-else-if="inventory?.unverifiable" theme="warning"
            :message="$t('settings.sandbox.inventoryUnverifiableHint')" />

          <ul v-if="inventory" class="inventory-list">
            <li v-if="inventoryRecord">
              <span class="inventory-label">{{ $t('settings.sandbox.configName') }}</span>
              <span class="inventory-value">{{ inventoryRecord.name }}</span>
            </li>
            <li>
              <span class="inventory-label">{{ $t('settings.sandbox.sandboxCount') }}</span>
              <span class="inventory-value">{{ inventory.unverifiable
                ? $t('settings.sandbox.sandboxCountUnknown')
                : inventory.sandbox_count }}</span>
            </li>
            <li v-if="inventory.session_ids?.length">
              <span class="inventory-label">{{ $t('settings.sandbox.affectedSessions', {
                count: inventory.session_ids.length }) }}</span>
            </li>
            <li v-if="inventory.agent_names?.length">
              <span class="inventory-label">{{ $t('settings.sandbox.affectedAgents', {
                names: inventory.agent_names.join('、') }) }}</span>
            </li>
          </ul>
        </section>

        <!--
          Force delete only surfaces where its justification is on screen: the
          occupancy could not be verified, so the admin decides with the
          unverifiable warning right above the button.
        -->
        <section v-if="inventoryNotice === 'unverifiable' && inventoryRecord" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.forceDeleteTitle') }}</h4>
          <t-popconfirm theme="warning" :content="$t('settings.sandbox.forceDeleteConfirm')"
            :confirm-btn="{ content: $t('settings.sandbox.forceDelete'), theme: 'danger' }"
            :cancel-btn="{ content: $t('common.cancel') }" placement="top"
            @confirm="forceRemove(inventoryRecord)">
            <t-button theme="danger" variant="outline" size="small">
              {{ $t('settings.sandbox.forceDelete') }}
            </t-button>
          </t-popconfirm>
        </section>
      </t-loading>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { FEATURES } from '@/config/branding'
import { computed, onMounted, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SandboxConfigEditorDrawer from '@/components/SandboxConfigEditorDrawer.vue'
import SandboxBackendBadge from '@/components/settings/SandboxBackendBadge.vue'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  checkSandboxConfig,
  deleteSandboxConfig,
  getSandboxConfigInventory,
  isNamedSandboxBackend,
  listSandboxConfigs,
  NAMED_SANDBOX_BACKEND_TYPES,
  parseSandboxConflict,
  setSandboxWorkspacePolicy,
  type SandboxConfigRecord,
  type SandboxInventory,
} from '@/api/system'

const { t } = useI18n()

const sandboxGuideUrl = 'https://github.com/Tencent/WeKnora/blob/main/docs/sandbox-cluster.md'

const backendTypes = NAMED_SANDBOX_BACKEND_TYPES

const loading = ref(false)
const policySaving = ref(false)
const workspaceScriptsDisabled = ref(false)
const records = ref<SandboxConfigRecord[]>([])
const activeType = ref<string>('all')

const showEditor = ref(false)
const editingRecord = ref<SandboxConfigRecord | null>(null)

const showInventory = ref(false)
const inventoryLoading = ref(false)
const inventory = ref<SandboxInventory | null>(null)
const inventoryRecord = ref<SandboxConfigRecord | null>(null)
// Why the drawer is open: a plain look, or a delete the server refused.
const inventoryNotice = ref<'' | 'blocked' | 'unverifiable'>('')
// Agent names per config, filled in when a delete confirmation opens.
const deleteAgents = ref<Record<string, string[]>>({})

const backendLabel = (value: string) => t(`settings.sandbox.backends.${value}`)

const isLegacyRecord = (record: SandboxConfigRecord) => !isNamedSandboxBackend(record.sandbox_type)

const filteredRecords = computed(() => {
  const base = activeType.value === 'all'
    ? records.value
    : records.value.filter((r) => r.sandbox_type === activeType.value)
  return base
})

const countByType = (type: string) =>
  records.value.filter((r) => r.sandbox_type === type && isNamedSandboxBackend(r.sandbox_type)).length

// Legacy rows can only be deleted, and that action has its own button, so they
// get no menu at all.
const cardMenu = (record: SandboxConfigRecord) => {
  const options = [
    { content: t('common.edit'), value: 'edit' },
    { content: t('settings.sandbox.testConnection'), value: 'check' },
  ]
  if (record.sandbox_type === 'cube' || record.sandbox_type === 'e2b') {
    options.push({ content: t('settings.sandbox.viewSandboxes'), value: 'inventory' })
  }
  return options
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

// Agent references never block deletion, but the admin has to see which agents
// will start failing — otherwise the breakage is discovered mid-conversation.
// The lookup happens when the confirmation opens, so the warning appears in the
// same popup without every card probing its backend on page load.
function deleteConfirmText(record: SandboxConfigRecord): string {
  const agents = deleteAgents.value[record.id]
  if (agents?.length) {
    return t('settings.sandbox.confirmDeleteWithAgents', {
      name: record.name,
      agents: t('settings.sandbox.affectedAgents', { names: agents.join('、') }) + ' ',
    })
  }
  return t('settings.sandbox.confirmDelete', { name: record.name })
}

async function onDeleteConfirmOpen(visible: boolean, record: SandboxConfigRecord) {
  if (!visible || deleteAgents.value[record.id]) return
  try {
    const res = await getSandboxConfigInventory(record.id)
    deleteAgents.value = { ...deleteAgents.value, [record.id]: res?.data?.agent_names || [] }
  } catch {
    // An unreachable backend must not stop the admin from trying to delete.
  }
}

function openCreate() {
  editingRecord.value = null
  showEditor.value = true
}

function openEdit(record: SandboxConfigRecord) {
  if (isLegacyRecord(record)) return
  editingRecord.value = record
  showEditor.value = true
}

// What this config actually points at: the remote host, the container image, or
// the local runtime. Two configs of the same backend are told apart by it.
function targetSummary(record: SandboxConfigRecord): string {
  if (record.sandbox_type === 'docker') {
    return record.config?.docker?.image || ''
  }
  if (record.sandbox_type === 'local') {
    return t('settings.sandbox.localRuntimeSummary')
  }
  return endpointHost(record)
}

interface CardFact {
  key: string
  icon: string
  text: string
  /** Full value for opaque identifiers we only summarise on the card. */
  title?: string
  tone?: 'warning'
}

const REMOTE_BACKENDS = new Set(['cube', 'e2b'])

// Facts are derived from the config we already hold, so the list stays cheap:
// nothing here probes a provider. Anything that would need a remote call
// (liveness, running sandboxes) stays behind the card menu.
const cardFacts = computed<Record<string, CardFact[]>>(() => {
  const map: Record<string, CardFact[]> = {}
  for (const record of records.value) {
    map[record.id] = buildCardFacts(record)
  }
  return map
})

function buildCardFacts(record: SandboxConfigRecord): CardFact[] {
  const facts: CardFact[] = []
  const config = record.config || {}
  const remote = config.cube || config.e2b

  // Incomplete configs come first: they are why a skill fails at runtime.
  if (REMOTE_BACKENDS.has(record.sandbox_type)) {
    const templateID = remote?.template_id?.trim() || ''
    facts.push(templateID
      ? {
        key: 'template',
        icon: 'code',
        text: t('settings.sandbox.cardTemplateConfigured'),
        title: templateID,
      }
      : {
        key: 'template',
        icon: 'error-circle',
        text: t('settings.sandbox.templateNotConfigured'),
        tone: 'warning',
      })
    if (!remote?.api_key?.trim()) {
      facts.push({
        key: 'credential',
        icon: 'error-circle',
        text: t('settings.sandbox.cardCredentialMissing'),
        tone: 'warning',
      })
    }
  }
  if (record.sandbox_type === 'docker' && !config.docker?.image?.trim()) {
    facts.push({
      key: 'image',
      icon: 'error-circle',
      text: t('settings.sandbox.imageNotConfigured'),
      tone: 'warning',
    })
  }

  if (config.default_timeout_sec) {
    facts.push({
      key: 'timeout',
      icon: 'time',
      text: t('settings.sandbox.cardTimeout', { sec: config.default_timeout_sec }),
    })
  }
  const ttl = record.config?.cube?.cube_sandbox_ttl_seconds
    || record.config?.e2b?.e2b_sandbox_ttl_seconds
  if (ttl) {
    facts.push({
      key: 'ttl',
      icon: 'hourglass',
      text: t('settings.sandbox.cardTtl', { sec: ttl }),
    })
  }
  if (config.volume_mount?.enabled) {
    facts.push({
      key: 'volume',
      icon: 'folder',
      text: config.volume_mount.volume_name?.trim()
        || config.volume_mount.mount_path?.trim()
        || t('settings.sandbox.cardVolumeMounted'),
    })
  }
  const envCount = Object.keys(config.env_vars || {}).length
  if (envCount) {
    facts.push({
      key: 'env',
      icon: 'code-1',
      text: t('settings.sandbox.cardEnvVars', { count: envCount }),
    })
  }
  // A config allowed to reach private addresses widens the workspace's SSRF
  // surface, so it is called out rather than buried in the form.
  if (config.allow_private_endpoints) {
    facts.push({
      key: 'private',
      icon: 'lock-on',
      text: t('settings.sandbox.cardPrivateEndpoints'),
      tone: 'warning',
    })
  }
  return facts
}

async function load() {
  loading.value = true
  try {
    const res = await listSandboxConfigs()
    records.value = res?.data || []
    workspaceScriptsDisabled.value = res?.workspace_scripts_disabled === true
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.loadFailed'))
  } finally {
    loading.value = false
  }
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
  inventoryRecord.value = record
  inventoryNotice.value = ''
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
    showInventory.value = false
    await load()
  } catch (e: any) {
    const refusal = parseSandboxConflict(e)
    // Both refusals are about what the config still occupies, so both land in
    // the occupancy drawer where that state is spelled out. Counted sandboxes
    // are never overridable — forcing the row away would leave paused instances
    // nobody can reach, billing indefinitely — so only the unverifiable case
    // offers a force option there.
    if (refusal?.code === 'sandboxes_still_live') {
      showRefusal(record, 'blocked', refusal.inventory)
      return
    }
    if (refusal?.code === 'sandbox_inventory_unverifiable') {
      showRefusal(record, 'unverifiable', refusal.inventory)
      return
    }
    MessagePlugin.error(e?.message || t('settings.sandbox.deleteFailed'))
  }
}

function showRefusal(
  record: SandboxConfigRecord,
  notice: 'blocked' | 'unverifiable',
  inv?: SandboxInventory,
) {
  inventoryRecord.value = record
  inventoryNotice.value = notice
  inventory.value = inv || { sandbox_count: 0, unverifiable: notice === 'unverifiable' }
  inventoryLoading.value = false
  showInventory.value = true
}

async function forceRemove(record: SandboxConfigRecord) {
  await removeRecord(record, true)
}

onMounted(load)
</script>

<style lang="less" scoped>
.sandbox-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 20px;

  h2 {
    margin: 0 0 8px;
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }
}

.section-description {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 14px;
  line-height: 1.6;
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

.header-actions {
  display: flex;
  align-items: center;
  gap: 18px;
  flex-shrink: 0;
}

.header-action-link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--td-brand-color);
  font-size: 14px;
  font-weight: 600;
  text-decoration: none;

  &:hover {
    color: var(--td-brand-color-hover);
  }
}

.section-header__titlewrap {
  display: flex;
  align-items: center;
  gap: 6px;

  h2 {
    margin-bottom: 0;
  }
}

.hint-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 2px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: help;
  line-height: 1;

  &:hover,
  &:focus-visible {
    color: var(--td-brand-color);
  }
}

.hint-popover {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 340px;
}

.hint-popover__title {
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.hint-popover__text {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.55;
}

.sandbox-list-loading {
  min-height: 120px;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  padding: 0 0 16px;
  margin-bottom: 8px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.setting-info {
  flex: 1;
  min-width: 0;

  label {
    display: block;
    margin-bottom: 4px;
    color: var(--td-text-color-primary);
    font-size: 14px;
    font-weight: 500;
  }

  .desc {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  display: flex;
  align-items: center;
}

.sandbox-tabs-row {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  margin-bottom: 16px;
}

.sandbox-type-tabs {
  flex: 1;
  min-width: 0;
  margin-bottom: 0;

  :deep(.t-tabs__nav-item) {
    font-size: 13px;
  }

  :deep(.t-tabs__nav-item-wrapper) {
    padding: 0 12px;
    margin: 0;
  }

  :deep(.t-tabs__operations) {
    display: none;
  }

  :deep(.t-tabs__nav-scroll) {
    overflow-x: auto;
    scrollbar-width: none;

    &::-webkit-scrollbar {
      display: none;
    }
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

  &--clickable {
    cursor: pointer;

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }
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

.sandbox-card__target {
  font-family: var(--td-font-family-medium, inherit);
}

.sandbox-card__facts {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 6px;
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
}

.sandbox-card__fact {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  padding: 2px 7px;
  border-radius: 5px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.5;

  span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &.is-warning {
    background: color-mix(in srgb, var(--td-warning-color) 12%, transparent);
    color: var(--td-warning-color-7, var(--td-warning-color));
  }
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
  color: var(--td-text-color-placeholder);
  transition: color 0.15s ease, background-color 0.15s ease;

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-secondarycontainer);
  }
}

.sandbox-empty-hint {
  margin: 16px 0 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.inventory-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 13px;
}

.inventory-list li {
  display: flex;
  align-items: baseline;
  gap: 8px;
  line-height: 1.55;
}

.inventory-label {
  color: var(--td-text-color-secondary);
}

.inventory-value {
  color: var(--td-text-color-primary);
  font-weight: 500;
}
</style>
