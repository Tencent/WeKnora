<template>
  <!--
    SystemSettings — platform-wide tunables (system_settings table) for
    SystemAdmin. Gated server-side by RequireSystemAdmin middleware;
    the route also has meta.requiresSystemAdmin so non-admins never
    reach this component (see frontend/src/router/index.ts).

    Layout follows the outer Toolbox page: header action, contextual
    description, and an icon tabbar with the user search alongside it.
    The settings modal supplies the outer width and padding.

    UI principle: every control auto-persists, no Save button. The
    commit signal differs by control type so the user isn't surprised
    by writes while they're still composing:

      - Switch / Select (single-pick)         → @change. Selecting an
                                                 option IS the commit
                                                 signal; there's no
                                                 "in-progress" state.
      - Input / InputNumber                   → @blur (not @change —
                                                 t-input-number fires
                                                 @change on every digit).
      - SSRF whitelist (string_list)          → controlled tag-input +
                                                 per-tag inline popconfirm.

    auth.registration_mode triggers an
    inline t-popconfirm (same as Reset / bulk-apply) before persisting;
    cancelling rolls the in-progress edit back to the canonical value.
  -->
  <div class="system-settings">
    <div class="section-header">
      <div class="section-header__titlewrap">
        <h2>{{ t('system.globalSettings.title') }}</h2>
        <div id="system-users-actions" />
      </div>
      <div class="section-description">
        <p>{{ activeSectionDescription }}</p>
        <t-popup placement="bottom-start" trigger="hover" :overlay-inner-style="{ maxWidth: '420px' }">
          <button type="button" class="hint-trigger" :aria-label="t('system.globalSettings.priorityHint.disclosure')">
            <t-icon name="info-circle" size="16px" />
          </button>
          <template #content>
            <div class="hint-popover">
              <p class="hint-popover__scope">{{ t('system.globalSettings.description') }}</p>
              <p class="hint-popover__title">{{ t('system.globalSettings.priorityHint.disclosure') }}</p>
              <ul class="hint-popover__list">
                <li>{{ t('system.globalSettings.priorityHint.tier1') }}</li>
                <li>{{ t('system.globalSettings.priorityHint.tier2') }}</li>
                <li>{{ t('system.globalSettings.priorityHint.tier3') }}</li>
              </ul>
            </div>
          </template>
        </t-popup>
        <span v-if="activeSettingsSection === 'runtime'" class="section-description__note">
          {{ t('system.globalSettings.sections.runtime.restartHint') }}
        </span>
      </div>
    </div>

    <div v-if="loading && settings.length === 0" class="loading-state">
      <t-loading :text="t('system.globalSettings.loading')" />
    </div>

    <div v-else-if="settings.length === 0" class="empty-state">
      <t-icon name="info-circle" size="24px" />
      <span>{{ t('system.globalSettings.empty') }}</span>
    </div>

    <template v-else>
      <div class="settings-tabbar">
        <div class="settings-tabs" role="tablist" :aria-label="t('system.globalSettings.title')">
          <button v-for="tab in sectionTabs" :id="`system-settings-tab-${tab.key}`" :key="tab.key"
            type="button" role="tab" class="settings-tab" :data-section="tab.key"
            :aria-selected="activeSettingsSection === tab.key" :tabindex="activeSettingsSection === tab.key ? 0 : -1"
            :aria-controls="tab.key === 'users' ? 'system-settings-users' : 'system-settings-values'"
            @click="showPanel(tab.key)" @keydown="onTabKeydown($event, tab.key)">
            <t-icon :name="tab.icon" size="16px" />
            <span>{{ tab.label }}</span>
          </button>
        </div>
        <div id="system-users-toolbar" class="settings-toolbar" v-show="activeSettingsSection === 'users'" />
      </div>

      <UserManagement id="system-settings-users" role="tabpanel" aria-labelledby="system-settings-tab-users"
        v-show="activeSettingsSection === 'users'" :active="activeSettingsSection === 'users'"
        @announced="saveAnnouncement = $event" />

      <section id="system-settings-values" v-show="activeSettingsSection !== 'users'" role="tabpanel"
        class="settings-section-panel" :aria-labelledby="`system-settings-tab-${activeSettingsSection}`">
        <div class="settings-group">
          <div v-for="item in activeSectionSettings" :key="item.key" class="setting-row">
            <div class="setting-info">
              <div class="setting-label">
                <span>{{ keyLabel(item.key) }}</span>
                <t-tag v-if="item.requires_restart" theme="warning" variant="light" size="small"
                  class="setting-badge">{{
                    t('system.globalSettings.badgeRequiresRestart') }}</t-tag>
                <t-tag v-if="item.is_secret" theme="primary" variant="light" size="small" class="setting-badge">{{
                  t('system.globalSettings.badgeSecret') }}</t-tag>
                <t-tag v-if="isHighImpactKey(item.key)" theme="danger" variant="light" size="small"
                  class="setting-badge">{{
                    t('system.globalSettings.badgeHighRisk') }}</t-tag>
                <t-tag v-if="hasOverride(item)" theme="success" variant="light" size="small" class="setting-badge"
                  :title="t('system.globalSettings.badgeOverrideTooltip')">{{ t('system.globalSettings.badgeOverride')
                  }}</t-tag>
              </div>
              <p v-if="settingDescription(item)" class="desc">{{ settingDescription(item) }}</p>
              <div v-if="modifiedMeta(item)" class="setting-meta">
                {{ t('system.globalSettings.modifiedAt', { value: modifiedMeta(item) }) }}
              </div>
            </div>

            <div class="setting-control">
              <!--
            Two-row layout: input + spinner on top, secondary actions
            (currently just Reset) on a second row below, right-aligned
            under the input. We tried inlining the reset button on the
            same row as the input but the cluster of input + spinner +
            text-button read as visual noise; pushing reset down keeps
            the primary control visually clean while still placing the
            action close to the value it affects.
          -->
              <div class="setting-control-row">
                <t-popconfirm v-if="hasEnum(item) && isHighRiskKey(item.key)"
                  v-model:visible="highRiskPopconfirm.visible" :content="highRiskPopconfirm.content"
                  :theme="highRiskPopconfirm.theme" :confirm-btn="highRiskPopconfirm.confirmBtn"
                  :cancel-btn="t('system.globalSettings.confirm.cancelBtn')"
                  :popup-props="PROGRAMMATIC_POPCONFIRM_PROPS" placement="left"
                  @confirm="highRiskPopconfirm.finish(true)" @cancel="highRiskPopconfirm.finish(false)"
                  @visible-change="highRiskPopconfirm.onVisibleChange">
                  <div class="setting-control-anchor">
                    <t-select v-model="editValues[item.key]" :options="enumOptions(item)"
                      :aria-label="keyLabel(item.key)" :disabled="savingKey === item.key" class="setting-input"
                      @change="onHighRiskSelectChange(item)" />
                  </div>
                </t-popconfirm>
                <t-select v-else-if="hasEnum(item)" v-model="editValues[item.key]" :options="enumOptions(item)"
                  :aria-label="keyLabel(item.key)" :disabled="savingKey === item.key" class="setting-input"
                  @change="onChange(item)" />
                <t-popconfirm v-else-if="item.value_type === 'bool' && isHighRiskKey(item.key)"
                  v-model:visible="highRiskPopconfirm.visible" :content="highRiskPopconfirm.content"
                  :theme="highRiskPopconfirm.theme" :confirm-btn="highRiskPopconfirm.confirmBtn"
                  :cancel-btn="t('system.globalSettings.confirm.cancelBtn')"
                  :popup-props="PROGRAMMATIC_POPCONFIRM_PROPS" placement="left"
                  @confirm="highRiskPopconfirm.finish(true)" @cancel="highRiskPopconfirm.finish(false)"
                  @visible-change="highRiskPopconfirm.onVisibleChange">
                  <div class="setting-control-anchor">
                    <t-switch v-model="editValues[item.key]" :aria-label="keyLabel(item.key)"
                      :disabled="savingKey === item.key" @change="onHighRiskBoolChange(item)" />
                  </div>
                </t-popconfirm>
                <t-switch v-else-if="item.value_type === 'bool'" v-model="editValues[item.key]"
                  :aria-label="keyLabel(item.key)" :disabled="savingKey === item.key" @change="onChange(item)" />
                <t-input-number v-else-if="item.value_type === 'int'" v-model="editValues[item.key]"
                  :placeholder="placeholderFor(item)" :aria-label="keyLabel(item.key)"
                  :disabled="savingKey === item.key" theme="normal" :step="1" :min="minimumFor(item)"
                  class="setting-input" @blur="onChange(item)" />
                <t-popconfirm v-else-if="item.value_type === 'string_list' && item.key === 'ssrf.whitelist'"
                  v-model:visible="ssrfPopconfirm.visible" :content="ssrfPopconfirm.content"
                  :theme="ssrfPopconfirm.theme" :confirm-btn="ssrfPopconfirm.confirmBtn"
                  :cancel-btn="t('system.globalSettings.confirm.cancelBtn')"
                  :popup-props="PROGRAMMATIC_POPCONFIRM_PROPS" placement="left" @confirm="ssrfPopconfirm.finish(true)"
                  @cancel="ssrfPopconfirm.finish(false)" @visible-change="ssrfPopconfirm.onVisibleChange">
                  <div class="setting-control-anchor">
                    <t-tag-input :key="`ssrf-tag-${ssrfTagInputKey()}`" :model-value="ssrfWhitelistModelValue()"
                      :placeholder="emptyListPlaceholder" :aria-label="keyLabel(item.key)"
                      :disabled="savingKey === item.key" class="setting-input setting-input--wide" clearable
                      @update:model-value="onSsrfWhitelistModelUpdate" />
                  </div>
                </t-popconfirm>
                <t-input v-else v-model="editValues[item.key]" :placeholder="placeholderFor(item)"
                  :aria-label="keyLabel(item.key)" :disabled="savingKey === item.key" class="setting-input" clearable
                  @blur="onChange(item)" />

                <!--
            Per-row saving spinner. Appears next to the control while
            a PUT is in flight; the controls stay disabled (see
            :disabled bindings above) so concurrent edits can't race.
          -->
                <div v-if="savingKey === item.key" class="setting-save-state" role="status">
                  <t-loading size="small" />
                  <span>{{ t('system.globalSettings.saving') }}</span>
                </div>
                <div v-else-if="savedKey === item.key" class="setting-save-state setting-save-state--success"
                  role="status">
                  <t-icon name="check-circle-filled" />
                  <span>{{ t('system.globalSettings.saved') }}</span>
                </div>
              </div>

              <!--
            Reset-to-default lives on the row below the input, right-
            aligned under it. Hidden entirely for virtual (ENV / default)
            rows so the layout collapses to a single row in the common
            case — the "已覆盖" badge is already the cue that an
            override exists, so the button only appears where it can do
            something.
          -->
              <div v-if="hasOverride(item) || hasBulkAction(item)" class="setting-control-actions">
                <!--
              Both quota rows apply the effective default (MB takes precedence)
              to every existing tenant. We do this as a separate explicit action rather
              than auto-cascade on save so a SystemAdmin who tweaks the
              default while triaging a single new-tenant question
              doesn't accidentally rewrite production quotas. Disabled
              while either quota row is dirty, so the confirmation
              always describes the saved effective default.
            -->
                <t-popconfirm v-if="hasBulkAction(item)" :content="bulkActionConfirmBody()"
                  :confirm-btn="{ content: t('system.globalSettings.bulkApply.confirmBtn'), theme: 'primary' }"
                  :cancel-btn="{ content: t('system.globalSettings.confirm.cancelBtn') }" placement="left"
                  @confirm="runBulkAction(item)">
                  <t-button variant="text" size="small" :disabled="quotaActionDisabled"
                    :title="t('system.globalSettings.bulkApply.tooltip')" class="setting-bulk-btn">
                    <template #icon><t-icon name="usergroup" /></template>
                    {{ t('system.globalSettings.bulkApply.label') }}
                  </t-button>
                </t-popconfirm>

                <t-popconfirm v-if="hasOverride(item)"
                  :content="t('system.globalSettings.reset.confirmBody', { label: keyLabel(item.key) })"
                  :confirm-btn="{ content: t('system.globalSettings.reset.confirmBtn'), theme: 'warning' }"
                  :cancel-btn="{ content: t('system.globalSettings.confirm.cancelBtn') }" placement="left"
                  @confirm="resetSetting(item)">
                  <t-button variant="text" size="small" :disabled="savingKey === item.key"
                    :title="t('system.globalSettings.reset.tooltip')" class="setting-reset-btn">
                    <template #icon><t-icon name="refresh" /></template>
                    {{ t('system.globalSettings.reset.label') }}
                  </t-button>
                </t-popconfirm>
              </div>
            </div>
          </div>
        </div>
      </section>
      <div class="sr-only" role="status" aria-live="polite">{{ saveAnnouncement }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted, computed, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  listSystemSettings,
  updateSystemSetting,
  resetSystemSetting,
  applyDefaultStorageQuotaToAllTenants,
  type SystemSettingItem,
} from '@/api/system'
import UserManagement from './UserManagement.vue'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { PASSWORD_SPECIAL_CHARS } from '@/utils/passwordPolicy'
import { isSettingValueDirty, resolveCurrentSetting } from './systemSettingsEdit'
import { STORAGE_QUOTA_KEYS, resolveStorageQuota, formatStorageQuota } from './storageQuota'

