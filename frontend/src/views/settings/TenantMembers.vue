<template>
  <div class="tenant-members">
    <div class="section-header">
      <h2>{{ $t('tenantMember.title') }}</h2>
      <p class="section-description">{{ $t('tenantMember.sectionDescription') }}</p>
    </div>

    <!-- Action bar: count + add-member button (Owner only). -->
    <div class="action-bar">
      <span class="count">
        {{ $t('tenantMember.totalCount', { n: members.length }) }}
      </span>
      <t-button
        v-if="canManage"
        theme="primary"
        size="small"
        @click="openAddDialog"
      >
        {{ $t('tenantMember.add.button') }}
      </t-button>
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

    <!-- Member table -->
    <t-table
      v-else
      row-key="user_id"
      :data="members"
      :columns="columns"
      size="medium"
      hover
      stripe
    >
      <template #role="{ row }">
        <t-select
          v-if="canManage && row.user_id !== authStore.user?.id"
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
          v-if="canManage && row.user_id !== authStore.user?.id"
          theme="danger"
          variant="text"
          size="small"
          @click="confirmRemove(row)"
        >
          {{ $t('tenantMember.remove.button') }}
        </t-button>
      </template>
    </t-table>

    <!-- Add dialog -->
    <t-dialog
      v-model:visible="addDialogVisible"
      :header="$t('tenantMember.add.dialogTitle')"
      :on-confirm="submitAdd"
      :confirm-btn="{ content: $t('tenantMember.add.submit'), loading: adding }"
      :cancel-btn="{ content: $t('common.cancel') }"
      width="480px"
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
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import {
  listMembers,
  addMember,
  updateMemberRole,
  removeMember,
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

// Add dialog model — reset on each open. Default role is contributor:
// inviting a fresh member with viewer is too restrictive for the
// expected "let them collaborate on KBs" use case, and admin/owner
// should be a deliberate promote step after the user accepts.
const addForm = reactive<{ email: string; role: TenantRole }>({
  email: '',
  role: 'contributor',
})

// canManage gates every mutation widget. The server enforces the same
// rule (Owner+); the UI gate just hides controls that would 403 anyway,
// matching the security note in stores/auth.ts.
const canManage = computed(() => authStore.currentTenantRole === 'owner')

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

async function loadMembers() {
  if (!activeTenantId.value) {
    error.value = t('tenantMember.errors.noTenant')
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
  // errors on failure. We treat anything non-true as a validation
  // failure — keeps the dialog open with the inline messages shown.
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
    // Map the server's structured error responses (404 / 409 / 400) to
    // human-readable messages. The Axios layer surfaces them as
    // err.response?.data?.error in this codebase.
    const status = err?.response?.status
    const apiMsg = err?.response?.data?.error || err?.message
    if (status === 404) {
      MessagePlugin.error(t('tenantMember.errors.userNotFound'))
    } else if (status === 409) {
      MessagePlugin.error(t('tenantMember.errors.alreadyMember'))
    } else {
      MessagePlugin.error(apiMsg || t('tenantMember.errors.generic'))
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
    const status = err?.response?.status
    if (status === 409) {
      MessagePlugin.error(t('tenantMember.errors.lastOwner'))
    } else if (status === 404) {
      MessagePlugin.error(t('tenantMember.errors.notFound'))
    } else {
      MessagePlugin.error(err?.response?.data?.error || err?.message || t('tenantMember.errors.generic'))
    }
    // Force the t-select to revert visually by mutating then restoring.
    // Vue's reactive system needs a real change to refire the bind.
    row.role = '' as TenantRole
    setTimeout(() => {
      row.role = prev
    }, 0)
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
        const status = err?.response?.status
        if (status === 409) {
          MessagePlugin.error(t('tenantMember.errors.lastOwner'))
        } else if (status === 404) {
          MessagePlugin.error(t('tenantMember.errors.notFound'))
        } else {
          MessagePlugin.error(err?.response?.data?.error || err?.message || t('tenantMember.errors.generic'))
        }
      } finally {
        dlg.destroy()
      }
    },
    onClose: () => dlg.destroy(),
  })
}

onMounted(() => {
  loadMembers()
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

.action-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;

  .count {
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }
}

.loading-inline,
.error-inline {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 16px 0;
}
</style>
