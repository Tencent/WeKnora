export const domPurifyForbidTags = ['script', 'style', 'object', 'embed', 'form', 'input'] as const;
export const domPurifyForbidAttr = ['onerror', 'onload', 'onclick', 'onmouseover', 'onfocus', 'onblur'] as const;

export const domPurifyAllowedUriRegexp =
  /^(?:(?:(?:f|ht)tps?|mailto|tel|callto|cid|xmpp):|(?:local|minio|cos|tos|s3|oss|ks3|obs):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i;

/** Shared DOMPurify security options (FORBID_*, URI scheme, DOM flags). */
export const domPurifySecurityOptions = {
  ALLOWED_URI_REGEXP: domPurifyAllowedUriRegexp,
  FORBID_TAGS: [...domPurifyForbidTags],
  FORBID_ATTR: [...domPurifyForbidAttr],
  KEEP_CONTENT: true,
  RETURN_DOM: false,
  RETURN_DOM_FRAGMENT: false,
  RETURN_DOM_IMPORT: false,
  SANITIZE_DOM: true,
  SANITIZE_NAMED_PROPS: true,
  WHOLE_DOCUMENT: false,
} as const;

/** Shared DOMPurify hooks: strip scripts/event attrs; noopener links; alt on images. */
export const domPurifySecurityHooks = {
  beforeSanitizeElements: (currentNode: Element) => {
    if (currentNode.tagName === 'SCRIPT') {
      currentNode.remove();
      return null;
    }
    domPurifyForbidAttr.forEach((attr) => {
      if (currentNode.hasAttribute(attr)) {
        currentNode.removeAttribute(attr);
      }
    });
  },
  afterSanitizeElements: (currentNode: Element) => {
    if (currentNode.tagName === 'A') {
      const href = currentNode.getAttribute('href');
      if (href && href.startsWith('http')) {
        currentNode.setAttribute('rel', 'noopener noreferrer');
        currentNode.setAttribute('target', '_blank');
      }
    }
    if (currentNode.tagName === 'IMG' && !currentNode.getAttribute('alt')) {
      currentNode.setAttribute('alt', '');
    }
  },
};

/** Shared DOMPurify config for chat / dev markdown (KaTeX + Mermaid SVG). */
export const markdownDomPurifyConfig = {
  ALLOWED_TAGS: [
    'p', 'br', 'strong', 'em', 'u', 'code', 'pre', 'ul', 'ol', 'li', 'blockquote',
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'a', 'span', 'table', 'thead', 'tbody',
    'tr', 'th', 'td', 'img', 'figure', 'figcaption', 'div',
    'svg', 'g', 'path', 'rect', 'circle', 'ellipse', 'line', 'polygon',
    'polyline', 'text', 'tspan', 'defs', 'marker', 'filter', 'use',
    'clippath', 'lineargradient', 'radialgradient', 'stop', 'pattern',
    'image', 'foreignobject', 'desc', 'title', 'switch', 'symbol', 'mask',
    'math', 'annotation', 'semantics', 'mo', 'mi', 'mn', 'msup', 'mrow', 'mfrac', 'msqrt', 'mroot', 'mstyle',
    'button',
  ],
  ALLOWED_ATTR: [
    'href', 'title', 'target', 'rel', 'data-tooltip', 'data-url', 'data-kb-id',
    'data-chunk-id', 'data-doc', 'data-slug', 'class', 'role', 'tabindex', 'src', 'alt', 'data-protected-src',
    'width', 'height', 'style', 'id', 'type', 'aria-label', 'data-mermaid', 'disabled',
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
    'mathvariant', 'encoding', 'aria-hidden',
  ],
  USE_PROFILES: { html: true, svg: true, mathMl: true },
  ...domPurifySecurityOptions,
  HOOKS: domPurifySecurityHooks,
};