const deploymentCapabilities = useDeploymentCapabilitiesStore()

const { t, tm, te, locale } = useI18n()
const route = useRoute()
const router = useRouter()

// Friendly labels per key live in i18n (system.globalSettings.keyLabels.*).
// Adding a new entry there must accompany every new key registered in
// service/system_setting.go on the backend; locales without an entry
// fall back to the raw key so a misconfigured deploy still renders.
function keyLabel(k: string): string {
  const path = `system.globalSettings.keyLabels.${k}`
  return te(path) ? (t(path) as string) : k
}

// Descriptions are registered in Chinese on the backend for operator docs;
// user-facing copy lives in i18n (system.globalSettings.keyDescriptions.*).
function settingDescription(item: { key: string; description?: string }): string {
  const path = `system.globalSettings.keyDescriptions.${item.key}`
  if (te(path)) {
    if (path === 'system.globalSettings.keyDescriptions.auth.complex_password_enabled') {
      return t(path, { specialChars: PASSWORD_SPECIAL_CHARS }) as string
    }
    return t(path) as string
  }
  return item.description ?? ''
}

// Enum keys whose change triggers a whole-value inline popconfirm before
// PUT. ssrf.whitelist is not here — it uses per-tag confirm instead.
const HIGH_RISK_KEYS = new Set<string>([
  'auth.registration_mode',
  'sandbox.docker_enabled',
])

