import { reactive, ref, computed, watch } from 'vue'
import { defineStore } from 'pinia'
import i18n from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import type { DeploymentCapabilityKey } from '@/config/deploymentCapabilities'

type MenuChild = Record<string, any>
type MenuMinRole = 'viewer' | 'contributor' | 'admin' | 'owner'

interface MenuItem {
  title: string
  titleKey?: string
  icon: string
  path: string
  childrenPath?: string
  children?: MenuChild[]
  /** 侧栏可见的最低角色；未设置则始终可见（如新对话/知识库） */
  minRole?: MenuMinRole
  requiredCapability?: DeploymentCapabilityKey
}

const createMenuChildren = () => reactive<MenuChild[]>([])

export const useMenuStore = defineStore('menuStore', () => {
  const menuArr = reactive<MenuItem[]>([
    {
      title: '',
      titleKey: 'menu.newChat',
      icon: 'prefixIcon',
      path: 'creatChat',
      childrenPath: 'chat',
      children: createMenuChildren()
    },
    { title: '', titleKey: 'menu.knowledgeBase', icon: 'zhishiku', path: 'knowledge-bases' },
    {
      title: '',
      titleKey: 'menu.mcp',
      icon: 'mcp',
      path: 'mcp',
      // MCP 管理：仅管理员/所有者可见；编辑(contributor)/访客(viewer)不露入口。
      minRole: 'admin',
      requiredCapability: 'settings.mcp',
    },
    {
      title: '',
      titleKey: 'menu.agents',
      icon: 'agent',
      path: 'agents',
      requiredCapability: 'agents',
    },
    {
      title: '',
      titleKey: 'menu.members',
      icon: 'members',
      path: 'members',
      // 成员管理：仅管理员/所有者可见；编辑(contributor)/访客(viewer)不露入口。
      minRole: 'admin',
    },
    {
      title: '',
      titleKey: 'menu.orgUnits',
      icon: 'organization',
      path: 'org-units',
      // 组织层级：仅管理员/所有者可见；编辑/访客不露入口。
      minRole: 'admin',
    },
    {
      title: '',
      titleKey: 'menu.dataCharts',
      icon: 'chart',
      path: 'data-charts',
      // 数据图表：仅管理员/所有者可见。
      minRole: 'admin',
    },
    {
      title: '',
      titleKey: 'menu.organizations',
      icon: 'organization',
      path: 'organizations',
      requiredCapability: 'organizations',
    },
    { title: '', titleKey: 'menu.settings', icon: 'setting', path: 'settings' },
    { title: '', titleKey: 'menu.logout', icon: 'logout', path: 'logout' }
  ])

  /** 侧栏「新对话」入口：与 menu.vue SHOW_SIDEBAR_CHAT 同步隐藏 */
  const SHOW_SIDEBAR_NEW_CHAT = false

  const isFirstSession = ref(false)
  const firstQuery = ref('')
  const firstMentionedItems = ref<any[]>([])
  const firstModelId = ref('')
  const firstImageFiles = ref<any[]>([])
  const firstAttachmentFiles = ref<any[]>([])
  const prefillQuery = ref('')

  const applyMenuTranslations = () => {
    menuArr.forEach(item => {
      if (item.titleKey) {
        item.title = i18n.global.t(item.titleKey)
      }
    })
  }

  applyMenuTranslations()

  watch(
    () => i18n.global.locale.value,
    () => {
      applyMenuTranslations()
    }
  )

  const liteHiddenPaths = new Set(['logout', 'organizations'])

  const canSeeMenuItem = (item: MenuItem): boolean => {
    if (item.path === 'creatChat' && !SHOW_SIDEBAR_NEW_CHAT) return false
    const authStore = useAuthStore()
    const deploymentCapabilities = useDeploymentCapabilitiesStore()
    // 单空间 + 组织树为主路径：上下级用「共享给下级」，不走跨 Tenant 的
    // Organization。侧栏再露「共享空间」会与「组织层级」混淆，故一律隐藏；
    // 路由/API 仍保留，供日后跨客户协作深链使用。
    if (item.path === 'organizations') {
      return false
    }
    if (authStore.isLiteMode && liteHiddenPaths.has(item.path)) {
      return false
    }
    if (!deploymentCapabilities.isSupported(item.requiredCapability)) {
      return false
    }
    if (!item.minRole) return true
    // 与 Settings.vue SECTION_MIN_ROLE 一致：超管 bypass；角色未加载时不露入口，避免闪烁。
    if (authStore.canAccessAllTenants) return true
    if (!authStore.currentTenantRole) return false
    return authStore.hasRole(item.minRole)
  }

  const visibleMenuArr = computed(() => {
    return menuArr.filter((item) => canSeeMenuItem(item))
  })

  const chatMenuIndex = menuArr.findIndex(item => item.path === 'creatChat')

  const clearMenuArr = () => {
    const chatMenu = menuArr[chatMenuIndex]
    if (chatMenu && chatMenu.children) {
      chatMenu.children = createMenuChildren()
    }
  }

  const updatemenuArr = (obj: any) => {
    const chatMenu = menuArr[chatMenuIndex]
    if (!chatMenu.children) {
      chatMenu.children = createMenuChildren()
    }
    const exists = chatMenu.children.some((item: MenuChild) => item.id === obj.id)
    if (!exists) {
      chatMenu.children.push(obj)
    }
  }

  const updataMenuChildren = (item: MenuChild) => {
    const chatMenu = menuArr[chatMenuIndex]
    if (!chatMenu.children) {
      chatMenu.children = createMenuChildren()
    }
    chatMenu.children.unshift(item)
  }

  const updatasessionTitle = (sessionId: string, title: string) => {
    const chatMenu = menuArr[chatMenuIndex]
    chatMenu.children?.forEach((item: MenuChild) => {
      if (item.id === sessionId) {
        item.title = title
        item.isNoTitle = false
      }
    })
  }

  const changeIsFirstSession = (payload: boolean) => {
    isFirstSession.value = payload
  }

  const changeFirstQuery = (payload: string, mentionedItems: any[] = [], modelId: string = '', imageFiles: any[] = [], attachmentFiles: any[] = []) => {
    firstQuery.value = payload
    firstMentionedItems.value = mentionedItems
    firstModelId.value = modelId
    firstImageFiles.value = imageFiles
    firstAttachmentFiles.value = attachmentFiles
  }

  const setPrefillQuery = (q: string) => {
    prefillQuery.value = q
  }

  const consumePrefillQuery = () => {
    const q = prefillQuery.value
    prefillQuery.value = ''
    return q
  }

  return {
    menuArr,
    visibleMenuArr,
    isFirstSession,
    firstQuery,
    firstMentionedItems,
    firstModelId,
    firstImageFiles,
    firstAttachmentFiles,
    prefillQuery,
    clearMenuArr,
    updatemenuArr,
    updataMenuChildren,
    updatasessionTitle,
    changeIsFirstSession,
    changeFirstQuery,
    setPrefillQuery,
    consumePrefillQuery
  }
})
