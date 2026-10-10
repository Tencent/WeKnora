<template>
  <div class="local-users">
    <!-- Counters cover the whole table, not just the current page, so they
         stay stable while paging. -->
    <div class="stat-row">
      <div v-for="card in statCards" :key="card.key" class="stat-card">
        <span class="stat-card__label">{{ card.label }}</span>
        <span class="stat-card__value">{{ card.value }}</span>
      </div>
    </div>

    <div class="toolbar">
      <t-input
        v-model="keyword"
        class="toolbar__search"
        :placeholder="t('userManagement.local.searchPlaceholder')"
        clearable
        @enter="reload"
        @clear="reload"
      >
        <template #prefix-icon><t-icon name="search" /></template>
      </t-input>
      <div class="toolbar__spacer" />
      <t-button variant="outline" :loading="loading" @click="reload">
        <template #icon><t-icon name="refresh" /></template>
        {{ t('userManagement.local.refresh') }}
      </t-button>
      <!-- CreateUserDialog anchors its popup to the default slot, so the
           trigger button lives inside it (same wiring as SystemSettings). -->
      <CreateUserDialog v-model:visible="createUserVisible" @announced="onAnnounced">
        <t-button theme="primary">
          <template #icon><t-icon name="user-add" /></template>
          {{ t('userManagement.local.createUser') }}
        </t-button>
      </CreateUserDialog>
    </div>

    <t-table
      row-key="id"
      :data="users"
      :columns="columns"
      :loading="loading"
      size="medium"
      hover
      :pagination="pagination"
      @page-change="onPageChange"
    >
      <template #user="{ row }">
        <div class="user-cell">
          <span class="user-cell__name">{{ row.username }}</span>
          <span class="user-cell__email">{{ row.email }}</span>
        </div>
      </template>

      <template #workspace="{ row }">
        <span v-if="row.tenant_name" class="muted">{{ row.tenant_name }}</span>
        <span v-else class="muted muted--empty">{{ t('userManagement.local.noWorkspace') }}</span>
      </template>

      <template #source="{ row }">
        <t-tag :theme="sourceTheme(row.auth_source)" size="small" variant="light-outline">
          {{ t(`userManagement.local.source.${row.auth_source}`) }}
        </t-tag>
        <!-- OIDC-only accounts have no local password, so "reset password" is
             meaningless for them. Flag it rather than let the admin wonder. -->
        <t-tooltip
          v-if="!row.has_local_password"
          :content="t('userManagement.local.noLocalPassword')"
          placement="top"
        >
          <t-icon name="info-circle" class="source-warn" />
        </t-tooltip>
      </template>

      <template #role="{ row }">
        <t-tag :theme="row.is_system_admin ? 'warning' : 'default'" size="small" variant="light">
          {{
            row.is_system_admin
              ? t('userManagement.local.role.systemAdmin')
              : t('userManagement.local.role.member')
          }}
        </t-tag>
      </template>

      <template #status="{ row }">
        <span class="status-cell" :class="row.is_active ? 'status-cell--on' : 'status-cell--off'">
          <span class="status-dot" />
          {{
            row.is_active
              ? t('userManagement.local.status.active')
              : t('userManagement.local.status.disabled')
          }}
        </span>
      </template>

      <template #created_at="{ row }">
        <span class="muted">{{ formatDate(row.created_at) }}</span>
      </template>

      <template #actions="{ row }">
        <div class="row-actions">
          <!-- Reset opens a single shared modal pre-filled with this row's
               email, so the admin never has to retype it. -->
          <t-tooltip :content="t('userManagement.local.action.resetPassword')" placement="top">
            <t-button
              shape="square"
              variant="text"
              :disabled="isSelf(row)"
              @click="openResetPassword(row)"
            >
              <t-icon name="lock-on" />
            </t-button>
          </t-tooltip>

          <!-- The server also refuses disable/delete on the caller's own row
               and delete of the last system admin; disabling up front avoids a
               round-trip that can only fail. -->
          <t-popconfirm
            v-if="row.is_active"
            :content="t('userManagement.local.confirmDisableBody', { name: row.email })"
            :confirm-btn="{ content: t('userManagement.local.action.disable'), theme: 'warning' }"
            :cancel-btn="{ content: t('common.cancel') }"
            placement="bottom-right"
            @confirm="disableUser(row)"
          >
            <t-button
              shape="square"
              variant="text"
              :disabled="isSelf(row)"
              :title="t('userManagement.local.action.disable')"
              @click.stop
            >
              <t-icon name="minus-circle" />
            </t-button>
          </t-popconfirm>

          <t-popconfirm
            v-else
            :content="t('userManagement.local.confirmEnableBody', { name: row.email })"
            :confirm-btn="{ content: t('userManagement.local.action.enable'), theme: 'primary' }"
            :cancel-btn="{ content: t('common.cancel') }"
            placement="bottom-right"
            @confirm="enableUser(row)"
          >
            <t-button
              shape="square"
              variant="text"
              theme="success"
              :title="t('userManagement.local.action.enable')"
              @click.stop
            >
              <t-icon name="check-circle" />
            </t-button>
          </t-popconfirm>

          <t-popconfirm
            :content="t('userManagement.local.confirmDeleteBody', { name: row.email })"
            :confirm-btn="{ content: t('userManagement.local.action.delete'), theme: 'danger' }"
            :cancel-btn="{ content: t('common.cancel') }"
            placement="bottom-right"
            @confirm="deleteUser(row)"
          >
            <t-button
              shape="square"
              variant="text"
              theme="danger"
              :disabled="isSelf(row)"
              :title="t('userManagement.local.action.delete')"
              @click.stop
            >
              <t-icon name="delete" />
            </t-button>
          </t-popconfirm>
        </div>
      </template>

      <template #empty>
        <div class="empty-state">{{ t('userManagement.local.empty') }}</div>
      </template>
    </t-table>

    <!-- Shared reset-password modal. Reuses the SystemAdmin reset API and the
         shared password-policy rules; only the trigger differs from the
         系统管理 page (per-row here rather than a single form). -->
    <t-dialog
      v-model:visible="resetPasswordVisible"
      :header="t('system.globalSettings.passwordReset.dialogTitle')"
      :confirm-btn="{
        content: t('system.globalSettings.passwordReset.confirmBtn'),
        loading: resetSubmitting,
        theme: 'danger',
      }"
      :cancel-btn="{ content: t('common.cancel') }"
      width="520px"
      @confirm="submitResetPassword"
    >
      <t-alert
        theme="warning"
        :message="t('system.globalSettings.passwordReset.warning')"
        class="reset-alert"
      />
      <t-form
        ref="resetFormRef"
        :data="resetForm"
        :rules="resetRules"
        label-align="top"
        class="reset-form"
      >
        <t-form-item :label="t('system.globalSettings.passwordReset.emailLabel')" name="email">
          <t-input v-model="resetForm.email" readonly />
        </t-form-item>
        <t-form-item
          :label="t('system.globalSettings.passwordReset.newPasswordLabel')"
          name="newPassword"
        >
          <t-input
            v-model="resetForm.newPassword"
            type="password"
            autocomplete="new-password"
            :disabled="resetSubmitting"
            :placeholder="t('system.globalSettings.passwordReset.newPasswordPlaceholder')"
          >
            <template #prefix-icon><t-icon name="lock-on" /></template>
          </t-input>
        </t-form-item>
        <t-form-item
          :label="t('system.globalSettings.passwordReset.confirmPasswordLabel')"
          name="confirmPassword"
        >
          <t-input
            v-model="resetForm.confirmPassword"
            type="password"
            autocomplete="new-password"
            :disabled="resetSubmitting"
            :placeholder="t('system.globalSettings.passwordReset.confirmPasswordPlaceholder')"
          />
        </t-form-item>
      </t-form>
    </t-dialog>

    <div class="sr-only" role="status" aria-live="polite">{{ announcement }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, reactive } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import type { FormInstanceFunctions, FormRule, PageInfo, PrimaryTableCol } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import CreateUserDialog from '../CreateUserDialog.vue'