const HIGH_IMPACT_KEYS = new Set<string>([
  'auth.registration_mode',
  'tenant.auto_create_api_key',
  'ssrf.whitelist',
  'sandbox.docker_enabled',
])

function isHighRiskKey(key: string): boolean {
  return HIGH_RISK_KEYS.has(key)
}

function isHighImpactKey(key: string): boolean {
  return HIGH_IMPACT_KEYS.has(key)
}

type PopconfirmBtn = { content: string; theme?: 'primary' | 'danger' | 'warning' }

// TDesign popconfirm defaults to trigger:click on its inner Popup. Inputs
// wrapped for programmatic confirm must override that, otherwise focus /
// click on the field opens an empty bubble before the user commits a change.
const PROGRAMMATIC_POPCONFIRM_PROPS = { trigger: 'context-menu' as const }

// Shared inline t-popconfirm controller (anchored to the control row,
// same interaction model as Reset / bulk-apply). Replaces modal dialogs.
// State must be reactive (not nested refs) so template bindings unwrap.
function createInlinePopconfirm() {
  const state = reactive({
    visible: false,
    content: '',
    theme: 'warning' as 'default' | 'warning' | 'danger',
    confirmBtn: { content: '', theme: 'primary' } as PopconfirmBtn,
  })
  let resolver: ((ok: boolean) => void) | null = null
  let settled = false

  function ask(opts: {
    content: string
    theme?: 'default' | 'warning' | 'danger'
    confirmBtn: PopconfirmBtn
  }): Promise<boolean> {
    state.content = opts.content
    state.theme = opts.theme ?? 'warning'
    state.confirmBtn = opts.confirmBtn
    settled = false
    return new Promise((resolve) => {
      resolver = resolve
      state.visible = true
    })
  }

  function finish(ok: boolean) {
    if (settled) return
    settled = true
    state.visible = false
    const r = resolver
    resolver = null
    r?.(ok)
  }

  function onVisibleChange(v: boolean) {
    if (!v && resolver) finish(false)
  }

  return Object.assign(state, { ask, finish, onVisibleChange })
}

