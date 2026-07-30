<template>
  <!--
    SystemSettings — platform-wide tunables (system_settings table) for
    SystemAdmin. Gated server-side by RequireSystemAdmin middleware;
    the route also has meta.requiresSystemAdmin so non-admins never
    reach this component (see frontend/src/router/index.ts).

    Visual contract: matches the canonical Settings-modal pane skeleton
    (`.section-header` + `.settings-group` + `.setting-row` /
    `.setting-info` / `.setting-control`) used by GeneralSettings,
    OllamaSettings, etc. Avoid bespoke layout here; the modal already
    constrains width and padding via `.content-wrapper--full`.

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
      - System admins                         → tag-input @change with
                                                 inline popconfirm per delta.

    auth.registration_mode triggers an
    inline t-popconfirm (same as Reset / bulk-apply) before persisting;
    cancelling rolls the in-progress edit back to the canonical value.
  -->
  <div class="system-settings">
    <div class="section-header">
      <h2>{{ t('system.globalSettings.title') }}</h2>
      <p class="section-description">
        {{ t('system.globalSettings.description') }}
      </p>
    </div>

    <div v-if="loading && settings.length === 0" class="loading-state">
      <t-loading :text="t('system.globalSettings.loading')" />
    </div>

    <div v-else-if="settings.length === 0" class="empty-state">
      <t-icon name="info-circle" size="24px" />
      <span>{{ t('system.globalSettings.empty') }}</span>
    </div>

    <template v-else>
      <div class="settings-intro-panel">
        <div class="priority-hint-title">
          <t-icon name="info-circle" />
          <span>{{ t('system.globalSettings.priorityHint.disclosure') }}</span>
        </div>
        <ul class="priority-hint-list">
          <li>{{ t('system.globalSettings.priorityHint.tier1') }}</li>
          <li>{{ t('system.globalSettings.priorityHint.tier2') }}</li>
          <li>{{ t('system.globalSettings.priorityHint.tier3') }}</li>
        </ul>
      </div>

      <t-tabs v-model="activeSettingsSection" class="settings-section-tabs">
        <t-tab-panel value="access" :label="sectionTabLabel('access')" />
        <t-tab-panel value="tenant" :label="sectionTabLabel('tenant')" />
        <t-tab-panel value="runtime" :label="sectionTabLabel('runtime')" />
        <t-tab-panel value="security" :label="sectionTabLabel('security')" />
        <t-tab-panel
          v-if="hasUnknownSettings"
          value="other"
          :label="sectionTabLabel('other')"
        />
      </t-tabs>

      <section class="settings-section-panel" :aria-labelledby="`settings-section-${activeSettingsSection}`">
        <div
          class="settings-section-intro"
          :class="{ 'settings-section-intro--runtime': activeSettingsSection === 'runtime' }"
        >
          <div>
            <h3 :id="`settings-section-${activeSettingsSection}`">{{ activeSectionTitle }}</h3>
            <p>{{ activeSectionDescription }}</p>
          </div>
          <t-tag v-if="activeSettingsSection === 'runtime'" theme="warning" variant="light" size="small">
            {{ t('system.globalSettings.sections.runtime.restartHint') }}
          </t-tag>
        </div>

        <div
          v-if="activeSettingsSection === 'runtime'"
          class="runtime-table-header"
          aria-hidden="true"
        >
          <span>{{ t('system.globalSettings.runtimeTable.setting') }}</span>
          <span>{{ t('system.globalSettings.runtimeTable.value') }}</span>
        </div>

        <div
          class="settings-group"
          :class="{ 'settings-group--runtime': activeSettingsSection === 'runtime' }"
        >
      <!--
        System-admins management. Visually identical to SSRF whitelist
        (a tag-input with one entry per email). NOT a system_setting
        row — it's backed by the user table via promote/revoke APIs.
        We sit it at the top because changing who can edit this page
        is structurally more important than tweaking any value below.
        Self-edit safety: the current user is excluded from the visible
        tags (they can't revoke themselves anyway, and showing a tag
        that can't be removed is worse than not showing it).
      -->
          <div v-if="activeSettingsSection === 'access'" class="setting-row setting-row--admin">
        <div class="setting-info">
              <div class="setting-label">
            <span>{{ t('system.globalSettings.admins.label') }}</span>
                <t-tag theme="danger" variant="light" size="small" class="setting-badge">
                  {{ t('system.globalSettings.badgeHighRisk') }}
                </t-tag>
              </div>
          <p class="desc">{{ t('system.globalSettings.admins.description') }}</p>
        </div>
        <div class="setting-control">
          <div class="setting-control-row">
            <t-popconfirm
              v-model:visible="adminPopconfirm.visible"
              :content="adminPopconfirm.content"
              :theme="adminPopconfirm.theme"
              :confirm-btn="adminPopconfirm.confirmBtn"
              :cancel-btn="t('system.globalSettings.confirm.cancelBtn')"
              :popup-props="PROGRAMMATIC_POPCONFIRM_PROPS"
              placement="left"
              @confirm="adminPopconfirm.finish(true)"
              @cancel="adminPopconfirm.finish(false)"
              @visible-change="adminPopconfirm.onVisibleChange"
            >
              <div class="setting-control-anchor">
                <t-tag-input
                  v-model="adminEmails"
                  :placeholder="t('system.globalSettings.admins.placeholder')"
                      :aria-label="t('system.globalSettings.admins.label')"
                  :disabled="adminBusy"
                  class="setting-input setting-input--wide"
                  clearable
                  @change="onAdminsChange"
                />
              </div>
            </t-popconfirm>
                <div v-if="adminBusy" class="setting-save-state" role="status">
                  <t-loading size="small" />
                  <span>{{ t('system.globalSettings.saving') }}</span>
                </div>
          </div>
        </div>
      </div>

          <div v-if="activeSettingsSection === 'access'" class="setting-row setting-row--password-reset">
            <div class="setting-info">
              <div class="setting-label">
                <span>{{ t('system.globalSettings.passwordReset.label') }}</span>
                <t-tag theme="danger" variant="light" size="small" class="setting-badge">
                  {{ t('system.globalSettings.badgeHighRisk') }}
                </t-tag>
              </div>
              <p class="desc">{{ t('system.globalSettings.passwordReset.description') }}</p>
            </div>
            <div class="setting-control">
              <t-button
                theme="danger"
                variant="text"
                class="password-reset-trigger"
                @click="openPasswordResetDialog"
              >
                <template #icon><t-icon name="lock-on" /></template>
                {{ t('system.globalSettings.passwordReset.action') }}
              </t-button>
            </div>
          </div>

      <div
            v-for="item in activeSectionSettings"
        :key="item.key"
        class="setting-row"
      >
        <div class="setting-info">
              <div class="setting-label">
            <span>{{ keyLabel(item.key) }}</span>
            <t-tag
              v-if="item.requires_restart"
              theme="warning"
              variant="light"
              size="small"
              class="setting-badge"
            >{{ t('system.globalSettings.badgeRequiresRestart') }}</t-tag>
            <t-tag
              v-if="item.is_secret"
              theme="primary"
              variant="light"
              size="small"
              class="setting-badge"
            >{{ t('system.globalSettings.badgeSecret') }}</t-tag>
            <t-tag
                  v-if="isHighImpactKey(item.key)"
                  theme="danger"
                  variant="light"
                  size="small"
                  class="setting-badge"
                >{{ t('system.globalSettings.badgeHighRisk') }}</t-tag>
                <t-tag
              v-if="hasOverride(item)"
              theme="success"
              variant="light"
              size="small"
              class="setting-badge"
              :title="t('system.globalSettings.badgeOverrideTooltip')"
            >{{ t('system.globalSettings.badgeOverride') }}</t-tag>
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
          <t-popconfirm
            v-if="hasEnum(item) && isHighRiskKey(item.key)"
            v-model:visible="highRiskPopconfirm.visible"
            :content="highRiskPopconfirm.content"
            :theme="highRiskPopconfirm.theme"
            :confirm-btn="highRiskPopconfirm.confirmBtn"
            :cancel-btn="t('system.globalSettings.confirm.cancelBtn')"
            :popup-props="PROGRAMMATIC_POPCONFIRM_PROPS"
            placement="left"
            @confirm="highRiskPopconfirm.finish(true)"
            @cancel="highRiskPopconfirm.finish(false)"
            @visible-change="highRiskPopconfirm.onVisibleChange"
          >
            <div class="setting-control-anchor">
              <t-select
                v-model="editValues[item.key]"
                :options="enumOptions(item)"
                    :aria-label="keyLabel(item.key)"
                :disabled="savingKey === item.key"
                class="setting-input"
                @change="onHighRiskSelectChange(item)"
              />
            </div>
          </t-popconfirm>
          <t-select
            v-else-if="hasEnum(item)"
            v-model="editValues[item.key]"
            :options="enumOptions(item)"
                :aria-label="keyLabel(item.key)"
            :disabled="savingKey === item.key"
            class="setting-input"
            @change="onChange(item)"
          />
          <t-switch
            v-else-if="item.value_type === 'bool'"
            v-model="editValues[item.key]"
                :aria-label="keyLabel(item.key)"
            :disabled="savingKey === item.key"
            @change="onChange(item)"
          />
          <t-input-number
            v-else-if="item.value_type === 'int'"
            v-model="editValues[item.key]"
            :placeholder="placeholderFor(item)"
                :aria-label="keyLabel(item.key)"
            :disabled="savingKey === item.key"
            theme="normal"
            :step="1"
            :min="minimumFor(item)"
            class="setting-input"
            @blur="onChange(item)"
          />
          <t-popconfirm
            v-else-if="item.value_type === 'string_list' && item.key === 'ssrf.whitelist'"
            v-model:visible="ssrfPopconfirm.visible"
            :content="ssrfPopconfirm.content"
            :theme="ssrfPopconfirm.theme"
            :confirm-btn="ssrfPopconfirm.confirmBtn"
            :cancel-btn="t('system.globalSettings.confirm.cancelBtn')"
            :popup-props="PROGRAMMATIC_POPCONFIRM_PROPS"
            placement="left"
            @confirm="ssrfPopconfirm.finish(true)"
            @cancel="ssrfPopconfirm.finish(false)"
            @visible-change="ssrfPopconfirm.onVisibleChange"
          >
            <div class="setting-control-anchor">
              <t-tag-input
                :key="`ssrf-tag-${ssrfTagInputKey()}`"
                :model-value="ssrfWhitelistModelValue()"
                :placeholder="emptyListPlaceholder"
                    :aria-label="keyLabel(item.key)"
                :disabled="savingKey === item.key"
                class="setting-input setting-input--wide"
                clearable
                @update:model-value="onSsrfWhitelistModelUpdate"
              />
            </div>
          </t-popconfirm>
          <t-input
            v-else
            v-model="editValues[item.key]"
            :placeholder="placeholderFor(item)"
                :aria-label="keyLabel(item.key)"
            :disabled="savingKey === item.key"
            class="setting-input"
            clearable
            @blur="onChange(item)"
          />

          <!--
            Per-row saving spinner. Appears next to the control while
            a PUT is in flight; the controls stay disabled (see
            :disabled bindings above) so concurrent edits can't race.
          -->
              <div v-if="savingKey === item.key" class="setting-save-state" role="status">
                <t-loading size="small" />
                <span>{{ t('system.globalSettings.saving') }}</span>
              </div>
              <div
                v-else-if="savedKey === item.key"
                class="setting-save-state setting-save-state--success"
                role="status"
              >
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
          <div
            v-if="hasOverride(item) || hasBulkAction(item)"
            class="setting-control-actions"
          >
            <!--
              Per-key bulk action. Currently only one key
              (tenant.default_storage_quota_gb) carries one — clicking
              writes the current setting value onto every existing
              tenant. We do this as a separate explicit action rather
              than auto-cascade on save so a SystemAdmin who tweaks the
              default while triaging a single new-tenant question
              doesn't accidentally rewrite production quotas. Hidden
              when the row is dirty because applying a not-yet-saved
              value would confuse "what just happened".
            -->
            <t-popconfirm
              v-if="hasBulkAction(item)"
              :content="bulkActionConfirmBody(item)"
              :confirm-btn="{ content: t('system.globalSettings.bulkApply.confirmBtn'), theme: 'primary' }"
              :cancel-btn="{ content: t('system.globalSettings.confirm.cancelBtn') }"
              placement="left"
              @confirm="runBulkAction(item)"
            >
              <t-button
                variant="text"
                size="small"
                :disabled="savingKey === item.key || isDirty(item)"
                :title="t('system.globalSettings.bulkApply.tooltip')"
                class="setting-bulk-btn"
              >
                <template #icon><t-icon name="usergroup" /></template>
                {{ t('system.globalSettings.bulkApply.label') }}
              </t-button>
            </t-popconfirm>

            <t-popconfirm
              v-if="hasOverride(item)"
              :content="t('system.globalSettings.reset.confirmBody', { label: keyLabel(item.key) })"
              :confirm-btn="{ content: t('system.globalSettings.reset.confirmBtn'), theme: 'warning' }"
              :cancel-btn="{ content: t('system.globalSettings.confirm.cancelBtn') }"
              placement="left"
              @confirm="resetSetting(item)"
            >
              <t-button
                variant="text"
                size="small"
                :disabled="savingKey === item.key"
                :title="t('system.globalSettings.reset.tooltip')"
                class="setting-reset-btn"
              >
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
    <t-dialog
      v-model:visible="passwordResetVisible"
      :header="t('system.globalSettings.passwordReset.dialogTitle')"
      width="440px"
      placement="center"
      dialog-class-name="password-reset-dialog"
      :confirm-btn="{
        content: t('system.globalSettings.passwordReset.confirmBtn'),
        theme: 'danger',
        loading: passwordResetSubmitting,
      }"
      :cancel-btn="{
        content: t('system.globalSettings.confirm.cancelBtn'),
        variant: 'outline',
      }"
      :close-on-overlay-click="!passwordResetSubmitting"
      :close-btn="!passwordResetSubmitting"
      @confirm="submitPasswordReset"
      @close="resetPasswordResetForm"
    >
      <t-alert
        theme="warning"
        :message="t('system.globalSettings.passwordReset.warning')"
        class="password-reset-warning"
      />
      <t-form
        ref="passwordResetFormRef"
        :data="passwordResetForm"
        :rules="passwordResetRules"
        label-align="top"
        class="password-reset-form"
      >
        <t-form-item :label="t('system.globalSettings.passwordReset.emailLabel')" name="email">
          <t-input
            v-model="passwordResetForm.email"
            type="email"
            clearable
            autocomplete="off"
            :disabled="passwordResetSubmitting"
            :placeholder="t('system.globalSettings.passwordReset.emailPlaceholder')"
          />
        </t-form-item>
        <t-form-item :label="t('system.globalSettings.passwordReset.newPasswordLabel')" name="newPassword">
          <t-input
            v-model="passwordResetForm.newPassword"
            type="password"
            autocomplete="new-password"
            :disabled="passwordResetSubmitting"
            :placeholder="t('system.globalSettings.passwordReset.newPasswordPlaceholder')"
          >
            <template #prefix-icon><t-icon name="lock-on" /></template>
          </t-input>
        </t-form-item>
        <t-form-item :label="t('system.globalSettings.passwordReset.confirmPasswordLabel')" name="confirmPassword">
          <t-input
            v-model="passwordResetForm.confirmPassword"
            type="password"
            autocomplete="new-password"
            :disabled="passwordResetSubmitting"
            :placeholder="t('system.globalSettings.passwordReset.confirmPasswordPlaceholder')"
            @enter="submitPasswordReset"
          >
            <template #prefix-icon><t-icon name="lock-on" /></template>
          </t-input>
        </t-form-item>
      </t-form>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import type { FormInstanceFunctions, FormRule } from 'tdesign-vue-next'
