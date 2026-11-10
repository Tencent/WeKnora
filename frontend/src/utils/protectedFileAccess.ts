/**
 * 受保护文件（provider:// / resource:// 等）的访问上下文。
 *
 * 后端按访问主体拆分文件代理，鉴权模型互不相同：
 *   - `/files`                                → 登录态 Bearer + X-Tenant-ID
 *   - `/api/v1/knowledge-bases/:id/files`     → 知识库访问权限（跨租户共享库）
 *   - `/api/v1/sessions/:id/messages/:mid/files` → 会话消息归属 + 共享智能体权限
 *   - `/api/v1/embed/:channel_id/files`       → 嵌入访客的 Embed token
 *
 * 选哪条代理取决于当前请求的鉴权平面，而不是取决于哪个组件在渲染图片。
 * 这里把该决策收敛成单一真相源，渲染组件只需声明作用域，不再各自拼 URL。
 */

export const PROVIDER_SCHEME_PATTERN = 'resource|local|minio|cos|tos|s3|oss|ks3|obs';

const PROVIDER_FILE_SCHEME_RE = new RegExp(`^(${PROVIDER_SCHEME_PATTERN}):\\/\\/\\S+$`, 'i');
const STORAGE_BACKEND_FILE_SCHEME_RE = new RegExp(
  `^storage:\\/\\/[0-9A-Za-z_-]+\\/(${PROVIDER_SCHEME_PATTERN}):\\/\\/\\S+$`,
  'i',
);

const KB_FILE_PROXY_PATH_RE = /^\/api\/v1\/knowledge-bases\/[^/]+\/files$/;
const EMBED_FILE_PROXY_PATH_RE = /^\/api\/v1\/embed\/[^/]+\/files$/;
const MESSAGE_FILE_PROXY_PATH_RE = /^\/api\/v1\/sessions\/[^/]+\/messages\/[^/]+\/files$/;

export type ProtectedFileAccessContext =
  /** 登录态用户：Bearer + 选中租户。 */
  | { mode: 'tenant' }
  /** 嵌入访客：只持有 Embed token，无 Bearer、无租户上下文。 */
  | { mode: 'embed'; channelId: string; token: string }
  /** 知识库作用域：登录态用户读取共享库中归属其他租户的对象。 */
  | { mode: 'knowledgeBase'; kbId: string }
  /** 消息作用域：登录态用户读取共享智能体回复中的源空间资源。 */
  | { mode: 'message'; sessionId: string; messageId: string };

export interface ProtectedFileRequest {
  url: string;
  headers: Record<string, string>;
}

const TENANT_ACCESS: ProtectedFileAccessContext = { mode: 'tenant' };

interface ProtectedFileAccessState {
  current: ProtectedFileAccessContext;
}

// 与 blob 缓存同样挂在 window 上：Vite 热更新会替换模块但不会重建文档，
// 模块级变量会丢失嵌入应用启动时注册的上下文。
const accessState: ProtectedFileAccessState = (() => {
  const fresh = (): ProtectedFileAccessState => ({ current: TENANT_ACCESS });
  if (typeof window === 'undefined') return fresh();
  const scope = window as typeof window & {
    __weknoraProtectedFileAccessV1__?: ProtectedFileAccessState;
  };
  scope.__weknoraProtectedFileAccessV1__ ||= fresh();
  return scope.__weknoraProtectedFileAccessV1__;
})();

/**
 * 注册当前文档的默认访问上下文。由应用入口调用一次（嵌入应用在拿到
 * channelId/token 后注册），此后所有受保护文件请求自动走对应代理。
 */
export function setDefaultProtectedFileAccess(
  access: ProtectedFileAccessContext | null,
): void {
  accessState.current = access ?? TENANT_ACCESS;
}

export function getDefaultProtectedFileAccess(): ProtectedFileAccessContext {
  return accessState.current;
}