const ssrfPopconfirm = createInlinePopconfirm()
const highRiskPopconfirm = createInlinePopconfirm()

// Friendly labels for enum options live in i18n
// (system.globalSettings.enumLabels.<key>.<value>). Falls back to the
// raw enum value when no translation exists.
function enumLabel(itemKey: string, optionValue: string): string {
  const path = `system.globalSettings.enumLabels.${itemKey}.${optionValue}`
  return te(path) ? (t(path) as string) : optionValue
}

const emptyListPlaceholder = computed(() => t('system.globalSettings.tagInputPlaceholder'))

const settings = ref<SystemSettingItem[]>([])
const loading = ref(false)
const savingKey = ref<string | null>(null)
const savedKey = ref<string | null>(null)
const saveAnnouncement = ref('')
let savedKeyTimer: ReturnType<typeof setTimeout> | null = null

type SettingsSection = 'users' | 'access' | 'tenant' | 'runtime' | 'security' | 'other'

// Product-oriented order, rather than the registry's alphabetical key order.
// Unknown/out-of-band rows remain visible in a conditional "Other" tab so the
// backend's diagnostic contract is preserved when a deployment contains an
// unexpected key.
const SETTINGS_SECTION_KEYS: Record<Exclude<SettingsSection, 'other' | 'users'>, readonly string[]> = {
  access: [
    'auth.registration_mode',
    'auth.complex_password_enabled',
    'auth.default_tenant_mode',
    'tenant.self_service_creation_enabled',
    'tenant.max_owned_per_user',
  ],
  tenant: [
    'tenant.default_storage_quota_gb',
    'tenant.default_storage_quota_mb',
    'tenant.auto_create_api_key',
    'tenant.auto_accept_invitation',
  ],
  runtime: [
    'asynq.core_concurrency',
    'asynq.enrichment_concurrency',
    'asynq.postprocess_concurrency',
    'asynq.maintenance_concurrency',
    'asynq.shared_concurrency',
    'asynq.wiki_concurrency',
    'model.max_concurrency',
  ],
  security: ['ssrf.whitelist', 'sandbox.docker_enabled'],
}

const activeSettingsSection = ref<SettingsSection>('access')
const knownSettingKeys = new Set(Object.values(SETTINGS_SECTION_KEYS).flat())
const settingsByKey = computed<Map<string, SystemSettingItem>>(() => new Map(settings.value.map((item) => [item.key, item])))
const unknownSettings = computed(() => settings.value.filter((item) => !knownSettingKeys.has(item.key)))
const hasUnknownSettings = computed(() => unknownSettings.value.length > 0)

const sectionTabs = computed(() => {
  const tabs: { key: SettingsSection; icon: string; label: string }[] = [
    { key: 'access', icon: 'lock-on', label: t('system.globalSettings.sections.access.title') },
    { key: 'users', icon: 'usergroup', label: t('system.globalSettings.userList.rowLabel') },
    { key: 'tenant', icon: 'folder', label: t('system.globalSettings.sections.tenant.title') },
    { key: 'runtime', icon: 'server', label: t('system.globalSettings.sections.runtime.title') },
    { key: 'security', icon: 'secured', label: t('system.globalSettings.sections.security.title') },
  ]
  if (hasUnknownSettings.value) tabs.push({ key: 'other', icon: 'ellipsis', label: t('system.globalSettings.sections.other.title') })
  return tabs
})

const PANEL_KEYS: readonly SettingsSection[] = ['access', 'users', 'tenant', 'runtime', 'security', 'other']

function panelFromQuery(): SettingsSection | null {
  const raw = Array.isArray(route.query.panel) ? route.query.panel[0] : route.query.panel
  return typeof raw === 'string' && (PANEL_KEYS as readonly string[]).includes(raw) ? raw as SettingsSection : null
}

/** Follows ?panel= so a navigated link can open a tab other than the first. */
function applyRoutePanel() {
  const panel = panelFromQuery()
  if (!panel || panel === activeSettingsSection.value) return
  if (panel === 'other' && !hasUnknownSettings.value) return
  activeSettingsSection.value = panel
}

function showPanel(key: SettingsSection) {
  activeSettingsSection.value = key
  if (route.path !== '/platform/settings' || route.query.section !== 'system-global') return
  if (route.query.panel === key) return
  void router.replace({ path: route.path, query: { ...route.query, panel: key } })
}

