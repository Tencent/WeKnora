export const CROSS_TENANT_ACCESS_PAGE_SIZE = 200

export interface CrossTenantAccessPage<T> {
  users: T[]
  next_cursor: string
}

export type CrossTenantAccessPageLoader<T> = (params: {
  cursor?: string
  limit: number
}) => Promise<CrossTenantAccessPage<T>>

/** Load every server page so all granted accounts remain manageable in the tag list. */
export async function fetchAllCrossTenantAccessUsers<T>(
  loadPage: CrossTenantAccessPageLoader<T>,
): Promise<T[]> {
  const users: T[] = []
  let cursor: string | undefined

  do {
    const page = await loadPage({ cursor, limit: CROSS_TENANT_ACCESS_PAGE_SIZE })
    const batch = page.users ?? []
    users.push(...batch)
    cursor = page.next_cursor || undefined
  } while (cursor)

  return users
}
