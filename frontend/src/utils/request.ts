// src/utils/request.js
import axios from "axios";
import { generateRandomString, MAX_FILE_SIZE_MB, MAX_SKILL_BUNDLE_SIZE_MB } from "./index";
import i18n from '@/i18n'
import { getApiBaseUrl } from './api-base';
import { isSkillBundleUploadUrl } from './uploadLimit';

const t = (key: string) => i18n.global.t(key)

// API基础URL
const BASE_URL = getApiBaseUrl();


// 创建Axios实例
const instance = axios.create({
  baseURL: BASE_URL, // 使用配置的API基础URL
  timeout: 30000, // 请求超时时间
  headers: {
    "Content-Type": "application/json",
    "X-Request-ID": `${generateRandomString(12)}`,
  },
});

// 获取当前用户语言（用于 Accept-Language header）
export function getCurrentLanguage(): string {
  return i18n.global.locale?.value || localStorage.getItem('locale') || 'zh-CN'
}


instance.interceptors.request.use(
  (config) => {
    const existingAuth = config.headers?.Authorization ?? config.headers?.authorization;
    const isEmbedAuth = typeof existingAuth === 'string' && existingAuth.startsWith('Embed ');
    const isEmbedPath = typeof config.url === 'string' && config.url.includes('/api/v1/embed/');

    // 嵌入渠道使用 Embed token；勿用本地 JWT 覆盖（否则调试页会 401）
    if (!isEmbedAuth) {
      const token = localStorage.getItem('weknora_token');
      if (token) {
        config.headers["Authorization"] = `Bearer ${token}`;
      }
    }
    
    // 添加用户语言偏好
    config.headers["Accept-Language"] = getCurrentLanguage();
    
    // 添加跨空间访问请求头：只要 setSelectedTenant 写过激活空间，
    // 每个请求都要附 X-Tenant-ID。早期版本会 short-circuit
    // "selectedTenantId === defaultTenantId 时不附"以减少 header 体积，
    // 但这条优化会被任何把 weknora_tenant 写成激活空间的代码（OIDC
    // 回调、UserMenu loadUserInfo、router hydrate）触发，导致后续请求
    // 静默丢失 header，前端"切换了"但实际仍跑在 home 空间里——把"切
    // 换之后只有第一批请求带 X-Tenant-ID"调成永久状态。
    // 后端 IsTenantAccessible 已经允许 header 指向 home 空间（自家），
    // 所以无脑附不会引入新风险。
    if (!isEmbedAuth && !isEmbedPath) {
      const selectedTenantId = localStorage.getItem('weknora_selected_tenant_id');
      if (selectedTenantId) {
        config.headers["X-Tenant-ID"] = selectedTenantId;
      }
    }
    
    config.headers["X-Request-ID"] = `${generateRandomString(12)}`;
    return config;
  },
  (error) => {
    return Promise.reject(error);
  }
);

// Token刷新标志，防止多个请求同时刷新token
let isRefreshing = false;
let failedQueue: Array<{ resolve: Function; reject: Function }> = [];

// Share-link endpoints (/auth/invitations/lookup, /auth/register-by-invite)
// are reachable by anonymous users opening an invite link. A 401 from these
// must surface to the page (e.g. expired token), not trigger the
// refresh-then-redirect-to-login flow (issue #1617). '/auth/register' already
// covers '/auth/register-by-invite' via substring match.
const PUBLIC_AUTH_PATHS = ['/auth/auto-setup', '/auth/login', '/auth/register', '/auth/oidc/', '/auth/invitations/lookup', '/api/v1/embed/'];

function isPublicAuthRequest(url?: string): boolean {
  if (!url) return false;
  return PUBLIC_AUTH_PATHS.some(p => url.includes(p));
}

// 处理队列中的请求
const processQueue = (error: any, token: string | null = null) => {
  failedQueue.forEach(({ resolve, reject }) => {
    if (error) {
      reject(error);
    } else {
      resolve(token);
    }
  });
  
  failedQueue = [];
};