function onTabKeydown(event: KeyboardEvent, key: SettingsSection) {
  const tabs = sectionTabs.value
  const index = tabs.findIndex(tab => tab.key === key)
  let next: number
  switch (event.key) {
    case 'ArrowRight': next = (index + 1) % tabs.length; break
    case 'ArrowLeft': next = (index - 1 + tabs.length) % tabs.length; break
    case 'Home': next = 0; break
    case 'End': next = tabs.length - 1; break
    default: return
  }
  event.preventDefault()
  showPanel(tabs[next]!.key)
  const tablist = (event.currentTarget as HTMLElement).parentElement
  tablist?.querySelector<HTMLButtonElement>(`[data-section="${activeSettingsSection.value}"]`)?.focus()
}

watch(() => route.query.panel, applyRoutePanel, { immediate: true })

watch(hasUnknownSettings, (hasUnknown) => {
  if (!hasUnknown && activeSettingsSection.value === 'other') {
    showPanel('access')
    return
  }
  applyRoutePanel()
})

const activeSectionSettings = computed(() => {
  if (activeSettingsSection.value === 'users') return []
  if (activeSettingsSection.value === 'other') return unknownSettings.value
  return SETTINGS_SECTION_KEYS[activeSettingsSection.value]
    .map((key) => settingsByKey.value.get(key))
    .filter((item): item is SystemSettingItem => Boolean(item))
})

const activeSectionDescription = computed(() =>
  activeSettingsSection.value === 'users' ? t('system.globalSettings.userList.rowDesc')
    : t(`system.globalSettings.sections.${activeSettingsSection.value}.description`),
)


function markSettingSaved(item: SystemSettingItem) {
  savedKey.value = item.key
  saveAnnouncement.value = t('system.globalSettings.saveAnnouncement', {
    label: keyLabel(item.key),
  })
  if (savedKeyTimer) clearTimeout(savedKeyTimer)
  savedKeyTimer = setTimeout(() => {
    if (savedKey.value === item.key) savedKey.value = null
    savedKeyTimer = null
  }, 2000)
}

// Guards ssrf.whitelist while an async confirm roundtrip is in flight.
const listConfirmBusyKey = ref<string | null>(null)

// Bumped when the SSRF tag-input is snapped back to the saved list so
// Vue remounts the control and clears TDesign's internal tag state.
const ssrfTagInputKeys = reactive<Record<string, number>>({})

// Briefly blocks model updates while the SSRF tag-input remount settles.
const ssrfSnapLocked = ref(false)

// Reactive map of in-progress edits, keyed by setting key. We don't
// mutate the canonical `settings` array directly so a failed save
// leaves the original value visible until the user retries or refreshes.
// Initialised lazily in loadSettings; setting.value is the JSON-decoded
// form (number / boolean / string / string[]).
const editValues = reactive<Record<string, unknown>>({})

function hasEnum(item: SystemSettingItem): boolean {
  return Array.isArray(item.enum) && item.enum.length > 0
}

function enumOptions(item: SystemSettingItem): { label: string; value: string }[] {
  const opts = item.enum ?? []
  return opts.map((v) => ({ label: enumLabel(item.key, v), value: v }))
}

// hasOverride reports whether the row carries a real DB override (vs a
// virtual row backed by ENV/default). Distinguishing these is what
// `last_modified_by` was made for: empty string means the value came
// from registry/ENV. Drives the "已覆盖" badge.
function hasOverride(item: SystemSettingItem): boolean {
  return Boolean(item.last_modified_by && item.last_modified_by.trim() !== '')
}

// modifiedMeta returns a humane "上次修改" line for rows that have been
// persisted (last_modified_by non-empty AND updated_at not the Go zero
// value). Returns '' for virtual rows so the meta line collapses
// entirely instead of rendering "1/1/1 08:05:43" garbage.
function modifiedMeta(item: SystemSettingItem): string {
  if (!hasOverride(item)) return ''
  const ts = item.updated_at
  if (!ts || ts.startsWith('0001-')) return ''
  const formatted = formatDate(ts)
  // Prefer the resolved username/email the server enriches via
  // last_modified_by_name. Fall back to the UUID's first 8 chars when
  // the user can't be resolved (deleted account, transient lookup
  // failure) — the full ID is still in the audit log.
  const actor = item.last_modified_by_name && item.last_modified_by_name.trim() !== ''
    ? item.last_modified_by_name
    : (item.last_modified_by || '').slice(0, 8)
  return `${formatted} · ${actor}`
}

const SSRF_WHITELIST_KEY = 'ssrf.whitelist'

function ssrfWhitelistModelValue(): string[] {
  const v = editValues[SSRF_WHITELIST_KEY]
  return Array.isArray(v) ? (v as string[]) : []
}

function ssrfTagInputKey(): number {
  return ssrfTagInputKeys[SSRF_WHITELIST_KEY] ?? 0
}

function resetSsrfTagInput() {
  ssrfTagInputKeys[SSRF_WHITELIST_KEY] = (ssrfTagInputKeys[SSRF_WHITELIST_KEY] ?? 0) + 1
}

function globalSettingsText(path: string, params?: Record<string, string>): string {
  if (!te(path)) return path
  const msg = params ? t(path, params) : t(path)
  return typeof msg === 'string' ? msg : path
}

// Controlled SSRF tag-input: we commit editValues so a declined delta
// can be rolled back without the component re-applying a removal.
function onSsrfWhitelistModelUpdate(next: string[]) {
  if (listConfirmBusyKey.value === SSRF_WHITELIST_KEY || ssrfSnapLocked.value) return
  editValues[SSRF_WHITELIST_KEY] = next
  void onSsrfWhitelistChange()
}

