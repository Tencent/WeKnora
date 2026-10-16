<template>
  <section class="user-management" :aria-label="t('system.globalSettings.userList.rowLabel')">
    <Teleport to="#system-users-actions">
      <div v-show="active">
        <CreateUserDialog v-model:visible="createVisible" :active="active" @created="fetchPage(0)" @announced="announce">
          <t-button variant="text" size="small" class="user-management__create"
            :disabled="loading || busyId !== null || resetVisibleId !== null">
            <template #icon><t-icon name="add" size="16px" /></template>
            {{ t('system.globalSettings.createUser.action') }}
          </t-button>
        </CreateUserDialog>
      </div>
    </Teleport>
    <Teleport to="#system-users-toolbar">
      <div v-show="active" class="user-management__tools">
        <t-input v-model="search" :disabled="locked" class="user-management__search" clearable
          :placeholder="t('system.globalSettings.userList.searchPlaceholder')"
          :aria-label="t('system.globalSettings.userList.searchPlaceholder')">
          <template #prefix-icon><t-icon name="search" size="16px" /></template>
        </t-input>
        <t-button variant="text" shape="square" :loading="loading" :disabled="locked"
          :title="t('system.globalSettings.userList.refresh')" :aria-label="t('system.globalSettings.userList.refresh')"
          @click="fetchPage(offset)">
          <template #icon><t-icon name="refresh" size="16px" /></template>
        </t-button>
      </div>
    </Teleport>
    <t-alert v-if="loadError" theme="error" :message="loadError" />
    <div class="user-management__table">
      <div class="user-management__scroll">
        <t-table :aria-label="t('system.globalSettings.userList.title')" row-key="id" :data="filteredUsers" :columns="columns" :loading="loading" size="medium" hover>
          <template #user="{ row }">
            <div class="user-management__identity">
              <span class="user-management__name">{{ row.username }}
                <span v-if="row.id === authStore.currentUserId" class="user-management__self">{{ t('common.me') }}</span>
              </span>
              <span class="user-management__email">{{ row.email }}</span>
            </div>
          </template>
          <template #status="{ row }">
            <span class="user-management__status" :class="{ 'is-active': row.is_active }">
              <span class="user-management__status-dot" aria-hidden="true" />
              {{ row.is_active ? t('system.globalSettings.userList.active') : t('system.globalSettings.userList.inactive') }}
            </span>
          </template>
          <template #role="{ row }">
            <span v-if="row.id === authStore.currentUserId" class="user-management__own-role">
              {{ t('system.globalSettings.userList.roleAdmin') }}
            </span>
            <t-popconfirm v-else :visible="active && pendingRole?.id === row.id"
              :content="row.is_system_admin
                ? t('system.globalSettings.userList.confirmRevoke', { name: row.username })
                : t('system.globalSettings.userList.confirmPromote', { name: row.username })"
              :popup-props="{ trigger: 'context-menu', overlayClassName: 'wk-popover user-management-confirm-popup' }" placement="left" theme="warning"
              :confirm-btn="{ content: t(row.is_system_admin ? 'system.globalSettings.userList.revokeBtn' : 'system.globalSettings.userList.promoteBtn'), theme: row.is_system_admin ? 'danger' : 'primary', loading: busyId === row.id }"
              :cancel-btn="t('common.cancel')" @confirm="changeRole(row)" @cancel="pendingRole = null"
              @visible-change="(v: boolean) => { if (!v && !busyId) pendingRole = null }">
              <t-select :model-value="row.is_system_admin ? 'admin' : 'user'" :options="roleOptions"
                :aria-label="`${row.username} · ${t('system.globalSettings.userList.colRole')}`" :borderless="true"
                class="user-management__role" :disabled="loading || locked"
                :popup-props="{ overlayClassName: 'wk-popover user-management-role-popup' }"
                @change="(value: unknown) => { if (value !== (row.is_system_admin ? 'admin' : 'user')) pendingRole = row }" />
            </t-popconfirm>
          </template>
          <template #created="{ row }"><span class="user-management__date">{{ formatDate(row.created_at) }}</span></template>
          <template #actions="{ row }">
            <div class="user-management__actions">
              <ResetPasswordDialog :visible="resetVisibleId === row.id" :active="active" :email="row.email"
                @update:visible="value => { resetVisibleId = value ? row.id : null }"
                @busy-change="resetBusy = $event" @announced="announce">
                <t-button variant="text" class="user-management__action"
                  :disabled="loading || busyId !== null || resetBusy || (resetVisibleId !== null && resetVisibleId !== row.id) || createVisible || row.id === authStore.currentUserId || !row.is_active"
                  :title="t('system.globalSettings.userList.resetPassword')" :aria-label="t('system.globalSettings.userList.resetPassword')">
                  {{ t('system.globalSettings.userList.resetPassword') }}
                </t-button>
              </ResetPasswordDialog>
              <t-popconfirm :visible="active && statusVisibleId === row.id" placement="left" theme="warning"
                :popup-props="{ overlayClassName: 'wk-popover user-management-confirm-popup' }"
                :content="t(row.is_active ? 'system.globalSettings.userList.confirmDisable' : 'system.globalSettings.userList.confirmEnable', { name: row.username })"
                :confirm-btn="{ content: t(row.is_active ? 'system.globalSettings.userList.disable' : 'system.globalSettings.userList.enable'), theme: row.is_active ? 'danger' : 'primary', loading: busyId === row.id }"
                :cancel-btn="t('common.cancel')" @visible-change="(value: boolean) => { statusVisibleId = value ? row.id : null }"
                @confirm="changeStatus(row)">
                <t-button variant="text" class="user-management__action"
                  :disabled="loading || locked || row.id === authStore.currentUserId"
                  :title="t(row.is_active ? 'system.globalSettings.userList.disable' : 'system.globalSettings.userList.enable')"
                  :aria-label="t(row.is_active ? 'system.globalSettings.userList.disable' : 'system.globalSettings.userList.enable')">
                  {{ t(row.is_active ? 'system.globalSettings.userList.disable' : 'system.globalSettings.userList.enable') }}
                </t-button>
              </t-popconfirm>
            </div>
          </template>
        </t-table>
      </div>
      <div class="user-management__pager">
        <span class="user-management__count">{{ t('system.globalSettings.userList.pageCount', { shown: filteredUsers.length, total: users.length }) }}</span>
        <t-button variant="text" shape="square" size="small" :disabled="offset === 0 || loading || locked"
          :aria-label="t('system.globalSettings.userList.prev')" :title="t('system.globalSettings.userList.prev')" @click="fetchPage(offset - limit)">
          <template #icon><t-icon name="chevron-left" /></template>
        </t-button>
        <span class="user-management__page">{{ Math.floor(offset / limit) + 1 }}</span>
        <t-button variant="text" shape="square" size="small" :disabled="!hasMore || loading || locked"
          :aria-label="t('system.globalSettings.userList.next')" :title="t('system.globalSettings.userList.next')" @click="fetchPage(offset + limit)">
          <template #icon><t-icon name="chevron-right" /></template>
        </t-button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { listAllUsers, setUserStatus, promoteUserToSystemAdmin, revokeSystemAdmin, type SystemAdminUser } from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import CreateUserDialog from './CreateUserDialog.vue'
