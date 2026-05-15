<template>
  <div class="user-menu" :class="{ 'user-menu--collapsed': uiStore.sidebarCollapsed }" ref="menuRef">
    <!-- 用户按钮 -->
    <div class="user-button" @click="toggleMenu">
      <div class="user-avatar">
        <img v-if="userAvatar" :src="userAvatar" :alt="$t('common.avatar')" />
        <span v-else class="avatar-placeholder">{{ userInitial }}</span>
      </div>
      <template v-if="!uiStore.sidebarCollapsed">
        <div class="user-info">
          <div class="user-name">{{ userName }}</div>
          <div class="user-email">{{ userEmail }}</div>
        </div>
        <t-icon :name="menuVisible ? 'chevron-up' : 'chevron-down'" class="dropdown-icon" />
      </template>
    </div>

    <!-- 下拉菜单 -->
    <Transition name="dropdown">
      <div v-if="menuVisible" class="user-dropdown" @click.stop>
        <div class="menu-item" @click="handleQuickNav('models')">
          <t-icon name="control-platform" class="menu-icon" />
          <span>{{ $t('settings.modelManagement') }}</span>
        </div>
        <div class="menu-item" @click="handleQuickNav('websearch')">
          <svg 
            width="16" 
            height="16" 
            viewBox="0 0 18 18" 
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
            class="menu-icon svg-icon"
          >
            <circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.2" fill="none"/>
            <path d="M 9 2 A 3.5 7 0 0 0 9 16" stroke="currentColor" stroke-width="1.2" fill="none"/>
            <path d="M 9 2 A 3.5 7 0 0 1 9 16" stroke="currentColor" stroke-width="1.2" fill="none"/>
            <line x1="2.94" y1="5.5" x2="15.06" y2="5.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
            <line x1="2.94" y1="12.5" x2="15.06" y2="12.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
          </svg>
          <span>{{ $t('settings.webSearchConfig') }}</span>
        </div>
        <div class="menu-item" @click="handleQuickNav('mcp')">
          <t-icon name="tools" class="menu-icon" />
          <span>{{ $t('settings.mcpService') }}</span>
        </div>
        <div class="menu-item" @click="handleQuickNav('api')">
          <t-icon name="secured" class="menu-icon" />
          <span>{{ $t('settings.apiInfo') }}</span>
        </div>
        <div
          ref="imMenuItemRef"
          class="menu-item menu-item--submenu"
          :class="{ 'is-open': imSubmenuOpen }"
          @mouseenter="showIMSubmenu"
          @mouseleave="scheduleHideIMSubmenu"
        >
          <t-icon name="link" class="menu-icon" />
          <span class="menu-item-label">{{ $t('imOverview.menuTitle') }}</span>
          <span
            v-if="hasActiveIMChannels"
            class="live-indicator"
            :title="$t('imOverview.liveIndicator')"
            aria-hidden="true"
          >
            <span class="live-indicator-dot"></span>
          </span>
          <t-icon name="chevron-right" class="menu-chevron" />
        </div>
        <div class="menu-divider"></div>
        <div class="menu-item" @click="handleSettings">
          <t-icon name="setting" class="menu-icon" />
          <span>{{ $t('general.allSettings') }}</span>
        </div>
        <!-- Tenant switcher submenu — only meaningful when the user belongs
             to more than one tenant. Superusers (canAccessAllTenants) keep
             using the sidebar TenantSelector for "any tenant in the system";
             the entry here is the curated "tenants I'm a member of" list,
             matching what the backend memberships claim covers. -->
        <div
          v-if="showTenantSwitcher"
          ref="tenantMenuItemRef"
          class="menu-item menu-item--submenu"
          :class="{ 'is-open': tenantSubmenuOpen }"
          @mouseenter="showTenantSubmenu"
          @mouseleave="scheduleHideTenantSubmenu"
        >
          <t-icon name="swap" class="menu-icon" />
          <span class="menu-item-label">{{ $t('tenant.switcher.menuLabel') }}</span>
          <t-icon name="chevron-right" class="menu-chevron" />
        </div>
        <div class="menu-divider"></div>
        <div class="menu-item" @click="openClawhubSkill">
          <span class="menu-icon menu-icon--emoji" role="img" :aria-label="$t('common.clawhubSkill')">🦞</span>
          <span class="menu-text-with-icon">
            <span>{{ $t('common.clawhubSkill') }}</span>
            <span class="menu-new-badge">{{ $t('common.newBadge') }}</span>
            <svg class="menu-external-icon" viewBox="0 0 16 16" aria-hidden="true">
              <path
                fill="currentColor"
                d="M12.667 8a.667.667 0 0 1 .666.667v4a2.667 2.667 0 0 1-2.666 2.666H4.667a2.667 2.667 0 0 1-2.667-2.666V5.333a2.667 2.667 0 0 1 2.667-2.666h4a.667.667 0 1 1 0 1.333h-4a1.333 1.333 0 0 0-1.333 1.333v7.334A1.333 1.333 0 0 0 4.667 13.333h6a1.333 1.333 0 0 0 1.333-1.333v-4A.667.667 0 0 1 12.667 8Zm2.666-6.667v4a.667.667 0 0 1-1.333 0V3.276l-5.195 5.195a.667.667 0 0 1-.943-.943l5.195-5.195h-2.057a.667.667 0 0 1 0-1.333h4a.667.667 0 0 1 .666.666Z"
              />
            </svg>
          </span>
        </div>
        <div class="menu-item" @click="openChromeExtension">
          <t-icon name="extension" class="menu-icon" />
          <span class="menu-text-with-icon">
            <span>{{ $t('common.chromeExtension') }}</span>
            <span class="menu-new-badge">{{ $t('common.newBadge') }}</span>
            <svg class="menu-external-icon" viewBox="0 0 16 16" aria-hidden="true">
              <path
                fill="currentColor"
                d="M12.667 8a.667.667 0 0 1 .666.667v4a2.667 2.667 0 0 1-2.666 2.666H4.667a2.667 2.667 0 0 1-2.667-2.666V5.333a2.667 2.667 0 0 1 2.667-2.666h4a.667.667 0 1 1 0 1.333h-4a1.333 1.333 0 0 0-1.333 1.333v7.334A1.333 1.333 0 0 0 4.667 13.333h6a1.333 1.333 0 0 0 1.333-1.333v-4A.667.667 0 0 1 12.667 8Zm2.666-6.667v4a.667.667 0 0 1-1.333 0V3.276l-5.195 5.195a.667.667 0 0 1-.943-.943l5.195-5.195h-2.057a.667.667 0 0 1 0-1.333h4a.667.667 0 0 1 .666.666Z"
              />
            </svg>
          </span>
        </div>
        <div
          class="menu-item"
          :title="$t('common.githubStarTip')"
          @click="openGithub"
        >
          <t-icon name="logo-github" class="menu-icon" />
          <span class="menu-text-with-icon">
            <span>{{ $t('common.github') }}</span>
            <t-icon name="star-filled" class="menu-github-star-icon" size="14px" aria-hidden="true" />
            <svg class="menu-external-icon" viewBox="0 0 16 16" aria-hidden="true">
              <path
                fill="currentColor"
                d="M12.667 8a.667.667 0 0 1 .666.667v4a2.667 2.667 0 0 1-2.666 2.666H4.667a2.667 2.667 0 0 1-2.667-2.666V5.333a2.667 2.667 0 0 1 2.667-2.666h4a.667.667 0 1 1 0 1.333h-4a1.333 1.333 0 0 0-1.333 1.333v7.334A1.333 1.333 0 0 0 4.667 13.333h6a1.333 1.333 0 0 0 1.333-1.333v-4A.667.667 0 0 1 12.667 8Zm2.666-6.667v4a.667.667 0 0 1-1.333 0V3.276l-5.195 5.195a.667.667 0 0 1-.943-.943l5.195-5.195h-2.057a.667.667 0 0 1 0-1.333h4a.667.667 0 0 1 .666.666Z"
              />
            </svg>
          </span>
        </div>
        <template v-if="!authStore.isLiteMode">
          <div class="menu-divider"></div>
          <div class="menu-item danger" @click="handleLogout">
            <t-icon name="logout" class="menu-icon" />
            <span>{{ $t('auth.logout') }}</span>
          </div>
        </template>
      </div>
    </Transition>

    <!-- IM submenu is teleported to body because the sidebar (.aside_box) has
         overflow:hidden, which would otherwise clip any absolutely-positioned
         child that reaches past its bounds. -->
    <Teleport to="body">
      <div
        v-if="imSubmenuOpen"
        class="im-submenu-floating"
        :style="imSubmenuStyle"
        @mouseenter="showIMSubmenu"
        @mouseleave="scheduleHideIMSubmenu"
      >
        <IMChannelsOverviewPanel
          :active="imSubmenuOpen"
          @close="closeAll"
          @channels-changed="onChannelsChanged"
        />
      </div>
    </Teleport>

    <!-- Tenant switcher floating panel — shares the same teleport rationale
         as the IM submenu. Lists every tenant the user is an active member
         of (from the JWT-issued memberships claim cached in authStore). -->
    <Teleport to="body">
      <div
        v-if="tenantSubmenuOpen"
        class="tenant-submenu-floating"
        :style="tenantSubmenuStyle"
        @mouseenter="showTenantSubmenu"
        @mouseleave="scheduleHideTenantSubmenu"
      >
        <div class="tenant-submenu-header">
          {{ $t('tenant.switcher.menuLabel') }}
        </div>
        <div class="tenant-submenu-list">
          <div
            v-for="m in switchableMemberships"
            :key="m.tenant_id"
            class="tenant-submenu-item"
            :class="{ 'is-current': isCurrentTenant(m.tenant_id) }"
            @click="switchToTenant(m)"
          >
            <div class="tenant-submenu-item-avatar" :class="{ 'is-current': isCurrentTenant(m.tenant_id) }">
              {{ tenantInitial(m) }}
            </div>
            <div class="tenant-submenu-item-info">
              <span class="tenant-submenu-item-name">{{ tenantDisplayName(m) }}</span>
              <span class="tenant-submenu-item-role">{{ formatRole(m.role) }}</span>
            </div>
            <span
              v-if="isCurrentTenant(m.tenant_id)"
              class="tenant-submenu-item-badge"
            >{{ $t('tenant.switcher.currentBadge') }}</span>
          </div>
          <div v-if="switchableMemberships.length === 0" class="tenant-submenu-empty">
            {{ $t('tenant.switcher.empty') }}
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useUIStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { MessagePlugin } from 'tdesign-vue-next'
import { getCurrentUser, logout as logoutApi } from '@/api/auth'
import { useI18n } from 'vue-i18n'
import IMChannelsOverviewPanel from '@/components/IMChannelsOverviewPanel.vue'
import { listAllIMChannels, type IMChannelOverview } from '@/api/agent'
import { navigateAfterTenantSwitch } from '@/utils/tenantSwitch'

