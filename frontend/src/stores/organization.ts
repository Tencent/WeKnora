import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type {
  Organization,
  OrganizationMember,
  SharedKnowledgeBase,
  SharedAgentInfo,
  OrganizationPreview,
  ResourceCountsByOrg,
  SearchableOrganizationItem,
  UpdateOrganizationRequest,
  ShareKnowledgeBaseRequest,
  UpdateSharePermissionRequest,
  InviteMemberRequest,
  ReviewJoinRequestRequest,
  RequestRoleUpgradeRequest,
  ApiResponse,
  KnowledgeBaseShare,
  AgentShareResponse
} from '@/api/organization'
import {
  listMyOrganizations,
  createOrganization,
  updateOrganization as updateOrganizationApi,
  deleteOrganization,
  joinOrganization as joinOrganizationApi,
  joinOrganizationById as joinOrganizationByIdApi,
  previewOrganization,
  leaveOrganization,
  generateInviteCode,
  listMembers,
  updateMemberRole,
  removeMember,
  listSharedKnowledgeBases,
  listSharedAgents,
  searchSearchableOrganizations,
  shareKnowledgeBase as shareKnowledgeBaseApi,
  removeShare as removeShareApi,
  updateSharePermission as updateSharePermissionApi,
  shareAgent as shareAgentApi,
  removeAgentShare as removeAgentShareApi,
  inviteMember as inviteMemberApi,
  reviewJoinRequest as reviewJoinRequestApi,
  requestRoleUpgrade as requestRoleUpgradeApi
} from '@/api/organization'
import { createVersionedRequestCoordinator } from './versionedRequest'
import {
  applyOrganizationResourceDelta,
  upsertById,
  mergeById,
  reviewMemberCountDelta
} from './organizationState'

