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
          <!-- 多租户 / superuser：首行租户名，次行 username · 角色。单租户：昵称 + 邮箱。 -->
          <template v-if="showTenantIdentityLine">
            <div class="user-tenant-name" :title="activeTenantName">{{ activeTenantName }}</div>
            <div class="user-tenant-meta">
              <span v-if="userName && userName !== activeTenantName" class="user-tenant-meta-name">{{ userName }}</span>
              <span v-if="(userName && userName !== activeTenantName) && currentRoleLabel"
                class="user-tenant-meta-sep">·</span>
              <t-icon v-if="currentRoleIcon" :name="currentRoleIcon" size="12px" class="user-tenant-meta-icon" />
              <span v-if="currentRoleLabel" class="user-tenant-meta-role">{{ currentRoleLabel }}</span>
            </div>
          </template>
          <template v-else>
            <div class="user-name">{{ userName }}</div>
            <div class="user-email">{{ userEmail }}</div>
          </template>
        </div>
        <t-icon :name="menuVisible ? 'chevron-up' : 'chevron-down'" class="dropdown-icon" />
      </template>
    </div>

    <!-- 下拉菜单 -->
    <Transition name="dropdown">
      <div v-if="menuVisible" class="user-dropdown" @click.stop>
        <!-- 弹出菜单：账号（头像+昵称）／当前租户（名称+权限）；底部侧栏样式不改。 -->
        <div v-if="userName" class="dropdown-user-header">
          <div class="dropdown-user-avatar">
            <img v-if="userAvatar" :src="userAvatar" :alt="$t('common.avatar')" />
            <span v-else class="dropdown-user-avatar-placeholder">{{ userInitial }}</span>
          </div>
          <div class="dropdown-user-meta">
            <div class="dropdown-user-name">{{ userName }}</div>
          </div>
        </div>

        <div v-if="userName && !authStore.isLiteMode" ref="tenantMenuItemRef" class="dropdown-tenant-panel" :class="{
          'is-open': tenantSubmenuOpen,
          'is-clickable': showTenantSwitcher,
        }" @mouseenter="showTenantSwitcher && showTenantSubmenu()"
          @mouseleave="showTenantSwitcher && scheduleHideTenantSubmenu()">
          <t-icon name="system-sum" class="menu-icon" aria-hidden="true" />
          <div class="dropdown-tenant-panel-main">
            <span class="dropdown-tenant-panel-name" :title="activeTenantName || userName">
              {{ activeTenantName || userName }}
            </span>
            <div v-if="currentRoleLabel" class="dropdown-tenant-panel-role">
              <t-icon v-if="currentRoleIcon" :name="currentRoleIcon" size="12px"
                class="dropdown-tenant-panel-role-icon" />
              <span>{{ currentRoleLabel }}</span>
            </div>
          </div>
          <t-icon v-if="showTenantSwitcher" name="swap" class="dropdown-tenant-panel-trail"
            :title="$t('tenant.switcher.menuLabel')" />
        </div>
        <div class="menu-divider"></div>
        <!-- QuickNav 入口与 Settings 的最低角色对齐：members/models/websearch/mcp/api
             分别对应 viewer/viewer/admin/admin/owner（详情见 Settings.vue 的
             SECTION_MIN_ROLE）。低角色用户看到这些入口点进去也只能看到
             role-denied 兜底页，索性藏起来。 -->
        <div v-if="canSeeQuickNav('members')" class="menu-item" @click="handleQuickNav('members')">
          <t-icon name="usergroup" class="menu-icon" />
          <span>{{ $t('tenantMember.title') }}</span>
        </div>
        <div v-if="canSeeQuickNav('models')" class="menu-item" @click="handleQuickNav('models')">
          <t-icon name="control-platform" class="menu-icon" />
          <span>{{ $t('settings.modelManagement') }}</span>
        </div>
        <div v-if="canSeeQuickNav('websearch')" class="menu-item" @click="handleQuickNav('websearch')">
          <svg width="16" height="16" viewBox="0 0 18 18" fill="none" xmlns="http://www.w3.org/2000/svg"
            class="menu-icon svg-icon">
            <circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.2" fill="none" />
            <path d="M 9 2 A 3.5 7 0 0 0 9 16" stroke="currentColor" stroke-width="1.2" fill="none" />
            <path d="M 9 2 A 3.5 7 0 0 1 9 16" stroke="currentColor" stroke-width="1.2" fill="none" />
            <line x1="2.94" y1="5.5" x2="15.06" y2="5.5" stroke="currentColor" stroke-width="1.2"
              stroke-linecap="round" />
            <line x1="2.94" y1="12.5" x2="15.06" y2="12.5" stroke="currentColor" stroke-width="1.2"
              stroke-linecap="round" />
          </svg>
          <span>{{ $t('settings.webSearchConfig') }}</span>
        </div>
        <div v-if="canSeeQuickNav('mcp')" class="menu-item" @click="handleQuickNav('mcp')">
          <t-icon name="tools" class="menu-icon" />
          <span>{{ $t('settings.mcpService') }}</span>
        </div>
        <div v-if="canSeeQuickNav('api')" class="menu-item" @click="handleQuickNav('api')">
          <t-icon name="secured" class="menu-icon" />
          <span>{{ $t('settings.apiInfo') }}</span>
        </div>
        <div ref="imMenuItemRef" class="menu-item menu-item--submenu" :class="{ 'is-open': imSubmenuOpen }"
          @mouseenter="showIMSubmenu" @mouseleave="scheduleHideIMSubmenu">
          <t-icon name="link" class="menu-icon" />
          <span class="menu-item-label">{{ $t('imOverview.menuTitle') }}</span>
          <span v-if="hasActiveIMChannels" class="live-indicator" :title="$t('imOverview.liveIndicator')"
            aria-hidden="true">
            <span class="live-indicator-dot"></span>
          </span>
          <t-icon name="chevron-right" class="menu-chevron" />
        </div>
        <div class="menu-divider"></div>
        <div class="menu-item" @click="handleSettings">
          <t-icon name="setting" class="menu-icon" />
          <span>{{ $t('general.allSettings') }}</span>
        </div>
        <!-- 切换租户入口在下拉「当前租户」区块 hover；此处仅为分隔线与菜单项。 -->
        <div class="menu-divider"></div>
        <div class="menu-item" @click="openClawhubSkill">
          <span class="menu-icon menu-icon--emoji" role="img" :aria-label="$t('common.clawhubSkill')">🦞</span>
          <span class="menu-text-with-icon">
            <span>{{ $t('common.clawhubSkill') }}</span>
            <span class="menu-new-badge">{{ $t('common.newBadge') }}</span>
            <svg class="menu-external-icon" viewBox="0 0 16 16" aria-hidden="true">
              <path fill="currentColor"
                d="M12.667 8a.667.667 0 0 1 .666.667v4a2.667 2.667 0 0 1-2.666 2.666H4.667a2.667 2.667 0 0 1-2.667-2.666V5.333a2.667 2.667 0 0 1 2.667-2.666h4a.667.667 0 1 1 0 1.333h-4a1.333 1.333 0 0 0-1.333 1.333v7.334A1.333 1.333 0 0 0 4.667 13.333h6a1.333 1.333 0 0 0 1.333-1.333v-4A.667.667 0 0 1 12.667 8Zm2.666-6.667v4a.667.667 0 0 1-1.333 0V3.276l-5.195 5.195a.667.667 0 0 1-.943-.943l5.195-5.195h-2.057a.667.667 0 0 1 0-1.333h4a.667.667 0 0 1 .666.666Z" />
            </svg>
          </span>
        </div>
        <div class="menu-item" @click="openChromeExtension">
          <t-icon name="extension" class="menu-icon" />
          <span class="menu-text-with-icon">
            <span>{{ $t('common.chromeExtension') }}</span>
            <span class="menu-new-badge">{{ $t('common.newBadge') }}</span>
            <svg class="menu-external-icon" viewBox="0 0 16 16" aria-hidden="true">
              <path fill="currentColor"
                d="M12.667 8a.667.667 0 0 1 .666.667v4a2.667 2.667 0 0 1-2.666 2.666H4.667a2.667 2.667 0 0 1-2.667-2.666V5.333a2.667 2.667 0 0 1 2.667-2.666h4a.667.667 0 1 1 0 1.333h-4a1.333 1.333 0 0 0-1.333 1.333v7.334A1.333 1.333 0 0 0 4.667 13.333h6a1.333 1.333 0 0 0 1.333-1.333v-4A.667.667 0 0 1 12.667 8Zm2.666-6.667v4a.667.667 0 0 1-1.333 0V3.276l-5.195 5.195a.667.667 0 0 1-.943-.943l5.195-5.195h-2.057a.667.667 0 0 1 0-1.333h4a.667.667 0 0 1 .666.666Z" />
            </svg>
          </span>
        </div>
        <div class="menu-item" :title="$t('common.githubStarTip')" @click="openGithub">
          <t-icon name="logo-github" class="menu-icon" />
          <span class="menu-text-with-icon">
            <span>{{ $t('common.github') }}</span>
            <t-icon name="star-filled" class="menu-github-star-icon" size="16px" aria-hidden="true" />
            <svg class="menu-external-icon" viewBox="0 0 16 16" aria-hidden="true">
              <path fill="currentColor"
                d="M12.667 8a.667.667 0 0 1 .666.667v4a2.667 2.667 0 0 1-2.666 2.666H4.667a2.667 2.667 0 0 1-2.667-2.666V5.333a2.667 2.667 0 0 1 2.667-2.666h4a.667.667 0 1 1 0 1.333h-4a1.333 1.333 0 0 0-1.333 1.333v7.334A1.333 1.333 0 0 0 4.667 13.333h6a1.333 1.333 0 0 0 1.333-1.333v-4A.667.667 0 0 1 12.667 8Zm2.666-6.667v4a.667.667 0 0 1-1.333 0V3.276l-5.195 5.195a.667.667 0 0 1-.943-.943l5.195-5.195h-2.057a.667.667 0 0 1 0-1.333h4a.667.667 0 0 1 .666.666Z" />
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
      <div v-if="imSubmenuOpen" class="im-submenu-floating" :style="imSubmenuStyle" @mouseenter="showIMSubmenu"
        @mouseleave="scheduleHideIMSubmenu">
        <IMChannelsOverviewPanel :active="imSubmenuOpen" @close="closeAll" @channels-changed="onChannelsChanged" />
      </div>
    </Teleport>

    <!-- Tenant switcher floating panel — shares the same teleport rationale
         as the IM submenu. Data comes from authStore.memberships, kept fresh via
         GET /auth/me when the submenu opens (throttled) and after invite/create. -->
    <Teleport to="body">
      <div v-if="tenantSubmenuOpen" class="tenant-submenu-floating" :style="tenantSubmenuStyle"
        @mouseenter="showTenantSubmenu" @mouseleave="scheduleHideTenantSubmenu">
        <div class="tenant-submenu-header">
          {{ $t('tenant.switcher.menuLabel') }}
        </div>
        <div class="tenant-submenu-list">
          <div v-for="m in switchableMemberships" :key="m.tenant_id" class="tenant-submenu-item"
            :class="{ 'is-current': isCurrentTenant(m.tenant_id) }" @click="switchToTenant(m)">
            <div class="tenant-submenu-item-avatar" :class="{ 'is-current': isCurrentTenant(m.tenant_id) }">
              {{ tenantInitial(m) }}
              <!-- Home 标识：home tenant 行的 avatar 右下角加一个小 home
                   icon。比起在 meta 行单独立一个「我的」pill，这里更省地、
                   也保持各行徽标列对齐。 -->
              <span v-if="isHomeTenant(m.tenant_id)" class="tenant-submenu-item-home-dot"
                :title="$t('tenant.switcher.homeTooltip')">
                <t-icon name="home" size="9px" />
              </span>
            </div>
            <!-- 两行布局：第一行是 tenant 名（拿满剩余宽度，避免被徽标截断
                 — 之前 home + 当前 两个徽标在同一行时，长 tenant 名直接
                 被压成省略号）；第二行 role（带角色图标） + 「当前」徽标。
                 home 徽标已挪到 tenant 名首字母 avatar 角落，不再在 meta
                 行额外占位，避免徽标列宽不齐。 -->
            <div class="tenant-submenu-item-info">
              <span class="tenant-submenu-item-name">{{ tenantDisplayName(m) }}</span>
              <div class="tenant-submenu-item-meta">
                <span class="tenant-submenu-item-role">
                  <t-icon v-if="roleIcon(m.role)" :name="roleIcon(m.role)" size="12px"
                    class="tenant-submenu-item-role-icon" />
                  {{ formatRole(m.role) }}
                </span>
                <span v-if="isCurrentTenant(m.tenant_id)" class="tenant-submenu-item-badge">{{
                  $t('tenant.switcher.currentBadge') }}</span>
              </div>
            </div>
          </div>
          <div v-if="switchableMemberships.length === 0" class="tenant-submenu-empty">
            {{ $t('tenant.switcher.empty') }}
          </div>
        </div>
        <!-- 自助创建新工作区入口：放在租户列表底部，所有能 hover 出这个
             子菜单的用户都能看到（包括单租户用户）。后端 router 已对
             POST /api/v1/tenants 去掉跨租户超管守卫，handler 内部会把
             当前用户 EnsureOwner 成新租户的 Owner。 -->
        <div class="tenant-submenu-create" @click="openCreateTenantDialog">
          <t-icon name="add" class="tenant-submenu-create-icon" />
          <span class="tenant-submenu-create-label">{{ $t('tenant.create.action') }}</span>
        </div>
      </div>
    </Teleport>

    <!-- 创建工作区弹窗 -->
    <CreateTenantDialog v-model:visible="createTenantDialogVisible" @created="onTenantCreated" />
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
import CreateTenantDialog from '@/components/CreateTenantDialog.vue'
import { listAllIMChannels, type IMChannelOverview } from '@/api/agent'
import { navigateAfterTenantSwitch } from '@/utils/tenantSwitch'
import type { TenantInfo } from '@/api/tenant'
import { useRoleLabel, useHomeTenant } from '@/composables/useRoleLabel'

