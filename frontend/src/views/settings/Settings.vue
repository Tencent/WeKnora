<template>
  <SettingsModalShell :visible="visible" :title="$t('general.settings')" @close="modalShell.requestClose">
    <template #nav>
      <template v-for="group in navGroups" :key="group.key">
        <div class="nav-group-title">{{ group.label }}</div>
        <template v-for="item in group.items" :key="item.key">
          <div :class="['nav-item', {
            'active': currentSection === item.key,
            'has-submenu': item.children && item.children.length > 0,
            'expanded': expandedMenus.includes(item.key)
          }]" @click="handleNavClick(item)">
            <SettingsNavIcon :icon="item.icon" />
            <span class="nav-label">{{ item.label() }}</span>
            <t-icon v-if="item.children && item.children.length > 0"
              :name="expandedMenus.includes(item.key) ? 'chevron-down' : 'chevron-right'"
              class="expand-icon" />
          </div>

          <!-- 子菜单 -->
          <Transition name="submenu">
            <div v-if="item.children && expandedMenus.includes(item.key)" class="submenu">
              <div v-for="(child, childIndex) in item.children" :key="childIndex"
                :class="['submenu-item', { 'active': currentSubSection === child.key }]"
                @click.stop="handleSubMenuClick(item.key, child.key)">
                <span class="submenu-label">{{ child.label }}</span>
              </div>
            </div>
          </Transition>
        </template>
      </template>
    </template>
    <div class="content-wrapper" :class="{
      'content-wrapper--wide': currentEntry?.layout === 'wide',
      'content-wrapper--full': currentEntry?.layout === 'full',
    }">
      <!-- 角色不允许访问当前 section（deep-link 进来 / 跨空间切换后角色降级）—— 优先于具体 section 渲染。
           正常导航走 navItems filter 不会到这里，但 watch(navItems) 的 fallback 会在角色降级
           的瞬间触发；这一段做兜底兼容旧 URL。 -->
      <div v-if="!canSeeSection(currentSection)" class="section role-denied">
        <div class="role-denied-icon">
          <t-icon name="lock-on" size="48px" />
        </div>
        <div class="role-denied-title">{{ $t('settings.roleDenied.title') }}</div>
        <div class="role-denied-desc">{{ $t('settings.roleDenied.desc') }}</div>
      </div>
      <!-- Sections come from the settings registry: builtins plus plugin pages. -->
      <div v-else-if="currentEntry" class="section">
        <component :is="currentEntry.component" v-bind="currentEntry.props?.() ?? {}" />
      </div>
    </div>
  </SettingsModalShell>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { LocationQueryRaw } from 'vue-router'
import { useUIStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useModalShell } from '@/composables/useModalShell'
import SettingsModalShell from '@/components/SettingsModalShell.vue'
import SettingsNavIcon from '@/extensions/SettingsNavIcon.vue'
import { registerBuiltinSettings } from '@/extensions/builtin/settings'
import {
  groupSections,
  sectionPermitted,
  sectionSupported,
  settingsGroups,
  settingsSections,
  type SettingsAccessContext,
  type SettingsSection,
} from '@/extensions/settingsSections'
import { SYSTEM_ADMIN_SETTINGS_SECTIONS } from '@/config/settingsAccess'
import { isToolboxSection, toolboxLocation } from '@/config/toolbox'
import {
  buildSettingsRouteQuery,
  normalizeSettingsSection as normalizeSettingsSectionFromQuery,
  settingsQueryUnchanged,
} from '@/config/settingsRoute'

const route = useRoute()
const router = useRouter()
const uiStore = useUIStore()
const authStore = useAuthStore()
const deploymentCapabilities = useDeploymentCapabilitiesStore()

const { t } = useI18n()

// Sections are registered, not listed here: builtins first, plugins later.
registerBuiltinSettings()

const currentSection = ref<string>('general')
const currentSubSection = ref<string>('')
const expandedMenus = ref<string[]>([])

// Who may see a section comes from each section's access rules (for builtins,
// settingsAccess.ts and deploymentCapabilities.ts, aligned with the guards in
// internal/router). They only decide whether the UI offers the entry; the
// server checks every route itself.
const accessContext = computed<SettingsAccessContext>(() => ({
  isSystemAdmin: authStore.isSystemAdmin,
  canAccessAllTenants: authStore.canAccessAllTenants,
  hasRole: (role) => authStore.hasRole(role),
  isSupported: (capability) => deploymentCapabilities.isSupported(capability),
  capabilities: deploymentCapabilities.capabilities,
}))

const normalizeSettingsSection = (section: string) => {
  return normalizeSettingsSectionFromQuery(section, route.query.tab as string | undefined)
}

const syncSettingsRoute = (sectionKey: string) => {
  if (route.path !== '/platform/settings') return
  const query = buildSettingsRouteQuery(sectionKey, route.query)
  if (settingsQueryUnchanged(route.query, query)) return
  void router.replace({
    path: '/platform/settings',
    query: query as LocationQueryRaw,
  })
}

