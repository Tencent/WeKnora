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
      <div class="section-header-row">
        <h2>{{ t('system.globalSettings.title') }}</h2>
        <!-- Platform audit-log entry. SystemAdmin already gated the
             whole view via meta.requiresSystemAdmin (router/index.ts)
             so we don't re-check role here — every visitor of this
             page is eligible. Mirrors the audit button placement in
             tenant settings (frontend/src/views/settings/TenantMembers.vue). -->
        <t-button
          variant="text"
          size="small"
          class="header-audit-btn"
          @click="openAuditDrawer"
        >
          <template #icon><t-icon name="history" /></template>
          {{ t('system.globalSettings.audit.tabLabel') }}
        </t-button>
      </div>
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
      <div class="settings-overview">
        <div class="auto-save-note">
          <t-icon name="check-circle" />
          <span>{{ t('system.globalSettings.autoSaveHint') }}</span>
        </div>
        <div class="settings-overview-tags" aria-live="polite">
          <t-tag theme="success" variant="light" size="small">
            {{ t('system.globalSettings.summary.overridden', { count: overriddenCount }) }}
          </t-tag>
          <t-tag theme="warning" variant="light" size="small">
            {{ t('system.globalSettings.summary.restart', { count: restartRequiredCount }) }}
          </t-tag>
        </div>
      </div>

      <!-- Resolver details matter, but are secondary to finding the setting
           itself. Keep them one click away instead of occupying the first
           viewport on every visit. Native details/summary also gives us a
           keyboard-accessible disclosure without extra state. -->
      <details class="config-source-details">
        <summary>
          <span class="config-source-summary">
            <t-icon name="info-circle" />
            {{ t('system.globalSettings.priorityHint.disclosure') }}
          </span>
          <t-icon name="chevron-down" class="config-source-chevron" />
        </summary>
        <ul class="priority-hint-list">
          <li>{{ t('system.globalSettings.priorityHint.tier1') }}</li>
          <li>{{ t('system.globalSettings.priorityHint.tier2') }}</li>
          <li>{{ t('system.globalSettings.priorityHint.tier3') }}</li>
        </ul>
      </details>

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
        <div class="settings-section-intro">
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

    <!-- Platform audit-log drawer. Lazy-loaded on first open; closing
         and reopening doesn't re-fetch (refresh is explicit via the
         button inside the drawer). Backend route is SystemAdmin-gated,
         and this whole view is too, so we don't bother with a role
         check — any visitor here is eligible to read the feed. -->
    <t-drawer
      v-model:visible="auditDrawerVisible"
      :header="t('system.globalSettings.audit.tabLabel')"
      drawer-class-name="system-settings-audit-drawer"
      size="880px"
      :footer="false"
      placement="right"
      destroy-on-close
    >
      <div class="audit-drawer-inner audit-panel audit-panel--drawer">
        <div class="audit-header">
          <span class="audit-desc">{{ t('system.globalSettings.audit.description') }}</span>
          <t-button
            variant="text"
            size="small"
            class="audit-refresh-btn"
            :loading="auditLoading"
            :disabled="auditLoading"
            @click="reloadAuditLog"
          >
            <template #icon><t-icon name="refresh" /></template>
            {{ t('system.globalSettings.audit.refresh') }}
          </t-button>
        </div>

        <div class="audit-drawer-fill">
          <div v-if="auditError" class="audit-drawer-branch audit-drawer-branch--error">
            <div class="error-inline">
              <t-alert theme="error" :message="auditError">
                <template #operation>
                  <t-button size="small" @click="reloadAuditLog">
                    {{ t('system.globalSettings.audit.retry') }}
                  </t-button>
                </template>
              </t-alert>
            </div>
          </div>

          <div
            v-else-if="!auditLoading && auditEntries.length === 0"
            class="audit-drawer-branch audit-drawer-branch--empty empty-state empty-state--audit"
          >
            <t-empty :description="t('system.globalSettings.audit.empty')" />
          </div>

          <div v-else class="audit-scroll-area narrow-scrollbar audit-drawer-branch" ref="auditScrollRoot">
            <div class="data-table-shell audit-table-shell">
              <t-table
                row-key="id"
                :data="auditEntries"
                :columns="auditColumns"
                size="medium"
                hover
                expand-on-row-click
                :expanded-row-keys="auditExpandedRowKeys"
                @expand-change="onAuditExpandChange"
              >
                <template #created_at="{ row }">
                  <div class="audit-time">
                    <span class="audit-time-date">{{ formatAuditDatePart(row.created_at) }}</span>
                    <span class="audit-time-clock">{{ formatAuditTimePart(row.created_at) }}</span>
                  </div>
                </template>
                <template #actor="{ row }">
                  <div class="audit-actor">
                    <span class="audit-actor-name">
                      {{ row.actor_user_id ? auditActorLabel(row.actor_user_id) :
                        t('system.globalSettings.audit.systemActor') }}
                    </span>
                    <span v-if="row.actor_role" class="audit-actor-role">
                      {{ auditActorRoleLabel(row.actor_role) }}
                    </span>
                  </div>
                </template>
                <template #action="{ row }">
                  <t-tag :theme="auditActionTheme(row.action)" size="small" variant="light-outline">
                    {{ formatAuditAction(row.action) }}
                  </t-tag>
                </template>
                <template #target="{ row }">
                  <div class="audit-target">
                    <span v-if="auditTargetKey(row)" class="audit-target-key">{{ auditTargetKey(row) }}</span>
                    <span v-if="auditTargetDiff(row)" class="audit-target-diff">{{ auditTargetDiff(row) }}</span>
                    <span v-else-if="!auditTargetKey(row)" class="audit-target-empty">—</span>
                  </div>
                </template>
                <template #outcome="{ row }">
                  <t-tag :theme="auditOutcomeTheme(row.outcome)" size="small" variant="light">
                    {{ t('system.globalSettings.audit.outcome.' + row.outcome) }}
                  </t-tag>
                </template>
                <template #expandedRow="{ row }">
                  <div class="audit-expanded">
                    <div class="audit-expanded-grid">
                      <div class="audit-expanded-cell">
                        <span class="audit-expanded-label">{{ t('system.globalSettings.audit.expanded.actorId') }}</span>
                        <span class="audit-expanded-value mono">{{ row.actor_user_id || '—' }}</span>
                      </div>
                      <div v-if="row.target_user_id" class="audit-expanded-cell">
                        <span class="audit-expanded-label">{{ t('system.globalSettings.audit.expanded.targetUserId') }}</span>
                        <span class="audit-expanded-value mono">{{ row.target_user_id }}</span>
                      </div>
                      <div v-if="row.target_type" class="audit-expanded-cell">
                        <span class="audit-expanded-label">{{ t('system.globalSettings.audit.expanded.targetType') }}</span>
                        <span class="audit-expanded-value mono">{{ row.target_type }}</span>
                      </div>
                      <div v-if="row.target_id" class="audit-expanded-cell">
                        <span class="audit-expanded-label">{{ t('system.globalSettings.audit.expanded.targetId') }}</span>
                        <span class="audit-expanded-value mono">{{ row.target_id }}</span>
                      </div>
                    </div>
                    <div class="audit-expanded-details">
                      <span class="audit-expanded-label">{{ t('system.globalSettings.audit.expanded.details') }}</span>
                      <pre class="audit-expanded-json mono">{{ auditDetailsJSON(row) }}</pre>
                    </div>
                  </div>
                </template>
              </t-table>
            </div>

            <div ref="auditLoadSentinelEl" class="audit-load-sentinel" aria-hidden="true" />

            <div v-if="auditLoading && auditEntries.length > 0" class="audit-loading-more">
              <t-loading size="small" />
              <span>{{ t('system.globalSettings.audit.loading') }}</span>
            </div>

            <p v-if="!auditHasMore && auditEntries.length > 0 && !auditLoading" class="audit-end-hint">
              {{ t('system.globalSettings.audit.end') }}
            </p>
          </div>
        </div>
      </div>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted, computed, nextTick, watch } from 'vue'
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
  listSystemAuditLog,
  type SystemSettingItem,
  type AuditLog,
  type AuditAction,
  type AuditOutcome,
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