const { t } = useI18n()

const router = useRouter()
const uiStore = useUIStore()
const authStore = useAuthStore()
const { formatRole, roleIcon } = useRoleLabel()
const { homeTenantId, isHomeTenantActive, isHomeTenant } = useHomeTenant()

// 顶部用户卡片展示的租户名 / 当前角色：跟着 tenant 切换器实时变。
// activeTenantName 优先用切换器选中的名字（含 fallback 到 home tenant 名字），
// 单租户用户也能正常显示自己的 home tenant 名。
const activeTenantName = computed(() => {
  return (
    authStore.selectedTenantName ||
    authStore.tenant?.name ||
    ''
  )
})
const currentRoleLabel = computed(() => formatRole(authStore.currentTenantRole))
const currentRoleIcon = computed(() => roleIcon(authStore.currentTenantRole))

// 单租户用户（memberships <= 1 且非 superuser）= 永远 home + owner，第三
// 行就是 user-email 信息的重复，没必要占视觉空间；只对多租户 / superuser
// 渲染。Lite 模式下没有 RBAC 概念，统一隐藏。
const showTenantIdentityLine = computed(() => {
  if (authStore.isLiteMode) return false
  if (authStore.canAccessAllTenants) return true
  return (authStore.memberships ?? []).length > 1
})