async function onSsrfWhitelistChange() {
  const item = settings.value.find((s) => s.key === SSRF_WHITELIST_KEY)
  if (!item || !isDirty(item)) return
  if (listConfirmBusyKey.value === SSRF_WHITELIST_KEY) return
  await handleSSRFListChange(item)
}

async function snapSsrfWhitelistToSaved(item: SystemSettingItem) {
  const saved = Array.isArray(item.value) ? (item.value as string[]) : []
  editValues[SSRF_WHITELIST_KEY] = [...saved]
  resetSsrfTagInput()
  ssrfSnapLocked.value = true
  await nextTick()
  await nextTick()
  ssrfSnapLocked.value = false
}

function isDirty(item: SystemSettingItem): boolean {
  return isSettingValueDirty(editValues[item.key], item.value)
}

function formatDate(isoString: string): string {
  try {
    const d = new Date(isoString)
    return d.toLocaleString('zh-CN', { hour12: false })
  } catch {
    return isoString
  }
}

// placeholderFor renders the current effective value (DB / ENV / default)
// as a placeholder hint inside the edit control. For string_list it's
// joined with comma; for booleans we show nothing (the switch already
// reflects the value).
function placeholderFor(item: SystemSettingItem): string {
  const v = item.value
  if (v === null || v === undefined) return ''
  if (Array.isArray(v)) return v.join(', ')
  return String(v)
}

function minimumFor(item: SystemSettingItem): number {
  if (item.key.startsWith('asynq.') && item.key.endsWith('_concurrency')) return 1
  return 0
}

async function loadSettings() {
  loading.value = true
  try {
    const list = await listSystemSettings()
    settings.value = list
    // Reset edit values to the canonical state on every load — no
    // partial drafts survive a refresh, which avoids the "I came back
    // and my unsaved edits look saved" trap.
    for (const item of list) {
      // Defensive copy for arrays so the t-tag-input doesn't mutate
      // the canonical settings entry through the v-model binding.
      editValues[item.key] = Array.isArray(item.value)
        ? [...(item.value as unknown[])]
        : item.value
    }
  } catch (err: any) {
    const msg = err?.message || t('system.globalSettings.messages.loadFailed')
    MessagePlugin.error(msg)
  } finally {
    loading.value = false
  }
}

// onChange persists non-SSRF settings. SSRF whitelist and system admins
// have dedicated handlers with inline popconfirm.
async function onChange(item: SystemSettingItem) {
  const currentItem = resolveCurrentSetting(settingsByKey.value, item.key)
  if (!currentItem) return
  if (!isDirty(currentItem)) return

  // SSRF whitelist gets the per-entry confirm flow — same shape as the
  // admin tag-input above. Adding or removing each host/CIDR is its
  // own privileged change (a single bad CIDR can punch a hole through
  // the egress firewall), so we ask once per delta instead of once
  // per "save". This matches the operator's mental model: every tag
  // they touch is acknowledged on its own.
  await persistSetting(currentItem)
}

async function onHighRiskSelectChange(item: SystemSettingItem) {
  const currentItem = resolveCurrentSetting(settingsByKey.value, item.key)
  if (!currentItem) return
  const newValue = editValues[item.key]
  if (!isDirty(currentItem)) return

  // Revert the select immediately so cancel leaves the saved value
  // visible; re-apply only after the inline popconfirm is confirmed.
  editValues[item.key] = currentItem.value

  const ok = await highRiskPopconfirm.ask({
    content: highRiskConfirmBody(currentItem, newValue),
    theme: 'danger',
    confirmBtn: {
      content: t('system.globalSettings.confirm.confirmBtn'),
      theme: 'danger',
    },
  })
  if (!ok) return

  editValues[item.key] = newValue
  await persistSetting(currentItem)
}

async function onHighRiskBoolChange(item: SystemSettingItem) {
  const currentItem = resolveCurrentSetting(settingsByKey.value, item.key)
  if (!currentItem) return
  const newValue = editValues[item.key]
  if (!isDirty(currentItem)) return

  editValues[item.key] = currentItem.value
  if (newValue !== true) {
    editValues[item.key] = newValue
    await persistSetting(currentItem)
    return
  }

  const ok = await highRiskPopconfirm.ask({
    content: t('system.globalSettings.confirm.bodySandboxDockerEnabled'),
    theme: 'danger',
    confirmBtn: {
      content: t('system.globalSettings.confirm.confirmBtn'),
      theme: 'danger',
    },
  })
  if (!ok) return

  editValues[item.key] = true
  await persistSetting(currentItem)
}

function confirmSsrfListEntryChange(
  action: 'add' | 'remove',
  entry: string,
): Promise<boolean> {
  const base = `system.globalSettings.listConfirm.${SSRF_WHITELIST_KEY}.${action}`
  return ssrfPopconfirm.ask({
    content: globalSettingsText(`${base}.body`, { entry }),
    theme: action === 'add' ? 'danger' : 'warning',
    confirmBtn: {
      content: globalSettingsText(`${base}.confirmBtn`),
      theme: action === 'add' ? 'danger' : 'primary',
    },
  })
}