export const useOrganizationStore = defineStore('organization', () => {
  // State
  const organizations = ref<Organization[]>([])
  const currentOrganization = ref<Organization | null>(null)
  const currentMembers = ref<OrganizationMember[]>([])
  const sharedKnowledgeBases = ref<SharedKnowledgeBase[]>([])
  const sharedAgents = ref<SharedAgentInfo[]>([])
  const searchableOrganizations = ref<SearchableOrganizationItem[]>([])
  const previewData = ref<OrganizationPreview | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)
  /** 各空间内知识库/智能体数量（由 GET /organizations 的 resource_counts 填充，供列表侧栏使用） */
  const resourceCounts = ref<ResourceCountsByOrg | null>(null)
  /** 用于去重：同一时刻只允许一次 GET /organizations 请求 */
  let organizationsLoadedAt = 0
  /** 共享资源缓存 TTL，与 chatResources 对齐 */
  const SHARED_RESOURCE_TTL_MS = 60_000
  const SEARCHABLE_ORGANIZATION_TTL_MS = 5 * 60_000
  let sharedKbLoadedAt = 0
  let sharedAgentsLoadedAt = 0
  let searchableOrganizationsQuery = ''
  const searchableOrganizationCache = new Map<
    string,
    { data: SearchableOrganizationItem[]; loadedAt: number }
  >()

  // Computed
  const myOrganizations = computed(() => organizations.value)
  
  const ownedOrganizations = computed(() => 
    organizations.value.filter(org => org.is_owner)
  )

  const joinedOrganizations = computed(() => 
    organizations.value.filter(org => !org.is_owner)
  )

  /** 当前用户作为管理员/创建者可见的待审批加入申请总数（用于侧栏提醒） */
  const totalPendingJoinRequestCount = computed(() =>
    organizations.value.reduce((sum, org) => sum + (org.pending_join_request_count ?? 0), 0)
  )

  // Actions

  /**
   * Fetch all organizations the user belongs to.
   * 去重 + 短期缓存，列表页与侧栏等多处共用。
   */
  const organizationsRequest = createVersionedRequestCoordinator(
    async () => {
      loading.value = true
      error.value = null
      try {
        return await listMyOrganizations()
      } finally {
        loading.value = false
      }
    },
    (response) => {
      if (response.success && response.data) {
        organizations.value = response.data.organizations
        resourceCounts.value = response.data.resource_counts ?? null
        organizationsLoadedAt = Date.now()
      } else {
        resourceCounts.value = null
        error.value = response.message || 'Failed to fetch organizations'
      }
    }
  )

  async function fetchOrganizations(options?: { force?: boolean }) {
    const force = options?.force ?? false
    if (
      !force &&
      organizationsLoadedAt > 0 &&
      Date.now() - organizationsLoadedAt < SHARED_RESOURCE_TTL_MS
    ) {
      return
    }
    return organizationsRequest.fetch(force)
  }

  function patchOrganization(id: string, patch: Partial<Organization>) {
    organizationsRequest.invalidate()
    organizationsLoadedAt = 0
    organizations.value = organizations.value.map(org =>
      org.id === id ? { ...org, ...patch } : org
    )
    if (currentOrganization.value?.id === id) {
      currentOrganization.value = { ...currentOrganization.value, ...patch }
    }
  }

  function upsertOrganization(organization: Organization) {
    organizationsRequest.invalidate()
    organizationsLoadedAt = 0
    organizations.value = upsertById(organizations.value, organization)
    if (currentOrganization.value?.id === organization.id) {
      currentOrganization.value = organization
    }
  }

  /**
   * Merge a detail payload (which may omit list-only aggregate fields) onto the
   * existing card instead of replacing it, so badges like share_count survive.
   */
  function mergeOrganizationDetail(organization: Organization) {
    organizationsRequest.invalidate()
    organizationsLoadedAt = 0
    const next = mergeById(organizations.value, organization)
    organizations.value = next
    currentOrganization.value =
      next.find(org => org.id === organization.id) ?? organization
  }

  function adjustOrganizationResourceCount(
    organizationId: string,
    resource: 'knowledge_bases' | 'agents',
    delta: number
  ) {
    organizationsRequest.invalidate()
    organizationsLoadedAt = 0
    const result = applyOrganizationResourceDelta(
      organizations.value,
      resourceCounts.value,
      organizationId,
      resource,
      delta
    )
    organizations.value = result.organizations
    resourceCounts.value = result.resourceCounts
    if (currentOrganization.value?.id === organizationId) {
      currentOrganization.value =
        result.organizations.find(org => org.id === organizationId) ?? currentOrganization.value
    }
  }

  /**
   * Create a new organization
   */
  async function create(name: string, description?: string) {
    loading.value = true
    error.value = null
    try {
      const response = await createOrganization({ name, description })
      if (response.success && response.data) {
        upsertOrganization(response.data)
        // 创建成功后重置缓存时间戳，确保后续 fetchOrganizations() 不会被 TTL 缓存跳过，
        // 从而刷新 resource_counts 等创建接口不返回的聚合字段。
        organizationsLoadedAt = 0
        void fetchOrganizations({ force: true })
        return response.data
      } else {
        error.value = response.message || 'Failed to create organization'
        return null
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to create organization'
      return null
    } finally {
      loading.value = false
    }
  }

  /**
   * Update an organization
   */
  async function updateOrganization(id: string, updates: UpdateOrganizationRequest) {
    loading.value = true
    error.value = null
    try {
      const response = await updateOrganizationApi(id, updates)
      if (response.success && response.data) {
        upsertOrganization(response.data)
        void fetchOrganizations({ force: true })
        return response.data
      } else {
        error.value = response.message || 'Failed to update organization'
        return null
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to update organization'
      return null
    } finally {
      loading.value = false
    }
  }

  async function update(id: string, name?: string, description?: string) {
    return updateOrganization(id, { name, description })
  }

  /**
   * Delete an organization
   */
  async function remove(id: string) {
    loading.value = true
    error.value = null
    try {
      const response = await deleteOrganization(id)
      if (response.success) {
        organizationsRequest.invalidate()
        organizationsLoadedAt = 0
        organizations.value = organizations.value.filter(o => o.id !== id)
        if (currentOrganization.value?.id === id) {
          currentOrganization.value = null
        }
        return true
      } else {
        error.value = response.message || 'Failed to delete organization'
        return false
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to delete organization'
      return false
    } finally {
      loading.value = false
    }
  }

  /**
   * Preview an organization by invite code (without joining)
   */
  async function preview(inviteCode: string) {
    loading.value = true
    error.value = null
    previewData.value = null
    try {
      const response = await previewOrganization(inviteCode)
      if (response.success && response.data) {
        previewData.value = response.data
        return response.data
      } else {
        error.value = response.message || 'Failed to preview organization'
        return null
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to preview organization'
      return null
    } finally {
      loading.value = false
    }
  }

  /**
   * Join an organization by invite code
   */
  async function join(inviteCode: string) {
    loading.value = true
    error.value = null
    try {
      const response = await joinOrganizationApi({ invite_code: inviteCode })
      if (response.success && response.data) {
        upsertOrganization(response.data)
        invalidateSearchableOrganizations(response.data.id)
        void fetchOrganizations({ force: true })
        return response.data
      } else {
        error.value = response.message || 'Failed to join organization'
        return null
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to join organization'
      return null
    } finally {
      loading.value = false
    }
  }

  /**
   * Leave an organization
   */
  async function leave(id: string) {
    loading.value = true
    error.value = null
    try {
      const response = await leaveOrganization(id)
      if (response.success) {
        organizationsRequest.invalidate()
        organizationsLoadedAt = 0
        organizations.value = organizations.value.filter(o => o.id !== id)
        if (currentOrganization.value?.id === id) {
          currentOrganization.value = null
        }
        return true
      } else {
        error.value = response.message || 'Failed to leave organization'
        return false
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to leave organization'
      return false
    } finally {
      loading.value = false
    }
  }

  /**
   * Generate a new invite code
   */
  async function refreshInviteCode(id: string) {
    loading.value = true
    error.value = null
    try {
      const response = await generateInviteCode(id)
      if (response.success && response.data) {
        patchOrganization(id, { invite_code: response.data.invite_code })
        void fetchOrganizations({ force: true })
        return response.data.invite_code
      } else {
        error.value = response.message || 'Failed to generate invite code'
        return null
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to generate invite code'
      return null
    } finally {
      loading.value = false
    }
  }

  /**
   * Fetch members of an organization
   */
  async function fetchMembers(orgId: string) {
    loading.value = true
    error.value = null
    try {
      const response = await listMembers(orgId)
      if (response.success && response.data) {
        currentMembers.value = response.data.members
        return response.data.members
      } else {
        error.value = response.message || 'Failed to fetch members'
        return []
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to fetch members'
      return []
    } finally {
      loading.value = false
    }
  }

  /**
   * Update a member's role (member identified by tenant_id)
   */
  async function changeMemberRole(orgId: string, tenantId: number, role: 'admin' | 'editor' | 'viewer') {
    loading.value = true
    error.value = null
    try {
      const response = await updateMemberRole(orgId, tenantId, { role })
      if (response.success) {
        const member = currentMembers.value.find(m => m.tenant_id === tenantId)
        if (member) {
          member.role = role
        }
        return true
      } else {
        error.value = response.message || 'Failed to update member role'
        return false
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to update member role'
      return false
    } finally {
      loading.value = false
    }
  }

  /**
   * Remove a member from organization (member identified by tenant_id)
   */
  async function kickMember(orgId: string, tenantId: number) {
    loading.value = true
    error.value = null
    try {
      const response = await removeMember(orgId, tenantId)
      if (response.success) {
        currentMembers.value = currentMembers.value.filter(m => m.tenant_id !== tenantId)
        const organization = organizations.value.find(org => org.id === orgId)
        patchOrganization(orgId, {
          member_count: Math.max(0, (organization?.member_count ?? 0) - 1)
        })
        void fetchOrganizations({ force: true })
        return true
      } else {
        error.value = response.message || 'Failed to remove member'
        return false
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to remove member'
      return false
    } finally {
      loading.value = false
    }
  }

  /**
   * Fetch shared knowledge bases.
   * 去重 + 短期缓存，避免对话页等多处并发重复请求。
   */
  const sharedKnowledgeBasesRequest = createVersionedRequestCoordinator(
    async () => {
      loading.value = true
      error.value = null
      try {
        return await listSharedKnowledgeBases()
      } finally {
        loading.value = false
      }
    },
    (response) => {
      if (response.success && response.data) {
        sharedKnowledgeBases.value = response.data.filter(s => s.knowledge_base != null)
        sharedKbLoadedAt = Date.now()
      } else {
        error.value = response.message || 'Failed to fetch shared knowledge bases'
      }
    }
  )

  async function fetchSharedKnowledgeBases(options?: { force?: boolean }) {
    const force = options?.force ?? false
    if (
      !force &&
      sharedKbLoadedAt > 0 &&
      Date.now() - sharedKbLoadedAt < SHARED_RESOURCE_TTL_MS
    ) {
      return sharedKnowledgeBases.value
    }
    await sharedKnowledgeBasesRequest.fetch(force)
    return sharedKnowledgeBases.value
  }

  /**
   * Fetch shared agents (shared to me through organizations).
   * 去重 + 短期缓存。
   */
  const sharedAgentsRequest = createVersionedRequestCoordinator(
    listSharedAgents,
    (response) => {
      if (response.success && response.data) {
        sharedAgents.value = response.data.filter(s => s.agent != null)
        sharedAgentsLoadedAt = Date.now()
      }
    }
  )

  async function fetchSharedAgents(options?: { force?: boolean }) {
    const force = options?.force ?? false
    if (
      !force &&
      sharedAgentsLoadedAt > 0 &&
      Date.now() - sharedAgentsLoadedAt < SHARED_RESOURCE_TTL_MS
    ) {
      return sharedAgents.value
    }
    await sharedAgentsRequest.fetch(force)
    return sharedAgents.value
  }

  interface InvalidateOrganizationDataOptions {
    organizations?: boolean
    sharedKnowledgeBases?: boolean
    sharedAgents?: boolean
    searchableOrganizations?: boolean
    excludeSearchableOrganizationId?: string
  }

  function invalidateSearchableOrganizations(organizationId?: string) {
    searchableOrganizationCache.clear()
    if (organizationId) {
      searchableOrganizations.value = searchableOrganizations.value.filter(
        organization => organization.id !== organizationId
      )
    }
  }

  function invalidateOrganizationData(options: InvalidateOrganizationDataOptions) {
    if (options.organizations) {
      organizationsRequest.invalidate()
      organizationsLoadedAt = 0
    }
    if (options.sharedKnowledgeBases) {
      sharedKnowledgeBasesRequest.invalidate()
      sharedKbLoadedAt = 0
    }
    if (options.sharedAgents) {
      sharedAgentsRequest.invalidate()
      sharedAgentsLoadedAt = 0
    }
    if (options.searchableOrganizations) {
      invalidateSearchableOrganizations(options.excludeSearchableOrganizationId)
    }
  }

  async function fetchSearchableOrganizations(
    query: string,
    options?: { force?: boolean; limit?: number }
  ) {
    const normalizedQuery = query.trim()
    searchableOrganizationsQuery = normalizedQuery
    const cached = searchableOrganizationCache.get(normalizedQuery)
    if (
      !options?.force &&
      cached &&
      Date.now() - cached.loadedAt < SEARCHABLE_ORGANIZATION_TTL_MS
    ) {
      searchableOrganizations.value = cached.data
      return cached.data
    }

    const response = await searchSearchableOrganizations(normalizedQuery, options?.limit ?? 20)
    if (response.success && response.data) {
      const data = response.data.data.filter(organization => !organization.is_already_member)
      searchableOrganizationCache.set(normalizedQuery, { data, loadedAt: Date.now() })
      if (searchableOrganizationsQuery === normalizedQuery) {
        searchableOrganizations.value = data
      }
      return data
    }
    if (searchableOrganizationsQuery === normalizedQuery) {
      searchableOrganizations.value = []
    }
    return []
  }

  function clearSearchableOrganizations() {
    searchableOrganizationsQuery = ''
    searchableOrganizations.value = []
  }

  async function joinById(
    organizationId: string,
    message?: string,
    role?: 'admin' | 'editor' | 'viewer',
    options?: { requiresApproval?: boolean }
  ) {
    const response = await joinOrganizationByIdApi(organizationId, message, role)
    if (response.success) {
      invalidateOrganizationData({
        searchableOrganizations: true,
        excludeSearchableOrganizationId: organizationId
      })
      if (!options?.requiresApproval && response.data) {
        upsertOrganization(response.data)
        void fetchOrganizations({ force: true })
      }
    }
    return response
  }

  async function shareKnowledgeBase(
    knowledgeBaseId: string,
    request: ShareKnowledgeBaseRequest
  ): Promise<ApiResponse<KnowledgeBaseShare>> {
    const response = await shareKnowledgeBaseApi(knowledgeBaseId, request)
    if (response.success) {
      adjustOrganizationResourceCount(request.organization_id, 'knowledge_bases', 1)
      invalidateOrganizationData({ sharedKnowledgeBases: true })
      void Promise.all([
        fetchOrganizations({ force: true }),
        fetchSharedKnowledgeBases({ force: true })
      ])
    }
    return response
  }

  async function unshareKnowledgeBase(
    knowledgeBaseId: string,
    shareId: string,
    organizationId: string
  ): Promise<ApiResponse<void>> {
    const response = await removeShareApi(knowledgeBaseId, shareId)
    if (response.success) {
      adjustOrganizationResourceCount(organizationId, 'knowledge_bases', -1)
      invalidateOrganizationData({ sharedKnowledgeBases: true })
      void Promise.all([
        fetchOrganizations({ force: true }),
        fetchSharedKnowledgeBases({ force: true })
      ])
    }
    return response
  }

  async function changeKnowledgeBaseSharePermission(
    knowledgeBaseId: string,
    shareId: string,
    request: UpdateSharePermissionRequest
  ): Promise<ApiResponse<void>> {
    const response = await updateSharePermissionApi(knowledgeBaseId, shareId, request)
    if (response.success) {
      invalidateOrganizationData({ sharedKnowledgeBases: true })
      void fetchSharedKnowledgeBases({ force: true })
    }
    return response
  }

  async function shareAgent(
    agentId: string,
    request: ShareKnowledgeBaseRequest
  ): Promise<ApiResponse<AgentShareResponse>> {
    const response = await shareAgentApi(agentId, request)
    if (response.success) {
      adjustOrganizationResourceCount(request.organization_id, 'agents', 1)
      invalidateOrganizationData({ sharedAgents: true, sharedKnowledgeBases: true })
      void Promise.all([
        fetchOrganizations({ force: true }),
        fetchSharedAgents({ force: true }),
        fetchSharedKnowledgeBases({ force: true })
      ])
    }
    return response
  }

  async function unshareAgent(
    agentId: string,
    shareId: string,
    organizationId: string
  ): Promise<ApiResponse<void>> {
    const response = await removeAgentShareApi(agentId, shareId)
    if (response.success) {
      adjustOrganizationResourceCount(organizationId, 'agents', -1)
      invalidateOrganizationData({ sharedAgents: true, sharedKnowledgeBases: true })
      void Promise.all([
        fetchOrganizations({ force: true }),
        fetchSharedAgents({ force: true }),
        fetchSharedKnowledgeBases({ force: true })
      ])
    }
    return response
  }

  async function inviteOrganizationMember(
    organizationId: string,
    request: InviteMemberRequest
  ): Promise<ApiResponse<void>> {
    const response = await inviteMemberApi(organizationId, request)
    if (response.success) {
      const organization = organizations.value.find(org => org.id === organizationId)
      patchOrganization(organizationId, {
        member_count: (organization?.member_count ?? 0) + 1
      })
      void fetchOrganizations({ force: true })
    }
    return response
  }

  async function reviewOrganizationJoinRequest(
    organizationId: string,
    requestId: string,
    request: ReviewJoinRequestRequest,
    options?: { requestType?: 'join' | 'upgrade' }
  ): Promise<ApiResponse<void>> {
    const response = await reviewJoinRequestApi(organizationId, requestId, request)
    if (response.success) {
      const organization = organizations.value.find(org => org.id === organizationId)
      patchOrganization(organizationId, {
        member_count:
          (organization?.member_count ?? 0) +
          reviewMemberCountDelta(request.approved, options?.requestType),
        pending_join_request_count: Math.max(
          0,
          (organization?.pending_join_request_count ?? 0) - 1
        )
      })
      void fetchOrganizations({ force: true })
    }
    return response
  }

  async function requestOrganizationRoleUpgrade(
    organizationId: string,
    request: RequestRoleUpgradeRequest
  ) {
    const response = await requestRoleUpgradeApi(organizationId, request)
    if (response.success) {
      patchOrganization(organizationId, { has_pending_upgrade: true })
      void fetchOrganizations({ force: true })
    }
    return response
  }

  /**
   * Set current organization for detail view
   */
  function setCurrentOrganization(org: Organization | null) {
    if (org) {
      mergeOrganizationDetail(org)
      void fetchOrganizations({ force: true })
    } else {
      currentOrganization.value = null
    }
  }

  function clearCurrentOrganizationContext() {
    currentOrganization.value = null
    currentMembers.value = []
  }

  /**
   * Get user's permission for a specific knowledge base
   * Returns 'owner' if user owns the KB, or the share permission ('admin' | 'editor' | 'viewer'), or null if no access
   */
  function getKBPermission(kbId: string): 'owner' | 'admin' | 'editor' | 'viewer' | null {
    const shared = sharedKnowledgeBases.value.find(
      s => s.knowledge_base?.id === kbId
    )
    return shared?.permission || null
  }

  /**
   * Check if user can edit a knowledge base (owner, admin, or editor)
   */
  function canEditKB(kbId: string, isOwner: boolean): boolean {
    if (isOwner) return true
    const permission = getKBPermission(kbId)
    return permission === 'admin' || permission === 'editor'
  }

  /**
   * Check if user can delete/manage a knowledge base (owner or admin only)
   */
  function canManageKB(kbId: string, isOwner: boolean): boolean {
    if (isOwner) return true
    const permission = getKBPermission(kbId)
    return permission === 'admin'
  }

  /**
   * Clear all state
   */
  function clearState() {
    organizations.value = []
    currentOrganization.value = null
    currentMembers.value = []
    sharedKnowledgeBases.value = []
    sharedAgents.value = []
    searchableOrganizations.value = []
    resourceCounts.value = null
    previewData.value = null
    error.value = null
    sharedKbLoadedAt = 0
    sharedAgentsLoadedAt = 0
    organizationsLoadedAt = 0
    searchableOrganizationsQuery = ''
    searchableOrganizationCache.clear()
    organizationsRequest.invalidate()
    sharedKnowledgeBasesRequest.invalidate()
    sharedAgentsRequest.invalidate()
  }

  return {
    // State
    organizations,
    currentOrganization,
    currentMembers,
    sharedKnowledgeBases,
    sharedAgents,
    searchableOrganizations,
    resourceCounts,
    previewData,
    loading,
    error,

    // Computed
    myOrganizations,
    ownedOrganizations,
    joinedOrganizations,
    totalPendingJoinRequestCount,

    // Actions
    fetchOrganizations,
    create,
    updateOrganization,
    update,
    remove,
    preview,
    join,
    leave,
    refreshInviteCode,
    fetchMembers,
    changeMemberRole,
    kickMember,
    fetchSharedKnowledgeBases,
    fetchSharedAgents,
    fetchSearchableOrganizations,
    clearSearchableOrganizations,
    invalidateOrganizationData,
    joinById,
    shareKnowledgeBase,
    unshareKnowledgeBase,
    changeKnowledgeBaseSharePermission,
    shareAgent,
    unshareAgent,
    inviteOrganizationMember,
    reviewOrganizationJoinRequest,
    requestOrganizationRoleUpgrade,
    setCurrentOrganization,
    clearCurrentOrganizationContext,
    getKBPermission,
    canEditKB,
    canManageKB,
    clearState
  }
})