const overriddenCount = computed(() => settings.value.filter(hasOverride).length)
const restartRequiredCount = computed(() => settings.value.filter((item) => item.requires_restart).length)

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

// ---- Platform audit log (system-scope, tenant_id=0) ---------------------
//
// Wired against GET /api/v1/system/admin/audit-log (SystemAdmin only).
// The drawer mirrors the structural choices of the tenant audit drawer
// in frontend/src/views/settings/TenantMembers.vue: cursor-paged by
// descending id, lazy-loaded on first open, infinite-scroll via an
// IntersectionObserver pinned to the scroll root. Refresh is explicit
// via a button inside the drawer so closing/reopening doesn't quietly
// fire a new fetch the operator didn't ask for.

const auditDrawerVisible = ref(false)
const auditEntries = ref<AuditLog[]>([])
const auditLoading = ref(false)
const auditError = ref('')
const auditCursor = ref<number>(0) // 0 = "from the top"
const auditHasMore = ref(true)
const auditLoadedOnce = ref(false)
const AUDIT_PAGE_SIZE = 50

const auditScrollRoot = ref<HTMLElement | null>(null)
const auditLoadSentinelEl = ref<HTMLElement | null>(null)
let auditScrollObserver: IntersectionObserver | null = null

// We render a stacked "date / time" cell rather than ellipsing a single
// flat string — the screenshot review surfaced that the joined form
// reads as a wall of identical timestamps when 50 events fall in the
// same minute. A two-line cell also frees horizontal space for the
// (much more important) target diff column.

