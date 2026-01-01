import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { UserInfo, TenantInfo, KnowledgeBaseInfo } from '@/api/auth'
import type { TenantInfo as TenantInfoFromAPI } from '@/api/tenant'
import i18n from '@/i18n'

export const useAuthStore = defineStore('auth', () => {
  // 상태
  const user = ref<UserInfo | null>(null)
  const tenant = ref<TenantInfo | null>(null)
  const token = ref<string>('')
  const refreshToken = ref<string>('')
  const knowledgeBases = ref<KnowledgeBaseInfo[]>([])
  const currentKnowledgeBase = ref<KnowledgeBaseInfo | null>(null)
  const selectedTenantId = ref<number | null>(null)
  const selectedTenantName = ref<string | null>(null)
  const allTenants = ref<TenantInfoFromAPI[]>([])

  // 계산된 속성
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
    // 다른 테넌트를 선택한 경우 선택한 테넌트 ID를 사용하고, 그렇지 않으면 사용자의 기본 테넌트 ID를 사용합니다.
    return selectedTenantId.value || (tenant.value?.id ? Number(tenant.value.id) : null)
  })

  // 작업 방법
  const setUser = (userData: UserInfo) => {
    user.value = userData
    // localStorage에 저장
    localStorage.setItem('weknora_user', JSON.stringify(userData))
  }

  const setTenant = (tenantData: TenantInfo) => {
    tenant.value = tenantData
    // localStorage에 저장
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
    // 입력이 배열인지 확인
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


  const logout = () => {
    // 상태 초기화
    user.value = null
    tenant.value = null
    token.value = ''
    refreshToken.value = ''
    knowledgeBases.value = []
    currentKnowledgeBase.value = null
    selectedTenantId.value = null
    selectedTenantName.value = null
    allTenants.value = []

    // localStorage 지우기
    localStorage.removeItem('weknora_user')
    localStorage.removeItem('weknora_tenant')
    localStorage.removeItem('weknora_token')
    localStorage.removeItem('weknora_refresh_token')
    localStorage.removeItem('weknora_knowledge_bases')
    localStorage.removeItem('weknora_current_kb')
    localStorage.removeItem('weknora_selected_tenant_id')
    localStorage.removeItem('weknora_selected_tenant_name')

  }

  const initFromStorage = () => {
    // localStorage에서 상태 복원
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
  }

  // 초기화 시 localStorage에서 상태 복원
  initFromStorage()

  return {
    // 상태
    user,
    tenant,
    token,
    refreshToken,
    knowledgeBases,
    currentKnowledgeBase,
    selectedTenantId,
    selectedTenantName,
    allTenants,
    
    // 계산된 속성
    isLoggedIn,
    hasValidTenant,
    currentTenantId,
    currentUserId,
    canAccessAllTenants,
    effectiveTenantId,
    
    // 방법
    setUser,
    setTenant,
    setToken,
    setRefreshToken,
    setKnowledgeBases,
    setCurrentKnowledgeBase,
    setSelectedTenant,
    setAllTenants,
    getSelectedTenant,
    logout,
    initFromStorage
  }
})
