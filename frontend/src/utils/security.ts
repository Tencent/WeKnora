/**
 * 安全工具类 - 防止 XSS 攻击
 */

import DOMPurify from 'dompurify';
import type { Config, NodeHook } from 'dompurify';
import {
  domPurifySecurityHooks,
  domPurifySecurityOptions,
  markdownDomPurifyConfig,
  markdownDomPurifySecurityHooks,
} from './markdownDomPurify.ts';

const PROVIDER_IMAGE_PLACEHOLDER = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==';
const PROVIDER_FILE_SCHEME_RE = /^(local|minio|cos|tos|s3|oss|ks3|obs):\/\/\S+$/i;

type SecurityHooks = {
  beforeSanitizeElements: NodeHook;
  afterSanitizeElements: NodeHook;
};

function sanitizeWithSecurityHooks(
  html: string,
  config: Config,
  hooks: SecurityHooks,
): string {
  DOMPurify.addHook('beforeSanitizeElements', hooks.beforeSanitizeElements);
  DOMPurify.addHook('afterSanitizeElements', hooks.afterSanitizeElements);
  try {
    return DOMPurify.sanitize(html, config);
  } finally {
    DOMPurify.removeHook('afterSanitizeElements', hooks.afterSanitizeElements);
    DOMPurify.removeHook('beforeSanitizeElements', hooks.beforeSanitizeElements);
  }
}

// 配置 DOMPurify 的安全策略
const DOMPurifyConfig = {
  // 允许的标签
  ALLOWED_TAGS: [
    'p', 'br', 'strong', 'em', 'u', 's', 'del', 'ins',
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
    'ul', 'ol', 'li', 'blockquote', 'pre', 'code',
    'a', 'img', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
    'div', 'span', 'figure', 'figcaption', 'details', 'summary', 'think', 'button',
    // Mermaid SVG 支持的标签
    'svg', 'g', 'path', 'rect', 'circle', 'ellipse', 'line', 'polygon',
    'polyline', 'text', 'tspan', 'defs', 'marker', 'filter', 'use',
    'clippath', 'lineargradient', 'radialgradient', 'stop', 'pattern',
    'image', 'foreignobject', 'desc', 'title', 'switch', 'symbol', 'mask',
    // KaTeX MathML 支持的标签
    'math', 'annotation', 'semantics', 'mo', 'mi', 'mn', 'msup', 'mrow', 'mfrac', 'msqrt', 'mroot', 'mstyle'
  ],
  // 允许的属性
  ALLOWED_ATTR: [
    'href', 'title', 'alt', 'src', 'class', 'id', 'style', 'data-protected-src', 'data-img-loading',
    'target', 'rel', 'width', 'height', 'open',
    'type', 'aria-label', 'disabled', 'role', 'tabindex',
    // Mermaid SVG 支持的属性
    'd', 'fill', 'stroke', 'stroke-width', 'stroke-linecap', 'stroke-linejoin',
    'stroke-dasharray', 'stroke-dashoffset', 'stroke-miterlimit', 'stroke-opacity',
    'fill-opacity', 'opacity', 'transform', 'viewbox', 'preserveaspectratio',
    'x', 'y', 'x1', 'y1', 'x2', 'y2', 'cx', 'cy', 'rx', 'ry', 'r',
    'dx', 'dy', 'text-anchor', 'dominant-baseline', 'font-family', 'font-size',
    'font-weight', 'font-style', 'letter-spacing', 'word-spacing',
    'marker-start', 'marker-mid', 'marker-end', 'markerunits', 'markerwidth',
    'markerheight', 'refx', 'refy', 'orient', 'points', 'offset',
    'gradientunits', 'gradienttransform', 'spreadmethod', 'stop-color', 'stop-opacity',
    'patternunits', 'patterntransform', 'clippathunits', 'maskunits',
    'filterunits', 'primitiveunits', 'xmlns', 'xmlns:xlink', 'xlink:href',
    'version', 'baseprofile', 'enable-background', 'overflow', 'visibility',
    'display', 'pointer-events', 'cursor', 'data-emit', 'direction',
    // KaTeX MathML 支持的属性
    'mathvariant', 'encoding', 'aria-hidden'
  ],
  USE_PROFILES: { html: true, svg: true, mathMl: true },
  ...domPurifySecurityOptions,
};

/**
 * 安全地清理 HTML 内容
 * @param html 需要清理的 HTML 字符串
 * @returns 清理后的安全 HTML 字符串
 */