const auditColumns = computed(() => [
  { colKey: 'created_at', title: t('system.globalSettings.audit.columns.time'), width: 120 },
  { colKey: 'actor', title: t('system.globalSettings.audit.columns.actor'), width: 180 },
  { colKey: 'action', title: t('system.globalSettings.audit.columns.action'), width: 150 },
  {
    colKey: 'target',
    title: t('system.globalSettings.audit.columns.target'),
    // No fixed width / no ellipsis: this is where the diff content
    // lives, and clipping it to "..." negates the entire reason we
    // synthesise the cell in the first place. CSS handles wrapping.
    minWidth: 240,
  },
  { colKey: 'outcome', title: t('system.globalSettings.audit.columns.outcome'), width: 80, align: 'center' as const },
])

// Two helpers feeding the stacked time cell. Falling back to the raw
// string keeps the table readable when Intl chokes on a malformed
// timestamp (shouldn't happen, but cheap to defend).
function formatAuditDatePart(s: string | undefined): string {
  if (!s) return '-'
  try {
    return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).format(new Date(s))
  } catch {
    return s
  }
}

function formatAuditTimePart(s: string | undefined): string {
  if (!s) return ''
  try {
    return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }).format(new Date(s))
  } catch {
    return ''
  }
}

// Action chip colour: promote is reassuring green; revoke / setting
// change are worth a second look (warning orange); denied / access
// rejections show danger so an operator can scan a chronological feed
// and immediately spot abuse.
function auditActionTheme(
  action: AuditAction,
): 'success' | 'warning' | 'danger' | 'primary' | 'default' {
  switch (action) {
    case 'system.admin_promoted':
      return 'success'
    case 'system.admin_revoked':
    case 'system.setting_changed':
    case 'system.queue_task_retried':
    case 'system.queue_task_run_now':
      return 'warning'
    case 'system.user_password_reset':
    case 'system.queue_task_deleted':
    case 'system.queue_task_cancelled':
      return 'danger'
    case 'rbac.access_denied':
      return 'danger'
    default:
      return 'default'
  }
}

function auditOutcomeTheme(o: AuditOutcome): 'success' | 'danger' | 'default' {
  if (o === 'denied') return 'danger'
  if (o === 'success') return 'success'
  return 'default'
}