import { getAuthConfig, type AuthConfigResponse } from '@/api/auth'
import { resetUserPassword } from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import { newPasswordRules } from '@/utils/passwordPolicy'
import {
  deleteManagedUser,
  disableManagedUser,
  enableManagedUser,
  listManagedUsers,
  type AccountAuthSource,
  type ManagedUser,
  type ManagedUserStats,
} from '@/api/system/user-management'

const { t, locale } = useI18n()
const authStore = useAuthStore()

const users = ref<ManagedUser[]>([])
const stats = ref<ManagedUserStats>({
  total: 0,
  active: 0,
  disabled: 0,
  admins: 0,
  ldap_users: 0,
  oidc_users: 0,
})
const loading = ref(false)
const keyword = ref('')
const page = ref(1)
const pageSize = ref(20)
const createUserVisible = ref(false)
const announcement = ref('')

const statCards = computed(() => [
  { key: 'total', label: t('userManagement.local.stats.total'), value: stats.value.total },
  { key: 'active', label: t('userManagement.local.stats.active'), value: stats.value.active },
  { key: 'disabled', label: t('userManagement.local.stats.disabled'), value: stats.value.disabled },
  { key: 'admins', label: t('userManagement.local.stats.admins'), value: stats.value.admins },
  { key: 'ldap', label: t('userManagement.local.stats.ldapUsers'), value: stats.value.ldap_users },
  { key: 'oidc', label: t('userManagement.local.stats.oidcUsers'), value: stats.value.oidc_users },
])