// handleSSRFListChange reconciles the current edit against the saved
// list one entry at a time. The strategy is "confirmed deltas only":
// we start from the saved value, then walk the user's added/removed
// sets and apply each entry the operator individually approves. If
// every prompt is declined we end up identical to the saved value
// and short-circuit before hitting the API. Otherwise we save the
// merged result in a single PUT so the audit log and pubsub get one
// coherent post-image (instead of N noisy events).
async function handleSSRFListChange(item: SystemSettingItem) {
  listConfirmBusyKey.value = item.key
  try {
    const oldArr = Array.isArray(item.value) ? (item.value as string[]) : []
    const nextArr = Array.isArray(editValues[item.key])
      ? (editValues[item.key] as string[])
      : []

    const oldSet = new Set(oldArr)
    const nextSet = new Set(nextArr)

    const added: string[] = []
    for (const v of nextSet) if (!oldSet.has(v)) added.push(v)
    const removed: string[] = []
    for (const v of oldSet) if (!nextSet.has(v)) removed.push(v)

    if (added.length === 0 && removed.length === 0) return

    // Build the candidate value from approved deltas only. We keep
    // insertion order roughly aligned with the operator's intent:
    // start from the saved list (so unchanged entries keep their
    // position), drop approved removals, append approved additions.
    const finalSet = new Set(oldArr)
    for (const entry of added) {
      const ok = await confirmSsrfListEntryChange('add', entry)
      if (ok) {
        finalSet.add(entry)
      } else {
        await snapSsrfWhitelistToSaved(item)
        return
      }
    }
    for (const entry of removed) {
      const ok = await confirmSsrfListEntryChange('remove', entry)
      if (ok) {
        finalSet.delete(entry)
      } else {
        await snapSsrfWhitelistToSaved(item)
        return
      }
    }

    const finalArr = Array.from(finalSet)
    // Compare against saved value, not against `editValues`. If every
    // delta was declined, the saved list still wins; we just need to
    // snap the input back to it.
    const sameAsSaved =
      finalArr.length === oldArr.length &&
      finalArr.every((v, i) => v === oldArr[i])
    if (sameAsSaved) {
      await snapSsrfWhitelistToSaved(item)
      return
    }

    editValues[item.key] = finalArr
    await persistSetting(item)
  } finally {
    await nextTick()
    listConfirmBusyKey.value = null
  }
}

function highRiskConfirmBody(item: SystemSettingItem, value: unknown): string {
  const renderedValue = Array.isArray(value)
    ? value.length === 0
      ? t('system.globalSettings.confirm.emptyValue')
      : value.join(', ')
    : String(value)
  return t('system.globalSettings.confirm.bodyAuthRegistrationMode', {
    label: keyLabel(item.key),
    value: renderedValue,
  })
}

function hasBulkAction(item: SystemSettingItem): boolean {
  return STORAGE_QUOTA_KEYS.some((key) => key === item.key)
}

const quotaActionDisabled = computed(() => savingKey.value !== null ||
  settings.value.some((item) => hasBulkAction(item) && isDirty(item)))

async function refreshSandboxDockerCapability(key: string) {
  if (key !== 'sandbox.docker_enabled') return
  await deploymentCapabilities.ensureLoaded(true)
}

function bulkActionConfirmBody(): string {
  return t('system.globalSettings.bulkApply.confirmBody', resolveStorageQuota(settings.value))
}

async function runBulkAction(item: SystemSettingItem) {
  if (!hasBulkAction(item) || quotaActionDisabled.value) return
  savedKey.value = null
  savingKey.value = item.key
  try {
    const result = await applyDefaultStorageQuotaToAllTenants()
    MessagePlugin.success(
      t('system.globalSettings.bulkApply.success', {
        count: result.affected,
        ...formatStorageQuota(result.quota_bytes),
      }),
    )
    markSettingSaved(item)
  } catch (err: any) {
    const msg = err?.message || t('system.globalSettings.bulkApply.failed')
    saveAnnouncement.value = msg
    MessagePlugin.error(msg)
  } finally {
    savingKey.value = null
  }
}

// resetSetting drops the DB override and reloads the row so the UI
// reflects the resolved fallback (ENV value if set, otherwise the
// in-code default). We refetch the whole list rather than the single
// row because the list endpoint is what populates the canonical
// settings array and re-running it keeps the modified-by enrichment
// consistent for every row in the table.
async function resetSetting(item: SystemSettingItem) {
  savedKey.value = null
  savingKey.value = item.key
  try {
    await resetSystemSetting(item.key)
    await loadSettings()
    markSettingSaved(item)
    MessagePlugin.success(t('system.globalSettings.reset.success'))
    await refreshSandboxDockerCapability(item.key)
  } catch (err: any) {
    const msg = err?.message || t('system.globalSettings.reset.failed')
    saveAnnouncement.value = msg
    MessagePlugin.error(msg)
  } finally {
    savingKey.value = null
  }
}