// i18n 键名含点号（system.setting_changed）。用 t(path) 会按路径拆开解析，
// 无法命中 system.globalSettings.audit.action['system.*'] — 必须 tm + 字面量键。
function formatAuditAction(action: AuditAction): string {
  const bag = tm('system.globalSettings.audit.action') as unknown
  if (bag !== null && typeof bag === 'object' && typeof (bag as Record<string, string>)[action] === 'string') {
    return (bag as Record<string, string>)[action]
  }
  return action
}

// Actor display: most system-admin operations are performed by humans
// whose username we don't have a local mirror of. The audit row only
// carries the UUID, so we fall back to a short prefix for readability.
// If the actor is the current user, we resolve to their own profile.
function auditActorLabel(userId: string): string {
  const me = authStore.user
  if (me && me.id === userId) {
    return me.username?.trim() || me.email?.trim() || userId.slice(0, 8)
  }
  return userId.slice(0, 8)
}

function auditActorRoleLabel(role: string): string {
  const key = `system.globalSettings.audit.actorRole.${role}`
  if (te(key)) return t(key)
  return role
}

// Target rendering is split into two pieces so the table cell can
// show a structural "subject" (key / user) on its own line and the
// value diff on a second, monospaced line — far more legible than a
// single concatenated string clipped by ellipsis.

function auditDetailsObject(row: AuditLog): Record<string, unknown> | null {
  if (row.details && typeof row.details === 'object') {
    return row.details as Record<string, unknown>
  }
  return null
}

// First line of the target cell — the thing being acted on.
//   - setting_changed (regular key): the registry key
//   - setting_changed (bulk apply):  i18n label "(bulk) default storage quota"
//   - admin_promoted/revoked:        username (email) of the affected user
function auditTargetKey(row: AuditLog): string {
  const details = auditDetailsObject(row)
  if (row.action === 'system.setting_changed') {
    if (row.target_type === 'tenant_storage_quota') {
      return t('system.globalSettings.audit.target.bulkQuota')
    }
    if (details && typeof details.key === 'string' && details.key) return details.key
    return row.target_id || row.target_type || ''
  }
  if (
    row.action === 'system.admin_promoted'
    || row.action === 'system.admin_revoked'
    || row.action === 'system.user_password_reset'
  ) {
    if (!details) return row.target_user_id ? row.target_user_id.slice(0, 8) : ''
    const name = typeof details.target_username === 'string' ? details.target_username : ''
    const mail = typeof details.target_email === 'string' ? details.target_email : ''
    if (name && mail) return `${name} (${mail})`
    return name || mail || (row.target_user_id ? row.target_user_id.slice(0, 8) : '')
  }
  if (
    row.action === 'system.queue_task_retried'
    || row.action === 'system.queue_task_run_now'
    || row.action === 'system.queue_task_cancelled'
    || row.action === 'system.queue_task_deleted'
  ) {
    const queue = details && typeof details.queue === 'string' ? details.queue : ''
    const taskID = details && typeof details.task_id === 'string' ? details.task_id : row.target_id
    return queue && taskID ? `${queue}:${taskID}` : taskID || queue
  }
  if (row.target_user_id) return row.target_user_id.slice(0, 8)
  if (row.target_id) {
    return row.target_type ? `${row.target_type}:${row.target_id}` : row.target_id
  }
  return ''
}

// Second line — the change diff. Returns an empty string when there
// is no meaningful diff to display (the expanded row still surfaces
// the raw JSON for forensics).
function auditTargetDiff(row: AuditLog): string {
  const details = auditDetailsObject(row)
  if (!details) return ''
  if (row.action === 'system.setting_changed') {
    if (row.target_type === 'tenant_storage_quota') {
      const affected = typeof details.affected === 'number' ? details.affected : null
      const gb = typeof details.quota_gb === 'number' ? details.quota_gb : null
      if (affected !== null && gb !== null) {
        return t('system.globalSettings.audit.target.bulkQuotaDiff', {
          count: String(affected),
          gb: String(gb),
        })
      }
      return ''
    }
    return formatSettingDiff(details)
  }
  if (row.action === 'system.admin_promoted' && typeof details.idempotent === 'boolean') {
    if (details.idempotent === true) {
      return t('system.globalSettings.audit.target.promoteIdempotent')
    }
    return ''
  }
  if (row.action === 'system.admin_revoked' && typeof details.changed === 'boolean') {
    if (details.changed === false) {
      return t('system.globalSettings.audit.target.revokeNoop')
    }
    return ''
  }
  if (row.action === 'rbac.access_denied' && typeof details.required_role === 'string') {
    return t('system.globalSettings.audit.target.requiredRole', { role: details.required_role })
  }
  return ''
}

