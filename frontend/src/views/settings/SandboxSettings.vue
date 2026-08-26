<template>
  <div class="sandbox-settings">
    <div class="section-header">
      <div class="section-header__top">
        <div>
          <div class="section-header__titlewrap">
            <h2>{{ $t('settings.sandbox.title') }}</h2>
            <t-popup placement="bottom-start" trigger="hover" :overlay-inner-style="{ maxWidth: '380px' }">
              <button type="button" class="hint-trigger"
                :aria-label="$t('settings.sandbox.pageHintTitle')">
                <t-icon name="info-circle" size="16px" />
              </button>
              <template #content>
                <div class="hint-popover">
                  <p class="hint-popover__title">{{ $t('settings.sandbox.pageHintTitle') }}</p>
                  <p class="hint-popover__text">{{ $t('settings.sandbox.pageHint') }}</p>
                </div>
              </template>
            </t-popup>
          </div>
          <p class="section-description">{{ $t('settings.sandbox.description') }}</p>
        </div>
        <div class="header-actions">
          <a class="header-action-link" :href="sandboxGuideUrl" target="_blank" rel="noopener noreferrer">
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
          @click="openCard(record)" @keydown.enter="openCard(record)">
          <SandboxBackendBadge :type="record.sandbox_type" />
          <div class="sandbox-card__body">
            <div class="sandbox-card__header">
              <h3 class="sandbox-card__title" :title="record.name">{{ record.name }}</h3>
              <t-tag v-if="isLegacyRecord(record)" theme="warning" variant="light" size="small">
                {{ $t('settings.sandbox.legacyConfig') }}
              </t-tag>
              <div class="sandbox-card__actions" @click.stop>
                <t-dropdown
                  :options="cardMenu(record)"
                  placement="bottom-right"
                  attach="body"
                  trigger="click"
                  @click="(data: any) => onMenuAction(data.value, record)"
                >
                  <t-button variant="text" shape="square" size="small" class="sandbox-card__more">
                    <t-icon name="ellipsis" />
                  </t-button>
                </t-dropdown>
              </div>
            </div>
            <div class="sandbox-card__subtitle">
              <span class="sandbox-card__type">{{ backendLabel(record.sandbox_type) }}</span>
              <template v-if="record.description">
                <span class="sandbox-card__sep">·</span>
                <span class="sandbox-card__desc" :title="record.description">{{ record.description }}</span>
              </template>
            </div>
            <div v-if="targetSummary(record)" class="sandbox-card__url" :title="targetSummary(record)">
              {{ targetSummary(record) }}
            </div>
            <div
              v-if="supportsSkills(record) && (skillsOf(record) || skillsFailed(record))"
              class="sandbox-card__skills"
            >
              <template v-if="(skillsOf(record) || []).length">
                <t-tag
                  v-for="skill in visibleSkills(record)"
                  :key="skill.id"
                  size="small"
                  variant="light"
                  :theme="skillTagTheme(skill)"
                  :title="skill.name"
                >{{ skill.name }}</t-tag>
                <span v-if="extraSkillCount(record)" class="sandbox-card__skills-more">
                  {{ $t('settings.sandbox.cardSkillsMore', { count: extraSkillCount(record) }) }}
                </span>
              </template>
              <span v-else-if="skillsFailed(record)" class="sandbox-card__skills-empty">
                {{ $t('settings.sandbox.skillLoadFailed') }}
              </span>
              <span v-else class="sandbox-card__skills-empty">
                {{ $t('settings.sandbox.cardSkillsNone') }}
              </span>
            </div>
            <ul v-if="cardWarnings[record.id]?.length" class="sandbox-card__warnings">
              <li v-for="item in cardWarnings[record.id]" :key="item.key">
                <t-icon name="error-circle" size="12px" />
                <span>{{ item.text }}</span>
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
      :preset-type="activeType === 'all' ? '' : activeType" :initial-step="editorStep"
      :has-in-flight-skill="editorHasInFlightSkill"
      @saved="load" @skills-changed="onSkillsChanged" />

    <!--
      Same SettingDrawer chrome as the skill editor. The list is chat sessions
      that still hold a live sandbox for this config — their titles often look
      like skill names, so the section label has to say "session".
    -->
    <SettingDrawer
      v-model:visible="showInventory"
      :title="$t('settings.sandbox.inventoryTitle')"
      :description="inventorySubtitle"
      width="400px"
      :resizable="false"
      storage-key="setting-drawer:width:sandbox-inventory"
      :hide-footer="inventoryNotice !== 'unverifiable'"
    >
      <t-loading :loading="inventoryLoading" size="small">
        <div v-if="inventoryNotice === 'blocked'" class="inventory-banner">
          <t-alert theme="warning"
            :message="$t('settings.sandbox.sandboxesStillLive', { count: inventory?.sandbox_count ?? 0 })">
            <template #description>
              <p>{{ $t('settings.sandbox.blockedHint') }}</p>
            </template>
          </t-alert>
        </div>
        <div v-else-if="inventory?.unverifiable" class="inventory-banner">
          <t-alert theme="warning" :message="$t('settings.sandbox.inventoryUnverifiableHint')" />
        </div>

        <section class="setting-drawer__section inventory-section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.inventorySessions') }}</h4>
          <ul v-if="inventorySessions.length" class="inventory-list">
            <li v-for="id in inventorySessions" :key="id">
              <button type="button" class="inventory-row" @click="openSession(id)">
                <span class="inventory-row__text">
                  <span class="inventory-row__title" :title="id">{{ sessionTitle(id) }}</span>
                  <span class="inventory-row__meta">{{ $t('settings.sandbox.inventorySessionKind') }}</span>
                </span>
                <t-icon name="chevron-right" size="16px" />
              </button>
            </li>
          </ul>
          <p v-else-if="!inventoryLoading" class="inventory-empty">
            {{ $t('settings.sandbox.inventoryEmpty') }}
          </p>
        </section>

        <section v-if="inventoryAgents.length" class="setting-drawer__section inventory-section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.inventoryAgentsTitle') }}</h4>
          <p class="inventory-agents">{{ inventoryAgents.join('、') }}</p>
        </section>
      </t-loading>

      <!--
        Force delete only surfaces where its justification is on screen: the
        occupancy could not be verified, so the admin decides with the
        unverifiable warning above the button.
      -->
      <template v-if="inventoryNotice === 'unverifiable' && inventoryRecord" #footer-right>
        <t-popconfirm theme="warning" :content="$t('settings.sandbox.forceDeleteConfirm')"
          :confirm-btn="{ content: $t('settings.sandbox.forceDelete'), theme: 'danger' }"
          :cancel-btn="{ content: $t('common.cancel') }" placement="top"
          @confirm="forceRemove(inventoryRecord)">
          <t-button theme="danger">
            {{ $t('settings.sandbox.forceDelete') }}
          </t-button>
        </t-popconfirm>
      </template>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import SandboxConfigEditorDrawer from '@/components/SandboxConfigEditorDrawer.vue'
