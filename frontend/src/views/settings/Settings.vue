<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="settings-overlay">
        <div class="settings-modal">
          <!-- 关闭按钮 -->
          <button class="close-btn" @click="handleClose" :aria-label="$t('general.close')">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
            </svg>
          </button>

          <div class="settings-container">
            <!-- 左侧导航 -->
            <div class="settings-sidebar">
              <div class="sidebar-header">
                <h2 class="sidebar-title">{{ $t('general.settings') }}</h2>
              </div>
              <div class="settings-nav">
                <template v-for="group in navGroups" :key="group.key">
                  <div class="nav-group-title">{{ group.label }}</div>
                  <template v-for="item in group.items" :key="item.key">
                    <div 
                      :class="['nav-item', { 
                        'active': currentSection === item.key,
                        'has-submenu': item.children && item.children.length > 0,
                        'expanded': expandedMenus.includes(item.key)
                      }]"
                      @click="handleNavClick(item)"
                    >
                      <!-- 网络搜索使用自定义 SVG 图标 -->
                      <svg
                        v-if="item.key === 'websearch'"
                        width="18"
                        height="18"
                        viewBox="0 0 18 18"
                        fill="none"
                        xmlns="http://www.w3.org/2000/svg"
                        class="nav-icon"
                      >
                        <circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.2" fill="none"/>
                        <path d="M 9 2 A 3.5 7 0 0 0 9 16" stroke="currentColor" stroke-width="1.2" fill="none"/>
                        <path d="M 9 2 A 3.5 7 0 0 1 9 16" stroke="currentColor" stroke-width="1.2" fill="none"/>
                        <line x1="2.94" y1="5.5" x2="15.06" y2="5.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
                        <line x1="2.94" y1="12.5" x2="15.06" y2="12.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
                      </svg>
                      <!-- WeKnora Cloud 使用自定义 W 图标 -->
                      <svg
                        v-else-if="item.key === 'weknoracloud'"
                        width="18"
                        height="18"
                        viewBox="0 0 18 18"
                        fill="none"
                        xmlns="http://www.w3.org/2000/svg"
                        class="nav-icon"
                      >
                        <rect x="1.5" y="1.5" width="15" height="15" rx="3.5" stroke="currentColor" stroke-width="1.2" fill="none"/>
                        <path d="M4.5 5.5L6.5 12.5L9 7.5L11.5 12.5L13.5 5.5" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" fill="none"/>
                      </svg>
                      <t-icon v-else :name="item.icon" class="nav-icon" />
                      <span class="nav-label">{{ item.label }}</span>
                      <t-icon 
                        v-if="item.children && item.children.length > 0"
                        :name="expandedMenus.includes(item.key) ? 'chevron-down' : 'chevron-right'"
                        class="expand-icon"
                      />
                    </div>

                    <!-- 子菜单 -->
                    <Transition name="submenu">
                      <div 
                        v-if="item.children && expandedMenus.includes(item.key)" 
                        class="submenu"
                      >
                        <div
                          v-for="(child, childIndex) in item.children"
                          :key="childIndex"
                          :class="['submenu-item', { 'active': currentSubSection === child.key }]"
                          @click.stop="handleSubMenuClick(item.key, child.key)"
                        >
                          <span class="submenu-label">{{ child.label }}</span>
                        </div>
                      </div>
                    </Transition>
                  </template>
                </template>
              </div>
            </div>

            <!-- 右侧内容区域 -->
            <div class="settings-content">
              <div class="content-wrapper">
                <!-- 角色不允许访问当前 section（deep-link 进来 / 跨租户切换后角色降级）—— 优先于具体 section 渲染。
                     正常导航走 navItems filter 不会到这里，但 watch(navItems) 的 fallback 会在角色降级
                     的瞬间触发；这一段做兜底兼容旧 URL。 -->
                <div v-if="!canSeeSection(currentSection)" class="section role-denied">
                  <div class="role-denied-icon">
                    <t-icon name="lock-on" size="48px" />
                  </div>
                  <div class="role-denied-title">{{ $t('settings.roleDenied.title') }}</div>
                  <div class="role-denied-desc">{{ $t('settings.roleDenied.desc') }}</div>
                </div>
                <template v-else>
                <!-- 常规设置 -->
                <div v-if="currentSection === 'general'" class="section">
                  <GeneralSettings />
                </div>

                <!-- Ollama 设置 -->
                <div v-if="currentSection === 'ollama'" class="section">
                  <OllamaSettings />
                </div>

                <!-- WeKnora Cloud -->
                <div v-if="currentSection === 'weknoracloud'" class="section">
                  <WeKnoraCloudSettings />
                </div>

                <!-- 模型配置 -->
                <div v-if="currentSection === 'models'" class="section">
                  <ModelSettings />
                </div>

                <!-- 网络搜索配置 -->
                <div v-if="currentSection === 'websearch'" class="section">
                  <WebSearchSettings />
                </div>

                <!-- 消息管理 -->
                <div v-if="currentSection === 'chathistory'" class="section">
                  <ChatHistorySettings />
                </div>

                <!-- 向量数据库引擎 -->
                <div v-if="currentSection === 'vectorstore'" class="section">
                  <VectorStoreSettings />
                </div>

                <!-- 解析引擎 -->
                <div v-if="currentSection === 'parser'" class="section">
                  <ParserEngineSettings />
                </div>

                <!-- 存储引擎 -->
                <div v-if="currentSection === 'storage'" class="section">
                  <StorageEngineSettings />
                </div>

                <!-- 系统信息 -->
                <div v-if="currentSection === 'system'" class="section">
                  <SystemInfo />
                </div>

                <!-- 用户信息（账户基础信息：ID / 用户名 / 邮箱 / 注册时间）。
                     从 ApiInfo.vue 拆出来，原页面挂的是 owner-only 入口，
                     用户的基本信息不该跟 owner 权限绑定。 -->
                <div v-if="currentSection === 'userprofile'" class="section">
                  <UserProfile />
                </div>

                <!-- 租户信息 -->
                <div v-if="currentSection === 'tenant'" class="section">
                  <TenantInfo />
                </div>

                <!-- 成员管理 (#1303 PR 3) -->
                <div v-if="currentSection === 'members'" class="section">
                  <TenantMembers />
                </div>

                <!-- API 信息 -->
                <div v-if="currentSection === 'api'" class="section">
                  <ApiInfo />
                </div>

                <!-- MCP 服务 -->
                <div v-if="currentSection === 'mcp'" class="section">
                  <McpSettings />
                </div>
                </template>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUIStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { useI18n } from 'vue-i18n'
import SystemInfo from './SystemInfo.vue'
import TenantInfo from './TenantInfo.vue'
import ApiInfo from './ApiInfo.vue'
import UserProfile from './UserProfile.vue'
import GeneralSettings from './GeneralSettings.vue'
import ModelSettings from './ModelSettings.vue'
import OllamaSettings from './OllamaSettings.vue'
import McpSettings from './McpSettings.vue'
import WebSearchSettings from './WebSearchSettings.vue'
import ChatHistorySettings from './ChatHistorySettings.vue'
import VectorStoreSettings from './VectorStoreSettings.vue'
import ParserEngineSettings from './ParserEngineSettings.vue'
import StorageEngineSettings from './StorageEngineSettings.vue'
import WeKnoraCloudSettings from './WeKnoraCloudSettings.vue'
import TenantMembers from './TenantMembers.vue'

const route = useRoute()
const router = useRouter()
const uiStore = useUIStore()
const authStore = useAuthStore()
const { t } = useI18n()

const currentSection = ref<string>('general')
const currentSubSection = ref<string>('')
const expandedMenus = ref<string[]>([])

type NavItem = {
  key: string
  icon: string
  label: string
  children?: Array<{ key: string; label: string }>
}

type NavGroup = {
  key: string
  label: string
  items: NavItem[]
}

// 设置二级导航的最低可见角色：和 internal/router/router.go 的守卫矩阵对齐。
// 以「页面里至少有 1 个有意义的写操作所要求的最低角色」为基准，把基础设
// 施配置（models 写、ollama 下载、websearch 写、parser/storage/vector/mcp
// CRUD、chat-history 配置）统一收到 admin；只读类（general / system info /
// tenant-info / members 名册）保留 viewer 可见；最高敏感的 reset api
// key 是 owner-only。改这张表前请在 router.go 里复核对应路由组。
//
// 特别说明：
// - chathistory 页面唯一的「启用消息索引」开关 PUT /tenants/kv/chat-history-config
//   后端走 g.Admin()。给 viewer/contributor 看到入口、点开开关、保存时
//   403，体验很差，所以入口本身归 admin。
// - models 列表 viewer 可读，页面内的「+ 添加模型 / 编辑 / 删除」按钮在
//   ModelSettings.vue 里另用 hasRole('admin') 自己 gate，所以入口保留
//   viewer 是合理的（contributor 也能浏览模型列表）。
type RoleKey = 'viewer' | 'contributor' | 'admin' | 'owner'
const SECTION_MIN_ROLE: Record<string, RoleKey> = {
  general: 'viewer',
  ollama: 'admin',
  weknoracloud: 'admin',
  models: 'viewer',
  websearch: 'admin',
  chathistory: 'admin',
  vectorstore: 'admin',
  parser: 'admin',
  storage: 'admin',
  mcp: 'admin',
  system: 'viewer',
  userprofile: 'viewer',
  tenant: 'viewer',
  members: 'viewer',
  api: 'owner',
}

const canSeeSection = (key: string): boolean => {
  const min = SECTION_MIN_ROLE[key] ?? 'viewer'
  // canAccessAllTenants（superuser）和路由层一样必须 bypass，否则 cross-tenant
  // 管理员看不到自己有权操作的入口（参考 TenantMembers.vue 的 canManage）。
  if (authStore.canAccessAllTenants) return true
  return authStore.hasRole(min)
}

const navItems = computed(() => {
  // 一律走 SECTION_MIN_ROLE 表，避免 ad-hoc isAdmin/isOwner 散落在多处。
  // 服务端在每条路由上仍以 g.Viewer/Admin/Owner 为准，这里只决定 UI 是
  // 否露入口；改动入口规则请同步更新 SECTION_MIN_ROLE 注释里的对照路由。
  const all: NavItem[] = [
    { key: 'general', icon: 'setting', label: t('general.title') },
    { key: 'ollama', icon: 'server', label: 'Ollama' },
    { key: 'weknoracloud', icon: '', label: 'WeKnora Cloud' },
    { key: 'models', icon: 'control-platform', label: t('settings.modelManagement') },
    { key: 'websearch', icon: 'search', label: t('settings.webSearchConfig') },
    { key: 'chathistory', icon: 'chat', label: t('chatHistorySettings.title') },
    { key: 'vectorstore', icon: 'data-base', label: t('settings.vectorStoreEngine') },
    { key: 'parser', icon: 'file-search', label: t('settings.parserEngine') },
    { key: 'storage', icon: 'cloud', label: t('settings.storageEngine') },
    { key: 'mcp', icon: 'tools', label: t('settings.mcpService') },
    { key: 'system', icon: 'info-circle', label: t('settings.systemSettings') },
    { key: 'userprofile', icon: 'user', label: t('userProfile.title') },
    { key: 'tenant', icon: 'user-circle', label: t('settings.tenantInfo') },
    { key: 'members', icon: 'usergroup', label: t('tenantMember.title') },
    { key: 'api', icon: 'secured', label: t('settings.apiInfo') },
  ]
  // currentTenantRole 为空表示「membership 还没加载」—— 比起渲染整套
  // viewer 入口然后角色一返回又消失，先卡住不渲染更稳，跟原先 members
  // 入口的策略一致。
  if (!authStore.currentTenantRole && !authStore.canAccessAllTenants) {
    return [] as NavItem[]
  }
  return all.filter((it) => canSeeSection(it.key))
})

const navGroups = computed<NavGroup[]>(() => {
  const itemMap = new Map(navItems.value.map((item) => [item.key, item]))
  const pickItems = (keys: string[]) => keys.map((key) => itemMap.get(key)).filter(Boolean) as NavItem[]
  return [
    { key: 'basic', label: t('settings.navGroups.basic'), items: pickItems(['general']) },
    { key: 'ai', label: t('settings.navGroups.ai'), items: pickItems(['models', 'ollama', 'weknoracloud', 'websearch', 'mcp']) },
    { key: 'data', label: t('settings.navGroups.data'), items: pickItems(['vectorstore', 'parser', 'storage']) },
    { key: 'workspace', label: t('settings.navGroups.workspace'), items: pickItems(['userprofile', 'tenant', 'members']) },
    { key: 'system', label: t('settings.navGroups.system'), items: pickItems(['chathistory', 'system', 'api']) },
  ].filter((group) => group.items.length > 0)
})

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
  
  // 切换到对应页面
  currentSection.value = item.key
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
  uiStore.closeSettings()
  // 如果当前路由是设置页，返回上一页
  if (route.path === '/platform/settings') {
    router.back()
  }
}