/**
 * 合并默认上下文与组件传入的作用域。
 *
 * 默认上下文携带的是鉴权平面（嵌入访客 vs 登录态用户），组件级 override 只能
 * 在同一平面内细化作用域。嵌入访客没有 Bearer，若让 `knowledgeBase` override
 * 覆盖 embed 平面，请求会落到需要登录态的代理上并返回 401。
 */
export function resolveProtectedFileAccess(
  override?: ProtectedFileAccessContext | null,
): ProtectedFileAccessContext {
  const fallback = accessState.current;
  if (fallback.mode === 'embed') return fallback;
  if (!override) return fallback;
  if (override.mode === 'knowledgeBase' && !override.kbId.trim()) return fallback;
  if (override.mode === 'message' && (!override.sessionId.trim() || !override.messageId.trim())) {
    return fallback;
  }
  return override;
}

/** 是否为需要经代理拉取的存储路径（provider:// 或 storage://<backend>/provider://）。 */
export function isProviderFileURL(url: string): boolean {
  const trimmed = url.trim();
  return PROVIDER_FILE_SCHEME_RE.test(trimmed) || STORAGE_BACKEND_FILE_SCHEME_RE.test(trimmed);
}

/** 是否为受保护文件代理之一的路径。 */
export function isProtectedFileProxyPath(pathname: string): boolean {
  return (
    pathname === '/files'
    || KB_FILE_PROXY_PATH_RE.test(pathname)
    || MESSAGE_FILE_PROXY_PATH_RE.test(pathname)
    || EMBED_FILE_PROXY_PATH_RE.test(pathname)
  );
}

function tenantRequestHeaders(): Record<string, string> {
  const headers: Record<string, string> = {};
  try {
    const token = (localStorage.getItem('weknora_token') || '').trim();
    if (token) {
      headers['Authorization'] = `Bearer ${token}`;
    }

    const selectedTenantId = (localStorage.getItem('weknora_selected_tenant_id') || '').trim();
    if (selectedTenantId) {
      // Always attach when a selected tenant is set. Same rationale as
      // utils/request.ts / api/chat/streame.ts: the
      // "selectedTenantId === defaultTenantId → skip" short-circuit
      // silently drops the header whenever any code path writes the
      // active tenant into weknora_tenant, leaving authenticated file
      // fetches landing on the home tenant.
      headers['X-Tenant-ID'] = selectedTenantId;
    }
  } catch {
    // ignore localStorage read errors
  }
  return headers;
}

/**
 * 为一个存储路径构造代理请求。返回 null 表示当前上下文无法发起该请求
 * （非存储路径，或嵌入上下文尚未拿到 token），调用方应跳过并稍后重试。
 */
export function buildProtectedFileRequest(
  sourceURL: string,
  access: ProtectedFileAccessContext,
): ProtectedFileRequest | null {
  const filePath = sourceURL.trim();
  if (!isProviderFileURL(filePath)) return null;

  const query = new URLSearchParams({ file_path: filePath }).toString();

  if (access.mode === 'embed') {
    const channelId = access.channelId.trim();
    const token = access.token.trim();
    // 嵌入访客只有 Embed token 这一种凭据；缺失时退回 /files 只会得到 401，
    // 不如跳过等待 bootstrap 完成后的下一次水合。
    if (!channelId || !token) return null;
    return {
      url: `/api/v1/embed/${encodeURIComponent(channelId)}/files?${query}`,
      headers: { Authorization: `Embed ${token}` },
    };
  }

  if (access.mode === 'knowledgeBase') {
    return {
      url: `/api/v1/knowledge-bases/${encodeURIComponent(access.kbId.trim())}/files?${query}`,
      headers: tenantRequestHeaders(),
    };
  }

  if (access.mode === 'message') {
    return {
      url: `/api/v1/sessions/${encodeURIComponent(access.sessionId.trim())}/messages/${encodeURIComponent(access.messageId.trim())}/files?${query}`,
      headers: tenantRequestHeaders(),
    };
  }

  return { url: `/files?${query}`, headers: tenantRequestHeaders() };
}