const columns: PrimaryTableCol<ManagedUser>[] = [
  { colKey: 'user', title: t('userManagement.local.columns.user'), minWidth: 200 },
  { colKey: 'workspace', title: t('userManagement.local.columns.workspace'), minWidth: 150 },
  { colKey: 'source', title: t('userManagement.local.columns.source'), width: 130 },
  { colKey: 'role', title: t('userManagement.local.columns.role'), width: 130 },
  { colKey: 'status', title: t('userManagement.local.columns.status'), width: 110 },
  { colKey: 'created_at', title: t('userManagement.local.columns.createdAt'), width: 170 },
  {
    colKey: 'actions',
    title: t('userManagement.local.columns.actions'),
    width: 160,
    align: 'right',
  },
]

const pagination = computed(() => ({
  current: page.value,
  pageSize: pageSize.value,
  total: stats.value.total,
  showJumper: true,
}))

// ---- reset password modal ----

const resetPasswordVisible = ref(false)
const resetSubmitting = ref(false)
const resetFormRef = ref<FormInstanceFunctions>()
const resetForm = reactive({ email: '', newPassword: '', confirmPassword: '' })
const complexPasswordEnabled = ref(false)

const resetRules = computed<Record<string, FormRule[]>>(() => ({
  newPassword: newPasswordRules(t, complexPasswordEnabled.value),
  confirmPassword: [
    { required: true, message: t('auth.confirmPasswordRequired'), trigger: 'blur' },
    {
      validator: (value: string) => value === resetForm.newPassword,
      message: t('auth.passwordMismatch'),
      trigger: 'blur',
    },
  ],
}))

/**
 * isSelf guards the actions the server also refuses on the caller's own row.
 * UI-only so the button reads as unavailable instead of failing on click.
 */
const isSelf = (row: ManagedUser) => !!authStore.currentUserId && row.id === authStore.currentUserId

const sourceTheme = (source: AccountAuthSource) =>
  source === 'ldap' ? 'success' : source === 'oidc' ? 'primary' : 'default'

function formatDate(s: string | undefined): string {
  if (!s) return '-'
  try {
    return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }).format(new Date(s))
  } catch {
    return s
  }
}

