export function getApiBaseUrl(): string {
  // Use same-origin requests by default.
  // In local Vite dev, `vite.config.ts` proxies `/api` to the Go backend.
  return '';
}
