type QuotaSetting = { key: string; value: unknown }

export const STORAGE_QUOTA_KEYS = ['tenant.default_storage_quota_mb', 'tenant.default_storage_quota_gb'] as const

// Match the service's byte-overflow guards before displaying a bulk action.
export function resolveStorageQuota(settings: readonly QuotaSetting[]): { value: number; unit: string } {
  const mb = Number(settings.find((item) => item.key === STORAGE_QUOTA_KEYS[0])?.value)
  if (Number.isInteger(mb) && mb > 0 && mb <= 8796093022207) return { value: mb, unit: 'MB' }
  const gb = Number(settings.find((item) => item.key === STORAGE_QUOTA_KEYS[1])?.value)
  return { value: Number.isInteger(gb) && gb > 0 && gb <= 8589934591 ? gb : 10, unit: 'GB' }
}

export function formatStorageQuota(bytes: number): { value: number; unit: string } {
  return bytes % (1024 ** 3) === 0
    ? { value: bytes / (1024 ** 3), unit: 'GB' }
    : { value: bytes / (1024 ** 2), unit: 'MB' }
}