async function load() {
  loading.value = true
  try {
    const resp = await listManagedUsers({
      keyword: keyword.value || undefined,
      offset: (page.value - 1) * pageSize.value,
      limit: pageSize.value,
    })
    users.value = resp.users || []
    if (resp.stats) stats.value = resp.stats
  } catch (error) {
    MessagePlugin.error(
      error instanceof Error && error.message
        ? `${t('userManagement.local.loadFailed')}: ${error.message}`
        : t('userManagement.local.loadFailed'),
    )
  } finally {
    loading.value = false
  }
}

/** Re-run the query from page 1 — used after any mutation or filter change. */
function reload() {
  page.value = 1
  void load()
}

function onPageChange(pageInfo: PageInfo) {
  page.value = pageInfo.current
  pageSize.value = pageInfo.pageSize
  void load()
}

function onAnnounced(msg: string) {
  announcement.value = msg
  reload()
}

async function openResetPassword(row: ManagedUser) {
  resetForm.email = row.email
  resetForm.newPassword = ''
  resetForm.confirmPassword = ''
  resetPasswordVisible.value = true
  try {
    const cfg: AuthConfigResponse = await getAuthConfig()
    complexPasswordEnabled.value = !!cfg.complex_password_enabled
  } catch {
    // Fall back to the permissive rule set; the server still enforces policy.
    complexPasswordEnabled.value = false
  }
}

async function submitResetPassword() {
  const result = await resetFormRef.value?.validate?.()
  if (result !== true) return
  resetSubmitting.value = true
  try {
    await resetUserPassword({ email: resetForm.email, new_password: resetForm.newPassword })
    const msg = t('system.globalSettings.passwordReset.success')
    announcement.value = msg
    MessagePlugin.success(msg)
    resetPasswordVisible.value = false
  } catch (error) {
    MessagePlugin.error(
      error instanceof Error && error.message
        ? error.message
        : t('system.globalSettings.passwordReset.failed'),
    )
  } finally {
    resetSubmitting.value = false
  }
}

async function disableUser(row: ManagedUser) {
  try {
    await disableManagedUser(row.id)
    MessagePlugin.success(t('userManagement.local.disableSuccess'))
    void load()
  } catch (error) {
    MessagePlugin.error(error instanceof Error && error.message ? error.message : String(error))
  }
}

async function enableUser(row: ManagedUser) {
  try {
    await enableManagedUser(row.id)
    MessagePlugin.success(t('userManagement.local.enableSuccess'))
    void load()
  } catch (error) {
    MessagePlugin.error(error instanceof Error && error.message ? error.message : String(error))
  }
}

async function deleteUser(row: ManagedUser) {
  try {
    await deleteManagedUser(row.id)
    MessagePlugin.success(t('userManagement.local.deleteSuccess'))
    // A delete can empty the last page, so step back when that happens.
    if (users.value.length === 1 && page.value > 1) page.value -= 1
    void load()
  } catch (error) {
    MessagePlugin.error(error instanceof Error && error.message ? error.message : String(error))
  }
}

onMounted(() => {
  void load()
})
</script>

<style scoped>
.local-users {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.stat-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 12px;
}

.stat-card {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
}

.stat-card__label {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.stat-card__value {
  font-size: 20px;
  font-weight: 600;
  line-height: 1.2;
  color: var(--td-text-color-primary);
}

.toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
}

.toolbar__search {
  max-width: 320px;
}

.toolbar__spacer {
  flex: 1;
}

.user-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.user-cell__name {
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.user-cell__email {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.muted {
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.muted--empty {
  font-style: italic;
  opacity: 0.75;
}

.source-warn {
  margin-left: 6px;
  color: var(--td-warning-color);
  vertical-align: middle;
}

.status-cell {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.status-cell--on {
  color: var(--td-success-color);
}

.status-cell--off {
  color: var(--td-text-color-placeholder);
}

.row-actions {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  justify-content: flex-end;
}

.empty-state {
  padding: 24px 0;
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.reset-alert {
  margin-bottom: 14px;
}

.reset-form :deep(.t-form__item) {
  margin-bottom: 14px;
}
</style>
