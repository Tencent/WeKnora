/**
 * 安全工具类 - 防止 XSS 攻击
 */

import DOMPurify from 'dompurify';

const PROVIDER_IMAGE_PLACEHOLDER = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==';

// 配置 DOMPurify 的安全策略
const DOMPurifyConfig = {
  // 允许的标签
  ALLOWED_TAGS: [
    'p', 'br', 'strong', 'em', 'u', 's', 'del', 'ins',
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
    'ul', 'ol', 'li', 'blockquote', 'pre', 'code',
    'a', 'img', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
    'div', 'span', 'figure', 'figcaption', 'think',
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
    'href', 'title', 'alt', 'src', 'class', 'id', 'style', 'data-protected-src',
    'target', 'rel', 'width', 'height',
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
  // 允许的协议
  ALLOWED_URI_REGEXP: /^(?:(?:(?:f|ht)tps?|mailto|tel|callto|cid|xmpp):|(?:local|minio|cos|tos|s3|oss|ks3):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i,
  // 禁止的标签和属性
  FORBID_TAGS: ['script', 'object', 'embed', 'form', 'input', 'button'],
  FORBID_ATTR: ['onerror', 'onload', 'onclick', 'onmouseover', 'onfocus', 'onblur'],
  // 其他安全配置
  KEEP_CONTENT: true,
  RETURN_DOM: false,
  RETURN_DOM_FRAGMENT: false,
  RETURN_DOM_IMPORT: false,
  SANITIZE_DOM: true,
  SANITIZE_NAMED_PROPS: true,
  WHOLE_DOCUMENT: false,
  // 自定义钩子函数
  HOOKS: {
    // 在清理前处理
    beforeSanitizeElements: (currentNode: Element) => {
      // 移除所有 script 标签
      if (currentNode.tagName === 'SCRIPT') {
        currentNode.remove();
        return null;
      }
      // 移除所有事件处理器
      const eventAttrs = ['onclick', 'onload', 'onerror', 'onmouseover', 'onfocus', 'onblur'];
      eventAttrs.forEach(attr => {
        if (currentNode.hasAttribute(attr)) {
          currentNode.removeAttribute(attr);
        }
      });
    },
    // 在清理后处理
    afterSanitizeElements: (currentNode: Element) => {
      // 确保所有链接都有 rel="noopener noreferrer"
      if (currentNode.tagName === 'A') {
        const href = currentNode.getAttribute('href');
        if (href && href.startsWith('http')) {
          currentNode.setAttribute('rel', 'noopener noreferrer');
          currentNode.setAttribute('target', '_blank');
        }
      }
      // 确保所有图片都有 alt 属性
      if (currentNode.tagName === 'IMG') {
        if (!currentNode.getAttribute('alt')) {
          currentNode.setAttribute('alt', '');
        }
      }
    }
  }
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
    return DOMPurify.sanitize(preparedHTML, DOMPurifyConfig);
  } catch (error) {
    console.error('HTML sanitization failed:', error);
    // 如果清理失败，返回转义的纯文本
    return escapeHTML(html);
  }
}

function protectProviderImageSrcInHTML(html: string): string {
  if (!html) return html;
  const decodeProviderURL = (raw: string): string =>
    raw
      .replace(/&#x2f;/gi, '/')
      .replace(/&#47;/g, '/')
      .replace(/&amp;/g, '&')
      .replace(/&quot;/g, '"');
  return html.replace(
    /<img\b([^>]*?)\ssrc=(["'])(local|minio|cos|tos|s3|oss|ks3):(?:\/\/|&#x2f;&#x2f;|&#47;&#47;)([^"']+)\2([^>]*)>/gi,
    (_m, before, quote, provider, restPathRaw, after) => {
      const restPath = decodeProviderURL(restPathRaw);
      const protectedSrc = `${provider}://${restPath}`;
      const fileProxyURL = `/files?${new URLSearchParams({ file_path: protectedSrc }).toString()}`;
      return `<img${before} src=${quote}${fileProxyURL}${quote} data-protected-src=${quote}${protectedSrc}${quote}${after}>`;
    },
  );
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
  if (/^(local|minio|cos|tos|s3|oss|ks3):\/\/\S+$/i.test(trimmed)) {
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

const protectedFileBlobCache = new Map<string, string>();

function getProtectedFileRequestHeaders(): Record<string, string> {
  const headers: Record<string, string> = {};
  try {
    const token = (localStorage.getItem('weknora_token') || '').trim();
    if (token) {
      headers['Authorization'] = `Bearer ${token}`;
    }

    const selectedTenantId = (localStorage.getItem('weknora_selected_tenant_id') || '').trim();
    const tenantRaw = localStorage.getItem('weknora_tenant');
    if (selectedTenantId) {
      try {
        const tenant = tenantRaw ? JSON.parse(tenantRaw) : null;
        const defaultTenantId = tenant?.id ? String(tenant.id) : '';
        if (selectedTenantId !== defaultTenantId) {
          headers['X-Tenant-ID'] = selectedTenantId;
        }
      } catch {
        // ignore tenant parse error and skip X-Tenant-ID
      }
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
export async function hydrateProtectedFileImages(root: ParentNode | null | undefined): Promise<void> {
  if (!root || typeof window === 'undefined') {
    return;
  }

  const images = root.querySelectorAll<HTMLImageElement>(
    'img[data-protected-src], img[src^="local://"], img[src^="minio://"], img[src^="cos://"], img[src^="tos://"], img[src^="s3://"], img[src^="oss://"], img[src^="ks3://"]',
  );
  if (!images.length) {
    return;
  }

  const headers = getProtectedFileRequestHeaders();

  await Promise.all(Array.from(images).map(async (img) => {
    const protectedSrc = (img.getAttribute('data-protected-src') || '').trim();
    const src = (img.getAttribute('src') || '').trim();
    const sourceURL = protectedSrc || src;
    if (!sourceURL) {
      return;
    }
    if (img.dataset.authHydrated === '1') {
      return;
    }
    img.dataset.authHydrated = '1';

    const isProviderScheme = /^(local|minio|cos|tos|s3|oss|ks3):\/\//.test(sourceURL);
    const requestURL = isProviderScheme
      ? `/files?${new URLSearchParams({ file_path: sourceURL }).toString()}`
      : sourceURL;

    if (!requestURL.startsWith('/files?') || !requestURL.includes('file_path=')) {
      img.dataset.authHydrated = '0';
      return;
    }

    const cachedBlobURL = protectedFileBlobCache.get(requestURL);
    if (cachedBlobURL) {
      img.src = cachedBlobURL;
      return;
    }

    try {
      const resp = await fetch(requestURL, {
        method: 'GET',
        headers,
        credentials: 'include',
      });
      if (!resp.ok) {
        throw new Error(`HTTP ${resp.status}`);
      }
      const blob = await resp.blob();
      const blobURL = URL.createObjectURL(blob);
      protectedFileBlobCache.set(requestURL, blobURL);
      img.src = blobURL;
      if (protectedSrc) {
        img.removeAttribute('data-protected-src');
      }
    } catch (error) {
      console.warn('[security] hydrateProtectedFileImages failed:', error);
      img.dataset.authHydrated = '0';
    }
  }));
}
