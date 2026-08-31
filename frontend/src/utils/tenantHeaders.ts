function hasHeader(headers: unknown, headerName: string): boolean {
  if (!headers || typeof headers !== 'object') return false

  const candidate = headers as {
    get?: (name: string) => unknown
  }
  if (typeof candidate.get === 'function') {
    const value = candidate.get(headerName)
    if (value !== undefined && value !== null) return true
  }

  return Object.entries(headers).some(([key, value]) =>
    key.toLowerCase() === headerName.toLowerCase() && value !== undefined && value !== null,
  )
}

export function appendSelectedTenantHeader<T extends Record<string, unknown>>(
  headers: T,
  selectedTenantId?: string | null,
): T {
  if (selectedTenantId && !hasHeader(headers, 'X-Tenant-ID')) {
    headers['X-Tenant-ID' as keyof T] = selectedTenantId as T[keyof T]
  }
  return headers
}
