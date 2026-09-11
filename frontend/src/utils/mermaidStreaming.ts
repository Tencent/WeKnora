export type CachedMermaidSvgHtml = string | readonly (string | null | undefined)[] | null | undefined;

// A cache slot holds a rendered SVG string, '' while unresolved (mermaid not
// loaded yet), or — encoded via this marker — a permanent render failure. A
// fence is only cached once it is complete and mermaid itself has loaded, so
// an encoded failure is a genuine parse/render error, not "still streaming".
// The marker is a control character that never appears in real SVG markup,
// so a slot's meaning is recoverable without widening CachedMermaidSvgHtml
// (still a plain string) through every caller of appendMermaidSvgCache.
const MERMAID_ERROR_MARKER = '\u0000mermaid-error\u0000';

export function encodeMermaidRenderError(code: string, message: string): string {
  return MERMAID_ERROR_MARKER + JSON.stringify({ code, message });
}

function decodeMermaidRenderError(value: string | null | undefined): { code: string; message: string } | null {
  if (typeof value !== 'string' || !value.startsWith(MERMAID_ERROR_MARKER)) return null;
  try {
    const parsed = JSON.parse(value.slice(MERMAID_ERROR_MARKER.length));
    if (parsed && typeof parsed.code === 'string' && typeof parsed.message === 'string') {
      return parsed;
    }
  } catch {
    // Malformed marker payload — treat as no diagnostic available.
  }
  return null;
}

const MERMAID_FENCE_START = '```mermaid';
const COMPLETE_MERMAID_FENCE_RE = /```mermaid[\s\S]*?```/;

export const replaceIncompleteMermaidWithPlaceholder = (
  content: string,
  placeholder: string,
): string => {
  if (!content) return '';

  const start = content.lastIndexOf(MERMAID_FENCE_START);
  if (start < 0) return content;

  const tail = content.slice(start + MERMAID_FENCE_START.length);
  if (tail.includes('```')) return content;

  return content.slice(0, start) + placeholder;
};

/** Keep every mermaid fence as a stable placeholder; render SVG via cache injection. */
export const maskMermaidBlocksForStreaming = (content: string, placeholder: string): string => {
  if (!content) return '';
  const masked = content.replace(/```mermaid[\s\S]*?```/g, placeholder);
  return replaceIncompleteMermaidWithPlaceholder(masked, placeholder);
};

export const hasCompleteMermaidBlock = (content: string): boolean => {
  return COMPLETE_MERMAID_FENCE_RE.test(content);
};

export const prepareStreamingMermaidMarkdown = (
  content: string,
  placeholder: string,
): string => {
  if (!content) return '';

  if (hasCompleteMermaidBlock(content)) {
    return maskMermaidBlocksForStreaming(content, placeholder);
  }

  return replaceIncompleteMermaidWithPlaceholder(content, placeholder);
};

export const extractMermaidCodes = (content: string): string[] => {
  if (!content) return [];
  const codes: string[] = [];
  const re = /```mermaid([\s\S]*?)```/g;
  let match: RegExpExecArray | null;
  while ((match = re.exec(content)) !== null) {
    // Keep blank fences: maskMermaidBlocksForStreaming emits one placeholder
    // per fence, and injectCachedMermaidSvg pairs them by position, so
    // dropping a fence here would shift every later diagram by one slot.
    codes.push(match[1].trim());
  }
  return codes;
};

export const extractFirstMermaidCode = (content: string): string | null => {
  return extractMermaidCodes(content)[0] || null;
};

export function normalizeCachedMermaidSvgs(cached: CachedMermaidSvgHtml): string[] {
  if (!cached) return [];
  if (typeof cached === 'string') return cached ? [cached] : [];
  return cached.map((svg) => svg || '');
}

// Require the loading modifier so a rendered/cached mermaid block cannot be
// paired with a later skeleton (the previous `chat-mermaid-block[^"]*` pattern
// swallowed the first diagram and everything after it).
const STREAMING_MERMAID_LOADING_RE =
  /<div class="chat-mermaid-block chat-mermaid-block--loading"[^>]*>[\s\S]*?<div class="streaming-mermaid-loading"[^>]*>[\s\S]*?<\/div>\s*<\/div>/g;

const MERMAID_UNRENDERED_CANVAS_RE =
  /<pre class="chat-mermaid-block__canvas mermaid"[^>]*data-mermaid="false"[^>]*>[\s\S]*?<\/pre>/g;

function replaceUnrenderedCanvas(
  match: string,
  innerHtml: string,
  dataMermaidValue: string = 'cached',
): string {
  const openEnd = match.indexOf('>');
  if (openEnd < 0) return match;
  const openTag = match
    .slice(0, openEnd + 1)
    .replace(/data-mermaid="false"/, `data-mermaid="${dataMermaidValue}"`);
  return `${openTag}${innerHtml}</pre>`;
}

/**
 * Inject trusted Mermaid SVG after DOMPurify, 1:1 with complete mermaid fences.
 *
 * A cache slot may also hold an encoded render failure (see
 * encodeMermaidRenderError) — a mermaid fence is only ever cached once it is
 * complete, so a failure here is a genuine syntax error, not "still
 * streaming"; that slot renders as a visible error (via buildErrorFragment)
 * instead of leaving the loading skeleton stuck forever.
 */
export const injectCachedMermaidSvg = (
  html: string,
  cachedSvgHtml: CachedMermaidSvgHtml,
  buildBlock: (innerHtml: string, preAttrs?: string) => string,
  buildErrorFragment: (code: string, message: string) => string,
): string => {
  if (!html) return html;
  const svgs = normalizeCachedMermaidSvgs(cachedSvgHtml);
  if (!svgs.some(Boolean)) return html;

  const index = { i: 0 };
  const withLoading = html.replace(STREAMING_MERMAID_LOADING_RE, (match) => {
    const svg = svgs[index.i++];
    const err = decodeMermaidRenderError(svg);
    if (err) return buildBlock(buildErrorFragment(err.code, err.message), 'data-mermaid="error"');
    return svg ? buildBlock(svg, 'data-mermaid="cached"') : match;
  });
  if (index.i >= svgs.length) return withLoading;
  return withLoading.replace(MERMAID_UNRENDERED_CANVAS_RE, (match) => {
    const svg = svgs[index.i++];
    const err = decodeMermaidRenderError(svg);
    if (err) return replaceUnrenderedCanvas(match, buildErrorFragment(err.code, err.message), 'error');
    return svg ? replaceUnrenderedCanvas(match, svg) : match;
  });
};
