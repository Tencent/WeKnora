<template>
  <div class="tenant-members">
    <div class="section-header">
      <h2>{{ $t('tenantMember.title') }}</h2>
      <p class="section-description">{{ $t('tenantMember.sectionDescription') }}</p>
    </div>

    <!-- Compact role-permissions matrix. Mirrors the OrganizationSettingsModal
         pattern so Owners (and Admins, who can't add members) understand
         what each role can actually do before they invite or promote. -->
    <div class="permissions-compact">
      <div class="permissions-compact-header">
        <span class="permissions-compact-title">{{ $t('tenantMember.permissions.title') }}</span>
        <span class="permissions-compact-desc">{{ $t('tenantMember.permissions.desc') }}</span>
      </div>
      <div class="permissions-compact-grid">
        <div
          v-for="r in roleMatrixOrder"
          :key="r"
          :class="['perm-role-block', r, { 'is-me': currentRole === r }]"
        >
          <div class="perm-role-tag">
            <t-icon :name="roleMatrixIcon(r)" size="12px" />
            <span>{{ $t('tenantMember.role.' + r) }}</span>
            <span v-if="currentRole === r" class="me-badge">{{ $t('common.me') }}</span>
          </div>
          <div class="perm-items">
            <span
              v-for="(perm, i) in roleMatrix[r]"
              :key="i"
              :class="['perm-item', perm.has ? 'has' : 'no']"
            >
              <t-icon :name="perm.has ? 'check' : 'close'" size="12px" />
              {{ $t('tenantMember.permissions.' + perm.key) }}
            </span>
          </div>
        </div>
      </div>
    </div>

    <!-- Action bar: count + search + add-member button (Owner only). -->
    <div class="action-bar">
      <span class="count">
        {{ $t('tenantMember.totalCount', { n: members.length }) }}
      </span>
      <div class="action-bar-right">
        <t-input
          v-model="searchQuery"
          :placeholder="$t('tenantMember.searchPlaceholder')"
          clearable
          size="small"
          style="width: 220px"
        >
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-button
          v-if="canManage"
          theme="primary"
          size="small"
          @click="openAddDialog"
        >
          {{ $t('tenantMember.add.button') }}
        </t-button>
        <t-button
          v-if="canLeave"
          theme="danger"
          variant="outline"
          size="small"
          @click="confirmLeaveTenant"
        >
          {{ $t('tenantMember.leave.button') }}
        </t-button>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>{{ $t('tenantMember.loading') }}</span>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="loadMembers">{{ $t('tenantMember.retry') }}</t-button>
        </template>
      </t-alert>
    </div>

    <!-- Empty state when there are no members at all OR the search query
         filters everything out. -->
    <div v-else-if="filteredMembers.length === 0" class="empty-state">
      <t-empty
        :description="
          searchQuery
            ? $t('tenantMember.emptySearch', { q: searchQuery })
            : $t('tenantMember.empty')
        "
      />
    </div>

    <!-- Member table -->
    <t-table
      v-else
      row-key="user_id"
      :data="filteredMembers"
      :columns="columns"
      size="medium"
      hover
      stripe
    >
      <template #role="{ row }">
        <t-select
          v-if="canManage && row.user_id !== currentUserId"
          :model-value="row.role"
          :options="roleOptions"
          size="small"
          style="width: 130px"
          @change="(val: string) => onRoleChange(row, val)"
        />
        <t-tag v-else :theme="roleTagTheme(row.role)" size="medium">
          {{ $t('tenantMember.role.' + row.role) }}
        </t-tag>
      </template>
      <template #joined_at="{ row }">{{ formatDate(row.joined_at) }}</template>
      <template #actions="{ row }">
        <t-button
          v-if="canManage && row.user_id !== currentUserId"
          theme="danger"
          variant="text"
          size="small"
          @click="confirmRemove(row)"
        >
          {{ $t('tenantMember.remove.button') }}
        </t-button>
      </template>
    </t-table>

    <!-- Add dialog. We use @confirm rather than :on-confirm so we can
         keep the dialog open on validation failure / API error and let
         the user retry without retyping. -->
    <t-dialog
      v-model:visible="addDialogVisible"
      :header="$t('tenantMember.add.dialogTitle')"
      :confirm-btn="{ content: $t('tenantMember.add.submit'), loading: adding }"
      :cancel-btn="{ content: $t('common.cancel') }"
      width="480px"
      @confirm="submitAdd"
    >
      <t-form ref="addFormRef" :data="addForm" :rules="addFormRules" :label-width="80">
        <t-form-item :label="$t('tenantMember.add.emailLabel')" name="email">
          <t-input
            v-model="addForm.email"
            :placeholder="$t('tenantMember.add.emailPlaceholder')"
            clearable
          />
        </t-form-item>
        <t-form-item :label="$t('tenantMember.add.roleLabel')" name="role">
          <t-select v-model="addForm.role" :options="roleOptions" />
        </t-form-item>
      </t-form>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import {
  listMembers,
  addMember,
  updateMemberRole,
  removeMember,
  leaveTenant,
  type TenantMember,
  type TenantRole,
} from '@/api/tenant/members'

const { t, locale } = useI18n()
const authStore = useAuthStore()

// State
const members = ref<TenantMember[]>([])
const loading = ref(false)
const error = ref('')
const adding = ref(false)
const addDialogVisible = ref(false)
const addFormRef = ref<any>(null)
const searchQuery = ref('')

// Add dialog model — reset on each open. Default role is contributor:
// inviting a fresh member with viewer is too restrictive for the
// expected "let them collaborate on KBs" use case, and admin/owner
// should be a deliberate promote step after the user accepts.
const addForm = reactive<{ email: string; role: TenantRole }>({
  email: '',
  role: 'contributor',
})

// Role-aware gates. The server enforces every mutation; UI gates here
// are presentational only, matching the security note in stores/auth.ts.
const currentRole = computed<TenantRole | ''>(() => (authStore.currentTenantRole || '') as TenantRole | '')
const canManage = computed(() => currentRole.value === 'owner')
// Anyone except the last Owner can leave; we additionally hide the
// button for Owner-the-only-one because clicking would just bounce off
// the server's last-Owner check. The server is still the source of
// truth.
const canLeave = computed(() => {
  if (!currentRole.value) return false
  if (currentRole.value !== 'owner') return true
  const owners = members.value.filter((m) => m.role === 'owner').length
  return owners > 1
})
const currentUserId = computed(() => authStore.user?.id ?? '')

// Use the active tenant id from the auth store; the route only allows
// :id == active tenant (auth middleware enforces membership), so we
// don't expose a tenant picker here.
const activeTenantId = computed(() => Number(authStore.currentTenantId ?? 0))

const roleOptions = computed(() => [
  { label: t('tenantMember.role.owner'), value: 'owner' },
  { label: t('tenantMember.role.admin'), value: 'admin' },
  { label: t('tenantMember.role.contributor'), value: 'contributor' },
  { label: t('tenantMember.role.viewer'), value: 'viewer' },
])

// Static role-permissions matrix. The keys reference i18n strings under
// `tenantMember.permissions.*` so each locale can rephrase per culture.
// Keep this aligned with the design-doc §4.3 matrix and the actual
// PR 2 enforcement; if a permission moves between roles, update both
// sides in the same PR.
type RolePerm = { key: string; has: boolean }
const roleMatrixOrder: TenantRole[] = ['owner', 'admin', 'contributor', 'viewer']
const roleMatrix: Record<TenantRole, RolePerm[]> = {
  owner: [
    { key: 'manageMembers', has: true },
    { key: 'manageTenantConfig', has: true },
    { key: 'manageInfra', has: true },
    { key: 'createOwnKB', has: true },
    { key: 'readAll', has: true },
  ],
  admin: [
    { key: 'manageMembers', has: false },
    { key: 'manageTenantConfig', has: false },
    { key: 'manageInfra', has: true },
    { key: 'createOwnKB', has: true },
    { key: 'readAll', has: true },
  ],
  contributor: [
    { key: 'manageMembers', has: false },
    { key: 'manageTenantConfig', has: false },
    { key: 'manageInfra', has: false },
    { key: 'createOwnKB', has: true },
    { key: 'readAll', has: true },
  ],
  viewer: [
    { key: 'manageMembers', has: false },
    { key: 'manageTenantConfig', has: false },
    { key: 'manageInfra', has: false },
    { key: 'createOwnKB', has: false },
    { key: 'readAll', has: true },
  ],
}

function roleMatrixIcon(role: TenantRole): string {
  switch (role) {
    case 'owner':
      return 'crown'
    case 'admin':
      return 'user-safety'
    case 'contributor':
      return 'edit'
    default:
      return 'browse'
  }
}

const columns = computed(() => [
  { colKey: 'username', title: t('tenantMember.columns.username'), ellipsis: true },
  { colKey: 'email', title: t('tenantMember.columns.email'), ellipsis: true },
  { colKey: 'role', title: t('tenantMember.columns.role'), width: 160 },
  { colKey: 'joined_at', title: t('tenantMember.columns.joinedAt'), width: 180 },
  { colKey: 'actions', title: '', width: 100, align: 'right' },
])

const addFormRules = {
  email: [
    { required: true, message: t('tenantMember.errors.emailRequired'), trigger: 'blur' },
    { email: true, message: t('tenantMember.errors.emailFormat'), trigger: 'blur' },
  ],
  role: [{ required: true, message: t('tenantMember.errors.roleRequired'), trigger: 'change' }],
}

// Pretty role tag colour: Owner stands out, Admin is warning, the rest
// stay neutral so the table doesn't become a confetti cannon.
function roleTagTheme(role: TenantRole): 'primary' | 'warning' | 'success' | 'default' {
  switch (role) {
    case 'owner':
      return 'primary'
    case 'admin':
      return 'warning'
    case 'contributor':
      return 'success'
    default:
      return 'default'
  }
}

function formatDate(s: string | undefined): string {
  if (!s) return '-'
  try {
    const d = new Date(s)
    return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }).format(d)
  } catch {
    return s
  }
}

