import defaultLogo from '@/assets/img/weknora.png';

/** Logo URL from window.__RUNTIME_CONFIG__ (see public/config.js); falls back to default. */
export function getAppLogoUrl(): string {
  const url = window.__RUNTIME_CONFIG__?.APP_LOGO_URL?.trim() || '';
  return url || defaultLogo;
}