const isSectionSupported = (key: string): boolean => {
  const section = settingsSections.get(key)
  return !!section && sectionSupported(section, accessContext.value)
}

const canSeeSection = (key: string): boolean => {
  const section = settingsSections.get(key)
  // An unknown key (stale deep link) falls through to the fallback watchers.
  return !section || sectionPermitted(section, accessContext.value)
}

const currentEntry = computed<SettingsSection | undefined>(() => settingsSections.get(currentSection.value))

const navItems = computed<SettingsSection[]>(() => {
  // currentTenantRole 为空表示「membership 还没加载」—— 比起渲染整套
  // viewer 入口然后角色一返回又消失，先卡住不渲染更稳。
  if (!authStore.currentTenantRole && !authStore.canAccessAllTenants) return []
  return settingsSections.items.filter((section) =>
    sectionPermitted(section, accessContext.value) && sectionSupported(section, accessContext.value),
  )
})

const navGroups = computed(() => groupSections(settingsGroups.items, navItems.value))

// 导航项点击处理
const handleNavClick = (item: any) => {
  if (item.children && item.children.length > 0) {
    // 有子菜单，切换展开状态
    const index = expandedMenus.value.indexOf(item.key)
    if (index > -1) {
      expandedMenus.value.splice(index, 1)
    } else {
      expandedMenus.value.push(item.key)
    }
    currentSubSection.value = item.children[0].key
  } else {
    currentSubSection.value = ''
  }

  // 切换到对应页面，并同步 URL 为 ?section=<navKey>（含 integration-claw）。
  // 否则从其它 section 点进来时 query 不变，路由监听会把内容拉回去。
  currentSection.value = item.key
  syncSettingsRoute(item.key)
}