const { t } = useI18n()

const router = useRouter()
const uiStore = useUIStore()
const authStore = useAuthStore()

const menuRef = ref<HTMLElement>()
const imMenuItemRef = ref<HTMLElement>()
const tenantMenuItemRef = ref<HTMLElement>()
const menuVisible = ref(false)
const imSubmenuOpen = ref(false)
const imSubmenuStyle = ref<Record<string, string>>({})
const tenantSubmenuOpen = ref(false)
const tenantSubmenuStyle = ref<Record<string, string>>({})
const hasActiveIMChannels = ref(false)
let imSubmenuHideTimer: ReturnType<typeof setTimeout> | null = null
let tenantSubmenuHideTimer: ReturnType<typeof setTimeout> | null = null

// 用户信息
const userInfo = ref({
  username: t('common.defaultUser'),
  email: 'user@example.com',
  avatar: ''
})

const userName = computed(() => userInfo.value.username)
const userEmail = computed(() => userInfo.value.email)
const userAvatar = computed(() => userInfo.value.avatar)

// 用户名首字母（用于无头像时显示）
const userInitial = computed(() => {
  return userName.value.charAt(0).toUpperCase()
})

// 切换菜单显示
const toggleMenu = () => {
  menuVisible.value = !menuVisible.value
}

