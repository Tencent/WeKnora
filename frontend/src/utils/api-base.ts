export function getAppBasePath(): string {
  // Respect Vite's base path so API calls work when the app is mounted behind
  // a reverse-proxy subpath. Strip trailing slash to avoid double slashes when
  // axios receives absolute API paths such as /api/v1/auth/login.
  return (import.meta.env.BASE_URL || '/').replace(/\/+$/, '');
}

export function getApiBaseUrl(): string {
  return getAppBasePath();
}

export function withAppBasePath(path: string): string {
  const base = getAppBasePath();
  const cleanPath = path.startsWith('/') ? path : `/${path}`;
  return `${base}${cleanPath}`.replace(/\/{2,}/g, '/');
}