export function sanitizeHTML(html: string): string {
  if (!html || typeof html !== 'string') {
    return '';
  }
  
  try {
    const preparedHTML = protectProviderImageSrcInHTML(html);
    return sanitizeWithSecurityHooks(
      preparedHTML,
      DOMPurifyConfig as unknown as Config,
      domPurifySecurityHooks,
    );
  } catch (error) {
    console.error('HTML sanitization failed:', error);
    // 如果清理失败，返回转义的纯文本
    return escapeHTML(html);
  }
}

/** Sanitize assistant markdown HTML (code/mermaid toolbars, KaTeX, SVG). */
export function sanitizeMarkdownHTML(html: string): string {
  if (!html || typeof html !== 'string') {
    return '';
  }

  try {
    const preparedHTML = protectProviderImageSrcInHTML(html);
    return sanitizeWithSecurityHooks(
      preparedHTML,
      markdownDomPurifyConfig as unknown as Config,
      markdownDomPurifySecurityHooks,
    );
  } catch (error) {
    console.error('Markdown HTML sanitization failed:', error);
    return escapeHTML(html);
  }
}

export function protectProviderImageSrcInHTML(html: string): string {
  if (!html) return html;
  return html.replace(
    /<img\b([^>]*?)\ssrc=(["'])(local|minio|cos|tos|s3|oss|ks3|obs):(?:\/\/|&#x2f;&#x2f;|&#47;&#47;)([^"']+)\2([^>]*)>/gi,
    (_m, before, quote, provider, restPathRaw, after) => {
      const restPath = decodeProviderURL(restPathRaw);
      const protectedSrc = `${provider}://${restPath}`;
      // A definitive 404 should not leave a skeleton behind. Streaming
      // re-renders call this function repeatedly, so remember the missing
      // source until the explicit end-of-stream retry clears the cache.
      if (protectedFileMissingSources.has(protectedSrc)) {
        return '';
      }
      // Reuse the already-hydrated blob if we have one, so repeated re-renders
      // (typewriter streaming) keep the same stable image instead of flashing
      // back to the placeholder every frame.
      const cachedBlobURL = protectedFileBlobBySource.get(protectedSrc);
      if (cachedBlobURL) {
        return `<img${before} src=${quote}${cachedBlobURL}${quote} data-protected-src=${quote}${protectedSrc}${quote}${after}>`;
      }
      // Not hydrated yet: render the 1x1 placeholder but tag it so CSS can give
      // it a stable skeleton box. Otherwise width:auto/height:auto collapse the
      // 1x1 gif to a ~1px line that violently jumps to full size once loaded.
      return `<img${before} src=${quote}${PROVIDER_IMAGE_PLACEHOLDER}${quote} data-protected-src=${quote}${protectedSrc}${quote} data-img-loading=${quote}1${quote}${after}>`;
    },
  );
}

function decodeProviderURL(raw: string): string {
  return raw
    .trim()
    .replace(/&#x2f;/gi, '/')
    .replace(/&#47;/g, '/')
    .replace(/&amp;/g, '&')
    .replace(/&quot;/g, '"');
}

function isProviderFileURL(url: string): boolean {
  return PROVIDER_FILE_SCHEME_RE.test(url.trim());
}

function providerSourceFromImageSrc(src: string): string | null {
  const decodedSrc = decodeProviderURL(src);
  if (isProviderFileURL(decodedSrc)) {
    return decodedSrc;
  }

  try {
    const baseURL = typeof window !== 'undefined' ? window.location.origin : 'http://localhost';
    const url = new URL(decodedSrc, baseURL);
    const isFileProxy =
      url.pathname === '/files' ||
      /^\/api\/v1\/embed\/[^/]+\/files$/.test(url.pathname);
    if (!isFileProxy) {
      return null;
    }

    const filePath = (url.searchParams.get('file_path') || '').trim();
    return isProviderFileURL(filePath) ? filePath : null;
  } catch {
    return null;
  }
}

function normalizeProtectedImageElement(img: HTMLImageElement): string | null {
  const protectedSrc = providerSourceFromImageSrc(
    img.getAttribute('data-protected-src') || '',
  );
  const src = img.getAttribute('src') || '';
  const sourceURL = protectedSrc || providerSourceFromImageSrc(src);
  if (!sourceURL) {
    return null;
  }

  img.setAttribute('data-protected-src', sourceURL);
  if (!src.trim().startsWith('blob:')) {
    img.setAttribute('src', PROVIDER_IMAGE_PLACEHOLDER);
  }
  return sourceURL;
}

/**
 * 转义 HTML 特殊字符
 * @param text 需要转义的文本
 * @returns 转义后的文本
 */
export function escapeHTML(text: string): string {
  if (!text || typeof text !== 'string') {
    return '';
  }
  
  const map: { [key: string]: string } = {
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#x27;',
    '/': '&#x2F;',
    '`': '&#x60;',
    '=': '&#x3D;'
  };
  
  return text.replace(/[&<>"'`=\/]/g, (s) => map[s]);
}

/**
 * 验证 URL 是否安全
 * @param url 需要验证的 URL
 * @returns 是否为安全 URL
 */
export function isValidURL(url: string): boolean {
  if (!url || typeof url !== 'string') {
    return false;
  }
  const trimmed = url.trim();
  if (!trimmed) {
    return false;
  }

  // 允许以 / 开头的站内相对路径（如本地存储 /files/images/xxx.jpg）
  if (trimmed.startsWith('/') && !trimmed.startsWith('//')) {
    return true;
  }

  // 允许 provider:// 形式，由前端后续鉴权拉取并替换为 blob URL
  if (isProviderFileURL(trimmed)) {
    return true;
  }
  
  try {
    const urlObj = new URL(trimmed);
    return ['http:', 'https:'].includes(urlObj.protocol);
  } catch {
    return false;
  }
}

/**
 * 安全地处理 Markdown 内容
 * @param markdown Markdown 文本
 * @returns 安全的 HTML 字符串
 */
export function safeMarkdownToHTML(markdown: string): string {
  if (!markdown || typeof markdown !== 'string') {
    return '';
  }
  
  // 首先转义可能的 HTML 标签
  const escapedMarkdown = markdown
    .replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, '')
    .replace(/<iframe\b[^<]*(?:(?!<\/iframe>)<[^<]*)*<\/iframe>/gi, '')
    .replace(/<object\b[^<]*(?:(?!<\/object>)<[^<]*)*<\/object>/gi, '')
    .replace(/<embed\b[^<]*(?:(?!<\/embed>)<[^<]*)*<\/embed>/gi, '');
  
  return escapedMarkdown;
}

/**
 * 清理用户输入
 * @param input 用户输入
 * @returns 清理后的安全输入
 */
export function sanitizeUserInput(input: string): string {
  if (!input || typeof input !== 'string') {
    return '';
  }
  
  // 移除控制字符
  let cleaned = input.replace(/[\x00-\x1F\x7F-\x9F]/g, '');
  
  // 限制长度
  if (cleaned.length > 10000) {
    cleaned = cleaned.substring(0, 10000);
  }
  
  return cleaned.trim();
}

/**
 * 验证图片 URL 是否安全
 * @param url 图片 URL
 * @returns 是否为安全的图片 URL
 */
export function isValidImageURL(url: string): boolean {
  if (!isValidURL(url)) {
    return false;
  }
  
  return true;
}

/**
 * 创建安全的图片元素
 * @param src 图片源
 * @param alt 替代文本
 * @param title 标题
 * @returns 安全的图片 HTML
 */
export function createSafeImage(src: string, alt: string = '', title: string = ''): string {
  if (!isValidImageURL(src)) {
    return '';
  }
  
  // src is validated by isValidImageURL; keep URL structure unchanged.
  // Only escape quotes to avoid breaking attributes.
  const safeSrc = src.replace(/"/g, '&quot;');
  const safeAlt = escapeHTML(alt);
  const safeTitle = escapeHTML(title);
  
  return `<img src="${safeSrc}" alt="${safeAlt}" title="${safeTitle}" class="markdown-image" style="max-width: 100%; height: auto;">`;
}

type ProtectedFileLoadResult =
  | { status: 'loaded'; blobURL: string }
  | { status: 'missing' }
  | { status: 'failed' };

type ProtectedFileCacheState = {
  blobByRequest: Map<string, string>;
  blobBySource: Map<string, string>;
  missingSources: Set<string>;
  failures: Map<string, number>;
  inflight: Map<string, Promise<ProtectedFileLoadResult>>;
};

// Keep object URLs alive across Vite hot updates. A hot update replaces this
// module but not the page document, so module-local Maps would forget valid
// blob URLs and make already-loaded images fall back to the 1x1 placeholder.
const protectedFileCacheState = (() => {
  const fresh = (): ProtectedFileCacheState => ({
    blobByRequest: new Map(),
    blobBySource: new Map(),
    missingSources: new Set(),
    failures: new Map(),
    inflight: new Map(),
  });
  if (typeof window === 'undefined') return fresh();
  const scope = window as typeof window & {
    __weknoraProtectedFileCacheV1__?: ProtectedFileCacheState;
  };
  scope.__weknoraProtectedFileCacheV1__ ||= fresh();
  return scope.__weknoraProtectedFileCacheV1__;
})();

const protectedFileBlobCache = protectedFileCacheState.blobByRequest;
// Blob URL keyed by the protected source URL (e.g. `local://...`). Once an image
// has been hydrated, re-renders of the same markdown can emit the blob src
// directly instead of the placeholder. Without this, the typewriter re-renders
// the answer every frame, recreating each <img> as a placeholder that hydration
// only restores a microtask later — which reads as a per-frame flicker.
const protectedFileBlobBySource = protectedFileCacheState.blobBySource;
const protectedFileMissingSources = protectedFileCacheState.missingSources;
// Throttle retries of failed file fetches. During streaming the same markdown
// is re-rendered on every chunk, producing brand-new <img> elements (so the
// per-element `authHydrated` flag is reset each time). Without throttling a
// not-yet-generated file (404) would be re-requested on every chunk. We record
// the last failure time per URL and skip re-fetching within a cooldown window,
// while still allowing a later attempt once the file becomes available.
const protectedFileFailureCache = protectedFileCacheState.failures;
const protectedFileInflight = protectedFileCacheState.inflight;
const PROTECTED_FILE_RETRY_COOLDOWN_MS = 5000;

function getProtectedFileRequestHeaders(): Record<string, string> {
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
 * 将 Markdown 里通过 /files 代理的图片，改为用带鉴权 Header 的 fetch 拉取后再显示。
 * 用于避免在 URL 中暴露 token。
 */
/**
 * 清除失败重试冷却记录。在流式结束等场景调用，让此前因文件尚未生成而 404
 * 的图片可以立即重新尝试加载，而无需等待冷却窗口结束。
 */
export function clearProtectedFileFailureCache(): void {
  protectedFileFailureCache.clear();
  protectedFileMissingSources.clear();
}

function protectedImageSource(img: HTMLImageElement): string {
  return normalizeProtectedImageElement(img)
    || (img.getAttribute('data-protected-src') || '').trim()
    || (img.getAttribute('src') || '').trim();
}

function forEachProtectedImageWithSource(
  root: ParentNode,
  sourceURL: string,
  callback: (img: HTMLImageElement) => void,
): void {
  root.querySelectorAll<HTMLImageElement>('img[data-protected-src]').forEach((candidate) => {
    if (protectedImageSource(candidate) === sourceURL) callback(candidate);
  });
}

function removeMissingProtectedImages(root: ParentNode, sourceURL: string): void {
  forEachProtectedImageWithSource(root, sourceURL, (img) => {
    const parent = img.parentElement;
    img.remove();
    // Markdown emits a dedicated paragraph for a standalone image. Remove that
    // wrapper too so a missing image leaves no vertical placeholder/gap.
    if (parent?.tagName === 'P' && !parent.textContent?.trim() && parent.children.length === 0) {
      parent.remove();
    }
  });
}

function applyHydratedProtectedImage(root: ParentNode, sourceURL: string, blobURL: string): void {
  forEachProtectedImageWithSource(root, sourceURL, (img) => {
    img.src = blobURL;
    img.dataset.authHydrated = '1';
    img.removeAttribute('data-img-loading');
  });
}

export async function hydrateProtectedFileImages(
  root: ParentNode | null | undefined,
  embed?: { channelId: string; token: string },
  kbId?: string,
): Promise<void> {
  if (!root || typeof window === 'undefined') {
    return;
  }

  const images = root.querySelectorAll<HTMLImageElement>(
    'img[data-protected-src], img[src^="local://"], img[src^="minio://"], img[src^="cos://"], img[src^="tos://"], img[src^="s3://"], img[src^="oss://"], img[src^="ks3://"], img[src^="obs://"]',
  );
  if (!images.length) {
    return;
  }

  // Embed visitors carry no Bearer/tenant context; route through the
  // embed-scoped file proxy (auth via the Embed header, tenant from the channel).
  const headers = embed
    ? { Authorization: `Embed ${embed.token}` }
    : getProtectedFileRequestHeaders();

  await Promise.all(Array.from(images).map(async (img) => {
    const normalizedSourceURL = normalizeProtectedImageElement(img);
    const protectedSrc = (img.getAttribute('data-protected-src') || '').trim();
    const src = (img.getAttribute('src') || '').trim();
    const sourceURL = normalizedSourceURL || protectedSrc || src;
    if (!sourceURL) {
      return;
    }
    if (img.dataset.authHydrated === '1') {
      return;
    }
    if (protectedFileMissingSources.has(sourceURL)) {
      removeMissingProtectedImages(root, sourceURL);
      return;
    }
    img.dataset.authHydrated = '1';

    const isProviderScheme = isProviderFileURL(sourceURL);
    // When a KB context is known, route through the KB-scoped proxy. It is
    // authorized via RequireKBAccess (own / org-shared / agent-visible) and
    // serves objects owned by the KB's source tenant — so images in a shared
    // KB (local://<owner-tenant>/...) load for the borrowing tenant, which the
    // tenant-scoped /files route rejects as a cross-tenant path.
    const fileProxyBase = embed
      ? `/api/v1/embed/${embed.channelId}/files`
      : kbId
        ? `/api/v1/knowledge-bases/${encodeURIComponent(kbId)}/files`
        : '/files';
    const requestURL = isProviderScheme
      ? `${fileProxyBase}?${new URLSearchParams({ file_path: sourceURL }).toString()}`
      : sourceURL;

    const isProxyRequest =
      requestURL.includes('file_path=') &&
      (requestURL.startsWith('/files?') ||
        /^\/api\/v1\/knowledge-bases\/[^/]+\/files\?/.test(requestURL) ||
        /^\/api\/v1\/embed\/[^/]+\/files\?/.test(requestURL));
    if (!isProxyRequest) {
      img.dataset.authHydrated = '0';
      return;
    }

    const cachedBlobURL = protectedFileBlobCache.get(requestURL);
    if (cachedBlobURL) {
      applyHydratedProtectedImage(root, sourceURL, cachedBlobURL);
      return;
    }

    const lastFailure = protectedFileFailureCache.get(requestURL);
    if (lastFailure !== undefined && Date.now() - lastFailure < PROTECTED_FILE_RETRY_COOLDOWN_MS) {
      img.dataset.authHydrated = '0';
      return;
    }

    // Every component that references the same image awaits the shared task.
    // The previous Set-based de-dupe made later components return immediately;
    // only the component that started the fetch was updated, leaving all other
    // occurrences stuck on the transparent placeholder forever.
    let loadTask = protectedFileInflight.get(requestURL);
    if (!loadTask) {
      loadTask = (async (): Promise<ProtectedFileLoadResult> => {
        try {
          const resp = await fetch(requestURL, {
            method: 'GET',
            headers,
            credentials: 'include',
          });
          if (!resp.ok) {
            if (resp.status === 404) {
              protectedFileFailureCache.set(requestURL, Date.now());
              return { status: 'missing' };
            }
            throw new Error(`HTTP ${resp.status}`);
          }
          const blob = await resp.blob();
          const blobURL = URL.createObjectURL(blob);
          protectedFileBlobCache.set(requestURL, blobURL);
          protectedFileFailureCache.delete(requestURL);
          return { status: 'loaded', blobURL };
        } catch (error) {
          console.warn('[security] hydrateProtectedFileImages failed:', error);
          protectedFileFailureCache.set(requestURL, Date.now());
          return { status: 'failed' };
        } finally {
          protectedFileInflight.delete(requestURL);
        }
      })();
      protectedFileInflight.set(requestURL, loadTask);
    }

    const result = await loadTask;
    if (result.status === 'loaded') {
      protectedFileBlobBySource.set(sourceURL, result.blobURL);
      protectedFileMissingSources.delete(sourceURL);
      applyHydratedProtectedImage(root, sourceURL, result.blobURL);
      return;
    }
    if (result.status === 'missing') {
      protectedFileMissingSources.add(sourceURL);
      removeMissingProtectedImages(root, sourceURL);
      return;
    }
    if (result.status === 'failed') {
      img.dataset.authHydrated = '0';
    }
  }));
}