// 监听初始导航设置
watch(() => uiStore.settingsInitialSection, (section) => {
  if (section && visible.value) {
    currentSection.value = section
    const navItem = (navItems.value as any[]).find((item) => item.key === section)
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

// 切换租户后角色可能变化，原本可见的 admin-only 面板可能消失。
// 如果 currentSection 落到了不再显示的 key 上，就回退到第一个可见项。
watch(navItems, (items) => {
  if (!items.some((item) => item.key === currentSection.value)) {
    currentSection.value = items[0]?.key || 'general'
    currentSubSection.value = ''
  }
})

// ESC 键关闭
const handleEscape = (e: KeyboardEvent) => {
  if (e.key === 'Escape' && visible.value) {
    handleClose()
  }
}

// 处理快捷导航事件
const handleSettingsNav = (e: CustomEvent) => {
  const { section, subsection } = e.detail
  if (section) {
    currentSection.value = section
    // 如果有子菜单，自动展开
    const navItem = (navItems.value as any[]).find((item: any) => item.key === section)
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
  window.addEventListener('keydown', handleEscape)
  window.addEventListener('settings-nav', handleSettingsNav as EventListener)
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleEscape)
  window.removeEventListener('settings-nav', handleSettingsNav as EventListener)
})
</script>

<style lang="less" scoped>
/* 遮罩层 */
.settings-overlay {
  position: fixed;
  inset: 0;
  z-index: 1100;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  backdrop-filter: blur(4px);
}

/* 弹窗容器 */
.settings-modal {
  position: relative;
  width: 100%;
  max-width: 900px;
  height: 700px;
  background: var(--td-bg-color-container);
  border-radius: 12px;
  box-shadow: 0 6px 28px rgba(15, 23, 42, 0.08);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

/* 关闭按钮 */
.close-btn {
  position: absolute;
  top: 16px;
  right: 16px;
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;
  z-index: 10;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.settings-container {
  display: flex;
  height: 100%;
  width: 100%;
  overflow: hidden;
}

/* 左侧导航栏 */
.settings-sidebar {
  width: 220px;
  background-color: var(--td-bg-color-settings-modal);
  border-right: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
}

.sidebar-header {
  padding: 24px 16px 16px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.sidebar-title {
  font-size: 18px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0;
}

.settings-nav {
  padding: 12px 8px 16px;
  flex: 1;
}

.nav-group-title {
  padding: 12px 16px 6px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

.nav-item {
  display: flex;
  align-items: center;
  padding: 8px 16px;
  margin-bottom: 2px;
  border-radius: 6px;
  cursor: pointer;
  color: var(--td-text-color-secondary);
  font-size: 14px;
  transition: all 0.2s ease;
  user-select: none;

  &:hover {
    background-color: var(--td-bg-color-secondarycontainer-hover);
    color: var(--td-text-color-primary);
  }

  &.active {
    background-color: rgba(7, 192, 95, 0.1);
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.nav-icon {
  margin-right: 12px;
  font-size: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: inherit;
}

.nav-label {
  flex: 1;
}

.expand-icon {
  margin-left: 4px;
  font-size: 14px;
  transition: transform 0.2s ease;
}

/* 子菜单 */
.submenu {
  margin-left: 32px;
  margin-bottom: 4px;
  overflow: hidden;
}

.submenu-item {
  padding: 8px 16px;
  margin-bottom: 2px;
  border-radius: 4px;
  cursor: pointer;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  transition: all 0.2s ease;
  user-select: none;

  &:hover {
    background-color: var(--td-bg-color-secondarycontainer-hover);
    color: var(--td-text-color-primary);
  }

  &.active {
    background-color: rgba(7, 192, 95, 0.08);
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
  transition: all 0.2s ease;
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
.settings-content {
  flex: 1;
  overflow-y: auto;
  background-color: var(--td-bg-color-container);
}

.content-wrapper {
  max-width: 600px;
  padding: 40px 48px;
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
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;
}

.modal-enter-active .settings-modal,
.modal-leave-active .settings-modal {
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-from .settings-modal,
.modal-leave-to .settings-modal {
  transform: scale(0.95);
  opacity: 0;
}

/* 滚动条样式 */
.settings-sidebar::-webkit-scrollbar,
.settings-content::-webkit-scrollbar {
  width: 6px;
}

.settings-sidebar::-webkit-scrollbar-track {
  background: var(--td-bg-color-secondarycontainer);
}

.settings-sidebar::-webkit-scrollbar-thumb {
  background: var(--td-gray-color-5);
  border-radius: 3px;
}

.settings-sidebar::-webkit-scrollbar-thumb:hover {
  background: var(--td-gray-color-6);
}

.settings-content::-webkit-scrollbar-track {
  background: var(--td-bg-color-container);
}

.settings-content::-webkit-scrollbar-thumb {
  background: var(--td-gray-color-5);
  border-radius: 3px;
}

.settings-content::-webkit-scrollbar-thumb:hover {
  background: var(--td-gray-color-6);
}

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
    font-size: 16px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }
  .role-denied-desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    max-width: 360px;
    line-height: 1.6;
  }
}
</style>