import SandboxBackendBadge from '@/components/settings/SandboxBackendBadge.vue'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { getSession } from '@/api/chat/index'
import {
  deleteSandboxConfig,
  getSandboxConfigInventory,
  isNamedSandboxBackend,
  listConfigSkills,
  listSandboxConfigs,
  NAMED_SANDBOX_BACKEND_TYPES,
  parseSandboxConflict,
  setSandboxWorkspacePolicy,
  type ConfigSkill,
  type SandboxConfigRecord,
  type SandboxInventory,
} from '@/api/system'

const { t } = useI18n()
const router = useRouter()
const confirmDelete = useConfirmDelete()

const sandboxGuideUrl = 'https://github.com/Tencent/WeKnora/blob/main/docs/sandbox-cluster.md'

const backendTypes = NAMED_SANDBOX_BACKEND_TYPES

const loading = ref(false)
const policySaving = ref(false)
const workspaceScriptsDisabled = ref(false)
const records = ref<SandboxConfigRecord[]>([])
const activeType = ref<string>('all')
// Skill names on cube/e2b cards. Missing until the per-config list returns, so
// the empty hint does not flash on first paint. A failed fetch is tracked
// separately so it is not shown as "no skills installed".
const skillsByConfig = ref<Record<string, ConfigSkill[]>>({})
const skillsLoadFailed = ref<Record<string, boolean>>({})
const SKILL_TAG_LIMIT = 3

const showEditor = ref(false)
const editingRecord = ref<SandboxConfigRecord | null>(null)
// Which page of the editor to open on. Skills live there as the last step, so
// clicking a cube/e2b/docker card lands on that page.
const editorStep = ref<'skills' | undefined>(undefined)

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

// Skills are baked into the snapshot image. Local configs have no skills
// step, so their cards stay a connection summary.
function supportsSkills(record: SandboxConfigRecord) {
  return record.sandbox_type === 'cube' || record.sandbox_type === 'e2b' || record.sandbox_type === 'docker'
}

function skillsOf(record: SandboxConfigRecord): ConfigSkill[] | undefined {
  return skillsByConfig.value[record.id]
}

function skillsFailed(record: SandboxConfigRecord): boolean {
  return skillsLoadFailed.value[record.id] === true
}

function visibleSkills(record: SandboxConfigRecord): ConfigSkill[] {
  return (skillsOf(record) || []).slice(0, SKILL_TAG_LIMIT)
}

function extraSkillCount(record: SandboxConfigRecord): number {
  return Math.max(0, (skillsOf(record) || []).length - SKILL_TAG_LIMIT)
}