// Client-side filter by username/email; case-insensitive substring
// match keeps the implementation tiny while covering the common
// "find this person quickly" use case. Server-side search is overkill
// for the expected per-tenant member counts.
const filteredMembers = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return members.value
  return members.value.filter((m) => {
    return (
      (m.username || '').toLowerCase().includes(q) ||
      (m.email || '').toLowerCase().includes(q)
    )
  })
})

async function loadMembers() {
  if (!activeTenantId.value) {
    // No active tenant yet — keep silent; the watch below will retry
    // once the auth store finishes hydrating. Showing an error toast
    // on cold-mount would flash on every refresh.
    return
  }
  loading.value = true
  error.value = ''
  try {
    const resp = await listMembers(activeTenantId.value)
    if (resp.success && resp.data) {
      members.value = resp.data.members
    } else {
      error.value = resp.message || t('tenantMember.errors.generic')
    }
  } catch (err: any) {
    error.value = err?.message || t('tenantMember.errors.generic')
  } finally {
    loading.value = false
  }
}

function openAddDialog() {
  addForm.email = ''
  addForm.role = 'contributor'
  addDialogVisible.value = true
}

async function submitAdd() {
  // t-form's validate returns true on success or an object of field
  // errors on failure. Anything other than `true` => keep dialog open
  // with the inline messages shown. The @confirm event lets us control
  // visibility manually; :on-confirm would close on every return.
  const valid = await addFormRef.value?.validate?.()
  if (valid !== true) return

  adding.value = true
  try {
    const resp = await addMember(activeTenantId.value, {
      email: addForm.email.trim(),
      role: addForm.role,
    })
    if (resp.success && resp.data) {
      members.value = [...members.value, resp.data]
      addDialogVisible.value = false
      MessagePlugin.success(t('tenantMember.add.success'))
    } else {
      MessagePlugin.error(resp.message || t('tenantMember.errors.generic'))
    }
  } catch (err: any) {
    // The axios interceptor already flattens errors to
    // { status, message, error?, ... }, so we read err.status directly
    // — err.response.status is undefined here.
    const status = err?.status
    if (status === 404) {
      MessagePlugin.error(t('tenantMember.errors.userNotFound'))
    } else if (status === 409) {
      MessagePlugin.error(t('tenantMember.errors.alreadyMember'))
    } else if (status === 400) {
      MessagePlugin.error(err?.message || t('tenantMember.errors.invalidRole'))
    } else {
      MessagePlugin.error(err?.message || t('tenantMember.errors.generic'))
    }
  } finally {
    adding.value = false
  }
}

