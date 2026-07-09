export function getFrontendBasePath(): string {
  const base = (import.meta.env.BASE_URL || '/').replace(/\/+$/, '')
  return base === '' || base === '/' ? '' : base
}

export function withFrontendBasePath(path: string): string {
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  return `${getFrontendBasePath()}${normalizedPath}`
}