const SETTING_DIFF_MAX_LEN = 80
function formatSettingDiff(details: Record<string, unknown>): string {
  const fmt = (v: unknown): string => {
    if (v === null || v === undefined) {
      return t('system.globalSettings.audit.target.valueNull')
    }
    if (typeof v === 'string') return v
    if (typeof v === 'number' || typeof v === 'boolean') return String(v)
    try {
      return JSON.stringify(v)
    } catch {
      return String(v)
    }
  }
  const truncate = (s: string): string =>
    s.length > SETTING_DIFF_MAX_LEN ? s.slice(0, SETTING_DIFF_MAX_LEN - 1) + '…' : s
  const oldStr = truncate(fmt(details.old_value))
  const newStr = truncate(fmt(details.new_value))
  if (oldStr === newStr) return ''
  return `${oldStr} → ${newStr}`
}

// Expanded row state — local set of row ids the user has opened.
// We keep it ephemeral (not persisted) so reopening the drawer always
// shows a clean, collapsed view.
const auditExpandedRowKeys = ref<number[]>([])

function onAuditExpandChange(value: (string | number)[]) {
  // t-table calls back with the *new* full list of expanded keys.
  // Normalise to numbers because AuditLog.id is always a number.
  auditExpandedRowKeys.value = value
    .map((v) => (typeof v === 'number' ? v : Number(v)))
    .filter((v) => Number.isFinite(v))
}

function auditDetailsJSON(row: AuditLog): string {
  if (row.details === null || row.details === undefined) return '{}'
  if (typeof row.details === 'string') return row.details
  try {
    return JSON.stringify(row.details, null, 2)
  } catch {
    return String(row.details)
  }
}

async function loadAuditLog(reset: boolean) {
  if (auditLoading.value) return
  if (!reset && !auditHasMore.value) return

  auditLoading.value = true
  auditError.value = ''
  try {
    const resp = await listSystemAuditLog({
      after_id: reset ? undefined : auditCursor.value || undefined,
      limit: AUDIT_PAGE_SIZE,
    })
    if (resp.success) {
      const rows = resp.data || []
      auditEntries.value = reset ? rows : [...auditEntries.value, ...rows]
      auditCursor.value = resp.next_cursor || 0
      // Same convention as tenant audit: next_cursor=0 means "no
      // older rows", regardless of whether the current page was empty.
      auditHasMore.value = !!resp.next_cursor && rows.length > 0
      auditLoadedOnce.value = true
    } else {
      auditError.value = resp.message || t('system.globalSettings.audit.errors.generic')
    }
  } catch (err: any) {
    const status = err?.status
    if (status === 403) {
      auditError.value = t('system.globalSettings.audit.forbidden')
    } else {
      auditError.value = err?.message || t('system.globalSettings.audit.errors.generic')
    }
  } finally {
    auditLoading.value = false
  }
}

function detachAuditInfiniteScroll() {
  auditScrollObserver?.disconnect()
  auditScrollObserver = null
}

function attachAuditInfiniteScroll() {
  detachAuditInfiniteScroll()
  const root = auditScrollRoot.value
  const sentinel = auditLoadSentinelEl.value
  if (!root || !sentinel) return

  auditScrollObserver = new IntersectionObserver(
    (entries) => {
      const hitBottom = entries.some((e) => e.isIntersecting)
      if (!hitBottom || !auditHasMore.value || auditLoading.value) return
      void loadAuditLog(false)
    },
    { root, rootMargin: '100px 0px', threshold: 0 },
  )
  auditScrollObserver.observe(sentinel)
}