function skillTagTheme(skill: ConfigSkill): 'default' | 'warning' | 'primary' {
  if (skill.status === 'failed') return 'warning'
  if (skill.status === 'installing' || skill.status === 'removing') return 'primary'
  return 'default'
}

function skillInFlight(skill: ConfigSkill): boolean {
  return skill.status === 'installing' || skill.status === 'removing'
}

const editorHasInFlightSkill = computed(() => {
  const id = editingRecord.value?.id
  if (!id) return false
  return (skillsByConfig.value[id] || []).some(skillInFlight)
})

const filteredRecords = computed(() => {
  const base = activeType.value === 'all'
    ? records.value
    : records.value.filter((r) => r.sandbox_type === activeType.value)
  return base
})

const countByType = (type: string) =>
  records.value.filter((r) => r.sandbox_type === type && isNamedSandboxBackend(r.sandbox_type)).length

type CardMenuOption = { content: string; value: string; theme?: 'error' }

// Card click already opens skills, and the editor has the connection check,
// so the menu stays edit / running instances / delete.
const cardMenu = (record: SandboxConfigRecord): CardMenuOption[] => {
  if (isLegacyRecord(record)) {
    return [{ content: t('common.delete'), value: 'delete', theme: 'error' }]
  }
  const options: CardMenuOption[] = [
    { content: t('common.edit'), value: 'edit' },
  ]
  if (record.sandbox_type === 'cube' || record.sandbox_type === 'e2b') {
    options.push({ content: t('settings.sandbox.viewSandboxes'), value: 'inventory' })
  }
  options.push({ content: t('common.delete'), value: 'delete', theme: 'error' })
  return options
}

const inventorySubtitle = computed(() => {
  const record = inventoryRecord.value
  if (!record) return ''
  return `${record.name} · ${backendLabel(record.sandbox_type)}`
})

const inventorySessions = computed(() => inventory.value?.session_ids || [])
const inventoryAgents = computed(() => inventory.value?.agent_names || [])

const sessionTitles = ref<Record<string, string>>({})

function sessionTitle(id: string) {
  const title = sessionTitles.value[id]
  if (title) return title
  return t('settings.sandbox.inventoryUntitledSession')
}

async function loadSessionTitles(ids: string[]) {
  const unique = [...new Set(ids.filter(Boolean))]
  if (!unique.length) {
    sessionTitles.value = {}
    return
  }
  const next: Record<string, string> = {}
  await Promise.all(unique.map(async (id) => {
    try {
      const res: any = await getSession(id)
      next[id] = String(res?.data?.title || '').trim()
    } catch {
      next[id] = ''
    }
  }))
  sessionTitles.value = next
}

function openSession(id: string) {
  showInventory.value = false
  router.push(`/platform/chat/${encodeURIComponent(id)}`)
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
  editorStep.value = undefined
  showEditor.value = true
}

function openEdit(record: SandboxConfigRecord) {
  if (isLegacyRecord(record)) return
  editingRecord.value = record
  editorStep.value = undefined
  showEditor.value = true
}

function openSkills(record: SandboxConfigRecord) {
  if (isLegacyRecord(record) || !supportsSkills(record)) return
  editingRecord.value = record
  editorStep.value = 'skills'
  showEditor.value = true
}