// 快捷导航到设置的特定部分
const handleQuickNav = (section: string) => {
  menuVisible.value = false
  uiStore.openSettings()
  router.push('/platform/settings')
  
  // 延迟一下，确保设置页面已经渲染
  setTimeout(() => {
    // 触发设置页面切换到对应section
    const event = new CustomEvent('settings-nav', { detail: { section } })
    window.dispatchEvent(event)
  }, 100)
}

// 打开设置
const handleSettings = () => {
  menuVisible.value = false
  uiStore.openSettings()
  router.push('/platform/settings')
}

// Hover-driven submenu controls. A small hide delay tolerates the pointer
// slipping off briefly onto the gap between menu item and submenu pane.
const showIMSubmenu = () => {
  if (imSubmenuHideTimer) {
    clearTimeout(imSubmenuHideTimer)
    imSubmenuHideTimer = null
  }
  // Compute panel position based on the menu item's rect — the panel is
  // teleported to body so we can't rely on CSS `left: 100%`.
  positionIMSubmenu()
  imSubmenuOpen.value = true
}

const scheduleHideIMSubmenu = () => {
  if (imSubmenuHideTimer) clearTimeout(imSubmenuHideTimer)
  imSubmenuHideTimer = setTimeout(() => {
    imSubmenuOpen.value = false
    imSubmenuHideTimer = null
  }, 180)
}

