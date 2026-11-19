<template>
  <t-popup v-model:visible="panelVisible" trigger="click" placement="right-bottom" :overlay-inner-style="{ padding: 0 }">
    <slot :count="badgeCount" :open="panelVisible" />
    <template #content>
      <div class="notification-panel" :aria-label="t('notifications.title')">
        <div class="notification-panel__head">
          <span>{{ t('notifications.title') }}</span>
          <div class="notification-panel__actions">
            <button v-if="unread > 0" type="button" :disabled="busy" @click="markAllRead">{{ t('notifications.markAllRead') }}</button>
            <t-popconfirm v-if="readCount > 0" :content="t('notifications.clearReadConfirm')" theme="default" placement="bottom-right"
              :confirm-btn="{ content: t('notifications.clearRead'), theme: 'primary' }" :cancel-btn="t('common.cancel')"
              :popup-props="{ disabled: busy }" @confirm="clearRead">
              <button type="button" :disabled="busy">{{ t('notifications.clearRead') }}</button>
            </t-popconfirm>
          </div>
        </div>
        <div class="notification-panel__scroll">
          <section v-if="pendingCount" class="notification-panel__pending">
            <h3>{{ t('notifications.pending') }}<span>{{ pendingCount }}</span></h3>
            <button v-if="pendingInvitationCount > 0" type="button" class="notification-task" @click="openInvitations">
              <span class="notification-item__body">
                <span class="notification-item__heading">
                  <t-icon name="usergroup-add" size="16px" class="notification-task__icon" />
                  <span class="notification-item__title">{{ t('tenantInvitation.inboxTooltip') }}</span>
                </span>
                <span class="notification-item__text">{{ t('notifications.pendingInvitations', { count: pendingInvitationCount }) }}</span>
                <span class="notification-task__action">{{ t('notifications.handle') }}<t-icon name="chevron-right" size="12px" /></span>
              </span>
            </button>
            <button v-for="item in pending" :key="item.id" type="button" class="notification-task" @click="openPending(item)">
              <span class="notification-item__body">
                <span class="notification-item__heading">
                  <t-icon :name="item.kind === 'tool_approval' ? 'secured' : 'chat'" size="16px" class="notification-task__icon" />
                  <span class="notification-item__title">{{ item.title }}</span>
                </span>
                <span class="notification-item__footer">
                  <span>{{ pendingLabel(item.kind) }}</span>
                  <span class="notification-task__action">{{ item.kind === 'tool_approval' ? t('notifications.review') : t('notifications.handle') }}<t-icon name="chevron-right" size="12px" /></span>
                </span>
              </span>
            </button>
          </section>
          <section class="notification-panel__history">
            <h3>{{ t('notifications.history') }}<span v-if="unread">{{ t('notifications.unreadCount', { count: unread }) }}</span></h3>
            <button v-if="hasNew" type="button" class="notification-panel__refresh" :disabled="loading || busy" @click="refresh(true)">
              {{ t('notifications.newItems') }}
            </button>
            <button v-for="item in items" :key="item.id" type="button" class="notification-item"
              :class="{ 'is-unread': !item.read_at }" :disabled="busy" @click="open(item)">
              <span class="notification-item__body">
                <span class="notification-item__heading">
                  <span class="notification-item__title">{{ item.title }}</span>
                  <span v-if="!item.read_at" class="notification-item__unread">{{ t('notifications.unread') }}</span>
                </span>
                <span v-if="item.body" class="notification-item__text">{{ item.body }}</span>
                <span class="notification-item__footer">
                  <span class="notification-item__status">
                    <t-icon :name="historyIcon(item.kind)" size="14px" class="notification-item__icon"
                      :class="{ 'is-success': item.kind === 'automation.succeeded', 'is-error': item.kind === 'automation.failed', 'is-warning': item.kind === 'automation.paused' }" />
                    {{ kindLabel(item.kind) }}
                  </span>
                  <time class="notification-item__time" :datetime="item.created_at">{{ formatTime(item.created_at) }}</time>
                </span>
              </span>
            </button>
            <p v-if="!items.length && !loading && !loadError" class="notification-panel__empty">{{ t('notifications.empty') }}</p>
            <div v-if="loadError" class="notification-panel__error" role="status">
              {{ t('notifications.loadError') }}
              <button type="button" :disabled="loading || busy" @click="retry">{{ t('common.retry') }}</button>
            </div>
            <div v-if="loading" class="notification-panel__loading"><t-loading size="small" /></div>
            <button v-else-if="nextCursor && !loadError" type="button" class="notification-panel__more" :disabled="busy" @click="loadMore">
              {{ t('common.loadMore') }}
            </button>
          </section>
        </div>
      </div>
    </template>
  </t-popup>
  <MyInvitationsDialog v-model:visible="dialogVisible" />
  <Teleport to="body">
    <TransitionGroup name="notification-banner" tag="div" class="notification-banners" aria-live="polite"
      :aria-label="t('notifications.title')" @mouseenter="setBannerHover(true)" @mouseleave="setBannerHover(false)"
      @focusin="setBannerFocus" @focusout="setBannerFocus">
      <article v-for="banner in banners" :key="banner.key" class="notification-banner"
        :class="{ 'is-pending': banner.type === 'pending' }">
        <button type="button" class="notification-banner__content" :disabled="busy" @click="openBanner(banner)">
          <span class="notification-banner__body">
            <span class="notification-banner__meta">
              <t-icon class="notification-banner__icon" :class="{
                'is-success': banner.item.kind === 'automation.succeeded',
                'is-error': banner.item.kind === 'automation.failed',
                'is-warning': banner.type === 'pending' || banner.item.kind === 'automation.paused',
              }" :name="banner.type === 'pending' ? 'secured' : historyIcon(banner.item.kind)" size="13px" />
              <span class="notification-banner__label">
                {{ banner.type === 'pending' ? pendingLabel(banner.item.kind) : kindLabel(banner.item.kind) }}
              </span>
              <time class="notification-banner__time" :datetime="banner.item.created_at">{{ bannerTime(banner.item.created_at) }}</time>
            </span>
            <span class="notification-banner__title">{{ banner.item.title }}</span>
            <span v-if="banner.type === 'result' && banner.item.body" class="notification-banner__summary">{{ notificationPreview(banner.item.body) }}</span>
          </span>
        </button>
        <button type="button" class="notification-banner__close" :aria-label="t('common.close')" @click="dismissBanner(banner.key)">
          <t-icon name="close" size="14px" />
        </button>
      </article>
    </TransitionGroup>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import MyInvitationsDialog from '@/components/MyInvitationsDialog.vue'