function isEmbedPage(): boolean {
  if (typeof window === 'undefined') return false;
  return window.location.pathname.startsWith('/embed/');
}

function redirectToLogin() {
  if (typeof window === 'undefined') return;
  if (window.location.pathname === '/login') return;
  // Embed 渠道用 Embed token 鉴权，匿名访问不应被踢到登录页
  if (isEmbedPage()) return;
  window.location.href = '/login';
}

/**
 * Clear stored credentials and send the SPA to /login.
 *
 * Exported for callers that authenticate outside this axios instance (the SSE
 * client) and therefore can't rely on the response interceptor to do it.
 */
export function forceReloginRedirect() {
  localStorage.removeItem('weknora_token');
  localStorage.removeItem('weknora_refresh_token');
  localStorage.removeItem('weknora_user');
  localStorage.removeItem('weknora_tenant');
  redirectToLogin();
}

/**
 * Refresh the access token, de-duplicated across all callers.
 *
 * The axios interceptor and the SSE client share one in-flight refresh, so a
 * burst of 401s produces a single /auth/refresh call instead of N racing ones
 * (refresh tokens rotate — concurrent refreshes invalidate each other).
 *
 * Resolves with the new access token. On failure it has already cleared the
 * credentials and redirected to /login, so callers only need to surface a
 * message.
 */
export async function refreshAccessTokenShared(): Promise<string> {
  if (isRefreshing) {
    // A refresh is already in flight — wait for its result instead of
    // starting a second one.
    return new Promise<string>((resolve, reject) => {
      failedQueue.push({ resolve, reject });
    });
  }

  isRefreshing = true;
  const storedRefreshToken = localStorage.getItem('weknora_refresh_token');

  if (!storedRefreshToken) {
    localStorage.removeItem('weknora_token');
    localStorage.removeItem('weknora_user');
    localStorage.removeItem('weknora_tenant');
    const noRefreshTokenError = { message: t('error.pleaseRelogin') };
    processQueue(noRefreshTokenError, null);
    isRefreshing = false;
    redirectToLogin();
    throw noRefreshTokenError;
  }

  try {
    // 动态导入refresh token API
    const { refreshToken: refreshTokenAPI } = await import('../api/auth/index');
    const response = await refreshTokenAPI(storedRefreshToken);

    if (!response.success || !response.data) {
      throw new Error(response.message || t('error.tokenRefreshFailed'));
    }

    const { token, refreshToken: newRefreshToken } = response.data;

    localStorage.setItem('weknora_token', token);
    localStorage.setItem('weknora_refresh_token', newRefreshToken);
    processQueue(null, token);

    return token;
  } catch (refreshError) {
    // 刷新失败，清除所有token并跳转到登录页
    localStorage.removeItem('weknora_token');
    localStorage.removeItem('weknora_refresh_token');
    localStorage.removeItem('weknora_user');
    localStorage.removeItem('weknora_tenant');

    processQueue(refreshError, null);
    redirectToLogin();

    throw refreshError;
  } finally {
    isRefreshing = false;
  }
}

