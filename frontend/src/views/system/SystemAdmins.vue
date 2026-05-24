<template>
  <!--
    SystemAdmins — list & manage the platform-wide system administrators.
    Grants/revokes via the /api/v1/system/admin/{promote,revoke,list}
    endpoints (server-side guarded by RequireSystemAdmin middleware).

    P0 milestone scope is deliberately minimal:
      - Paginated list of current system admins
      - Promote a user by email
      - Revoke an existing admin (with last-admin & self-revoke server guards)

    Future milestones can layer richer UX on top — search, audit-log
    drawer, bulk operations — but the API surface and gating are stable.
  -->
  <div class="system-admins">
    <div class="page-header">
      <div>
        <h1 class="page-title">系统管理员</h1>
        <p class="page-desc">
          系统管理员独立于租户角色，拥有平台级管理权限（管理其他系统管理员、全局设置、内置模型等）。
        </p>
      </div>
      <t-button theme="primary" @click="openPromoteDialog">
        <template #icon><t-icon name="add" /></template>
        提升用户为系统管理员
      </t-button>
    </div>

    <t-card class="admins-card" :bordered="false">
      <t-table
        row-key="id"
        :data="admins"
        :columns="columns"
        :loading="loading"
        size="medium"
        hover
        stripe
      >
        <template #status="{ row }">
          <t-tag v-if="row.is_active" theme="success" variant="light">
            活跃
          </t-tag>
          <t-tag v-else theme="warning" variant="light">已停用</t-tag>
        </template>
        <template #actions="{ row }">
          <t-button
            theme="danger"
            variant="text"
            size="small"
            :disabled="row.id === currentUserId"
            @click="confirmRevoke(row)"
          >
            撤销权限
          </t-button>
        </template>
      </t-table>

      <div class="pagination-bar" v-if="total > 0">
        <t-pagination
          v-model="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-size-options="[20, 50, 100]"
          @change="loadAdmins"
        />
      </div>
    </t-card>

    <!-- Promote dialog -->
    <t-dialog
      v-model:visible="promoteDialogVisible"
      header="提升用户为系统管理员"
      :confirm-btn="{ content: '确认提升', loading: promoting }"
      @confirm="submitPromote"
      @close="resetPromoteDialog"
    >
      <p class="dialog-hint">
        请输入要提升的用户的 ID。该用户必须已经在系统中注册过；操作幂等，已是管理员的用户不会被重复提升。
      </p>
      <t-input
        v-model="promoteUserId"
        placeholder="用户 ID（UUID 形式）"
        :disabled="promoting"
        clearable
      />
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import {
  listSystemAdmins,
  promoteUserToSystemAdmin,
  revokeSystemAdmin,
  type SystemAdminUser,
} from '@/api/system'

const authStore = useAuthStore()
const currentUserId = computed(() => authStore.currentUserId)

const admins = ref<SystemAdminUser[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)

const promoteDialogVisible = ref(false)
const promoteUserId = ref('')
const promoting = ref(false)

// Columns are defined as a computed (vs a static const) so future
// localization/translation can hook in without component refactor.
const columns = [
  { colKey: 'username', title: '用户名', width: 180 },
  { colKey: 'email', title: '邮箱', width: 280 },
  { colKey: 'status', title: '状态', width: 100 },
  { colKey: 'created_at', title: '注册时间', width: 200 },
  { colKey: 'actions', title: '操作', width: 120, align: 'right' as const },
]

async function loadAdmins() {
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize.value
    // listSystemAdmins resolves to ListSystemAdminsResponse directly —
    // utils/request.ts already unwraps axios's response.data wrapper at
    // the interceptor layer (see line 97), so an extra `.data` here
    // would explode at runtime as "Cannot read property 'admins' of
    // undefined" and surface as the generic load-failed toast.
    const res = await listSystemAdmins({ offset, limit: pageSize.value })
    admins.value = res.admins ?? []
    total.value = res.total ?? 0
  } catch (err) {
    // Surface a toast and bail — keeping prior list so the user isn't
    // left staring at an empty table on a transient failure.
    MessagePlugin.error('加载系统管理员列表失败')
    console.error('listSystemAdmins failed:', err)
  } finally {
    loading.value = false
  }
}

function openPromoteDialog() {
  promoteUserId.value = ''
  promoteDialogVisible.value = true
}

function resetPromoteDialog() {
  promoteUserId.value = ''
  promoting.value = false
}

async function submitPromote() {
  const uid = promoteUserId.value.trim()
  if (!uid) {
    MessagePlugin.warning('请输入用户 ID')
    return
  }
  promoting.value = true
  try {
    await promoteUserToSystemAdmin(uid)
    MessagePlugin.success('已提升为系统管理员')
    promoteDialogVisible.value = false
    await loadAdmins()
  } catch (err: any) {
    // utils/request.ts 把后端的 {error: "..."} 提升到了顶层 message
    // 字段（line 197-213），同时保留原始 data 字段。我们优先读 message
    // —— 它已经是后端的 human-readable 错误文案，覆盖了 user-not-found
    // / 自我撤销等业务错误场景。
    const msg = err?.message || err?.error || '提升失败，请检查用户 ID 后重试'
    MessagePlugin.error(msg)
  } finally {
    promoting.value = false
  }
}

function confirmRevoke(row: SystemAdminUser) {
  // Confirmation dialog is mandatory — revoking is destructive and
  // the server's last-admin / self-revoke guards return 400 with a
  // human-readable message which we re-surface in submitRevoke.
  const confirmDialog = DialogPlugin.confirm({
    header: '撤销系统管理员权限',
    body: `确定要撤销用户 "${row.username}" (${row.email}) 的系统管理员权限吗？`,
    confirmBtn: { content: '确认撤销', theme: 'danger' },
    cancelBtn: '取消',
    onConfirm: async () => {
      await submitRevoke(row.id)
      confirmDialog.destroy()
    },
    onClose: () => confirmDialog.destroy(),
  })
}

async function submitRevoke(userId: string) {
  try {
    await revokeSystemAdmin(userId)
    MessagePlugin.success('已撤销系统管理员权限')
    await loadAdmins()
  } catch (err: any) {
    // 同 submitPromote 的注释 —— 后端 last-admin / self-revoke 保护
    // 走 400 + {error: "..."}，被拦截器抬到 err.message。
    const msg = err?.message || err?.error || '撤销失败'
    MessagePlugin.error(msg)
  }
}

onMounted(() => {
  loadAdmins()
})
</script>

<style scoped>
.system-admins {
  max-width: 1100px;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 20px;
}

.page-title {
  margin: 0 0 6px;
  font-size: 22px;
  font-weight: 600;
  color: var(--td-text-color-primary, #000);
}

.page-desc {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--td-text-color-secondary, #666);
  max-width: 720px;
}

.admins-card {
  margin-top: 8px;
}

.pagination-bar {
  margin-top: 16px;
  display: flex;
  justify-content: flex-end;
}

.dialog-hint {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--td-text-color-secondary, #666);
  line-height: 1.6;
}
</style>
