/**
 * Whether a storage-engine config request was rejected for permissions.
 *
 * The tenant KV key `storage-engine-config` is admin-only (it carries
 * integration secrets), while every agent creator — Contributors included —
 * needs the Viewer-safe status list to pick a usable provider. The store's
 * ensureStorageEngine therefore downgrades a permission rejection on the
 * config call to "no admin config" instead of failing the whole editor
 * dependency chain (#2991).
 *
 * The rejected value carries a non-enumerable `status` property (see
 * `withHttpStatus` in utils/request.ts); only 401/403 mean "this caller
 * may not read the admin-only config". Anything else stays fatal.
 */
export function isStorageConfigDenied(error: unknown): boolean {
  const status = (error as { status?: unknown } | null | undefined)?.status
  return status === 401 || status === 403
}