import { notificationPreview } from '@/utils/notificationPreview'
import { clearReadNotifications, listNotifications, markNotificationsRead, type AppNotification, type NotificationPage, type PendingNotification } from '@/api/automation'

const POLL_MS = 60_000
const authStore = useAuthStore()
const router = useRouter()
const { t, te, locale } = useI18n()
const pendingInvitationCount = computed(() => authStore.pendingInvitationCount)
const items = ref<AppNotification[]>([])
const pending = ref<PendingNotification[]>([])
const unread = ref(0)
const readCount = ref(0)
const pendingCount = computed(() => pendingInvitationCount.value + pending.value.length)
const badgeCount = computed(() => pendingCount.value + unread.value)
const nextCursor = ref('')
const historyExpanded = ref(false)
const hasNew = ref(false)
const loading = ref(false)
const busy = ref(false)
const loadError = ref<'' | 'refresh' | 'more'>('')
const dialogVisible = ref(false)
const panelVisible = ref(false)
let version = 0
let timer: ReturnType<typeof setInterval> | undefined
type NotificationBanner = { key: string } & (
  { type: 'pending'; item: PendingNotification } | { type: 'result'; item: AppNotification }
)
const banners = ref<NotificationBanner[]>([])
const seenBanners = new Set<string>()
const bannerTimers = new Map<string, ReturnType<typeof setTimeout>>()
let bannersInitialized = false
let newestNotificationTime = 0
let bannersPaused = false
let bannerHovered = false
let bannerFocused = false

function setBannerHover(hovered: boolean) {
  bannerHovered = hovered
  if (bannerHovered || bannerFocused) pauseBanners()
  else resumeBanners()
}
function setBannerFocus(event: FocusEvent) {
  bannerFocused = event.type === 'focusin' || !!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null)
  if (bannerHovered || bannerFocused) pauseBanners()
  else resumeBanners()
}

