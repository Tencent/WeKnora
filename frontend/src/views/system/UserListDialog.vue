<template>
  <t-dialog
    :visible="visible"
    @update:visible="v => emit('update:visible', v)"
    :header="t('system.globalSettings.userList.title')"
    width="980px"
    :footer="false"
    destroy-on-close
  >
    <div class="user-list">
      <div class="user-list__toolbar">
        <t-input
          v-model="search"
          class="user-list__search"
          :placeholder="t('system.globalSettings.userList.searchPlaceholder')"
          clearable
        >
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-button variant="outline" :loading="loading" @click="reload">
          <template #icon><t-icon name="refresh" /></template>
          {{ t('system.globalSettings.userList.refresh') }}
        </t-button>
      </div>

      <t-table
        row-key="id"
        :data="filteredUsers"
        :loading="loading"
        :columns="columns"
        size="small"
        bordered
        :max-height="440"
        hover
      >
        <template #status="{ row }">
          <t-tag :theme="row.is_active ? 'success' : 'default'" variant="light" size="small">
            {{ row.is_active ? t('system.globalSettings.userList.active') : t('system.globalSettings.userList.inactive') }}
          </t-tag>
        </template>
        <template #admin="{ row }">
          <t-popconfirm
            v-if="row.is_system_admin"
            :content="t('system.globalSettings.userList.confirmRevoke', { name: row.username })"
            theme="warning"
            :confirm-btn="{ content: t('system.globalSettings.userList.revokeBtn'), theme: 'danger', loading: roleBusyId === row.id }"
            @confirm="toggleRole(row)"
          >
            <t-button
              variant="text"
              size="small"
              theme="danger"
              :loading="roleBusyId === row.id"
              :disabled="row.id === authStore.currentUserId"
            >
              {{ t('system.globalSettings.userList.roleAdmin') }}
            </t-button>
          </t-popconfirm>
          <t-popconfirm
            v-else
            :content="t('system.globalSettings.userList.confirmPromote', { name: row.username })"
            theme="warning"
            :confirm-btn="{ content: t('system.globalSettings.userList.promoteBtn'), theme: 'primary', loading: roleBusyId === row.id }"
            @confirm="toggleRole(row)"
          >
            <t-button variant="text" size="small" theme="primary" :loading="roleBusyId === row.id">
              {{ t('system.globalSettings.userList.roleUser') }}
            </t-button>
          </t-popconfirm>
        </template>
        <template #created="{ row }">{{ formatDate(row.created_at) }}</template>
        <template #operation="{ row }">
          <t-popconfirm
            :content="row.is_active
              ? t('system.globalSettings.userList.confirmDisable', { name: row.username })
              : t('system.globalSettings.userList.confirmEnable', { name: row.username })"
            theme="warning"
            @confirm="toggleStatus(row)"
          >
            <t-button
              variant="text"
              size="small"
              :theme="row.is_active ? 'danger' : 'success'"
              :loading="busyId === row.id"
            >
              {{ row.is_active ? t('system.globalSettings.userList.disable') : t('system.globalSettings.userList.enable') }}
            </t-button>
          </t-popconfirm>
          <t-button
            variant="text"
            size="small"
            theme="primary"
            :disabled="!row.is_active"
            @click="openReset(row)"
          >
            {{ t('system.globalSettings.userList.resetPassword') }}
          </t-button>
        </template>
      </t-table>

      <div class="user-list__pager">
        <span class="user-list__count">{{ t('system.globalSettings.userList.pageCount', { shown: filteredUsers.length, total: users.length }) }}</span>
        <t-button variant="outline" size="small" :disabled="offset === 0" :loading="loading" @click="prevPage">
          <template #icon><t-icon name="chevron-left" /></template>
          {{ t('system.globalSettings.userList.prev') }}
        </t-button>
        <span class="user-list__page">{{ pageNumber }}</span>
        <t-button variant="outline" size="small" :disabled="!hasMore" :loading="loading" @click="nextPage">
          {{ t('system.globalSettings.userList.next') }}
          <template #icon><t-icon name="chevron-right" /></template>
        </t-button>
      </div>
    </div>

  </t-dialog>

  <!-- Reset-password dialog: sibling of the list dialog to avoid nested t-dialog issues -->
  <t-dialog
    :visible="resetVisible"
    @update:visible="onResetVisibleChange"
    :header="t('system.globalSettings.userList.resetTitle', { name: resetTarget?.username ?? '' })"
    width="480px"
    :confirm-on-enter="true"
    :confirm-loading="resetBusy"
    :close-on-overlay-click="true"
    @confirm="submitReset"
  >
    <p class="user-list__reset-hint">
      {{ t('system.globalSettings.userList.resetHint', { email: resetTarget?.email }) }}
    </p>
    <t-input
      v-model="newPassword"
      type="password"
      :placeholder="t('system.globalSettings.userList.newPasswordPlaceholder')"
      :status="resetError ? 'error' : 'default'"
      :tips="resetError || undefined"
    />
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  listAllUsers,
  setUserStatus,
  resetUserPassword,
  promoteUserToSystemAdmin,
  revokeSystemAdmin,
  type SystemAdminUser,
} from '@/api/system'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const authStore = useAuthStore()

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{
  (e: 'update:visible', v: boolean): void
}>()