// Cube/E2B/Docker cards lead with installed skills, so a click lands on that
// step. Connection and runtime stay behind the card menu's edit action.
function openCard(record: SandboxConfigRecord) {
  if (supportsSkills(record)) {
    openSkills(record)
    return
  }
  openEdit(record)
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

interface CardWarning {
  key: string
  text: string
}

const REMOTE_BACKENDS = new Set(['cube', 'e2b'])

// Cards only surface blockers: healthy defaults (template present, timeout,
// TTL, env count) belong in the editor, not on every tile. Nothing here
// probes a provider; liveness stays behind the card menu.
const cardWarnings = computed<Record<string, CardWarning[]>>(() => {
  const map: Record<string, CardWarning[]> = {}
  for (const record of records.value) {
    map[record.id] = buildCardWarnings(record)
  }
  return map
})

function buildCardWarnings(record: SandboxConfigRecord): CardWarning[] {
  const warnings: CardWarning[] = []
  const config = record.config || {}
  const remote = config.cube || config.e2b

  if (REMOTE_BACKENDS.has(record.sandbox_type)) {
    if (!remote?.template_id?.trim()) {
      warnings.push({
        key: 'template',
        text: t('settings.sandbox.templateNotConfigured'),
      })
    }
    // Cube API keys are optional; only E2B fails at runtime without one.
    if (record.sandbox_type === 'e2b' && !remote?.api_key?.trim()) {
      warnings.push({
        key: 'credential',
        text: t('settings.sandbox.cardCredentialMissing'),
      })
    }
  }
  if (record.sandbox_type === 'docker' && !config.docker?.image?.trim()) {
    warnings.push({
      key: 'image',
      text: t('settings.sandbox.imageNotConfigured'),
    })
  }
  return warnings
}

async function loadSkills(configs: SandboxConfigRecord[]) {
  const remotes = configs.filter(supportsSkills)
  const remoteIDs = new Set(remotes.map((record) => record.id))
  const next: Record<string, ConfigSkill[]> = {}
  for (const [id, skills] of Object.entries(skillsByConfig.value)) {
    if (remoteIDs.has(id)) next[id] = skills
  }
  const failed: Record<string, boolean> = {}
  await Promise.all(remotes.map(async (record) => {
    try {
      const res = await listConfigSkills(record.id)
      next[record.id] = (res?.data || []).filter((skill) => skill.status !== 'removed')
    } catch {
      // Keep a previous successful list. Only the first-load miss is an error
      // row; replacing it with [] would read as "no skills installed".
      if (next[record.id] === undefined) failed[record.id] = true
    }
  }))
  skillsByConfig.value = next
  skillsLoadFailed.value = failed
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
  await loadSkills(records.value)
}

async function onSkillsChanged(record?: SandboxConfigRecord) {
  if (record?.id) {
    records.value = records.value.map((item) => (item.id === record.id ? { ...item, ...record } : item))
  }
  await loadSkills(records.value)
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
    openEdit(record)
    return
  }
  if (action === 'inventory') {
    await openInventory(record)
    return
  }
  if (action === 'delete') {
    void confirmRemove(record)
  }
}

async function confirmRemove(record: SandboxConfigRecord) {
  await onDeleteConfirmOpen(true, record)
  confirmDelete({
    body: deleteConfirmText(record),
    onConfirm: () => removeRecord(record),
  })
}

async function openInventory(record: SandboxConfigRecord) {
  inventoryRecord.value = record
  inventoryNotice.value = ''
  showInventory.value = true
  inventoryLoading.value = true
  inventory.value = null
  sessionTitles.value = {}
  try {
    const res = await getSandboxConfigInventory(record.id)
    inventory.value = res?.data || null
    await loadSessionTitles(inventory.value?.session_ids || [])
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
  void loadSessionTitles(inventory.value.session_ids || [])
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
  padding: 14px 14px 14px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
  min-width: 0;

  &--clickable {
    cursor: pointer;

    &:hover {
      border-color: var(--td-brand-color-3, var(--td-brand-color));
      box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
    }

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
  gap: 4px;
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

.sandbox-card__subtitle {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
  min-width: 0;
}

.sandbox-card__type {
  font-weight: 500;
}

.sandbox-card__sep {
  color: var(--td-text-color-placeholder);
}

.sandbox-card__desc {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.sandbox-card__url {
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 11px;
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}

.sandbox-card__skills {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  margin-top: 2px;
  min-width: 0;

  :deep(.t-tag) {
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}

.sandbox-card__skills-empty,
.sandbox-card__skills-more {
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.4;
}

.sandbox-card__skills-empty:hover,
.sandbox-card__skills:hover .sandbox-card__skills-more {
  color: var(--td-brand-color);
}

.sandbox-card__warnings {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 8px;
  margin: 2px 0 0;
  padding: 0;
  list-style: none;

  li {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--td-warning-color-7, var(--td-warning-color));
    font-size: 12px;
    line-height: 1.4;
  }
}

.sandbox-card__actions {
  flex-shrink: 0;
}

.sandbox-card__more {
  flex-shrink: 0;
  padding: 2px;
  color: var(--td-text-color-placeholder);
  opacity: 0;
  transition: opacity 0.15s ease, color 0.15s ease, background-color 0.15s ease;

  &:hover,
  &:focus-visible {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-secondarycontainer);
  }
}

.sandbox-card:hover .sandbox-card__more,
.sandbox-card:focus-within .sandbox-card__more,
.sandbox-card__actions:focus-within .sandbox-card__more {
  opacity: 1;
}

.sandbox-empty-hint {
  margin: 16px 0 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.inventory-banner {
  margin-bottom: 4px;
}

.inventory-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.inventory-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  margin: 0;
  padding: 6px 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;

  &:hover,
  &:focus-visible {
    background: var(--td-bg-color-container-hover);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 2px;
  }
}

.inventory-row__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.inventory-row__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-primary);
  font-size: 13px;
  line-height: 1.4;
}

.inventory-row__meta {
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.4;
}

.inventory-row .t-icon {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
}

.inventory-empty,
.inventory-agents {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.5;
}
</style>

<style lang="less">
.setting-drawer .inventory-section.setting-drawer__section {
  padding: 0;
  gap: 8px;
  border-bottom: none;
  animation: none;
}
</style>