instance.interceptors.response.use(
  (response) => {
    // 根据业务状态码处理逻辑
    const { status, data } = response;
    if (status >= 200 && status < 300) {
      return data;
    } else {
      return Promise.reject(data);
    }
  },
  async (error: any) => {
    const originalRequest = error.config;
    
    if (!error.response) {
      return Promise.reject({ message: t('error.networkError') });
    }
    
    // 公开接口（auto-setup / login / register / oidc）的 401 不走 refresh 逻辑，直接返回错误
    if ((error.response.status === 401 || error.response.status === 403) && isPublicAuthRequest(originalRequest?.url)) {
      const { status, data } = error.response;
      const msg = typeof data === 'object'
        ? (typeof data?.error === 'string' ? data.error : (data?.error?.message || data?.message))
        : data;
      return Promise.reject({ status, message: msg || t('error.invalidCredentials') });
    }

    // Embed 调试页/挂件：无 JWT 时直接拒绝，勿走 refresh → /login
    if (error.response.status === 401 && isEmbedPage()) {
      const { status, data } = error.response;
      const msg = typeof data === 'object'
        ? (typeof data?.error === 'string' ? data.error : (data?.error?.message || data?.message))
        : data;
      return Promise.reject({ status, message: msg || t('error.invalidCredentials') });
    }

    // 如果是401错误且不是刷新token的请求，尝试刷新token
    if (error.response.status === 401 && !originalRequest._retry && !originalRequest.url?.includes('/auth/refresh')) {
      originalRequest._retry = true;
      try {
        // Shared with the SSE client so both planes queue behind one refresh.
        const token = await refreshAccessTokenShared();
        originalRequest.headers['Authorization'] = 'Bearer ' + token;
        return instance(originalRequest);
      } catch (refreshError) {
        // refreshAccessTokenShared already cleared credentials and redirected.
        return Promise.reject(refreshError);
      }
    }
    
    // 处理 Nginx 413 Request Entity Too Large
    if (error.response.status === 413) {
      const skillUpload = isSkillBundleUploadUrl(error.config?.url)
      return Promise.reject({ 
        status: 413, 
        message: skillUpload
          ? i18n.global.t('settings.sandbox.skillBundleTooLarge', { size: MAX_SKILL_BUNDLE_SIZE_MB })
          : i18n.global.t('error.fileSizeExceeded', { size: MAX_FILE_SIZE_MB }),
        success: false
      });
    }

    const { status, data } = error.response;
    // 将HTTP状态码一并抛出，方便上层判断401等场景
    // 后端返回格式: { success: false, error: { code, message, details } }
    // 提取 error.message 作为顶层 message，方便前端使用 error?.message 获取
    let errorMessage: string | undefined;
    if (typeof data === 'object') {
      if (typeof data?.error === 'string') {
        errorMessage = data.error;
      } else if (data?.error?.message) {
        errorMessage = data.error.message;
      } else {
        errorMessage = data?.message;
      }
    } else if (typeof data === 'string') {
      errorMessage = data;
    }
    return Promise.reject({ 
      status, 
      message: errorMessage,
      ...(typeof data === 'object' ? data : {}) 
    });
  }
);

export function get<T = any>(url: string, config?: any): Promise<T> {
  return instance.get<T>(url, config) as unknown as Promise<T>;
}

export async function getDown(url: string): Promise<Blob> {
  const res = await instance.get<Blob>(url, {
    responseType: "blob",
  }) as unknown as Blob;
  return res
}

export function postUpload(
  url: string,
  data = {},
  onUploadProgress?: (progressEvent: any) => void,
  config: any = {},
): Promise<any> {
  return instance.post(url, data, {
    ...config,
    headers: {
      "Content-Type": "multipart/form-data",
      "X-Request-ID": `${generateRandomString(12)}`,
      ...(config.headers || {}),
    },
    onUploadProgress: onUploadProgress || config.onUploadProgress,
  }) as unknown as Promise<any>;
}

export function postChat<T = any>(url: string, data = {}): Promise<T> {
  return instance.post(url, data, {
    headers: {
      "Content-Type": "text/event-stream;charset=utf-8",
      "X-Request-ID": `${generateRandomString(12)}`,
    },
  }) as unknown as Promise<T>;
}

export function post<T = any>(url: string, data = {}, config?: any): Promise<T> {
  return instance.post<T>(url, data, config) as unknown as Promise<T>;
}

export function put<T = any>(url: string, data = {}, config?: any): Promise<T> {
  return instance.put<T>(url, data, config) as unknown as Promise<T>;
}

export function patch<T = any>(url: string, data = {}, config?: any): Promise<T> {
  return instance.patch<T>(url, data, config) as unknown as Promise<T>;
}

export function del<T = any>(url: string, data?: any): Promise<T> {
  return instance.delete<T>(url, { data }) as unknown as Promise<T>;
}
