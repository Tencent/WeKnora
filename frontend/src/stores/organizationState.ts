export function upsertById<T extends { id: string }>(items: T[], item: T): T[] {
  const exists = items.some(existing => existing.id === item.id)
  return exists
    ? items.map(existing => existing.id === item.id ? item : existing)
    : [item, ...items]
}

/**
 * Merge a (possibly partial) organization payload onto the existing card.
 *
 * Detail endpoints (e.g. GET /organizations/:id) may omit list-only aggregate
 * fields such as share_count / agent_share_count / pending_join_request_count.
 * A plain replace would blank those badges until the background refresh lands,
 * so when a card already exists we shallow-merge instead of overwriting it.
 */
export function mergeById<T extends { id: string }>(items: T[], item: T): T[] {
  const existing = items.find(entry => entry.id === item.id)
  const merged = existing ? { ...existing, ...item } : item
  return upsertById(items, merged)
}

/**
 * How much a join-request review changes the organization's member_count.
 *
 * Only brand-new "join" approvals add a member. Approving an "upgrade" request
 * (an existing member asking for a higher role) must not change member_count.
 */
export function reviewMemberCountDelta(
  approved: boolean,
  requestType: 'join' | 'upgrade' | undefined
): number {
  return approved && requestType !== 'upgrade' ? 1 : 0
}

interface OrganizationResourceCounts {
  knowledge_bases: { by_organization: Record<string, number> }
  agents: { by_organization: Record<string, number> }
}

interface CountedOrganization {
  id: string
  share_count?: number
  agent_share_count?: number
}

export function applyOrganizationResourceDelta<T extends CountedOrganization>(
  organizations: T[],
  resourceCounts: OrganizationResourceCounts | null,
  organizationId: string,
  resource: 'knowledge_bases' | 'agents',
  delta: number
): { organizations: T[]; resourceCounts: OrganizationResourceCounts | null } {
  const field = resource === 'knowledge_bases' ? 'share_count' : 'agent_share_count'
  const organizationsAfterUpdate = organizations.map(organization => {
    if (organization.id !== organizationId) return organization
    return {
      ...organization,
      [field]: Math.max(0, (organization[field] ?? 0) + delta)
    }
  })

  if (!resourceCounts) {
    return { organizations: organizationsAfterUpdate, resourceCounts }
  }

  const counts = resourceCounts[resource].by_organization
  return {
    organizations: organizationsAfterUpdate,
    resourceCounts: {
      ...resourceCounts,
      [resource]: {
        by_organization: {
          ...counts,
          [organizationId]: Math.max(0, (counts[organizationId] ?? 0) + delta)
        }
      }
    }
  }
}