async function persistSetting(item: SystemSettingItem) {
  const newValue = editValues[item.key]
  savedKey.value = null
  savingKey.value = item.key
  try {
    const updated = await updateSystemSetting(item.key, newValue)
    // Replace the row in-place so the table stays at scroll position
    // and other rows' edit state isn't disturbed.
    const idx = settings.value.findIndex((s) => s.key === item.key)
    if (idx >= 0) {
      settings.value[idx] = updated
    }
    editValues[item.key] = Array.isArray(updated.value)
      ? [...(updated.value as unknown[])]
      : updated.value
    markSettingSaved(updated)
    MessagePlugin.success(t('system.globalSettings.messages.saveSuccess'))
    await refreshSandboxDockerCapability(item.key)
  } catch (err: any) {
    const msg = err?.message || t('system.globalSettings.messages.saveFailed')
    saveAnnouncement.value = msg
    MessagePlugin.error(msg)
    // Roll the input back to the canonical value on failure. Without
    // this an invalid edit (e.g. SSRF whitelist with a malformed CIDR
    // that the backend 400'd) would stay rendered as if accepted, and
    // the user couldn't tell whether the rejection actually stuck.
    const failed = settings.value.find((s) => s.key === item.key)
    if (failed) {
      editValues[item.key] = Array.isArray(failed.value)
        ? [...(failed.value as unknown[])]
        : failed.value
    }
  } finally {
    savingKey.value = null
  }
}

onMounted(() => {
  loadSettings()
})

onUnmounted(() => {
  if (savedKeyTimer) clearTimeout(savedKeyTimer)
})
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/settings-section.less';

.system-settings {
  width: 100%;
  container-type: inline-size;
}

// Use the same title styling as the other settings pages.
.section-header {
  .settings-section-header();
}

.section-header__titlewrap {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--app-space-4);
  padding-right: 28px;
}

.section-description {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-base);
  line-height: 20px;

  p { margin: 0; }
}

.section-description__note {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  margin-left: 8px;
}

.hint-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 2px;
  border: none;
  border-radius: var(--app-radius-xs);
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
  gap: 8px;
}

.hint-popover__scope {
  margin: 0 0 4px;
  font-size: var(--app-text-sm);
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.hint-popover__title {
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-weight: 600;
}

.hint-popover__list {
  margin: 0;
  padding: 0 0 0 18px;
  font-size: var(--app-text-sm);
  line-height: 1.6;
  color: var(--td-text-color-secondary);
  list-style: disc;

  li+li {
    margin-top: 4px;
  }
}

// Search sits in the tabbar, while the primary action belongs to the header.
.settings-tabbar {
  display: flex;
  align-items: center;
  gap: 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  margin-bottom: var(--app-space-4);
}

.settings-tabs {
  flex: 1;
  min-width: 0;
  display: flex;
  gap: 24px;
  overflow-x: auto;
  scrollbar-width: none;
}

.settings-toolbar { flex-shrink: 0; }

.settings-tab {
  position: relative;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 44px;
  padding: 0 2px;
  border: 0;
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-base);
  font-weight: 500;
  cursor: pointer;
  transition: color var(--app-motion-fast) ease;

  &::after {
    content: '';
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 2px;
    border-radius: var(--app-radius-pill);
    background: transparent;
  }
  &:hover { color: var(--td-text-color-primary); }
  &[aria-selected='true'] {
    color: var(--td-text-color-primary);
    > .t-icon { color: var(--td-brand-color); }
    &::after { background: var(--td-brand-color); }
  }
  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: -2px;
    border-radius: var(--app-radius-sm);
  }
}

.settings-section-panel { min-width: 0; }

@container (max-width: 900px) {
  .settings-tabbar { flex-wrap: wrap; gap: 0; }
  .settings-tabs { flex-basis: 100%; }
  .settings-toolbar { width: 100%; padding: 8px 0; }
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

.setting-save-state {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex-shrink: 0;
  min-width: 52px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.setting-save-state--success {
  color: var(--td-success-color);
}

.setting-reset-btn {
  // Sit flush with the input on the right; size="small" gives it the
  // right footprint to read as secondary action next to the primary
  // edit control.
  flex-shrink: 0;
}

// Anchor wrapper for inline t-popconfirm on inputs (SSRF /
// high-risk select). Popconfirm attaches to this box so the bubble
// appears beside the control, not a full-screen modal.
.setting-control-anchor {
  flex: 1;
  min-width: 0;
}

.loading-state,
.empty-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 60px 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);
}

// All settings tabs share one description/control grid.
.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  .setting-row();
  display: grid;
  grid-template-columns: minmax(0, 1fr) 320px;
  gap: 32px;
  padding: 24px 0;

  &:first-child { padding-top: 0; }
}

.setting-info {
  .setting-info();
  max-width: none;
  padding-right: 0;
}

.setting-label {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  font-size: var(--app-text-lg);
  font-weight: 500;
  color: var(--td-text-color-primary);
  margin-bottom: 4px;
  line-height: 1.4;
}

.setting-badge {
  vertical-align: middle;
}

.desc {
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
  margin: 0;
  line-height: 1.65;
  max-width: 600px;
}

.setting-meta {
  margin-top: 6px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.setting-control {
  .setting-control();
  min-width: 0;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
}

.setting-control-row {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.setting-control-actions {
  display: flex;
  justify-content: flex-end;
}

.setting-input {
  width: 240px;
}

.setting-input--wide {
  width: 320px;
}

@media (max-width: 860px) {
  .setting-row {
    grid-template-columns: minmax(0, 1fr);
    gap: 12px;
  }

  .setting-info {
    width: 100%;
    max-width: none;
    padding-right: 0;
  }

  .setting-control {
    width: 100%;
    align-items: flex-start;
  }

  .setting-control-row {
    width: 100%;
    justify-content: flex-start;
  }

  .setting-control-actions {
    width: 100%;
    justify-content: flex-start;
  }

  .setting-input,
  .setting-input--wide {
    width: 100%;
    flex: 1;
  }

  .desc {
    max-width: none;
  }
}
</style>
