<template>
  <!-- 全局右上角铃铛：合并展示"待处理邀请"与站内通知（自动化运行结果、等待审批等）。
       - 只在有待处理邀请或未读通知时渲染，空收件箱场景不占用角落像素。
       - 固定定位、z-index 远低于 t-drawer 默认 2500，业务页面右侧抽屉弹出时会自然覆盖铃铛。
       - 通知靠轮询刷新，页面不可见时暂停。 -->
  <template v-if="badgeCount > 0">
    <t-popup v-model:visible="panelVisible" trigger="click" placement="bottom-right" :overlay-inner-style="{ padding: 0 }">
      <t-badge :count="badgeCount" :max-count="99" :offset="[6, 4]" class="global-invitation-bell">
        <button type="button" class="global-invitation-bell__btn" :title="$t('notifications.title')"
          :aria-label="$t('notifications.title')">
          <t-icon name="notification" size="18px" />
        </button>
      </t-badge>
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
            <t-icon :name="iconFor(item.kind)" size="16px" class="notification-item__icon"
              :class="`is-${item.kind.replace('.', '-')}`" />
            <span class="notification-item__body">
              <span class="notification-item__title">
                {{ kindLabel(item.kind) }} · {{ item.title }}
              </span>
              <span v-if="item.body" class="notification-item__text">{{ item.body }}</span>
              <span class="notification-item__time">{{ formatTime(item.created_at) }}</span>
            </span>
          </button>
          <p v-if="!items.length && pendingInvitationCount === 0" class="notification-panel__empty">
            {{ $t('notifications.empty') }}
          </p>
        </div>
      </template>
    </t-popup>
  </template>
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

const ICONS: Record<string, string> = {
  'automation.succeeded': 'check-circle',
  'automation.failed': 'close-circle',
  'automation.awaiting_approval': 'help-circle',
  'automation.paused': 'pause-circle',
}

const iconFor = (kind: string) => ICONS[kind] ?? 'notification'
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
.global-invitation-bell {
  position: fixed;
  top: 12px;
  right: calc(16px + var(--assistant-dock-width, 0px));
  /* 远低于 TDesign 抽屉的默认 z-index (2500)，确保业务页右侧抽屉弹出时能正常盖住铃铛。
     高于普通页面内容（一般 0~10），避免被列表卡片覆盖。 */
  z-index: 100;
}

.global-invitation-bell__btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  /* 用 container 背景而非透明，铃铛悬在内容区上方时和不同颜色的页面背景都能看清。 */
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.04);
  transition: background-color 0.18s ease, color 0.18s ease, box-shadow 0.18s ease;

  &:hover {
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 1px;
  }
}

.notification-panel {
  width: 340px;
  max-height: 440px;
  overflow-y: auto;
  padding: 4px 0;
}

.notification-panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 12px;
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
  width: 100%;
  padding: 8px 12px;
  border: 0;
  background: none;
  color: var(--td-text-color-primary);
  font: inherit;
  text-align: left;
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-unread .notification-item__title::before {
    content: '';
    display: inline-block;
    width: 6px;
    height: 6px;
    margin-right: 6px;
    border-radius: 50%;
    background: var(--td-brand-color);
    vertical-align: middle;
  }
}

.notification-item__icon {
  flex-shrink: 0;
  margin-top: 2px;
  color: var(--td-text-color-secondary);

  &.is-automation-succeeded { color: var(--td-success-color); }
  &.is-automation-failed { color: var(--td-error-color); }
  &.is-automation-awaiting_approval,
  &.is-automation-paused { color: var(--td-warning-color); }
}

.notification-item__body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.notification-item__title {
  overflow: hidden;
  font-size: var(--app-text-sm);
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