const closeAll = () => {
  imSubmenuOpen.value = false
  tenantSubmenuOpen.value = false
  menuVisible.value = false
}

// ---------- Tenant switcher submenu ----------
//
// Same hover-driven submenu pattern as the IM panel above; data comes from
// authStore.memberships (populated by /auth/login). PR 4 of #1303 relaxed
// the X-Tenant-ID gate in middleware/auth.go to accept active membership
// rows, so flipping authStore.selectedTenantId here is enough — the next
// page reload re-issues every request with the new header and the server
// resolves the role server-side.
type Membership = {
  tenant_id: number
  tenant_name?: string
  role: string
}

// switchableMemberships is the curated list shown in the dropdown. We keep
// the active tenant in there (with a "Current" badge) so the user has a
// single place to glance at "where am I right now"; clicking the current
// row is a no-op (handled in switchToTenant).
const switchableMemberships = computed<Membership[]>(() => {
  return authStore.memberships ?? []
})

// Rendered only when there's something to switch *to*. Multi-tenant members
// (memberships.length > 1) need it for tenant switching; superusers keep
// using the sidebar TenantSelector for the "any tenant in the system" case,
// so we don't double-show the entry there.
const showTenantSwitcher = computed(() => {
  return switchableMemberships.value.length > 1
})

const isCurrentTenant = (id: number) => {
  const active = authStore.effectiveTenantId
  return active != null && Number(active) === Number(id)
}

const tenantDisplayName = (m: Membership) =>
  m.tenant_name && m.tenant_name.trim() !== '' ? m.tenant_name : `#${m.tenant_id}`

const tenantInitial = (m: Membership) => {
  const name = tenantDisplayName(m).trim()
  return (name.charAt(0) || '?').toUpperCase()
}

const formatRole = (role: string) => {
  // Roles match the backend enum: viewer/contributor/admin/owner. The
  // i18n bundle already carries human labels under tenantMember.role.*
  // (see PR 3); reuse those rather than inventing a new key namespace.
  const key = `tenantMember.role.${role}`
  const label = t(key)
  // Fallback when the locale doesn't have the key: show the raw role.
  return label === key ? role : label
}

