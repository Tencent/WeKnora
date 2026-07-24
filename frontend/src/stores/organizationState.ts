export function upsertById<T extends { id: string }>(items: T[], item: T): T[] {
  const exists = items.some(existing => existing.id === item.id)
  return exists
    ? items.map(existing => existing.id === item.id ? item : existing)
    : [item, ...items]
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
