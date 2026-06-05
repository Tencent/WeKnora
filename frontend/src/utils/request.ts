// src/utils/request.js
import axios from "axios";
import { generateRandomString, MAX_FILE_SIZE_MB } from "./index";
import i18n from '@/i18n'
import { getApiBaseUrl } from './api-base';

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
function getCurrentLanguage(): string {
  return i18n.global.locale?.value || localStorage.getItem('locale') || 'zh-CN'
}


instance.interceptors.request.use(
  (config) => {
    // 添加JWT token认证
    const token = localStorage.getItem('weknora_token');
    if (token) {
      config.headers["Authorization"] = `Bearer ${token}`;
    }
    
    // 添加用户语言偏好
    config.headers["Accept-Language"] = getCurrentLanguage();
    
    // 添加跨租户访问请求头：只要 setSelectedTenant 写过激活租户，
    // 每个请求都要附 X-Tenant-ID。早期版本会 short-circuit
    // "selectedTenantId === defaultTenantId 时不附"以减少 header 体积，
    // 但这条优化会被任何把 weknora_tenant 写成激活租户的代码（OIDC
    // 回调、UserMenu loadUserInfo、router hydrate）触发，导致后续请求
    // 静默丢失 header，前端"切换了"但实际仍跑在 home 租户里——把"切
    // 换之后只有第一批请求带 X-Tenant-ID"调成永久状态。
    // 后端 IsTenantAccessible 已经允许 header 指向 home 租户（自家），
    // 所以无脑附不会引入新风险。
    const selectedTenantId = localStorage.getItem('weknora_selected_tenant_id');
    if (selectedTenantId) {
      config.headers["X-Tenant-ID"] = selectedTenantId;
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
const PUBLIC_AUTH_PATHS = ['/auth/auto-setup', '/auth/login', '/auth/register', '/auth/oidc/', '/auth/invitations/lookup'];

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

function redirectToLogin() {
  if (typeof window === 'undefined') return;
  if (window.location.pathname === '/login') return;
  window.location.href = '/login';
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
    if (error.response.status === 401 && isPublicAuthRequest(originalRequest?.url)) {
      const { status, data } = error.response;
      return Promise.reject({ status, message: (typeof data === 'object' ? (data?.error?.message || data?.message) : data) || t('error.invalidCredentials') });
    }

    // 如果是401错误且不是刷新token的请求，尝试刷新token
    if (error.response.status === 401 && !originalRequest._retry && !originalRequest.url?.includes('/auth/refresh')) {
      if (isRefreshing) {
        // 如果正在刷新token，将请求加入队列
        return new Promise((resolve, reject) => {
          failedQueue.push({ resolve, reject });
        }).then(token => {
          originalRequest.headers['Authorization'] = 'Bearer ' + token;
          return instance(originalRequest);
        }).catch(err => {
          return Promise.reject(err);
        });
      }
      
      originalRequest._retry = true;
      isRefreshing = true;
      
      const refreshToken = localStorage.getItem('weknora_refresh_token');
      
      if (refreshToken) {
        try {
          // 动态导入refresh token API
          const { refreshToken: refreshTokenAPI } = await import('../api/auth/index');
          const response = await refreshTokenAPI(refreshToken);
          
          if (response.success && response.data) {
            const { token, refreshToken: newRefreshToken } = response.data;
            
            // 更新localStorage中的token
            localStorage.setItem('weknora_token', token);
            localStorage.setItem('weknora_refresh_token', newRefreshToken);
            
            // 更新请求头
            originalRequest.headers['Authorization'] = 'Bearer ' + token;
            
            // 处理队列中的请求
            processQueue(null, token);
            
            return instance(originalRequest);
          } else {
            throw new Error(response.message || t('error.tokenRefreshFailed'));
          }
        } catch (refreshError) {
          // 刷新失败，清除所有token并跳转到登录页
          localStorage.removeItem('weknora_token');
          localStorage.removeItem('weknora_refresh_token');
          localStorage.removeItem('weknora_user');
          localStorage.removeItem('weknora_tenant');
          
          processQueue(refreshError, null);
          
          redirectToLogin();
          
          return Promise.reject(refreshError);
        } finally {
          isRefreshing = false;
        }
      } else {
        // 没有refresh token，直接跳转到登录页
        localStorage.removeItem('weknora_token');
        localStorage.removeItem('weknora_user');
        localStorage.removeItem('weknora_tenant');
        
        redirectToLogin();
        
        return Promise.reject({ message: t('error.pleaseRelogin') });
      }
    }
    
    // 处理 Nginx 413 Request Entity Too Large
    if (error.response.status === 413) {
      return Promise.reject({ 
        status: 413, 
        message: i18n.global.t('error.fileSizeExceeded', { size: MAX_FILE_SIZE_MB }),
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

export function get(url: string, config?: any) {
  return instance.get(url, config);
}

export async function getDown(url: string) {
  let res = await instance.get(url, {
    responseType: "blob",
  });
  return res
}

export function postUpload(url: string, data = {}, onUploadProgress?: (progressEvent: any) => void) {
  return instance.post(url, data, {
    headers: {
      "Content-Type": "multipart/form-data",
      "X-Request-ID": `${generateRandomString(12)}`,
    },
    onUploadProgress,
  });
}

export function postChat(url: string, data = {}) {
  return instance.post(url, data, {
    headers: {
      "Content-Type": "text/event-stream;charset=utf-8",
      "X-Request-ID": `${generateRandomString(12)}`,
    },
  });
}

export function post(url: string, data = {}, config?: any) {
  return instance.post(url, data, config);
}

export function put(url: string, data = {}) {
  return instance.put(url, data);
}

export function del(url: string, data?: any) {
  return instance.delete(url, { data });
}
