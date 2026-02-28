/**
 * 安全工具类 - 防止 XSS 攻击
 */

import DOMPurify from 'dompurify';

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
    'image', 'foreignobject', 'desc', 'title', 'switch', 'symbol', 'mask'
  ],
  // 允许的属性
  ALLOWED_ATTR: [
    'href', 'title', 'alt', 'src', 'class', 'id', 'style',
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
    'display', 'pointer-events', 'cursor', 'data-emit', 'direction'
  ],
  // 允许的协议
  ALLOWED_URI_REGEXP: /^(?:(?:(?:f|ht)tps?|mailto|tel|callto|cid|xmpp):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i,
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
    return DOMPurify.sanitize(html, DOMPurifyConfig);
  } catch (error) {
    console.error('HTML sanitization failed:', error);
    // 如果清理失败，返回转义的纯文本
    return escapeHTML(html);
  }
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

  // 允许以 / 开头的站内相对路径（如本地存储 /files/images/xxx.jpg）
  if (url.startsWith('/') && !url.startsWith('//')) {
    return true;
  }
  
  try {
    const urlObj = new URL(url);
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
  
  const safeSrc = escapeHTML(src);
  const safeAlt = escapeHTML(alt);
  const safeTitle = escapeHTML(title);
  
  return `<img src="${safeSrc}" alt="${safeAlt}" title="${safeTitle}" class="markdown-image" style="max-width: 100%; height: auto;">`;
}
