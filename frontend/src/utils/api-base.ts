export function getApiBaseUrl(): string {
  // LocalHub plugin patch (2026-04-29): respect vite's BASE_URL so that
  // axios calls work at `/app/weknora/` (LocalHub reverse proxy). Without
  // this · axios hits `/api/v1/...` at LocalHub root · gets 404 "Cannot
  // POST". Strip trailing slash so axios doesn't produce `/app/weknora//api/v1/...`.
  // See: plugins/weknora/patches/api-base-baseurl.patch
  // `import.meta.env` is undefined outside Vite (node:test via tsx, plain SSR),
  // so fall back to the origin root there instead of throwing.
  const base = (import.meta.env?.BASE_URL || '/').replace(/\/+$/, '');
  return base;
}

/** Normalize a configured base into `''` (root) or `/prefix` (no trailing slash). */
function normalizedBase(base: string): string {
  const normalized = base.replace(/\/+$/, '');
  return normalized === '/' ? '' : normalized;
}

/** Page origin, or `''` when there is none (SSR, `file://` where origin is `'null'`). */
function pageOrigin(): string {
  if (typeof window === 'undefined' || !window.location) return '';
  const { origin } = window.location;
  return origin && origin !== 'null' ? origin : '';
}

/**
 * The path portion of a configured base: `/app/weknora` for both
 * `'/app/weknora/'` and `'https://cdn.example.com/app/weknora/'`.
 */
function basePathOf(base: string): string {
  const trimmed = base.trim();
  const pathname = /^https?:\/\//i.test(trimmed) ? trimmed.replace(/^https?:\/\/[^/]+/i, '') : trimmed;
  return normalizedBase(pathname);
}

/**
 * 给根相对路径（`/api/v1/...`、`/files` 等）挂上 Vite BASE_URL 前缀。
 *
 * 子路径部署（构建期 `VITE_BASE_PATH=/app/weknora/`）时，浏览器请求的
 * `/api/v1/...` 会落到网关根路径而不是应用前缀，代理返回 404。凡是不经
 * axios（axios 已由 baseURL 统一处理）而直接 `fetch`/`window.open` 的
 * 应用内路径都要经过这里。
 *
 * `base` 参数仅用于测试：默认值取当前构建的 BASE_URL。
 */
export function withApiBasePath(path: string, base: string = getApiBaseUrl()): string {
  const normalized = normalizedBase(base);
  if (!normalized) return path;
  return normalized + path;
}

/**
 * {@link withApiBasePath} 的逆运算：去掉路径上的 BASE_URL 前缀。
 *
 * 用于「拿到的 URL 可能已带前缀、也可能没带」的场景（例如后端回填的
 * 邀请链接、聊天内容里嵌的受保护文件地址）。按整段匹配，`/app2/x` 不会
 * 被 `/app` 前缀误伤；`base` 是绝对 URL 时只比较其路径部分。
 *
 * `base` 参数仅用于测试：默认值取当前构建的 BASE_URL。
 */
export function stripApiBasePath(path: string, base: string = getApiBaseUrl()): string {
  const normalized = basePathOf(base);
  if (!normalized) return path;
  if (path === normalized) return '/';
  if (path.startsWith(normalized + '/')) return path.slice(normalized.length);
  return path;
}

/**
 * Resolve the SPA's absolute base — `origin` plus the optional sub-path from
 * VITE BASE_URL — e.g. `https://host` for a root deploy and
 * `https://host/app/weknora` for a sub-path one. No trailing slash.
 *
 * Use this for URLs that point at the SPA itself (invite links, MCP OAuth
 * `frontend_redirect`, embed widget URLs). Add `/api/v1` (or use
 * {@link getAbsoluteApiBaseUrl}) for backend endpoints.
 *
 * VITE BASE_URL may be:
 *   - `''` or `'/'`       -> just the current page origin
 *   - a path prefix like `'/app/weknora/'` -> resolved against the page origin
 *   - an absolute URL like `'https://api.example.com/app'` -> used as-is
 *
 * When no origin is available (SSR, `file://` page) it falls back to the
 * relative path.
 *
 * `base` 参数仅用于测试：默认值取当前构建的 BASE_URL。
 */
export function getAbsoluteFrontendBaseUrl(base: string = getApiBaseUrl()): string {
  const configured = base.trim();
  if (/^https?:\/\//i.test(configured)) {
    return normalizedBase(configured);
  }
  const normalized = normalizedBase(configured);
  // VITE BASE_URL should already start with `/`, but normalize just in case so
  // we never produce `https://hostapp/weknora`.
  const path = normalized && !normalized.startsWith('/') ? `/${normalized}` : normalized;
  return `${pageOrigin()}${path}`;
}

/**
 * Resolve the API base URL (including the `/api/v1` suffix) into an absolute
 * URL that can be displayed and copy-pasted (curl examples, skill env vars…).
 * Falls back to the relative path when no origin is available.
 *
 * `base` 参数仅用于测试：默认值取当前构建的 BASE_URL。
 */
export function getAbsoluteApiBaseUrl(base: string = getApiBaseUrl()): string {
  return `${getAbsoluteFrontendBaseUrl(base)}/api/v1`;
}
