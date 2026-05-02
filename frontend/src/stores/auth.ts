import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { UserInfo, TenantInfo, KnowledgeBaseInfo } from '@/api/auth'
import type { TenantInfo as TenantInfoFromAPI } from '@/api/tenant'
import i18n from '@/i18n'
import { reloadFontFromStorage } from '@/composables/useFont'
import { reloadThemeFromStorage } from '@/composables/useTheme'

// Per-user UI preferences are namespaced by user id in localStorage.
// Reload them whenever the active user changes.
function reloadUserPreferences() {
  reloadFontFromStorage()
  reloadThemeFromStorage()
}

export const useAuthStore = defineStore('auth', () => {
  // 状态
  const user = ref<UserInfo | null>(null)
  const tenant = ref<TenantInfo | null>(null)
  const token = ref<string>('')
  const refreshToken = ref<string>('')
  const knowledgeBases = ref<KnowledgeBaseInfo[]>([])
  const currentKnowledgeBase = ref<KnowledgeBaseInfo | null>(null)
  const selectedTenantId = ref<number | null>(null)
  const selectedTenantName = ref<string | null>(null)
  const allTenants = ref<TenantInfoFromAPI[]>([])
  const isLiteMode = ref(false)

  // 计算属性
  const isLoggedIn = computed(() => {
    return !!token.value && !!user.value
  })

  const hasValidTenant = computed(() => {
    return !!tenant.value && !!tenant.value.api_key
  })

  const currentTenantId = computed(() => {
    return tenant.value?.id || ''
  })

  const currentUserId = computed(() => {
    return user.value?.id || ''
  })

  const canAccessAllTenants = computed(() => {
    return user.value?.can_access_all_tenants || false
  })

  const effectiveTenantId = computed(() => {
    // 如果选择了其他租户，使用选择的租户ID，否则使用用户默认租户ID
    return selectedTenantId.value || (tenant.value?.id ? Number(tenant.value.id) : null)
  })

  // 操作方法
  const setUser = (userData: UserInfo) => {
    const previousId = user.value?.id
    user.value = userData
    // 保存到localStorage
    localStorage.setItem('weknora_user', JSON.stringify(userData))
    if (previousId !== userData.id) {
      reloadUserPreferences()
    }
  }

  const setTenant = (tenantData: TenantInfo) => {
    tenant.value = tenantData
    // 保存到localStorage
    localStorage.setItem('weknora_tenant', JSON.stringify(tenantData))
  }

  const setToken = (tokenValue: string) => {
    token.value = tokenValue
    localStorage.setItem('weknora_token', tokenValue)
  }

  const setRefreshToken = (refreshTokenValue: string) => {
    refreshToken.value = refreshTokenValue
    localStorage.setItem('weknora_refresh_token', refreshTokenValue)
  }

  const setKnowledgeBases = (kbList: KnowledgeBaseInfo[]) => {
    // 确保输入是数组
    knowledgeBases.value = Array.isArray(kbList) ? kbList : []
    localStorage.setItem('weknora_knowledge_bases', JSON.stringify(knowledgeBases.value))
  }

  const setCurrentKnowledgeBase = (kb: KnowledgeBaseInfo | null) => {
    currentKnowledgeBase.value = kb
    if (kb) {
      localStorage.setItem('weknora_current_kb', JSON.stringify(kb))
    } else {
      localStorage.removeItem('weknora_current_kb')
    }
  }

  const setSelectedTenant = (tenantId: number | null, tenantName: string | null = null) => {
    selectedTenantId.value = tenantId
    selectedTenantName.value = tenantName
    if (tenantId !== null) {
      localStorage.setItem('weknora_selected_tenant_id', String(tenantId))
      if (tenantName) {
        localStorage.setItem('weknora_selected_tenant_name', tenantName)
      }
    } else {
      localStorage.removeItem('weknora_selected_tenant_id')
      localStorage.removeItem('weknora_selected_tenant_name')
    }
  }

  const setAllTenants = (tenants: TenantInfoFromAPI[]) => {
    allTenants.value = tenants
  }

  const getSelectedTenant = () => {
    return selectedTenantId.value
  }

  const setLiteMode = (value: boolean) => {
    isLiteMode.value = value
    if (value) {
      localStorage.setItem('weknora_lite_mode', 'true')
    } else {
      localStorage.removeItem('weknora_lite_mode')
    }
  }

  const logout = () => {
    // 清空状态
    user.value = null
    tenant.value = null
    token.value = ''
    refreshToken.value = ''
    knowledgeBases.value = []
    currentKnowledgeBase.value = null
    selectedTenantId.value = null
    selectedTenantName.value = null
    allTenants.value = []

    // 清空localStorage
    localStorage.removeItem('weknora_user')
    localStorage.removeItem('weknora_tenant')
    localStorage.removeItem('weknora_token')
    localStorage.removeItem('weknora_refresh_token')
    localStorage.removeItem('weknora_knowledge_bases')
    localStorage.removeItem('weknora_current_kb')
    localStorage.removeItem('weknora_selected_tenant_id')
    localStorage.removeItem('weknora_selected_tenant_name')
    localStorage.removeItem('weknora_lite_mode')
    isLiteMode.value = false
    try {
      sessionStorage.removeItem('weknora_lite_last_path')
    } catch {
      /* ignore */
    }
    reloadUserPreferences()
  }

  const initFromStorage = () => {
    // 从localStorage恢复状态
    const storedUser = localStorage.getItem('weknora_user')
    const storedTenant = localStorage.getItem('weknora_tenant')
    const storedToken = localStorage.getItem('weknora_token')
    const storedRefreshToken = localStorage.getItem('weknora_refresh_token')
    const storedKnowledgeBases = localStorage.getItem('weknora_knowledge_bases')
    const storedCurrentKb = localStorage.getItem('weknora_current_kb')
    const storedSelectedTenantId = localStorage.getItem('weknora_selected_tenant_id')
    const storedSelectedTenantName = localStorage.getItem('weknora_selected_tenant_name')

    if (storedUser) {
      try {
        user.value = JSON.parse(storedUser)
      } catch (e) {
        console.error(i18n.global.t('authStore.errors.parseUserFailed'), e)
      }
    }

    if (storedTenant) {
      try {
        tenant.value = JSON.parse(storedTenant)
      } catch (e) {
        console.error(i18n.global.t('authStore.errors.parseTenantFailed'), e)
      }
    }

    if (storedToken) {
      token.value = storedToken
    }

    if (storedRefreshToken) {
      refreshToken.value = storedRefreshToken
    }

    if (storedKnowledgeBases) {
      try {
        const parsed = JSON.parse(storedKnowledgeBases)
        knowledgeBases.value = Array.isArray(parsed) ? parsed : []
      } catch (e) {
        console.error(i18n.global.t('authStore.errors.parseKnowledgeBasesFailed'), e)
        knowledgeBases.value = []
      }
    }

    if (storedCurrentKb) {
      try {
        currentKnowledgeBase.value = JSON.parse(storedCurrentKb)
      } catch (e) {
        console.error(i18n.global.t('authStore.errors.parseCurrentKnowledgeBaseFailed'), e)
      }
    }

    if (storedSelectedTenantId) {
      try {
        selectedTenantId.value = Number(storedSelectedTenantId)
        if (storedSelectedTenantName) {
          selectedTenantName.value = storedSelectedTenantName
        }
      } catch (e) {
        console.error('Failed to parse selected tenant ID', e)
        selectedTenantId.value = null
        selectedTenantName.value = null
      }
    }

    isLiteMode.value = localStorage.getItem('weknora_lite_mode') === 'true'
  }

  // 初始化时从localStorage恢复状态
  initFromStorage()

  return {
    // 状态
    user,
    tenant,
    token,
    refreshToken,
    knowledgeBases,
    currentKnowledgeBase,
    selectedTenantId,
    selectedTenantName,
    allTenants,
    
    // 计算属性
    isLoggedIn,
    hasValidTenant,
    currentTenantId,
    currentUserId,
    canAccessAllTenants,
    effectiveTenantId,
    isLiteMode,
    
    // 方法
    setUser,
    setTenant,
    setToken,
    setRefreshToken,
    setKnowledgeBases,
    setCurrentKnowledgeBase,
    setSelectedTenant,
    setAllTenants,
    getSelectedTenant,
    setLiteMode,
    logout,
    initFromStorage
  }
})