async function onRoleChange(row: TenantMember, newRole: string) {
  const prev = row.role
  const next = newRole as TenantRole
  if (prev === next) return

  try {
    const resp = await updateMemberRole(activeTenantId.value, row.user_id, next)
    if (resp.success) {
      row.role = next
      MessagePlugin.success(t('tenantMember.roleChange.success'))
      return
    }
    MessagePlugin.error(resp.message || t('tenantMember.errors.generic'))
  } catch (err: any) {
    const status = err?.status
    if (status === 409) {
      MessagePlugin.error(t('tenantMember.errors.lastOwner'))
    } else if (status === 404) {
      MessagePlugin.error(t('tenantMember.errors.notFound'))
    } else {
      MessagePlugin.error(err?.message || t('tenantMember.errors.generic'))
    }
    // The t-select is bound via :model-value (one-way), so its rendered
    // value stays at `prev` automatically — no DOM hack needed.
  }
}

function confirmRemove(row: TenantMember) {
  // Use DialogPlugin.confirm rather than t-popconfirm so we can describe
  // the consequences in a multi-line modal (matches the pattern used in
  // ApiInfo.vue's reset-API-key flow).
  const dlg = DialogPlugin.confirm({
    header: t('tenantMember.remove.confirmTitle'),
    body: t('tenantMember.remove.confirmBody', { name: row.username || row.email }),
    confirmBtn: { content: t('tenantMember.remove.confirm'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      try {
        const resp = await removeMember(activeTenantId.value, row.user_id)
        if (resp.success) {
          members.value = members.value.filter((m) => m.user_id !== row.user_id)
          MessagePlugin.success(t('tenantMember.remove.success'))
        } else {
          MessagePlugin.error(resp.message || t('tenantMember.errors.generic'))
        }
      } catch (err: any) {
        const status = err?.status
        if (status === 409) {
          MessagePlugin.error(t('tenantMember.errors.lastOwner'))
        } else if (status === 404) {
          MessagePlugin.error(t('tenantMember.errors.notFound'))
        } else {
          MessagePlugin.error(err?.message || t('tenantMember.errors.generic'))
        }
      } finally {
        dlg.destroy()
      }
    },
    onClose: () => dlg.destroy(),
  })
}

function confirmLeaveTenant() {
  const dlg = DialogPlugin.confirm({
    header: t('tenantMember.leave.confirmTitle'),
    body: t('tenantMember.leave.confirmBody'),
    confirmBtn: { content: t('tenantMember.leave.confirm'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      try {
        const resp = await leaveTenant(activeTenantId.value)
        if (resp.success) {
          MessagePlugin.success(t('tenantMember.leave.success'))
          // The user is now no longer a member of this tenant. Logging
          // them out is the simplest correct behaviour; their next
          // login will land them in whatever home tenant they have
          // left, with PR 1's auto-promote covering the orphan case.
          authStore.logout()
          window.location.href = '/login'
        } else {
          MessagePlugin.error(resp.message || t('tenantMember.errors.generic'))
        }
      } catch (err: any) {
        const status = err?.status
        if (status === 409) {
          MessagePlugin.error(t('tenantMember.errors.lastOwner'))
        } else {
          MessagePlugin.error(err?.message || t('tenantMember.errors.generic'))
        }
      } finally {
        dlg.destroy()
      }
    },
    onClose: () => dlg.destroy(),
  })
}

// Re-load whenever the active tenant resolves (or changes via the
// tenant switcher). onMounted alone would race with auth-store
// hydration — currentTenantId is often 0 at the moment this component
// mounts on a cold reload.
watch(
  activeTenantId,
  (id) => {
    if (id) loadMembers()
  },
  { immediate: true },
)

onMounted(() => {
  if (activeTenantId.value) loadMembers()
})
</script>

<style lang="less" scoped>
.tenant-members {
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
    color: var(--td-text-color-secondary);
    font-size: 13px;
    margin: 0;
  }
}

.permissions-compact {
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-border-level-1-color);
  border-radius: 6px;
  padding: 12px 14px;
  margin-bottom: 20px;

  .permissions-compact-header {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin-bottom: 10px;

    .permissions-compact-title {
      font-size: 13px;
      font-weight: 600;
      color: var(--td-text-color-primary);
    }
    .permissions-compact-desc {
      font-size: 12px;
      color: var(--td-text-color-secondary);
    }
  }

  .permissions-compact-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: 10px;
  }

  .perm-role-block {
    border: 1px solid var(--td-border-level-1-color);
    border-radius: 4px;
    padding: 8px 10px;
    background: var(--td-bg-color-container);

    &.is-me {
      border-color: var(--td-brand-color);
    }

    .perm-role-tag {
      display: flex;
      align-items: center;
      gap: 4px;
      font-size: 12px;
      font-weight: 600;
      color: var(--td-text-color-primary);
      margin-bottom: 6px;

      .me-badge {
        margin-left: auto;
        font-size: 11px;
        font-weight: 400;
        color: var(--td-brand-color);
        padding: 1px 6px;
        background: var(--td-brand-color-light);
        border-radius: 8px;
      }
    }

    .perm-items {
      display: flex;
      flex-direction: column;
      gap: 3px;

      .perm-item {
        display: flex;
        align-items: center;
        gap: 4px;
        font-size: 12px;

        &.has {
          color: var(--td-text-color-primary);
        }
        &.no {
          color: var(--td-text-color-disabled);
          text-decoration: line-through;
        }
      }
    }
  }
}

.action-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
  gap: 12px;

  .count {
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }

  .action-bar-right {
    display: flex;
    align-items: center;
    gap: 8px;
  }
}

.loading-inline,
.error-inline {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 16px 0;
}

.empty-state {
  padding: 40px 0;
  display: flex;
  justify-content: center;
}
</style>