function reloadAuditLog() {
  auditCursor.value = 0
  auditHasMore.value = true
  loadAuditLog(true)
}

function openAuditDrawer() {
  auditDrawerVisible.value = true
  if (!auditLoadedOnce.value) {
    loadAuditLog(true)
  }
}

watch(
  auditDrawerVisible,
  async (open) => {
    if (!open) {
      detachAuditInfiniteScroll()
      return
    }
    await nextTick()
    attachAuditInfiniteScroll()
  },
  { flush: 'post' },
)

watch(
  () => auditError.value,
  async () => {
    if (!auditDrawerVisible.value) return
    await nextTick()
    if (!auditError.value) {
      attachAuditInfiniteScroll()
      return
    }
    detachAuditInfiniteScroll()
  },
  { flush: 'post' },
)

onUnmounted(() => {
  detachAuditInfiniteScroll()
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

/* Title + audit-log entry sit on the same row, parallel to the layout
   used in tenant member settings — keeps secondary actions anchored to
   the section header instead of floating loose above content. */
.section-header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;

  h2 {
    margin: 0;
  }
}

.header-audit-btn {
  flex-shrink: 0;
}

/* ===== Audit drawer (mirrors TenantMembers.vue's audit panel) =========
   Kept scoped to this view rather than extracted to a shared component:
   the two pages render distinct action labels and target formatters,
   and a generic <AuditLogPanel> would have to thread enough props
   through to make the abstraction more expensive than the duplication.
   Revisit if a third audit surface appears. */
.audit-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding-top: 8px;
}

.audit-panel--drawer {
  padding-top: 0;
}

.audit-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--td-bg-color-secondarycontainer);
  padding: 12px 16px;
  border-radius: 8px;
  gap: 12px;

  .audit-desc {
    flex: 1;
    min-width: 0;
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }

  .audit-refresh-btn {
    flex-shrink: 0;
  }
}

.audit-drawer-inner {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  gap: 14px;
  min-height: 0;
  width: 100%;
  box-sizing: border-box;
}