const visible = computed({
  get: () => props.visible,
  set: v => emit('update:visible', v),
})

const users = ref<SystemAdminUser[]>([])
const loading = ref(false)
const offset = ref(0)
const limit = 20
const hasMore = ref(false)
const search = ref('')
const busyId = ref<string | null>(null)
const roleBusyId = ref<string | null>(null)

const resetVisible = ref(false)
const resetTarget = ref<SystemAdminUser | null>(null)
const newPassword = ref('')
const resetError = ref('')
const resetBusy = ref(false)

const pageNumber = computed(() => Math.floor(offset.value / limit) + 1)

const filteredUsers = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return users.value
  return users.value.filter(
    u => u.username.toLowerCase().includes(q) || u.email.toLowerCase().includes(q),
  )
})

const columns = computed(() => [
  { colKey: 'username', title: t('system.globalSettings.userList.colUsername'), minWidth: 110, ellipsis: true },
  { colKey: 'email', title: t('system.globalSettings.userList.colEmail'), minWidth: 180, ellipsis: true },
  { colKey: 'status', title: t('system.globalSettings.userList.colStatus'), width: 80 },
  { colKey: 'admin', title: t('system.globalSettings.userList.colRole'), width: 150 },
  { colKey: 'created', title: t('system.globalSettings.userList.colCreated'), width: 100 },
  { colKey: 'operation', title: t('system.globalSettings.userList.colAction'), width: 150, fixed: 'right' as const },
])

function formatDate(s: string): string {
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function backendError(e: any, fallbackKey: string): string {
  return e?.response?.data?.error || e?.message || t(fallbackKey)
}

async function fetchPage() {
  loading.value = true
  try {
    const res = await listAllUsers({ offset: offset.value, limit })
    users.value = res.users
    hasMore.value = res.has_more
  } catch (e: any) {
    MessagePlugin.error(backendError(e, 'system.globalSettings.userList.loadFailed'))
  } finally {
    loading.value = false
  }
}

function reload() {
  return fetchPage()
}
function nextPage() {
  offset.value += limit
  fetchPage()
}
function prevPage() {
  offset.value = Math.max(0, offset.value - limit)
  fetchPage()
}

async function toggleStatus(row: SystemAdminUser) {
  busyId.value = row.id
  try {
    await setUserStatus({ user_id: row.id, is_active: !row.is_active })
    row.is_active = !row.is_active
    MessagePlugin.success(
      row.is_active
        ? t('system.globalSettings.userList.enabledMsg', { name: row.username })
        : t('system.globalSettings.userList.disabledMsg', { name: row.username }),
    )
  } catch (e: any) {
    MessagePlugin.error(backendError(e, 'system.globalSettings.userList.actionFailed'))
  } finally {
    busyId.value = null
  }
}

async function toggleRole(row: SystemAdminUser) {
  roleBusyId.value = row.id
  try {
    if (row.is_system_admin) {
      await revokeSystemAdmin(row.id)
      row.is_system_admin = false
      MessagePlugin.success(t('system.globalSettings.userList.revokedMsg', { name: row.username }))
    } else {
      await promoteUserToSystemAdmin({ user_id: row.id })
      row.is_system_admin = true
      MessagePlugin.success(t('system.globalSettings.userList.promotedMsg', { name: row.username }))
    }
  } catch (e: any) {
    MessagePlugin.error(backendError(e, 'system.globalSettings.userList.roleFailed'))
  } finally {
    roleBusyId.value = null
  }
}

function openReset(row: SystemAdminUser) {
  resetTarget.value = row
  newPassword.value = ''
  resetError.value = ''
  resetVisible.value = true
}

function onResetVisibleChange(v: boolean) {
  if (!v) closeReset()
}

function closeReset() {
  resetVisible.value = false
  newPassword.value = ''
  resetError.value = ''
}

async function submitReset() {
  if (!resetTarget.value) return
  const pwd = newPassword.value
  if (!pwd || pwd.length < 8) {
    resetError.value = t('system.globalSettings.userList.passwordTooShort')
    return
  }
  resetBusy.value = true
  try {
    await resetUserPassword({ email: resetTarget.value.email, new_password: pwd })
    MessagePlugin.success(t('system.globalSettings.userList.resetSuccess', { name: resetTarget.value.username }))
    closeReset()
  } catch (e: any) {
    resetError.value = backendError(e, 'system.globalSettings.userList.resetFailed')
  } finally {
    resetBusy.value = false
  }
}

watch(
  () => props.visible,
  v => {
    if (v) {
      offset.value = 0
      search.value = ''
      fetchPage()
    }
  },
)

onMounted(() => {
  if (props.visible) fetchPage()
})
</script>

<style scoped src="./userList.less"></style>
