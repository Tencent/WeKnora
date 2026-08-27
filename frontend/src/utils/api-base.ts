export function getApiBaseUrl(): string {
  // LocalHub plugin patch (2026-04-29): respect vite's BASE_URL so that
  // axios calls work at `/app/gridora/` (LocalHub reverse proxy). Without
  // this · axios hits `/api/v1/...` at LocalHub root · gets 404 "Cannot
  // POST". Strip trailing slash so axios doesn't produce `/app/gridora//api/v1/...`.
  // See: plugins/gridora/patches/api-base-baseurl.patch
  const base = (import.meta.env.BASE_URL || '/').replace(/\/+$/, '');
  return base;
}