// 与 Settings.vue 的 SECTION_MIN_ROLE 同步；这里只挂 quickNav 直接跳转的
// 那 4 项。改这张表前请同步 Settings.vue 的对照注释。
const QUICKNAV_MIN_ROLE: Record<string, 'viewer' | 'contributor' | 'admin' | 'owner'> = {
  members: 'viewer',
  models: 'viewer',
  websearch: 'admin',
  mcp: 'admin',
  api: 'owner',
}
const canSeeQuickNav = (key: string): boolean => {
  if (authStore.canAccessAllTenants) return true
  return authStore.hasRole(QUICKNAV_MIN_ROLE[key] ?? 'viewer')
}

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
  clampFloatingToViewport('.im-submenu-floating', imSubmenuStyle)
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

// ---------- Create new tenant ----------
// 普通用户在租户子菜单底部点 "+ 创建新工作区" → 弹 CreateTenantDialog →
// 后端写一行 owner 的 tenant_members → 直接切到新租户。复用 switchToTenant
// 同款的 setSelectedTenant + navigateAfterTenantSwitch 链路，避免 token
// 依然指向旧租户带来的 SSE / store 不一致。
const createTenantDialogVisible = ref(false)

const openCreateTenantDialog = () => {
  closeAll()
  createTenantDialogVisible.value = true
}