const switchToTenant = (m: Membership) => {
  if (isCurrentTenant(m.tenant_id)) {
    closeAll()
    return
  }
  // Treat switching back to the user's home tenant as "clear the
  // override" so request.ts stops attaching X-Tenant-ID. This mirrors
  // what TenantSelector.vue does in selectTenant().
  const homeTenantId = authStore.tenant?.id ? Number(authStore.tenant.id) : null
  if (homeTenantId !== null && homeTenantId === m.tenant_id) {
    authStore.setSelectedTenant(null, null)
  } else {
    authStore.setSelectedTenant(m.tenant_id, tenantDisplayName(m))
  }
  closeAll()
  MessagePlugin.success(t('tenant.switchSuccess'))
  // Hard reload so every cached store / open SSE stream / in-flight
  // request gets re-keyed under the new tenant. If the current path
  // embeds a tenant-scoped resource id, reload would white-screen the
  // user; navigateAfterTenantSwitch redirects to the platform home in
  // that case. Same helper as TenantSelector.vue.
  setTimeout(() => {
    navigateAfterTenantSwitch()
  }, 400)
}

const showTenantSubmenu = () => {
  if (tenantSubmenuHideTimer) {
    clearTimeout(tenantSubmenuHideTimer)
    tenantSubmenuHideTimer = null
  }
  positionTenantSubmenu()
  tenantSubmenuOpen.value = true
}

const scheduleHideTenantSubmenu = () => {
  if (tenantSubmenuHideTimer) clearTimeout(tenantSubmenuHideTimer)
  tenantSubmenuHideTimer = setTimeout(() => {
    tenantSubmenuOpen.value = false
    tenantSubmenuHideTimer = null
  }, 180)
}

const positionTenantSubmenu = () => {
  const el = tenantMenuItemRef.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const PANEL_WIDTH = 280
  const PANEL_MAX_HEIGHT = 360
  const GAP = 8
  const MARGIN = 8

  let left = rect.right + GAP
  if (left + PANEL_WIDTH + MARGIN > window.innerWidth) {
    left = Math.max(MARGIN, rect.left - PANEL_WIDTH - GAP)
  }

  let top = rect.top - 4
  const maxTop = window.innerHeight - Math.min(PANEL_MAX_HEIGHT, window.innerHeight - MARGIN * 2) - MARGIN
  if (top > maxTop) top = maxTop
  if (top < MARGIN) top = MARGIN

  tenantSubmenuStyle.value = {
    left: `${left}px`,
    top: `${top}px`,
  }
}

// Silent prefetch so the "live" indicator on the IM menu item reflects reality
// as soon as the user sees the avatar area. Errors are swallowed — the
// indicator just stays off if the request fails, which is the conservative
// default. The panel component emits channels-changed after toggle/refresh so
// we stay in sync without re-polling.
const refreshIMStatus = async () => {
  try {
    const resp = await listAllIMChannels()
    const data: IMChannelOverview[] = resp?.data || []
    hasActiveIMChannels.value = data.some((c) => c.enabled)
  } catch {
    // Intentionally ignored — indicator just stays off.
  }
}

const onChannelsChanged = (channels: IMChannelOverview[]) => {
  hasActiveIMChannels.value = channels.some((c) => c.enabled)
}

// Anchor the floating submenu just to the right of the hovered menu item,
// clamped to the viewport so it stays visible near the screen edge.
const positionIMSubmenu = () => {
  const el = imMenuItemRef.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const PANEL_WIDTH = 300
  const PANEL_MAX_HEIGHT = 520
  const GAP = 8
  const MARGIN = 8

  let left = rect.right + GAP
  // If the panel would overflow the right edge, flip to the left side.
  if (left + PANEL_WIDTH + MARGIN > window.innerWidth) {
    left = Math.max(MARGIN, rect.left - PANEL_WIDTH - GAP)
  }

  // Align the panel's top with the menu item, then clamp so it doesn't
  // spill past the bottom of the viewport.
  let top = rect.top - 4
  const maxTop = window.innerHeight - Math.min(PANEL_MAX_HEIGHT, window.innerHeight - MARGIN * 2) - MARGIN
  if (top > maxTop) top = maxTop
  if (top < MARGIN) top = MARGIN

  imSubmenuStyle.value = {
    left: `${left}px`,
    top: `${top}px`,
  }
}