function dismissBanner(key: string) {
  clearTimeout(bannerTimers.get(key))
  bannerTimers.delete(key)
  banners.value = banners.value.filter(banner => banner.key !== key)
}
function pauseBanners() {
  bannersPaused = true
  for (const timeout of bannerTimers.values()) clearTimeout(timeout)
  bannerTimers.clear()
}
function resumeBanners() {
  pauseBanners()
  bannersPaused = false
  for (const banner of banners.value) {
    bannerTimers.set(banner.key, setTimeout(() => dismissBanner(banner.key), 10_000))
  }
}
function clearBanners() {
  pauseBanners()
  banners.value = []
  bannersPaused = false
  bannerHovered = bannerFocused = false
}
function updateBanners(page: NotificationPage) {
  const livePending = new Set((page.pending ?? []).map(item => item.id))
  const read = new Set((page.items ?? []).filter(item => item.read_at).map(item => item.id))
  for (const banner of banners.value) {
    if (banner.type === 'pending' ? !livePending.has(banner.item.id) : read.has(banner.item.id)) dismissBanner(banner.key)
  }
  const candidates: NotificationBanner[] = [
    ...(page.pending ?? []).map(item => ({ key: `pending:${item.id}`, type: 'pending' as const, item })),
    ...(page.items ?? []).map(item => ({ key: `result:${item.id}`, type: 'result' as const, item })),
  ]
  const fresh = candidates.filter(banner => !seenBanners.has(banner.key) &&
    (banner.type === 'pending' || (!banner.item.read_at && Date.parse(banner.item.created_at) >= newestNotificationTime)))
  for (const banner of candidates) seenBanners.add(banner.key)
  for (const item of page.items ?? []) newestNotificationTime = Math.max(newestNotificationTime, Date.parse(item.created_at) || 0)
  // Seed the first successful response without replaying historical notifications.
  if (bannersInitialized && !panelVisible.value && fresh.length) {
    const next = [...fresh, ...banners.value]
      .sort((a, b) => Number(b.type === 'pending') - Number(a.type === 'pending')).slice(0, 3)
    const retained = new Set(next.map(banner => banner.key))
    for (const banner of banners.value) if (!retained.has(banner.key)) dismissBanner(banner.key)
    banners.value = next
    if (!bannersPaused) resumeBanners()
  }
  bannersInitialized = true
}
function openBanner(banner: NotificationBanner) {
  if (busy.value) return
  dismissBanner(banner.key)
  if (banner.type === 'pending') openPending(banner.item)
  else void open(banner.item)
}

function applyCounts(page: NotificationPage) {
  pending.value = page.pending ?? []
  unread.value = page.unread ?? 0
  readCount.value = page.read_count ?? 0
}

async function refresh(reset = false) {
  if (busy.value || (loading.value && !reset) || document.visibilityState === 'hidden') return
  const request = ++version
  loading.value = true
  try {
    const { data } = await listNotifications(20)
    if (request !== version) return
    updateBanners(data)
    applyCounts(data)
    if (reset || !historyExpanded.value || !panelVisible.value) {
      items.value = data.items ?? []
      nextCursor.value = data.next_cursor ?? ''
      historyExpanded.value = false
      hasNew.value = false
    } else {
      // Keep the cursor and reading position while browsing older pages.
      // New arrivals are revealed explicitly, so a busy feed cannot create gaps.
      const known = new Set(items.value.map(item => item.id))
      hasNew.value = (data.items ?? []).some(item => !known.has(item.id))
      const fresh = new Map((data.items ?? []).map(item => [item.id, item]))
      items.value = items.value.map(item => fresh.get(item.id) ?? item)
    }
    loadError.value = ''
  } catch {
    if (request === version) loadError.value = 'refresh'
  } finally {
    if (request === version) loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value || loading.value || busy.value) return
  const request = ++version
  loading.value = true
  try {
    const { data } = await listNotifications(20, nextCursor.value)
    if (request !== version) return
    const known = new Set(items.value.map(item => item.id))
    items.value.push(...(data.items ?? []).filter(item => !known.has(item.id)))
    nextCursor.value = data.next_cursor ?? ''
    historyExpanded.value = true
    applyCounts(data)
    loadError.value = ''
  } catch {
    if (request === version) loadError.value = 'more'
  } finally {
    if (request === version) loading.value = false
  }
}

function retry() {
  return loadError.value === 'more' ? loadMore() : refresh(true)
}

const onVisible = () => { if (document.visibilityState === 'visible') void refresh() }
watch(panelVisible, open => {
  if (open) {
    clearBanners()
    void refresh(true)
  }
})
watch(() => [authStore.effectiveTenantId, authStore.user?.id], () => {
  ++version
  items.value = []
  pending.value = []
  unread.value = readCount.value = 0
  nextCursor.value = ''
  historyExpanded.value = hasNew.value = loading.value = busy.value = false
  loadError.value = ''
  panelVisible.value = false
  clearBanners()
  seenBanners.clear()
  bannersInitialized = false
  newestNotificationTime = 0
  if (authStore.user) void refresh(true)
})
onMounted(() => {
  void refresh()
  timer = setInterval(() => { void refresh() }, POLL_MS)
  document.addEventListener('visibilitychange', onVisible)
})
onBeforeUnmount(() => {
  ++version
  clearInterval(timer)
  clearBanners()
  document.removeEventListener('visibilitychange', onVisible)
})

