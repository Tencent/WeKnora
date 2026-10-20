<template>
  <!-- 通知入口：合并展示"待处理邀请"与站内通知（自动化运行结果、等待审批等）。
       触发区域由调用方通过默认插槽渲染（侧栏底部用户栏右侧），面板向右弹出。
       通知靠轮询刷新，页面不可见时暂停。 -->
  <t-popup v-model:visible="panelVisible" trigger="click" placement="right-bottom" :overlay-inner-style="{ padding: 0 }">
      <slot :count="badgeCount" :open="panelVisible" />
      <template #content>
        <div class="notification-panel">
          <div class="notification-panel__head">
            <span>{{ $t('notifications.title') }}</span>
            <t-button v-if="unread > 0" size="small" variant="text" theme="primary" @click="markAllRead">
              {{ $t('notifications.markAllRead') }}
            </t-button>
          </div>
          <button v-if="pendingInvitationCount > 0" type="button" class="notification-item" @click="openInvitations">
            <t-icon name="usergroup-add" size="16px" class="notification-item__icon" />
            <span class="notification-item__body">
              <span class="notification-item__title">{{ $t('tenantInvitation.inboxTooltip') }}</span>
              <span class="notification-item__text">
                {{ $t('notifications.pendingInvitations', { count: pendingInvitationCount }) }}
              </span>
            </span>
          </button>
          <button v-for="item in items" :key="item.id" type="button" class="notification-item"
            :class="{ 'is-unread': !item.read_at }" @click="open(item)">
            <span class="notification-item__dot" :class="`is-${item.kind.replace('.', '-')}`" aria-hidden="true" />
            <span class="notification-item__body">
              <span class="notification-item__title">{{ item.title }}</span>
              <span v-if="item.body" class="notification-item__text">{{ item.body }}</span>
              <span class="notification-item__time">
                {{ kindLabel(item.kind) }} · {{ formatTime(item.created_at) }}
              </span>
            </span>
          </button>
          <p v-if="!items.length && pendingInvitationCount === 0" class="notification-panel__empty">
            {{ $t('notifications.empty') }}
          </p>
        </div>
      </template>
  </t-popup>
  <MyInvitationsDialog v-model:visible="dialogVisible" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import MyInvitationsDialog from '@/components/MyInvitationsDialog.vue'
import { listNotifications, markNotificationsRead, type AppNotification } from '@/api/automation'

const POLL_MS = 60_000

const authStore = useAuthStore()
const router = useRouter()
const { t, te, locale } = useI18n()

const pendingInvitationCount = computed(() => authStore.pendingInvitationCount)
const items = ref<AppNotification[]>([])
const unread = ref(0)
const badgeCount = computed(() => pendingInvitationCount.value + unread.value)

const dialogVisible = ref(false)
const panelVisible = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

async function refresh() {
  if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return
  try {
    const res = await listNotifications(20)
    items.value = res.data?.items ?? []
    unread.value = res.data?.unread ?? 0
  } catch {
    // Polling retries on the next tick.
  }
}

const onVisible = () => {
  if (document.visibilityState === 'visible') void refresh()
}

onMounted(() => {
  void refresh()
  timer = setInterval(refresh, POLL_MS)
  document.addEventListener('visibilitychange', onVisible)
})

onBeforeUnmount(() => {
  clearInterval(timer)
  document.removeEventListener('visibilitychange', onVisible)
})

const kindLabel = (kind: string) => {
  const key = `notifications.kinds.${kind.replace('.', '_')}`
  return te(key) ? t(key) : t('notifications.title')
}
const formatTime = (iso: string) => {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'short' }).format(date)
}

const openInvitations = () => {
  panelVisible.value = false
  dialogVisible.value = true
}

async function open(item: AppNotification) {
  panelVisible.value = false
  if (!item.read_at) {
    item.read_at = new Date().toISOString()
    unread.value = Math.max(0, unread.value - 1)
    markNotificationsRead([item.id]).catch(() => {})
  }
  if (item.link.startsWith('/')) void router.push(item.link)
}

async function markAllRead() {
  try {
    await markNotificationsRead()
    const now = new Date().toISOString()
    items.value = items.value.map((item) => ({ ...item, read_at: item.read_at ?? now }))
    unread.value = 0
  } catch {
    // Left unread; the next poll shows the real state.
  }
}
</script>

<style lang="less" scoped>
.notification-panel {
  width: 360px;
  max-height: 460px;
  overflow-y: auto;
  padding: 4px 0;
}

.notification-panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 0 4px 4px;
  padding: 6px 12px 8px;
  border-bottom: 1px solid var(--td-component-stroke);
  font-weight: 600;
}

.notification-panel__empty {
  margin: 0;
  padding: 16px 12px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  text-align: center;
}

.notification-item {
  display: flex;
  gap: 10px;
  width: calc(100% - 8px);
  margin: 2px 4px;
  padding: 8px 12px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: none;
  color: var(--td-text-color-primary);
  font: inherit;
  text-align: left;
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-unread {
    background: var(--td-bg-color-secondarycontainer);

    &:hover {
      background: var(--td-bg-color-secondarycontainer-hover);
    }
  }
}

.notification-item__dot {
  flex-shrink: 0;
  width: 6px;
  height: 6px;
  margin-top: 6px;
  border-radius: 50%;
  background: var(--td-text-color-placeholder);

  &.is-automation-succeeded { background: var(--td-success-color); }
  &.is-automation-failed { background: var(--td-error-color); }
  &.is-automation-awaiting_approval,
  &.is-automation-paused { background: var(--td-warning-color); }
}

.notification-item__body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.notification-item__title {
  overflow: hidden;
  font-size: var(--app-text-md);
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.notification-item__text {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.notification-item__time {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
}
</style>