// 子菜单点击处理
const handleSubMenuClick = (parentKey: string, childKey: string) => {
  currentSection.value = parentKey
  currentSubSection.value = childKey

  // 滚动到对应的模型类型区域
  setTimeout(() => {
    const element = document.querySelector(`[data-model-type="${childKey}"]`)
    if (element) {
      element.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
  }, 100)
}

// 控制弹窗显示
const visible = computed(() => {
  return route.path === '/platform/settings' || uiStore.showSettingsModal
})

// 关闭弹窗
const handleClose = () => {
  // Blur before unmount so TDesign textarea autosize won't run on a detached node.
  if (document.activeElement instanceof HTMLElement) {
    document.activeElement.blur()
  }
  uiStore.closeSettings()
  // 如果当前路由是设置页，返回上一页
  if (route.path === '/platform/settings') {
    const sec = route.query.section
    if (typeof sec === 'string' && SYSTEM_ADMIN_SETTINGS_SECTIONS.has(sec)) {
      router.push('/platform/knowledge-bases')
    } else {
      router.back()
    }
  }
}

// Existing in-product shortcuts can still call openSettings; moved tools
// always navigate to the toolbox, with the requested sandbox selection intact.
const redirectToToolbox = (section: string, subSection?: string | null) => {
  if (!isToolboxSection(section)) return false
  uiStore.closeSettings()
  void router.push(toolboxLocation(section, subSection || undefined))
  return true
}

// 监听初始导航设置
watch(() => uiStore.settingsInitialSection, (section) => {
  if (section && visible.value) {
    const normalizedSection = normalizeSettingsSection(section)
    if (redirectToToolbox(normalizedSection, uiStore.settingsInitialSubSection)) return
    if (deploymentCapabilities.loaded && !isSectionSupported(normalizedSection)) {
      MessagePlugin.warning(t('settings.capabilityUnavailable'))
      currentSection.value = navItems.value[0]?.key || 'general'
      currentSubSection.value = ''
      return
    }
    currentSection.value = normalizedSection
    syncSettingsRoute(normalizedSection)
    const navItem = (navItems.value as any[]).find((item) => item.key === normalizedSection)
    if (navItem && navItem.children && navItem.children.length > 0) {
      if (!expandedMenus.value.includes(section)) {
        expandedMenus.value.push(section)
      }
      currentSubSection.value = uiStore.settingsInitialSubSection || navItem.children[0].key
      if (uiStore.settingsInitialSubSection) {
        setTimeout(() => {
          const element = document.querySelector(`[data-model-type="${uiStore.settingsInitialSubSection}"]`)
          if (element) {
            element.scrollIntoView({ behavior: 'smooth', block: 'start' })
          }
        }, 300)
      }
    } else {
      currentSubSection.value = ''
    }
  }
}, { immediate: true })

watch(
  () => [visible.value, route.path, route.query.section, deploymentCapabilities.loaded] as const,
  ([isVisible, path, section, capabilitiesLoaded]) => {
    if (!isVisible || path !== '/platform/settings') return
    if (typeof section !== 'string') {
      syncSettingsRoute(currentSection.value || 'general')
      return
    }
    const normalizedSection = normalizeSettingsSectionFromQuery(
      section,
      typeof route.query.tab === 'string' ? route.query.tab : undefined,
    )
    if (capabilitiesLoaded && !isSectionSupported(normalizedSection)) {
      MessagePlugin.warning(t('settings.capabilityUnavailable'))
      const fallback = navItems.value[0]?.key || 'general'
      currentSection.value = fallback
      currentSubSection.value = ''
      syncSettingsRoute(fallback)
      return
    }
    currentSection.value = normalizedSection
    currentSubSection.value = ''
    syncSettingsRoute(normalizedSection)
  },
  { immediate: true },
)

// 切换空间后角色可能变化，原本可见的 admin-only 面板可能消失。
// 如果 currentSection 落到了不再显示的 key 上，就回退到第一个可见项。
watch(navItems, (items) => {
  if (!items.some((item) => item.key === currentSection.value)) {
    const fallback = items[0]?.key || 'general'
    currentSection.value = fallback
    currentSubSection.value = ''
    syncSettingsRoute(fallback)
  }
})

// Esc / 遮罩点击关闭（与其他设置类弹窗共用同一壳层交互）
const modalShell = useModalShell({
  visible: () => visible.value,
  close: handleClose,
})

// 处理快捷导航事件
const handleSettingsNav = (e: CustomEvent) => {
  const { section, subsection } = e.detail
  if (section) {
    const normalizedSection = normalizeSettingsSection(section)
    if (redirectToToolbox(normalizedSection, subsection)) return
    if (deploymentCapabilities.loaded && !isSectionSupported(normalizedSection)) {
      MessagePlugin.warning(t('settings.capabilityUnavailable'))
      currentSection.value = navItems.value[0]?.key || 'general'
      currentSubSection.value = ''
      return
    }
    currentSection.value = normalizedSection
    syncSettingsRoute(normalizedSection)
    // 如果有子菜单，自动展开
    const navItem = (navItems.value as any[]).find((item: any) => item.key === normalizedSection)
    if (navItem && navItem.children && navItem.children.length > 0) {
      if (!expandedMenus.value.includes(section)) {
        expandedMenus.value.push(section)
      }
      // 如果有 subsection，选中对应的子菜单项
      currentSubSection.value = subsection || navItem.children[0].key
    }
  }
}

onMounted(() => {
  window.addEventListener('settings-nav', handleSettingsNav as EventListener)
})

watch(currentSection, () => {
  if (document.activeElement instanceof HTMLElement) {
    document.activeElement.blur()
  }
})

onUnmounted(() => {
  window.removeEventListener('settings-nav', handleSettingsNav as EventListener)
})
</script>

<style lang="less" scoped>
/* 遮罩层 */
/* 弹窗容器 */
/* 关闭按钮 */
/* 左侧导航栏：略紧凑于最初版，字号与留白适中 */
.expand-icon {
  margin-left: 4px;
  font-size: var(--app-text-base);
  transition: transform var(--app-motion-base) ease;
}

/* 子菜单 */
.submenu {
  margin-left: 28px;
  margin-bottom: 3px;
  overflow: hidden;
}

.submenu-item {
  padding: 5px 12px;
  margin-bottom: 2px;
  border-radius: var(--app-radius-xs);
  cursor: pointer;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  transition: all var(--app-motion-base) ease;
  user-select: none;

  &:hover {
    background-color: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.active {
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.submenu-label {
  display: block;
}

/* 子菜单动画 */
.submenu-enter-active,
.submenu-leave-active {
  transition: all var(--app-motion-base) ease;
}

.submenu-enter-from {
  opacity: 0;
  max-height: 0;
}

.submenu-enter-to {
  opacity: 1;
  max-height: 300px;
}

.submenu-leave-from {
  opacity: 1;
  max-height: 300px;
}

.submenu-leave-to {
  opacity: 0;
  max-height: 0;
}

/* 右侧内容区域 */
.content-wrapper {
  // Bumped from 600 to 760 when the modal grew from 900→1080 (see
  // .settings-modal). Without this, single-column panes (General,
  // Tenant, API key, …) leave a wide right-hand gutter inside the
  // wider modal. 760 keeps comfortable reading-width on long
  // descriptions without the form fields stretching to the full
  // panel width — which would look stranger than a small gutter.
  max-width: 760px;
  padding: 40px 48px;

  /* 成员 / 审计表格列多，600px 会把操作列挤到贴边；铺满右侧内容列更稳。 */
  &--wide {
    max-width: none;
    width: 100%;
    padding: 32px 36px 40px;
    box-sizing: border-box;
  }

  &--full {
    max-width: none;
    width: 100%;
    padding: 30px 34px 40px;
    box-sizing: border-box;
  }
}

.section {
  animation: fadeIn 0.3s ease;
}

@keyframes fadeIn {
  from {
    opacity: 0;
    transform: translateY(10px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

/* 弹窗动画 */
/* 滚动条样式 */
.role-denied {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 64px 24px;
  gap: 12px;
  min-height: 240px;

  .role-denied-icon {
    color: var(--td-text-color-placeholder);
  }

  .role-denied-title {
    font-size: var(--app-text-xl);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .role-denied-desc {
    font-size: var(--app-text-md);
    color: var(--td-text-color-secondary);
    max-width: 360px;
    line-height: 1.6;
  }
}
</style>