import ResetPasswordDialog from './ResetPasswordDialog.vue'

const props = defineProps<{ active: boolean }>()
const emit = defineEmits<{ announced: [string] }>()
const { t, locale } = useI18n()
const authStore = useAuthStore()
const users = ref<SystemAdminUser[]>([])
const loading = ref(false)
const loadError = ref('')
const offset = ref(0)
const limit = 20
const hasMore = ref(false)
const search = ref('')
const busyId = ref<string | null>(null)
const pendingRole = ref<SystemAdminUser | null>(null)
const statusVisibleId = ref<string | null>(null)
const resetVisibleId = ref<string | null>(null)
const resetBusy = ref(false)
const createVisible = ref(false)
const locked = computed(() => busyId.value !== null || resetVisibleId.value !== null || createVisible.value)
const roleOptions = computed(() => [
  { value: 'user', label: t('system.globalSettings.userList.roleUser') },
  { value: 'admin', label: t('system.globalSettings.userList.roleAdmin') },
])
const columns = computed(() => [
  { colKey: 'user', title: t('system.globalSettings.userList.colUsername'), minWidth: 230 },
  { colKey: 'role', title: t('system.globalSettings.userList.colRole'), width: 160 },
  { colKey: 'status', title: t('system.globalSettings.userList.colStatus'), width: 100 },
  { colKey: 'created', title: t('system.globalSettings.userList.colCreated'), width: 130 },
  { colKey: 'actions', title: t('system.globalSettings.userList.colAction'), width: 170 },
])
const filteredUsers = computed(() => {
  const query = search.value.trim().toLowerCase()
  return users.value.filter(user => !query || `${user.username} ${user.email}`.toLowerCase().includes(query))
})
function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString(locale.value)
}
function errorMessage(error: any, key: string): string {
  return error?.response?.data?.error || error?.message || t(key)
}
function announce(message: string) { emit('announced', message) }
async function fetchPage(nextOffset: number) {
  if (loading.value || resetBusy.value) return
  loading.value = true
  loadError.value = ''
  pendingRole.value = null
  statusVisibleId.value = null
  try {
    const response = await listAllUsers({ offset: nextOffset, limit })
    users.value = response.users
    hasMore.value = response.has_more
    offset.value = response.offset
  } catch (error) {
    loadError.value = errorMessage(error, 'system.globalSettings.userList.loadFailed')
  } finally { loading.value = false }
}
async function changeStatus(row: SystemAdminUser) {
  if (busyId.value || row.id === authStore.currentUserId) return
  busyId.value = row.id
  try {
    const response = await setUserStatus({ user_id: row.id, is_active: !row.is_active })
    row.is_active = response.is_active
    const message = t(row.is_active ? 'system.globalSettings.userList.enabledMsg' : 'system.globalSettings.userList.disabledMsg', { name: row.username })
    MessagePlugin.success(message)
    announce(message)
  } catch (error) { MessagePlugin.error(errorMessage(error, 'system.globalSettings.userList.actionFailed')) }
  finally { busyId.value = null; statusVisibleId.value = null }
}
async function changeRole(row: SystemAdminUser) {
  if (busyId.value || row.id === authStore.currentUserId) return
  busyId.value = row.id
  try {
    const updated = row.is_system_admin ? await revokeSystemAdmin(row.id) : await promoteUserToSystemAdmin({ user_id: row.id })
    row.is_system_admin = updated.is_system_admin
    const message = t(row.is_system_admin ? 'system.globalSettings.userList.promotedMsg' : 'system.globalSettings.userList.revokedMsg', { name: row.username })
    MessagePlugin.success(message)
    announce(message)
  } catch (error) { MessagePlugin.error(errorMessage(error, 'system.globalSettings.userList.roleFailed')) }
  finally { busyId.value = null; pendingRole.value = null }
}
watch(() => props.active, active => {
  if (active && !locked.value) fetchPage(offset.value)
  if (!active) { pendingRole.value = null; statusVisibleId.value = null }
}, { immediate: true })
</script>

<style scoped lang="less" src="./userManagement.less"></style>
<style lang="less">
.user-management-role-popup, .user-management-confirm-popup { z-index: 3050 !important; }
</style>
