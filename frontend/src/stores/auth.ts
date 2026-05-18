import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { UserInfo, TenantInfo, KnowledgeBaseInfo } from '@/api/auth'
import type { TenantInfo as TenantInfoFromAPI } from '@/api/tenant'
import i18n from '@/i18n'
import { reloadFontFromStorage } from '@/composables/useFont'
import { reloadThemeFromStorage } from '@/composables/useTheme'
import { resetMigrationLatch } from '@/composables/preferenceStorage'
import { BUILTIN_QUICK_ANSWER_ID } from '@/api/agent'

// Per-user UI preferences are namespaced by user id in localStorage.
// Reload them whenever the active user changes.
function reloadUserPreferences() {
  // Reset the latch so migration runs once for the new active user.
  resetMigrationLatch()
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
  // memberships lists every tenant the user can authenticate into,
  // along with their role in each. Populated from /auth/login response.
  // v1 deployments will typically have length 1; the field is wired now
  // so PR 3 can render a tenant-switcher UI without a store migration.
  const memberships = ref<Array<{ tenant_id: number; tenant_name?: string; role: string }>>([])
  const isLiteMode = ref(false)
  // pendingInvitationCount is the number of pending tenant invitations
  // addressed to the current user. Renders as a badge next to the
  // avatar; updated by fetchPendingInvitationCount, which runs after
  // login, after tenant switch, and on a polling interval. Polling
  // (vs SSE) is fine — the count is checked rarely and a 1-2 minute
  // staleness window is acceptable for an inbox indicator.
  const pendingInvitationCount = ref<number>(0)

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

  // currentTenantName resolves the active tenant's display name across the
  // app (sidebar, breadcrumbs, headers). It MUST follow the same active-
  // tenant rule as currentTenantRole / effectiveTenantId: the user's
  // selected override takes precedence over their home tenant. Reading
  // tenant.value.name alone would render the home tenant's name even after
  // the user switched, which is the bug TenantSelector already worked
  // around inline; this computed centralises that fallback ladder.
  const currentTenantName = computed(() => {
    const tid =
      selectedTenantId.value !== null && selectedTenantId.value !== undefined
        ? String(selectedTenantId.value)
        : tenant.value?.id
        ? String(tenant.value.id)
        : ''
    if (!tid) return ''
    if (selectedTenantId.value && selectedTenantName.value) {
      return selectedTenantName.value
    }
    const fromMembership = memberships.value.find((m) => String(m.tenant_id) === tid)
    if (fromMembership?.tenant_name) return fromMembership.tenant_name
    if (tenant.value && String(tenant.value.id) === tid) return tenant.value.name || ''
    return ''
  })

  const currentUserId = computed(() => {
    return user.value?.id || ''
  })

  const canAccessAllTenants = computed(() => {
    return user.value?.can_access_all_tenants || false
  })

  // currentTenantRole returns the user's role in the active tenant
  // (defaulting to '' when memberships have not been loaded). Used by
  // role-aware UI gating; PR 2 wires backend enforcement, PR 3 uses
  // this for menu/button visibility.
  //
  // It MUST read effectiveTenantId, not tenant.value.id. tenant.value is
  // the user's home tenant (set once on login from /auth/me's tenant
  // field); selectedTenantId is the active override applied by the
  // tenant switcher. Reading the wrong field used to leak Owner-level
  // UI to a Viewer who switched into a tenant where they were a Viewer
  // — every gate above (`hasRole(...)`) returned the home-tenant role.
  //
  // SECURITY: This value is read from localStorage (`weknora_memberships`)
  // and therefore MUST be treated as UI-rendering-only — any user can
  // tamper with localStorage and grant themselves "owner" here. All real
  // authorisation decisions live on the server (auth middleware resolves
  // the role from tenant_members on every request). Never branch
  // security-sensitive logic on this value alone.
  const currentTenantRole = computed(() => {
    const tid =
      selectedTenantId.value !== null && selectedTenantId.value !== undefined
        ? String(selectedTenantId.value)
        : tenant.value?.id
        ? String(tenant.value.id)
        : ''
    if (!tid) return ''
    const match = memberships.value.find((m) => String(m.tenant_id) === tid)
    return match?.role || ''
  })

  // hasRole answers "is the current tenant role at least <min>?", used by
  // role-aware UI gating across KB / Agent / settings views. The numeric
  // ordering (viewer < contributor < admin < owner) mirrors the server-side
  // matrix in middleware/rbac.go so a v-if here lines up with the 403 the
  // backend would return.
  //
  // SECURITY: shares the same caveat as currentTenantRole — derived from
  // localStorage, MUST be treated as UI-rendering-only. Never rely on
  // hasRole() for security decisions; the server is the source of truth.
  const ROLE_LEVEL: Record<string, number> = {
    viewer: 10,
    contributor: 20,
    admin: 30,
    owner: 40,
  }
  const hasRole = (min: 'viewer' | 'contributor' | 'admin' | 'owner'): boolean => {
    return (ROLE_LEVEL[currentTenantRole.value] ?? 0) >= ROLE_LEVEL[min]
  }

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
    // 把后端持久化的 user 偏好（记忆开关等）同步到 settings store。
    // 用 import 而不是顶部 import 避免 stores 间的循环依赖：auth ↔ settings。
    // settings store 只把它当作"本地状态 + localStorage"更新，不会再原路 PUT 回去。
    if (userData.preferences) {
      import('@/stores/settings').then(({ useSettingsStore }) => {
        useSettingsStore().hydrateFromUserPreferences(userData.preferences)
      }).catch(() => {
        // 加载 settings store 失败不影响 setUser 主流程；下次 setUser
        // 触发时还会再次尝试同步。
      })
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

  // Wipe chat / KB selections that were saved under the previous tenant.
  // These keys are NOT tenant-scoped in storage; after a tenant switch they
  // would otherwise be reloaded verbatim and the chat input would post under
  // the new tenant with an Agent / model id that only existed in the old
  // tenant — backend 403s or "model not found". Called from setSelectedTenant
  // only on an actual tenant change, so logout / init paths are not touched.
  const clearTenantScopedClientState = () => {
    try {
      localStorage.removeItem('weknora_last_chat_model_id')
      localStorage.removeItem('weknora_current_kb')
      const raw = localStorage.getItem('WeKnora_settings')
      if (raw) {
        const parsed = JSON.parse(raw)
        if (parsed && typeof parsed === 'object') {
          parsed.selectedAgentId = BUILTIN_QUICK_ANSWER_ID
          parsed.selectedAgentSourceTenantId = null
          if (parsed.conversationModels && typeof parsed.conversationModels === 'object') {
            parsed.conversationModels.summaryModelId = ''
            parsed.conversationModels.rerankModelId = ''
            parsed.conversationModels.selectedChatModelId = ''
          }
          parsed.selectedKnowledgeBases = []
          parsed.selectedFiles = []
          parsed.selectedFileKbMap = {}
          parsed.knowledgeBaseId = ''
          localStorage.setItem('WeKnora_settings', JSON.stringify(parsed))
        }
      }
    } catch (e) {
      // localStorage may be disabled or contain malformed JSON — best effort.
      console.warn('[auth] failed to clear tenant-scoped client state', e)
    }
  }

  const setSelectedTenant = (tenantId: number | null, tenantName: string | null = null) => {
    const previousTenantId = selectedTenantId.value
    const tenantChanged = previousTenantId !== tenantId
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
    if (tenantChanged) {
      clearTenantScopedClientState()
    }
  }

  const setAllTenants = (tenants: TenantInfoFromAPI[]) => {
    allTenants.value = tenants
  }

  const setMemberships = (
    list: Array<{ tenant_id: number; tenant_name?: string; role: string }>
  ) => {
    memberships.value = Array.isArray(list) ? list : []
    localStorage.setItem('weknora_memberships', JSON.stringify(memberships.value))
  }

  // setPendingInvitationCount is the explicit setter used by the
  // polling composable below. Keeping the mutation behind a setter
  // matches the pattern of every other piece of auth state and lets
  // future devtools / strict-mode checks intercept the write.
  const setPendingInvitationCount = (n: number) => {
    pendingInvitationCount.value = Number.isFinite(n) && n >= 0 ? Math.floor(n) : 0
  }

  // fetchPendingInvitationCount hits the dedicated /me/invitations/
  // pending-count endpoint and updates the store. Errors are
  // swallowed — the badge degrades to its last-known value instead
  // of nagging the user with a toast. The caller can choose to fire
  // and forget (login flow) or await (manual refresh button).
  const fetchPendingInvitationCount = async () => {
    // Import lazily to avoid circular module ordering between auth
    // store, axios interceptor, and i18n bootstrapping. The store is
    // imported by very early modules in main.ts; a top-level api
    // import would tighten that graph unnecessarily.
    try {
      const { getMyPendingInvitationCount } = await import('@/api/tenant/invitations')
      const resp = await getMyPendingInvitationCount()
      if (resp.success && resp.data) {
        setPendingInvitationCount(resp.data.pending_count)
      }
    } catch {
      // best-effort; keep last known value
    }
  }

  // Reconcile user / home tenant / memberships with GET /api/v1/auth/me.
  // Login only populated memberships once; SPA navigations skip
  // hydrateSessionFromToken when isLoggedIn is already true — so inviting
  // flows or revokes would leave the sidebar switcher stale until reload.
  const refreshFromAuthMe = async (): Promise<boolean> => {
    try {
      const { getCurrentUser } = await import('@/api/auth')
      const response = await getCurrentUser()
      const u = response.data?.user
      if (!response.success || !u) return false

      setUser({
        id: u.id || '',
        username: u.username || '',
        email: u.email || '',
        avatar: u.avatar,
        tenant_id: String(u.tenant_id || response.data?.tenant?.id || ''),
        can_access_all_tenants: u.can_access_all_tenants || false,
        preferences: u.preferences,
        created_at: u.created_at || new Date().toISOString(),
        updated_at: u.updated_at || new Date().toISOString(),
      })

      const tenantSnapshot = response.data?.tenant
      if (tenantSnapshot) {
        setTenant({
          id: String(tenantSnapshot.id) || '',
          name: tenantSnapshot.name || '',
          api_key: tenantSnapshot.api_key || '',
          owner_id: tenantSnapshot.owner_id || u.id || '',
          description: tenantSnapshot.description,
          status: tenantSnapshot.status,
          business: tenantSnapshot.business,
          storage_quota: tenantSnapshot.storage_quota,
          storage_used: tenantSnapshot.storage_used,
          created_at: tenantSnapshot.created_at || new Date().toISOString(),
          updated_at: tenantSnapshot.updated_at || new Date().toISOString(),
        })
      }

      const list = response.data?.memberships
      if (Array.isArray(list)) {
        setMemberships(list)
      }

      return true
    } catch {
      return false
    }
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
    memberships.value = []
    pendingInvitationCount.value = 0

    // 清空localStorage
    localStorage.removeItem('weknora_user')
    localStorage.removeItem('weknora_tenant')
    localStorage.removeItem('weknora_token')
    localStorage.removeItem('weknora_refresh_token')
    localStorage.removeItem('weknora_knowledge_bases')
    localStorage.removeItem('weknora_current_kb')
    localStorage.removeItem('weknora_selected_tenant_id')
    localStorage.removeItem('weknora_selected_tenant_name')
    localStorage.removeItem('weknora_memberships')
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

    const storedMemberships = localStorage.getItem('weknora_memberships')
    if (storedMemberships) {
      try {
        const parsed = JSON.parse(storedMemberships)
        memberships.value = Array.isArray(parsed) ? parsed : []
      } catch (e) {
        console.error('Failed to parse memberships', e)
        memberships.value = []
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
    memberships,
    pendingInvitationCount,

    // 计算属性
    isLoggedIn,
    hasValidTenant,
    currentTenantId,
    currentTenantName,
    currentUserId,
    canAccessAllTenants,
    currentTenantRole,
    hasRole,
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
    setMemberships,
    setPendingInvitationCount,
    fetchPendingInvitationCount,
    refreshFromAuthMe,
    getSelectedTenant,
    setLiteMode,
    logout,
    initFromStorage
  }
})