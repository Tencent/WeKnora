import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type {
  Organization,
  OrganizationMember,
  SharedKnowledgeBase,
  SharedAgentInfo,
  OrganizationPreview,
  ResourceCountsByOrg
} from '@/api/organization'
import {
  listMyOrganizations,
  createOrganization,
  updateOrganization,
  deleteOrganization,
  joinOrganization,
  previewOrganization,
  leaveOrganization,
  generateInviteCode,
  listMembers,
  updateMemberRole,
  removeMember,
  listSharedKnowledgeBases,
  listSharedAgents
} from '@/api/organization'

export const useOrganizationStore = defineStore('organization', () => {
  // State
  const organizations = ref<Organization[]>([])
  const currentOrganization = ref<Organization | null>(null)
  const currentMembers = ref<OrganizationMember[]>([])
  const sharedKnowledgeBases = ref<SharedKnowledgeBase[]>([])
  const sharedAgents = ref<SharedAgentInfo[]>([])
  const previewData = ref<OrganizationPreview | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)
  /** 各空间内知识库/智能体数量（由 GET /organizations 的 resource_counts 填充，供列表侧栏使用） */
  const resourceCounts = ref<ResourceCountsByOrg | null>(null)
  /** 用于去重：同一时刻只允许一次 GET /organizations 请求 */
  let fetchOrganizationsPromise: Promise<void> | null = null

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
   * 去重：并发调用只发一次请求，共用同一 Promise。
   */
  async function fetchOrganizations() {
    if (fetchOrganizationsPromise) return fetchOrganizationsPromise
    loading.value = true
    error.value = null
    fetchOrganizationsPromise = (async () => {
      try {
        const response = await listMyOrganizations()
        if (response.success && response.data) {
          organizations.value = response.data.organizations
          resourceCounts.value = response.data.resource_counts ?? null
        } else {
          resourceCounts.value = null
          error.value = response.message || 'Failed to fetch organizations'
        }
      } catch (e: any) {
        error.value = e.message || 'Failed to fetch organizations'
        resourceCounts.value = null
      } finally {
        loading.value = false
        fetchOrganizationsPromise = null
      }
    })()
    return fetchOrganizationsPromise
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
        organizations.value.unshift(response.data)
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
  async function update(id: string, name?: string, description?: string) {
    loading.value = true
    error.value = null
    try {
      const response = await updateOrganization(id, { name, description })
      if (response.success && response.data) {
        const index = organizations.value.findIndex(o => o.id === id)
        if (index !== -1) {
          organizations.value[index] = response.data
        }
        if (currentOrganization.value?.id === id) {
          currentOrganization.value = response.data
        }
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

  /**
   * Delete an organization
   */
  async function remove(id: string) {
    loading.value = true
    error.value = null
    try {
      const response = await deleteOrganization(id)
      if (response.success) {
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
      const response = await joinOrganization({ invite_code: inviteCode })
      if (response.success && response.data) {
        // Check if already in list
        const exists = organizations.value.some(o => o.id === response.data!.id)
        if (!exists) {
          organizations.value.unshift(response.data)
        }
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
        const org = organizations.value.find(o => o.id === id)
        if (org) {
          org.invite_code = response.data.invite_code
        }
        if (currentOrganization.value?.id === id) {
          currentOrganization.value.invite_code = response.data.invite_code
        }
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
   * Update a member's role
   */
  async function changeMemberRole(orgId: string, userId: string, role: 'admin' | 'editor' | 'viewer') {
    loading.value = true
    error.value = null
    try {
      const response = await updateMemberRole(orgId, userId, { role })
      if (response.success) {
        const member = currentMembers.value.find(m => m.user_id === userId)
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
   * Remove a member from organization
   */
  async function kickMember(orgId: string, userId: string) {
    loading.value = true
    error.value = null
    try {
      const response = await removeMember(orgId, userId)
      if (response.success) {
        currentMembers.value = currentMembers.value.filter(m => m.user_id !== userId)
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
   * Fetch shared knowledge bases
   */
  async function fetchSharedKnowledgeBases() {
    loading.value = true
    error.value = null
    try {
      const response = await listSharedKnowledgeBases()
      if (response.success && response.data) {
        // Filter out shares whose knowledge_base was deleted (null)
        sharedKnowledgeBases.value = response.data.filter(s => s.knowledge_base != null)
        return sharedKnowledgeBases.value
      } else {
        error.value = response.message || 'Failed to fetch shared knowledge bases'
        return []
      }
    } catch (e: any) {
      error.value = e.message || 'Failed to fetch shared knowledge bases'
      return []
    } finally {
      loading.value = false
    }
  }

  /**
   * Fetch shared agents (shared to me through organizations)
   */
  async function fetchSharedAgents() {
    try {
      const response = await listSharedAgents()
      if (response.success && response.data) {
        sharedAgents.value = response.data.filter(s => s.agent != null)
        return sharedAgents.value
      }
      return []
    } catch (e: any) {
      return []
    }
  }

  /**
   * Set current organization for detail view
   */
  function setCurrentOrganization(org: Organization | null) {
    currentOrganization.value = org
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
    resourceCounts.value = null
    previewData.value = null
    error.value = null
  }

  return {
    // State
    organizations,
    currentOrganization,
    currentMembers,
    sharedKnowledgeBases,
    sharedAgents,
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
    setCurrentOrganization,
    getKBPermission,
    canEditKB,
    canManageKB,
    clearState
  }
})