.audit-drawer-fill {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.audit-drawer-branch {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.audit-drawer-branch--error {
  justify-content: center;

  .error-inline {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 20px 0 8px;
  }
}

.audit-drawer-branch--empty.empty-state--audit {
  flex: 1 1 auto;
  justify-content: center;
  align-items: center;
  padding: 24px 12px;
  min-height: 0;
}

.audit-scroll-area {
  flex: 1 1 auto;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
}

.audit-load-sentinel {
  height: 1px;
  width: 100%;
  pointer-events: none;
}

.audit-loading-more {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 12px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.audit-end-hint {
  text-align: center;
  font-size: 12px;
  color: var(--td-text-color-disabled);
  padding: 8px 0 14px;
  margin: 0;
}

.audit-time {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.3;

  .audit-time-date {
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }

  .audit-time-clock {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    font-variant-numeric: tabular-nums;
  }
}

.audit-actor {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.3;
  min-width: 0;

  .audit-actor-name {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .audit-actor-role {
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }
}

.audit-target {
  display: flex;
  flex-direction: column;
  gap: 4px;
  line-height: 1.35;
  min-width: 0;
  padding: 2px 0;

  .audit-target-key {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    word-break: break-all;
    font-family: var(--td-font-family-mono, monospace);
  }

  .audit-target-diff {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    font-family: var(--td-font-family-mono, monospace);
    word-break: break-all;
    line-height: 1.4;
  }

  .audit-target-empty {
    color: var(--td-text-color-placeholder);
  }
}

/* Expanded row: surfaces the raw audit row context (UUIDs, target
   type/id, full details JSON) so an investigator never has to hop to
   psql for the verbatim event. Background steps off-card to make the
   nested context visually distinct from the table rows. */
.audit-expanded {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 12px 16px;
  background: var(--td-bg-color-container-hover);
}

.audit-expanded-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 10px 18px;
}

.audit-expanded-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.audit-expanded-label {
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-secondary);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.audit-expanded-value {
  font-size: 12px;
  color: var(--td-text-color-primary);
  word-break: break-all;
}

.audit-expanded-details {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.audit-expanded-json {
  margin: 0;
  padding: 10px 12px;
  font-size: 12px;
  line-height: 1.55;
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 280px;
  overflow: auto;
}

.mono {
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Consolas, monospace);
}

.data-table-shell {
  overflow-x: auto;
  border-radius: 10px;
  border: 1px solid var(--td-component-stroke);
  background-color: var(--td-bg-color-container);

  &:deep(thead th) {
    font-weight: 600;
    font-size: 13px;
    background-color: var(--td-bg-color-secondarycontainer) !important;
  }

  &:deep(.t-table td),
  &:deep(.t-table th) {
    padding-top: 14px;
    padding-bottom: 14px;
    /* Center the cell content vertically: most rows have at least one
       single-line tag column (action / outcome), and a top-aligned
       layout floats those chips above the multi-line target cell —
       middle keeps the row's visual weight unified. */
    vertical-align: middle;
  }
}

/* Audit-specific table polish: no zebra stripes (the per-row "key /
   diff" stack already provides enough separation between rows; stripes
   on top read as visual noise), softer hover, denser separator. */
.audit-table-shell {
  /* Sticky table head: long audit feeds (50+ rows) lose the column
     labels once the user scrolls, which makes "what's this column?"
     a constant relearn. The drawer's outer scroll container is
     `.audit-scroll-area`, so `top: 0` here pins thead to that
     container's top. z-index keeps it above row hover/expand
     backgrounds, and the explicit background plus bottom border
     prevent row content bleeding through during scroll. */
  &:deep(thead th) {
    position: sticky;
    top: 0;
    z-index: 2;
    box-shadow: inset 0 -1px 0 var(--td-component-stroke);
  }

  &:deep(.t-table tbody tr:hover > td) {
    background-color: var(--td-bg-color-container-hover);
  }

  &:deep(.t-table tbody tr.t-table__expanded-row > td) {
    padding: 0 !important;
    background-color: transparent;
  }

  &:deep(.t-table__expandable-icon-cell) {
    width: 36px;
  }
}

.settings-overview {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
  min-height: 24px;
}

.auto-save-note {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--td-text-color-secondary);

  .t-icon {
    color: var(--td-success-color);
  }
}

.settings-overview-tags {
  display: flex;
  align-items: center;
  gap: 8px;
}

.config-source-details {
  margin-bottom: 18px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);

  summary {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    min-height: 38px;
    padding: 0 12px;
    cursor: pointer;
    color: var(--td-text-color-secondary);
    font-size: 13px;
    list-style: none;

    &::-webkit-details-marker {
      display: none;
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color-focus);
      outline-offset: 2px;
    }
  }

  &[open] {
    background: var(--td-bg-color-secondarycontainer);

    .config-source-chevron {
      transform: rotate(180deg);
    }
  }
}

.config-source-summary {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-weight: 500;

  .t-icon {
    color: var(--td-brand-color);
  }
}

.config-source-chevron {
  flex-shrink: 0;
  transition: transform 0.2s ease;
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

.priority-hint-list {
  margin: 0;
  padding: 0 36px 12px 34px;
  font-size: 13px;
  line-height: 1.65;
  color: var(--td-text-color-primary);
  list-style: disc;

  li + li {
    margin-top: 4px;
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
  .settings-overview,
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

/* t-drawer teleports its content-wrapper to body, so the height-chain
   needed for the internal scroll area must be declared globally. Same
   pattern as `.tenant-members-audit-drawer` in TenantMembers.vue. */
.t-drawer.system-settings-audit-drawer.t-drawer--right .t-drawer__content-wrapper--right {
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  max-height: 100vh;
  height: 100%;
}

.t-drawer.system-settings-audit-drawer .t-drawer__body {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  box-sizing: border-box;
  overflow: hidden !important;
}
</style>