const CHROME_EXTENSION_URL =
  'https://chromewebstore.google.com/detail/jpemjbopikggjlmikmclgbmkhhopjdgd?utm_source=item-share-cb'

const CLAWHUB_SKILL_URL = 'https://clawhub.ai/lyingbug/weknora'

// 打开 WeKnora Chrome 插件（Chrome应用商店）
const openChromeExtension = () => {
  menuVisible.value = false
  window.open(CHROME_EXTENSION_URL, '_blank')
}

const openClawhubSkill = () => {
  menuVisible.value = false
  window.open(CLAWHUB_SKILL_URL, '_blank')
}

// 打开 GitHub
const openGithub = () => {
  menuVisible.value = false
  window.open('https://github.com/Tencent/WeKnora', '_blank')
}

// 注销
const handleLogout = async () => {
  menuVisible.value = false
  
  try {
    // 调用后端API注销
    await logoutApi()
  } catch (error) {
    // 即使API调用失败，也继续执行本地清理
    console.error('注销API调用失败:', error)
  }
  
  // 清理所有状态和本地存储
  authStore.logout()
  
  MessagePlugin.success(t('auth.logout'))
  
  // 跳转到登录页
  router.push('/login')
}

// 加载用户信息
const loadUserInfo = async () => {
  try {
    const response = await getCurrentUser()
    if (response.success && response.data && response.data.user) {
      const user = response.data.user
      userInfo.value = {
        username: user.username || t('common.info'),
        email: user.email || 'user@example.com',
        avatar: user.avatar || ''
      }
      // 同时更新 authStore 中的用户信息，确保包含 can_access_all_tenants 字段
      authStore.setUser({
        id: user.id,
        username: user.username,
        email: user.email,
        avatar: user.avatar,
        tenant_id: user.tenant_id,
        can_access_all_tenants: user.can_access_all_tenants || false,
        created_at: user.created_at,
        updated_at: user.updated_at
      })
      // 如果返回了租户信息，也更新租户信息
      if (response.data.tenant) {
        authStore.setTenant({
          id: String(response.data.tenant.id),
          name: response.data.tenant.name,
          api_key: response.data.tenant.api_key || '',
          owner_id: user.id,
          created_at: response.data.tenant.created_at,
          updated_at: response.data.tenant.updated_at
        })
      }
    }
  } catch (error) {
    console.error('Failed to load user info:', error)
  }
}