import {
  listSystemSettings,
  updateSystemSetting,
  resetSystemSetting,
  applyDefaultStorageQuotaToAllTenants,
  listSystemAdmins,
  promoteUserToSystemAdmin,
  revokeSystemAdmin,
  resetUserPassword,
  type SystemSettingItem,
} from '@/api/system'
import { useAuthStore } from '@/stores/auth'

const authStore = useAuthStore()
const currentUserId = computed(() => authStore.currentUserId)

const { t, tm, te, locale } = useI18n()

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
  if (te(path)) return t(path) as string
  return item.description ?? ''
}

// Enum keys whose change triggers a whole-value inline popconfirm before
// PUT. ssrf.whitelist is not here — it uses per-tag confirm instead.
const HIGH_RISK_KEYS = new Set<string>([
  'auth.registration_mode',
])

const HIGH_IMPACT_KEYS = new Set<string>([
  'auth.registration_mode',
  'tenant.auto_create_api_key',
  'ssrf.whitelist',
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
const adminPopconfirm = createInlinePopconfirm()
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

type SettingsSection = 'access' | 'tenant' | 'runtime' | 'security' | 'other'

// Product-oriented order, rather than the registry's alphabetical key order.
// Unknown/out-of-band rows remain visible in a conditional "Other" tab so the
// backend's diagnostic contract is preserved when a deployment contains an
// unexpected key.
const SETTINGS_SECTION_KEYS: Record<Exclude<SettingsSection, 'other'>, readonly string[]> = {
  access: [
    'auth.registration_mode',
    'auth.default_tenant_mode',
    'tenant.self_service_creation_enabled',
    'tenant.max_owned_per_user',
  ],
  tenant: [
    'tenant.default_storage_quota_gb',
    'tenant.auto_create_api_key',
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
  security: ['ssrf.whitelist'],
}

const activeSettingsSection = ref<SettingsSection>('access')
const knownSettingKeys = new Set(Object.values(SETTINGS_SECTION_KEYS).flat())
const settingsByKey = computed(() => new Map(settings.value.map((item) => [item.key, item])))
const unknownSettings = computed(() => settings.value.filter((item) => !knownSettingKeys.has(item.key)))
const hasUnknownSettings = computed(() => unknownSettings.value.length > 0)

watch(hasUnknownSettings, (hasUnknown) => {
  if (!hasUnknown && activeSettingsSection.value === 'other') {
    activeSettingsSection.value = 'access'
  }
})

const activeSectionSettings = computed(() => {
  if (activeSettingsSection.value === 'other') return unknownSettings.value
  return SETTINGS_SECTION_KEYS[activeSettingsSection.value]
    .map((key) => settingsByKey.value.get(key))
    .filter((item): item is SystemSettingItem => Boolean(item))
})

const activeSectionTitle = computed(() =>
  t(`system.globalSettings.sections.${activeSettingsSection.value}.title`),
)
const activeSectionDescription = computed(() =>
  t(`system.globalSettings.sections.${activeSettingsSection.value}.description`),
)


function sectionTabLabel(section: SettingsSection): string {
  const count = section === 'other'
    ? unknownSettings.value.length
    : SETTINGS_SECTION_KEYS[section].filter((key) => settingsByKey.value.has(key)).length + (section === 'access' ? 2 : 0)
  return t(`system.globalSettings.sections.${section}.tab`, { count })
}

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

// Admin management state. We keep two parallel structures:
//   - adminEmails: the v-model bound to the t-tag-input (excludes
//     current user; that's the visible source of truth).
//   - adminEmailToId: email → user UUID, populated from the list
//     endpoint. Needed because revoke takes a UUID, not an email.
// Both reset on every reload to avoid stale entries persisting after
// a peer SystemAdmin makes a change. adminBusy disables the input and
// shows the row spinner only while promote/revoke API calls are in
// flight — not while the inline popconfirm is waiting for a click.
const adminEmails = ref<string[]>([])
const adminEmailToId = ref<Record<string, string>>({})
const adminBusy = ref(false)

const passwordResetVisible = ref(false)
const passwordResetSubmitting = ref(false)
const passwordResetFormRef = ref<FormInstanceFunctions>()
const passwordResetForm = reactive({
  email: '',
  newPassword: '',
  confirmPassword: '',
})
const passwordResetRules: Record<string, FormRule[]> = {
  email: [
    { required: true, message: t('system.globalSettings.passwordReset.validation.emailRequired'), trigger: 'blur' },
    { email: true, message: t('system.globalSettings.passwordReset.validation.emailInvalid'), trigger: 'blur' },
  ],
  newPassword: [
    { required: true, message: t('system.globalSettings.passwordReset.validation.passwordRequired'), trigger: 'blur' },
    { min: 8, message: t('system.globalSettings.passwordReset.validation.passwordLength'), trigger: 'blur' },
    { max: 32, message: t('system.globalSettings.passwordReset.validation.passwordLength'), trigger: 'blur' },
    { pattern: /[a-zA-Z]/, message: t('system.globalSettings.passwordReset.validation.passwordLetter'), trigger: 'blur' },
    { pattern: /\d/, message: t('system.globalSettings.passwordReset.validation.passwordNumber'), trigger: 'blur' },
  ],
  confirmPassword: [
    { required: true, message: t('system.globalSettings.passwordReset.validation.confirmRequired'), trigger: 'blur' },
    {
      validator: (value: string) => value === passwordResetForm.newPassword,
      message: t('system.globalSettings.passwordReset.validation.passwordMismatch'),
      trigger: 'blur',
    },
  ],
}

function resetPasswordResetForm() {
  passwordResetForm.email = ''
  passwordResetForm.newPassword = ''
  passwordResetForm.confirmPassword = ''
  passwordResetFormRef.value?.clearValidate?.()
}

async function openPasswordResetDialog() {
  resetPasswordResetForm()
  passwordResetVisible.value = true
  await nextTick()
  passwordResetFormRef.value?.clearValidate?.()
}

async function submitPasswordReset() {
  if (passwordResetSubmitting.value) return
  const valid = await passwordResetFormRef.value?.validate?.()
  if (valid !== true) return

  passwordResetSubmitting.value = true
  try {
    await resetUserPassword({
      email: passwordResetForm.email.trim(),
      new_password: passwordResetForm.newPassword,
    })
    saveAnnouncement.value = t('system.globalSettings.passwordReset.success')
    MessagePlugin.success(t('system.globalSettings.passwordReset.success'))
    passwordResetVisible.value = false
  } catch (err: any) {
    const msg = err?.message || t('system.globalSettings.passwordReset.failed')
    saveAnnouncement.value = msg
    MessagePlugin.error(msg)
  } finally {
    passwordResetSubmitting.value = false
  }
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
  const cur = editValues[item.key]
  const orig = item.value
  if (Array.isArray(cur) && Array.isArray(orig)) {
    if (cur.length !== orig.length) return true
    for (let i = 0; i < cur.length; i++) {
      if (cur[i] !== orig[i]) return true
    }
    return false
  }
  return cur !== orig
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
  if (!isDirty(item)) return

  // SSRF whitelist gets the per-entry confirm flow — same shape as the
  // admin tag-input above. Adding or removing each host/CIDR is its
  // own privileged change (a single bad CIDR can punch a hole through
  // the egress firewall), so we ask once per delta instead of once
  // per "save". This matches the operator's mental model: every tag
  // they touch is acknowledged on its own.
  await persistSetting(item)
}

async function onHighRiskSelectChange(item: SystemSettingItem) {
  const newValue = editValues[item.key]
  if (newValue === item.value) return

  // Revert the select immediately so cancel leaves the saved value
  // visible; re-apply only after the inline popconfirm is confirmed.
  editValues[item.key] = item.value

  const ok = await highRiskPopconfirm.ask({
    content: highRiskConfirmBody(item, newValue),
    theme: 'danger',
    confirmBtn: {
      content: t('system.globalSettings.confirm.confirmBtn'),
      theme: 'danger',
    },
  })
  if (!ok) return

  editValues[item.key] = newValue
  await persistSetting(item)
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

// hasBulkAction tells the template whether the current row carries an
// extra "apply to existing data" action beyond plain save/reset.
// Currently only `tenant.default_storage_quota_gb` does — saving the
// setting only affects future tenants, so the bulk button is the
// escape hatch for "rewrite all current tenants too".
function hasBulkAction(item: SystemSettingItem): boolean {
  return item.key === 'tenant.default_storage_quota_gb'
}

function bulkActionConfirmBody(item: SystemSettingItem): string {
  // Use the canonical (saved) value, not the in-progress edit, so the
  // operator sees exactly what will be written. The button is disabled
  // when the row is dirty (see template), so item.value is the value
  // that's currently in effect for new tenants.
  const v = item.value
  const valueText = v === null || v === undefined ? '' : String(v)
  return t('system.globalSettings.bulkApply.confirmBody', { value: valueText })
}

async function runBulkAction(item: SystemSettingItem) {
  if (!hasBulkAction(item)) return
  savedKey.value = null
  savingKey.value = item.key
  try {
    const result = await applyDefaultStorageQuotaToAllTenants()
    MessagePlugin.success(
      t('system.globalSettings.bulkApply.success', {
        count: result.affected,
        gb: result.quota_gb,
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

// loadAdmins refreshes the admin tag list + the email→id lookup
// table. We exclude the current user from the visible list so the
// "you can't revoke yourself" rule has nothing to enforce in the UI
// (the backend rejects it too, but hiding the tag is friendlier).
async function loadAdmins() {
  try {
    const resp = await listSystemAdmins({ limit: 200 })
    const map: Record<string, string> = {}
    const emails: string[] = []
    for (const u of resp.admins ?? []) {
      // Empty emails would collapse to a single tag "" that can't be
      // round-tripped to a user_id; skip them. Same defensive stance
      // as resolveMaxOwnedTenantsPerUser on the backend.
      if (!u.email) continue
      map[u.email] = u.id
      if (u.id !== currentUserId.value) {
        emails.push(u.email)
      }
    }
    adminEmailToId.value = map
    adminEmails.value = emails
  } catch (err: any) {
    const msg = err?.message || t('system.globalSettings.admins.loadFailed')
    MessagePlugin.error(msg)
  }
}

function confirmAdminChange(action: 'promote' | 'revoke', email: string): Promise<boolean> {
  const base = `system.globalSettings.admins.confirm.${action}`
  return adminPopconfirm.ask({
    content: globalSettingsText(`${base}.body`, { email }),
    theme: action === 'revoke' ? 'danger' : 'warning',
    confirmBtn: {
      content: globalSettingsText(`${base}.confirmBtn`),
      theme: action === 'revoke' ? 'danger' : 'primary',
    },
  })
}

// onAdminsChange diffs the new tag list against the canonical state
// and dispatches one promote / revoke per delta. Failures roll back
// the whole tag list to the server-side truth — this is simpler than
// trying to undo individual ops, and the network/error case for batch
// edits is rare enough that a full reload doesn't surprise anyone.
async function onAdminsChange(next: string[]) {
  if (adminBusy.value) return

  // Snapshot of what's currently authoritative — the email→id map's
  // keys, minus the current user. Anything in `next` that's not here
  // is an addition; anything here that's not in `next` is a removal.
  const authoritative = new Set<string>()
  for (const email of Object.keys(adminEmailToId.value)) {
    if (adminEmailToId.value[email] !== currentUserId.value) {
      authoritative.add(email)
    }
  }
  const nextSet = new Set(next.map((e) => e.trim()).filter(Boolean))

  // Drop the user-typed entry to canonical lowercase/trim before we
  // diff. We don't lowercase server-returned emails because the
  // backend stores the original case; matching against the map's keys
  // happens with the as-typed value, which is what the user sees.
  const added: string[] = []
  for (const email of nextSet) {
    if (!authoritative.has(email)) added.push(email)
  }
  const removed: string[] = []
  for (const email of authoritative) {
    if (!nextSet.has(email)) removed.push(email)
  }

  if (added.length === 0 && removed.length === 0) return

  // Confirm before any privilege change (no loading spinner yet — the
  // popconfirm is the only UI; adminBusy is reserved for API roundtrips).
  for (const email of added) {
    const ok = await confirmAdminChange('promote', email)
    if (!ok) {
      await loadAdmins()
      return
    }
  }
  for (const email of removed) {
    const userId = adminEmailToId.value[email]
    if (!userId) continue
    const ok = await confirmAdminChange('revoke', email)
    if (!ok) {
      await loadAdmins()
      return
    }
  }

  adminBusy.value = true
  let applied = 0
  try {
    for (const email of added) {
      await promoteUserToSystemAdmin({ email })
      applied++
    }
    for (const email of removed) {
      const userId = adminEmailToId.value[email]
      if (!userId) continue
      await revokeSystemAdmin(userId)
      applied++
    }
    await loadAdmins()
    if (applied > 0) {
      saveAnnouncement.value = t('system.globalSettings.admins.saveSuccess')
      MessagePlugin.success(t('system.globalSettings.admins.saveSuccess'))
    }
  } catch (err: any) {
    const msg = err?.message || t('system.globalSettings.admins.saveFailed')
    saveAnnouncement.value = msg
    MessagePlugin.error(msg)
    await loadAdmins()
  } finally {
    adminBusy.value = false
  }
}

onMounted(() => {
  loadSettings()
  loadAdmins()
})

onUnmounted(() => {
  if (savedKeyTimer) clearTimeout(savedKeyTimer)
})
</script>

<style lang="less" scoped>
.system-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 24px;

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
    line-height: 1.5;
  }
}


.settings-intro-panel {
  margin-bottom: 18px;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
}

.priority-hint-title {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-bottom: 8px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-secondary);

  .t-icon {
    color: var(--td-brand-color);
  }
}

.priority-hint-list {
  margin: 0;
  padding: 0 0 0 20px;
  font-size: 13px;
  line-height: 1.65;
  color: var(--td-text-color-primary);
  list-style: disc;

  li + li {
    margin-top: 4px;
  }
}

.settings-section-tabs {
  position: sticky;
  top: 0;
  z-index: 3;
  margin: 0 0 18px;
  background: var(--td-bg-color-container);
  box-shadow: 0 1px 0 var(--td-component-stroke);

  &:deep(.t-tabs__nav-item) {
    font-weight: 500;
  }
}

.settings-section-panel {
  min-width: 0;
}

.settings-section-intro {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 0 0 12px;
  border-bottom: 1px solid var(--td-component-stroke);

  h3 {
    margin: 0 0 4px;
    font-size: 16px;
    line-height: 1.4;
    color: var(--td-text-color-primary);
  }

  p {
    margin: 0;
    font-size: 13px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }
}

.settings-section-intro--runtime {
  border-bottom: none;
}

.runtime-table-header {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 280px;
  gap: 24px;
  padding: 10px 16px;
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  border-bottom: none;
  border-radius: 8px 8px 0 0;

  span:last-child {
    text-align: right;
  }
}

.settings-group--runtime {
  border: 1px solid var(--td-component-stroke);
  border-radius: 0 0 8px 8px;
  overflow: hidden;

  .setting-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 280px;
    gap: 24px;
    padding: 14px 16px;
  }

  .setting-info {
    max-width: none;
    padding-right: 0;
  }

  .setting-label {
    font-size: 14px;
  }

  .desc {
    max-width: 620px;
    font-size: 12px;
  }

  .setting-control {
    min-width: 0;
  }

  .setting-input {
    width: 210px;
  }
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
  font-size: 12px;
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

// Anchor wrapper for inline t-popconfirm on inputs (SSRF / admins /
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
  font-size: 13px;
}

// Skeleton mirrors GeneralSettings.vue 1:1 so the two panes feel like
// they came from the same hand. Values that diverge intentionally:
//   - .setting-label is a flex container (vs General's plain <label>)
//     because we render badges inline with the title; identical font /
//     spacing otherwise.
//   - .desc has a max-width so long backend descriptions don't push
//     the control off the canvas in narrow viewports.
.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 20px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.setting-info {
  flex: 1;
  max-width: 65%;
  padding-right: 24px;
}

.setting-label {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  font-size: 15px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  margin-bottom: 4px;
  line-height: 1.4;
}

.setting-badge {
  vertical-align: middle;
}

.desc {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  margin: 0;
  line-height: 1.5;
  max-width: 480px;
}

.setting-meta {
  margin-top: 6px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
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

.setting-saving {
  // Pin width so the row layout doesn't reflow when the spinner
  // appears / disappears mid-save.
  width: 16px;
  height: 16px;
  flex-shrink: 0;
}

.setting-input {
  width: 240px;
}

.setting-input--wide {
  width: 320px;
}

.password-reset-trigger {
  min-width: 112px;
  height: 32px;
  padding: 0 12px;
  color: var(--td-error-color);
  background: var(--td-error-color-light);
  border: 1px solid transparent;
  border-radius: 6px;

  &:hover {
    color: var(--td-error-color-hover);
    background: var(--td-error-color-light-hover);
    border-color: var(--td-error-color-focus);
  }

  &:active {
    color: var(--td-error-color-active);
    background: var(--td-error-color-focus);
  }
}

.password-reset-warning {
  margin-bottom: 20px;
}

@media (max-width: 860px) {
  .settings-section-intro {
    align-items: flex-start;
    flex-direction: column;
  }

  .runtime-table-header {
    display: none;
  }

  .settings-group--runtime {
    border-radius: 8px;

    .setting-row {
      display: flex;
      flex-direction: column;
      gap: 12px;
    }

    .setting-input {
      width: 100%;
    }
  }

  .setting-row {
    flex-direction: column;
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

<style lang="less">
/* The dialog is teleported to body, so its visual shell cannot be
   styled from the scoped block above. Keep this class specific to the
   password-reset flow instead of changing every TDesign dialog. */
.password-reset-dialog {
  padding: 0;
  overflow: hidden;
  border-color: var(--td-component-stroke);
  border-radius: 12px;
  box-shadow:
    0 12px 32px rgba(15, 23, 42, 0.12),
    0 2px 8px rgba(15, 23, 42, 0.08);

  .t-dialog__header {
    min-height: 64px;
    padding: 0 24px;
    font-size: 18px;
    line-height: 26px;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  .t-dialog__close {
    width: 28px;
    height: 28px;
    padding: 0;
    justify-content: center;
    border-radius: 6px;
  }

  .t-dialog__body {
    padding: 20px 24px 4px;
  }

  .password-reset-warning {
    padding: 12px 14px;
    border-radius: 8px;

    .t-alert__content {
      font-size: 13px;
      line-height: 20px;
    }
  }

  .password-reset-form {
    .t-form__item {
      margin-bottom: 16px;
    }

    .t-form__label--top {
      min-height: 28px;
      padding: 0;
      font-size: 14px;
      line-height: 22px;
    }

    .t-input {
      border-radius: 6px;
    }
  }

  .t-dialog__footer {
    box-sizing: border-box;
    padding: 16px 24px 20px;
    border-top: 1px solid var(--td-component-stroke);

    .t-button {
      min-width: 88px;
      border-radius: 6px;
    }
  }
}

@media (max-width: 480px) {
  .password-reset-dialog {
    width: calc(100vw - 24px) !important;

    .t-dialog__header {
      min-height: 56px;
      padding: 0 20px;
      font-size: 17px;
    }

    .t-dialog__body {
      padding: 16px 20px 4px;
    }

    .t-dialog__footer {
      padding: 14px 20px 18px;
    }
  }
}

</style>