const kindLabel = (kind: string) => {
  const key = `notifications.kinds.${kind.replace('.', '_')}`
  return te(key) ? t(key) : t('notifications.title')
}
const pendingLabel = (kind: string) => t(kind === 'tool_approval' ? 'notifications.awaitingApproval'
  : kind === 'mcp_oauth' ? 'notifications.awaitingAuthorization' : 'notifications.awaitingReply')
const historyIcon = (kind: string) => kind === 'automation.succeeded' ? 'check-circle'
  : kind === 'automation.failed' ? 'error-circle' : kind === 'automation.paused' ? 'pause-circle' : 'notification'
const formatTime = (iso: string) => {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'short' }).format(date)
}
const bannerTime = (iso: string) => {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? '' : new Intl.DateTimeFormat(locale.value, {
    hour: '2-digit', minute: '2-digit',
  }).format(date)
}
const openInvitations = () => { panelVisible.value = false; dialogVisible.value = true }
function navigate(link: string) {
  panelVisible.value = false
  if (link.startsWith('/') && !link.startsWith('//')) void router.push(link)
}
function openPending(item: PendingNotification) { navigate(item.link) }

async function mutate(action: () => Promise<unknown>, onSuccess: () => void, reset = false) {
  if (busy.value) return
  const request = ++version
  busy.value = true
  loading.value = false
  try {
    await action()
    if (request !== version) return
    onSuccess()
  } catch {
    if (request === version) MessagePlugin.error(t('common.operationFailed'))
  } finally {
    if (request === version) {
      busy.value = false
      await refresh(reset)
    }
  }
}
async function open(item: AppNotification) {
  if (busy.value) return
  navigate(item.link)
  if (!item.read_at) await mutate(() => markNotificationsRead([item.id]), () => {
    item.read_at = new Date().toISOString()
  })
}
async function markAllRead() {
  await mutate(() => markNotificationsRead(), () => {
    const now = new Date().toISOString()
    items.value = items.value.map(item => ({ ...item, read_at: item.read_at ?? now }))
    for (const banner of banners.value) if (banner.type === 'result') dismissBanner(banner.key)
  })
}
async function clearRead() {
  await mutate(clearReadNotifications, () => {
    items.value = items.value.filter(item => !item.read_at)
  }, true)
}
</script>