// 点击外部关闭菜单
const handleClickOutside = (e: MouseEvent) => {
  const target = e.target as Node
  if (menuRef.value && menuRef.value.contains(target)) return
  // The IM and tenant submenus are teleported to body, so they're not inside
  // menuRef — check them separately to avoid closing the dropdown when the
  // user clicks one of the floating panels.
  const imFloating = document.querySelector('.im-submenu-floating')
  if (imFloating && imFloating.contains(target)) return
  const tenantFloating = document.querySelector('.tenant-submenu-floating')
  if (tenantFloating && tenantFloating.contains(target)) return
  menuVisible.value = false
  imSubmenuOpen.value = false
  tenantSubmenuOpen.value = false
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  loadUserInfo()
  refreshIMStatus()
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style lang="less" scoped>
.user-menu {
  position: relative;
  width: 100%;

  &--collapsed {
    .user-button {
      justify-content: center;
      padding: 8px;
      gap: 0;
    }

    .user-avatar {
      width: 32px;
      height: 32px;

      .avatar-placeholder {
        font-size: 13px;
      }
    }

    .user-dropdown {
      left: calc(100% + 8px);
      bottom: 0;
      right: auto;
      min-width: 200px;
    }
  }
}

.user-button {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.2s;
  background: transparent;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:active {
    transform: scale(0.98);
  }
}

.user-avatar {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  overflow: hidden;
  flex-shrink: 0;
  background: linear-gradient(135deg, var(--td-brand-color) 0%, var(--td-brand-color-active) 100%);
  display: flex;
  align-items: center;
  justify-content: center;
  transition: width 0.2s ease, height 0.2s ease;

  img {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  .avatar-placeholder {
    color: var(--td-text-color-anti);
    font-size: 16px;
    font-weight: 600;
  }
}

.user-info {
  flex: 1;
  min-width: 0;
  text-align: left;

  .user-name {
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .user-email {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}

.dropdown-icon {
  font-size: 16px;
  color: var(--td-text-color-secondary);
  flex-shrink: 0;
  transition: transform 0.2s;
}

.user-dropdown {
  position: absolute;
  bottom: 100%;
  left: 8px;
  right: 8px;
  margin-bottom: 8px;
  background: var(--td-bg-color-container);
  border-radius: 8px;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.12);
  border: 1px solid var(--td-component-stroke);
  overflow: hidden;
  z-index: 1000;
}

.menu-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  cursor: pointer;
  transition: all 0.2s;
  font-size: 14px;
  color: var(--td-text-color-primary);

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.danger {
    color: var(--td-error-color);

    &:hover {
      background: var(--td-error-color-light);
    }

    .menu-icon {
      color: var(--td-error-color);
    }
  }

  // 包含右弹子菜单的菜单项
  &--submenu {
    position: relative;

    .menu-item-label {
      flex: 1;
    }

    .menu-chevron {
      font-size: 14px;
      color: var(--td-text-color-placeholder);
      flex-shrink: 0;
      transition: transform 0.15s;
    }

    &.is-open {
      background: var(--td-bg-color-container-hover);

      .menu-chevron {
        color: var(--td-text-color-secondary);
      }
    }

    // "Live" indicator — shown when at least one IM channel is enabled.
    // A small green dot with a halo that pulses to signal active connections.
    .live-indicator {
      position: relative;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 10px;
      height: 10px;
      margin-right: 2px;
      flex-shrink: 0;
    }

    .live-indicator-dot {
      position: relative;
      width: 6px;
      height: 6px;
      border-radius: 50%;
      background: var(--td-success-color, #07c160);

      // Pulsing halo around the dot. Prefers-reduced-motion disables it.
      &::after {
        content: '';
        position: absolute;
        inset: -3px;
        border-radius: 50%;
        background: var(--td-success-color, #07c160);
        opacity: 0.45;
        animation: im-live-pulse 1.6s ease-out infinite;
        pointer-events: none;
      }
    }

    @media (prefers-reduced-motion: reduce) {
      .live-indicator-dot::after {
        animation: none;
      }
    }
  }

  .menu-icon {
    font-size: 16px;
    color: var(--td-text-color-secondary);
    
    &.svg-icon {
      width: 16px;
      height: 16px;
      flex-shrink: 0;
    }

    &--emoji {
      width: 16px;
      height: 16px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      font-size: 15px;
      line-height: 1;
      flex-shrink: 0;
      color: inherit;
    }
  }

  .menu-text-with-icon {
    flex: 1;
    display: flex;
    align-items: center;
    gap: 6px;
    color: inherit;
    min-width: 0;

    > span:first-of-type {
      display: inline-flex;
      align-items: center;
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }

  .menu-new-badge {
    flex-shrink: 0;
    font-size: 10px;
    font-weight: 600;
    line-height: 1.2;
    padding: 2px 5px;
    border-radius: 4px;
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
    letter-spacing: 0.02em;
  }

  .menu-github-star-icon {
    flex-shrink: 0;
    color: var(--td-warning-color);
  }

  .menu-external-icon {
    width: 14px;
    height: 14px;
    color: var(--td-text-color-disabled);
    flex-shrink: 0;
    transition: color 0.2s ease;
    pointer-events: none;
  }

  &:hover .menu-external-icon {
    color: var(--td-brand-color);
  }
}

.menu-divider {
  height: 1px;
  background: var(--td-component-stroke);
  margin: 4px 0;
}

// 下拉动画
.dropdown-enter-active,
.dropdown-leave-active {
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

.dropdown-enter-from,
.dropdown-leave-to {
  opacity: 0;
  transform: translateY(8px);
}

.dropdown-enter-to,
.dropdown-leave-from {
  opacity: 1;
  transform: translateY(0);
}

// Live indicator halo animation — a soft expanding ring to signal that at
// least one IM channel is actively connected.
@keyframes im-live-pulse {
  0% {
    transform: scale(0.9);
    opacity: 0.45;
  }
  70% {
    transform: scale(1.8);
    opacity: 0;
  }
  100% {
    transform: scale(1.8);
    opacity: 0;
  }
}
</style>

<style lang="less">
// Non-scoped: the IM submenu is teleported to <body> so scoped styles
// from this component won't reach it. The panel component's own CSS is
// scoped and self-contained; this rule only positions the wrapper.
.im-submenu-floating {
  position: fixed;
  z-index: 1100;
  // Invisible padding forms a pointer bridge from the menu item to the
  // panel so hovering across the gap doesn't fire mouseleave-hide.
  padding-left: 2px;
}

// Tenant switcher submenu — same teleport rationale as .im-submenu-floating.
// All styling for the panel itself lives here (not in a child component) so
// the markup in UserMenu.vue stays self-contained.
.tenant-submenu-floating {
  position: fixed;
  z-index: 1100;
  width: 280px;
  max-height: 360px;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
  border: 0.5px solid var(--td-component-stroke);
  border-radius: 10px;
  box-shadow: 0 6px 24px rgba(0, 0, 0, 0.12);
  // Pointer bridge so the user can slide off the menu item onto the panel
  // without hitting the gap and triggering mouseleave-hide.
  padding-left: 2px;
  overflow: hidden;

  .tenant-submenu-header {
    padding: 10px 14px 8px;
    font-size: 12px;
    font-weight: 600;
    color: var(--td-text-color-secondary);
    border-bottom: 0.5px solid var(--td-component-stroke);
  }

  .tenant-submenu-list {
    overflow-y: auto;
    padding: 6px;
  }

  .tenant-submenu-item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 10px;
    border-radius: 6px;
    cursor: pointer;
    transition: background 0.15s;

    &:hover {
      background: var(--td-bg-color-secondarycontainer);
    }

    &.is-current {
      background: rgba(7, 192, 95, 0.08);
      cursor: default;

      .tenant-submenu-item-name {
        color: var(--td-brand-color);
        font-weight: 500;
      }
    }
  }

  .tenant-submenu-item-avatar {
    width: 32px;
    height: 32px;
    border-radius: 6px;
    background: var(--td-bg-color-secondarycontainer);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-secondary);
    flex-shrink: 0;

    &.is-current {
      background: linear-gradient(135deg, var(--td-brand-color) 0%, var(--td-brand-color-active) 100%);
      color: var(--td-text-color-anti);
    }
  }

  .tenant-submenu-item-info {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }

  .tenant-submenu-item-name {
    font-size: 13px;
    color: var(--td-text-color-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .tenant-submenu-item-role {
    font-size: 11px;
    color: var(--td-text-color-placeholder);
  }

  .tenant-submenu-item-badge {
    flex-shrink: 0;
    font-size: 10px;
    font-weight: 600;
    line-height: 1.2;
    padding: 2px 6px;
    border-radius: 4px;
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
  }

  .tenant-submenu-empty {
    padding: 16px 12px;
    text-align: center;
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }
}
</style>

