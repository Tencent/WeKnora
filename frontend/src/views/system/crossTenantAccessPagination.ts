export const CROSS_TENANT_ACCESS_PAGE_SIZE = 200

export interface CrossTenantAccessPage<T> {
  total: number
  users: T[]
}

export type CrossTenantAccessPageLoader<T> = (params: {
  offset: number
  limit: number
}) => Promise<CrossTenantAccessPage<T>>

/** Load every server page so all granted accounts remain manageable in the tag list. */
export async function fetchAllCrossTenantAccessUsers<T>(
  loadPage: CrossTenantAccessPageLoader<T>,
): Promise<T[]> {
  const users: T[] = []
  let offset = 0
  let total = 0

  do {
    const page = await loadPage({ offset, limit: CROSS_TENANT_ACCESS_PAGE_SIZE })
    const batch = page.users ?? []
    users.push(...batch)
    total = Math.max(0, page.total)
    offset += batch.length

    // A shrinking data set can produce an empty trailing page. Stop instead
    // of repeatedly requesting the same offset when total is temporarily stale.
    if (batch.length === 0) break
  } while (offset < total)

  return users
}