<style lang="less" scoped>
.notification-banners {
  position: fixed;
  top: max(20px, env(safe-area-inset-top));
  right: max(24px, env(safe-area-inset-right));
  z-index: 5000;
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: min(352px, calc(100vw - 32px));
  max-height: calc(100dvh - 40px);
  overflow-y: auto;
  padding: 12px;
  margin: -12px;
  pointer-events: none;
  font-family: var(--app-font-family);
}
.notification-banner {
  position: relative;
  flex-shrink: 0;
  border-radius: 14px;
  background: color-mix(in srgb, var(--td-bg-color-container) 94%, transparent);
  backdrop-filter: blur(20px) saturate(140%);
  -webkit-backdrop-filter: blur(20px) saturate(140%);
  color: var(--td-text-color-primary);
  box-shadow: 0 6px 24px rgb(0 0 0 / 9%), 0 1px 3px rgb(0 0 0 / 4%);
  pointer-events: auto;
  button:focus-visible { outline: 2px solid var(--td-brand-color-focus); outline-offset: 2px; }
}
.notification-banner__content {
  display: flex;
  width: 100%;
  padding: 12px 16px 14px;
  border: 0;
  border-radius: inherit;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
  &:disabled { cursor: default; }
}
.notification-banner__icon {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
  &.is-success { color: var(--td-success-color); }
  &.is-error { color: var(--td-error-color); }
  &.is-warning { color: var(--td-warning-color); }
}
.notification-banner__body { display: flex; flex: 1; flex-direction: column; gap: 3px; min-width: 0; }
.notification-banner__meta { display: flex; align-items: center; gap: 5px; padding-right: 20px; margin-bottom: 4px; }
.notification-banner__label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--td-text-color-secondary); font-size: var(--app-text-xs); line-height: 1.5; }
.notification-banner__time { margin-left: auto; flex-shrink: 0; color: var(--td-text-color-placeholder); font-size: var(--app-text-xs); font-variant-numeric: tabular-nums; }
.is-pending .notification-banner__label { color: var(--td-warning-color); }
.notification-banner__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-md);
  font-weight: 600;
  line-height: 1.5;
}
.notification-banner__summary {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  overflow-wrap: anywhere;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 1.55;
}
.notification-banner__close {
  position: absolute;
  top: 8px;
  right: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  &:hover { color: var(--td-text-color-primary); background: var(--td-bg-color-container-hover); }
}
.notification-banner-enter-active,
.notification-banner-leave-active,
.notification-banner-move { transition: opacity var(--app-motion-base) ease, transform var(--app-motion-base) ease; }
.notification-banner-enter-from,
.notification-banner-leave-to { opacity: 0; transform: translateX(16px); }
@media (max-width: 480px) { .notification-banners { top: max(12px, env(safe-area-inset-top)); right: 16px; } }
@media (prefers-reduced-motion: reduce) {
  .notification-banner-enter-active,
  .notification-banner-leave-active,
  .notification-banner-move { transition: none; }
}
.notification-panel {
  width: min(380px, calc(100vw - 32px));
  display: flex;
  flex-direction: column;
  max-height: min(560px, calc(100vh - 48px));
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: var(--app-text-md);
  font-weight: 400;
  line-height: 1.5;
}
.notification-panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  font-size: var(--app-text-base);
  font-weight: 600;
  flex-shrink: 0;
}
.notification-panel__actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
.notification-panel__actions button,
.notification-panel__error button,
.notification-panel__more,
.notification-panel__refresh {
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  font-weight: 400;
  cursor: pointer;
  padding: 4px;
  &:hover:not(:disabled) { color: var(--td-text-color-primary); background: var(--td-bg-color-container-hover); }
  &:disabled { cursor: default; opacity: 0.5; }
}
.notification-panel__scroll { min-height: 0; overflow-y: auto; padding: 12px 8px; overscroll-behavior: contain; }
.notification-panel h3 {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0 12px 8px;
  font-size: var(--app-text-xs);
  font-weight: 500;
  color: var(--td-text-color-secondary);
  > span { font-weight: 400; }
}
.notification-panel__pending { margin-bottom: 20px; }
.notification-task,
.notification-item {
  display: flex;
  align-items: flex-start;
  width: 100%;
  box-sizing: border-box;
  padding: 12px;
  border: 0;
  border-radius: var(--app-radius-md);
  color: var(--td-text-color-primary);
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: background-color 0.15s ease;
}
.notification-task {
  margin-bottom: 6px;
  background: color-mix(in srgb, var(--td-warning-color) 4%, var(--td-bg-color-container));
  &:hover { background: color-mix(in srgb, var(--td-warning-color) 7%, var(--td-bg-color-container)); }
}
.notification-task__icon { flex-shrink: 0; color: var(--td-warning-color); }
.notification-task__action {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
  font-size: var(--app-text-sm);
  font-weight: 500;
  color: var(--td-text-color-primary);
}
.notification-item {
  background: transparent;
  &:hover:not(:disabled) { background: color-mix(in srgb, var(--td-bg-color-container-hover) 55%, var(--td-bg-color-container)); }
  .notification-item__title { font-weight: 400; }
  &.is-unread {
    background: color-mix(in srgb, var(--td-bg-color-container-hover) 65%, var(--td-bg-color-container));
    .notification-item__title { font-weight: 600; }
    &:hover:not(:disabled) { background: var(--td-bg-color-container-hover); }
  }
  &:disabled { cursor: default; }
  & + & { margin-top: 4px; }
}
.notification-item__icon {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
  &.is-success { color: var(--td-success-color); }
  &.is-error { color: var(--td-error-color); }
  &.is-warning { color: var(--td-warning-color); }
}
.notification-item__body { display: flex; flex: 1; flex-direction: column; gap: 6px; min-width: 0; }
.notification-item__heading { display: flex; align-items: center; gap: 6px; min-width: 0; }
.notification-item__title {
  min-width: 0;
  overflow: hidden;
  font-size: var(--app-text-md);
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.notification-item__unread {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex-shrink: 0;
  margin-left: auto;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  font-weight: 400;
  &::before {
    content: '';
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--td-brand-color);
  }
}
.notification-item__text {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
  font-weight: 400;
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.notification-item__footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 4px 12px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
}
.notification-item__status { display: inline-flex; align-items: center; gap: 4px; }
.notification-item__time { font-variant-numeric: tabular-nums; }
.notification-panel__empty,
.notification-panel__error { margin: 0; padding: 16px 8px; color: var(--td-text-color-secondary); font-size: var(--app-text-xs); text-align: center; }
.notification-panel__loading { display: flex; justify-content: center; padding: 12px; }
.notification-panel__more,
.notification-panel__refresh { width: 100%; padding: 8px; margin-top: 6px; }
.notification-panel button:focus-visible { outline: 2px solid var(--td-brand-color-focus); outline-offset: 1px; }
</style>