const onTenantCreated = async (newTenant: TenantInfo) => {
  await authStore.refreshFromAuthMe()
  authStore.setSelectedTenant(newTenant.id, newTenant.name)
  setTimeout(() => {
    navigateAfterTenantSwitch()
  }, 300)
}

// ---------- Tenant switcher submenu ----------
//
// Same hover-driven submenu pattern as the IM panel above; data comes from
// authStore.memberships (refreshed from /auth/me when the submenu opens and
// after membership-changing actions). PR 4 of #1303 relaxed the X-Tenant-ID
// gate in middleware/auth.go to accept active membership rows, so flipping
// authStore.selectedTenantId here is enough — the next page reload re-issues
// every request with the new header and the server resolves the role server-side.
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

// Rendered whenever the user has at least one membership — even single-
// tenant users need this submenu to discover the "create new workspace"
// entry at the bottom. Multi-tenant users additionally use it to switch
// between memberships. Cross-tenant superusers keep using the sidebar
// TenantSelector for the "any tenant in the system" case, so we don't
// double-show that here.
const showTenantSwitcher = computed(() => {
  return switchableMemberships.value.length >= 1
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

const switchToTenant = (m: Membership) => {
  if (isCurrentTenant(m.tenant_id)) {
    closeAll()
    return
  }
  // Treat switching back to the user's home tenant as "clear the
  // override" so request.ts stops attaching X-Tenant-ID. This mirrors
  // what TenantSelector.vue does in selectTenant().
  const home = homeTenantId.value
  if (home !== null && home === m.tenant_id) {
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

let lastTenantSubmenuMembershipRefresh = 0
const TENANT_SUBMENU_MEMBERSHIP_REFRESH_MS = 2000

const showTenantSubmenu = () => {
  if (tenantSubmenuHideTimer) {
    clearTimeout(tenantSubmenuHideTimer)
    tenantSubmenuHideTimer = null
  }
  positionTenantSubmenu()
  tenantSubmenuOpen.value = true
  clampFloatingToViewport('.tenant-submenu-floating', tenantSubmenuStyle)
  const now = Date.now()
  if (now - lastTenantSubmenuMembershipRefresh >= TENANT_SUBMENU_MEMBERSHIP_REFRESH_MS) {
    lastTenantSubmenuMembershipRefresh = now
    void authStore.refreshFromAuthMe()
  }
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
  const PANEL_WIDTH = 264
  const GAP = 8
  const MARGIN = 8

  let left = rect.right + GAP
  if (left + PANEL_WIDTH + MARGIN > window.innerWidth) {
    left = Math.max(MARGIN, rect.left - PANEL_WIDTH - GAP)
  }

  const top = Math.max(MARGIN, rect.top)

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
  const GAP = 8
  const MARGIN = 8

  let left = rect.right + GAP
  // If the panel would overflow the right edge, flip to the left side.
  if (left + PANEL_WIDTH + MARGIN > window.innerWidth) {
    left = Math.max(MARGIN, rect.left - PANEL_WIDTH - GAP)
  }

  // Align with the menu item's top; bottom-clamping is done after render
  // when we know the panel's actual height (see clampSubmenuToViewport).
  // 之前用 PANEL_MAX_HEIGHT=520 提前 clamp 会把实际只有 ~140px 的面板
  // 顶到屏幕中上方，与菜单项错开很多。
  const top = Math.max(MARGIN, rect.top)

  imSubmenuStyle.value = {
    left: `${left}px`,
    top: `${top}px`,
  }
}

// 在面板真正渲染后，按它的实际高度做底部 clamp，避免面板跑出屏幕外。
const clampFloatingToViewport = (selector: string, target: { value: Record<string, string> }) => {
  requestAnimationFrame(() => {
    const panel = document.querySelector(selector) as HTMLElement | null
    if (!panel) return
    const MARGIN = 8
    const h = panel.offsetHeight
    const currentTop = parseFloat(target.value.top || '0') || 0
    const maxTop = window.innerHeight - h - MARGIN
    if (currentTop > maxTop) {
      target.value = { ...target.value, top: `${Math.max(MARGIN, maxTop)}px` }
    }
  })
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
        preferences: user.preferences,
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
      const membershipsSync = response.data.memberships
      if (Array.isArray(membershipsSync)) {
        authStore.setMemberships(membershipsSync)
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
      padding: 6px 3px;
      gap: 0;
    }

    .user-dropdown {
      left: calc(100% + 8px);
      bottom: 0;
      right: auto;
      /* 与展开侧栏时下拉可视宽度对齐（aside 宽 260px） */
      min-width: 260px;
    }
  }
}

.user-button {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 6px;
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
  width: 24px;
  height: 24px;
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
    font-size: 12px;
    font-weight: 600;
    line-height: 1;
  }
}

.user-info {
  flex: 1;
  min-width: 0;
  text-align: left;
  display: flex;
  flex-direction: column;
  gap: 2px;
  justify-content: center;

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

  .user-tenant-name {
    font-size: 14px;
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--td-text-color-primary);
    line-height: 1.35;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .user-tenant-meta {
    display: flex;
    align-items: center;
    gap: 4px;
    margin-top: 0;
    min-width: 0;
    font-size: 12px;
    line-height: 1.35;
    color: var(--td-text-color-secondary);

    .user-tenant-meta-name {
      flex: 0 1 auto;
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .user-tenant-meta-sep {
      flex-shrink: 0;
      color: var(--td-text-color-placeholder);
    }

    .user-tenant-meta-icon {
      flex-shrink: 0;
      color: inherit;
    }

    .user-tenant-meta-role {
      flex-shrink: 0;
    }
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
  /* 相对 .user-menu：左右由 left/right 拉宽；右缘用正值内缩，避免与侧栏内容区右边界完全重合 */
  left: -4px;
  right: -5px;
  margin-bottom: 6px;
  background: var(--td-bg-color-container);
  border-radius: 8px;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.12);
  border: 1px solid var(--td-component-stroke);
  overflow: hidden;
  z-index: 1000;
}

// 下拉顶部 — 账号区：24px 头像中心与下方 16px 菜单图标中心同竖线；
// margin-left −4px、gap 6px 保持昵称起点与菜单文案对齐（12 + 24 + 6 − 4 = 38）
.dropdown-user-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 9px 12px;
  min-width: 0;

  .dropdown-user-avatar {
    width: 24px;
    height: 24px;
    margin-left: -4px;
    border-radius: 50%;
    overflow: hidden;
    flex-shrink: 0;
    background: linear-gradient(135deg, var(--td-brand-color) 0%, var(--td-brand-color-active) 100%);
    display: flex;
    align-items: center;
    justify-content: center;

    img {
      width: 100%;
      height: 100%;
      object-fit: cover;
    }

    .dropdown-user-avatar-placeholder {
      color: var(--td-text-color-anti);
      font-size: 12px;
      font-weight: 600;
      line-height: 1;
    }
  }

  .dropdown-user-meta {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0;
    justify-content: center;
  }

  .dropdown-user-name {
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    line-height: 1.35;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}

// 下拉 — 当前工作区：与下方 .menu-item 同款对齐（左 16px 图标槽 + 文案列 + 右侧操作图标）
.dropdown-tenant-panel {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border-top: 1px solid var(--td-component-stroke);
  background: transparent;
  transition: background 0.15s ease;
  min-width: 0;

  >.menu-icon {
    font-size: 16px;
    color: var(--td-text-color-secondary);
    flex-shrink: 0;
  }

  &.is-clickable {
    cursor: pointer;

    &:hover,
    &.is-open {
      background: var(--td-bg-color-container-hover);

      .dropdown-tenant-panel-trail {
        color: var(--td-brand-color);
      }
    }
  }

  .dropdown-tenant-panel-main {
    flex: 1 1 auto;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }

  .dropdown-tenant-panel-name {
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    line-height: 1.35;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .dropdown-tenant-panel-trail {
    flex-shrink: 0;
    font-size: 16px;
    color: var(--td-text-color-placeholder);
    transition: color 0.15s ease;
  }

  .dropdown-tenant-panel-role {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    line-height: 1.35;
    color: var(--td-text-color-secondary);
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;

    .dropdown-tenant-panel-role-icon {
      flex-shrink: 0;
      color: inherit;
    }
  }
}

.menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
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
      font-size: 16px;
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

    >span:first-of-type {
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
    width: 16px;
    height: 16px;
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
  margin: 3px 0;
}

// 紧跟账号/租户区块后的分隔线：略收紧与上方的留白
.dropdown-user-header+.menu-divider,
.dropdown-tenant-panel+.menu-divider {
  margin-top: 1px;
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
  width: 264px;
  max-height: 340px;
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
    padding: 8px 12px 6px;
    font-size: 12px;
    font-weight: 600;
    color: var(--td-text-color-secondary);
    border-bottom: 0.5px solid var(--td-component-stroke);
  }

  .tenant-submenu-list {
    overflow-y: auto;
    padding: 4px;
  }

  .tenant-submenu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 7px 8px;
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
    width: 28px;
    height: 28px;
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
    gap: 2px;
  }

  .tenant-submenu-item-name {
    font-size: 13px;
    color: var(--td-text-color-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  // 第二行：role + 徽标，以 inline 形式排在一起。徽标缩到次级位置，
  // 让第一行的 tenant 名拿满宽度（之前长名字会被徽标挤成省略号）。
  .tenant-submenu-item-meta {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
    min-width: 0;
  }

  .tenant-submenu-item-role {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 11px;
    color: var(--td-text-color-placeholder);

    .tenant-submenu-item-role-icon {
      flex-shrink: 0;
      // 颜色继承 role 文字色，避免抢走视觉
      color: inherit;
    }
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

  // Home 标识改为叠在 avatar 右下角的小 dot，不在 meta 行额外占位，让
  // 各行徽标列宽对齐；用户切到非 home tenant 时这个小 icon 仍能一眼指
  // 出「我的主租户在哪一行」。
  .tenant-submenu-item-avatar {
    position: relative;
  }

  .tenant-submenu-item-home-dot {
    position: absolute;
    right: -3px;
    bottom: -3px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: var(--td-bg-color-container);
    color: var(--td-brand-color);
    border: 1.5px solid var(--td-bg-color-container);
    display: flex;
    align-items: center;
    justify-content: center;
    pointer-events: none;
    box-shadow: 0 0 0 0.5px var(--td-success-color-light);
  }

  .tenant-submenu-empty {
    padding: 12px 10px;
    text-align: center;
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }

  .tenant-submenu-create {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 10px;
    margin: 3px 4px 5px;
    border-top: .5px solid var(--td-component-stroke);
    border-radius: 6px;
    cursor: pointer;
    color: var(--td-brand-color);
    font-size: 14px;
    font-weight: 500;
    transition: background 0.15s;

    &:hover {
      background: rgba(7, 192, 95, 0.08);
    }

    .tenant-submenu-create-icon {
      font-size: 16px;
      flex-shrink: 0;
    }

    .tenant-submenu-create-label {
      flex: 1;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
      font-size: 12px;
    }
  }
}
</style>
