import i18n from '@/i18n';
import { buildMermaidBlockHtml, buildMermaidLoadingHtml } from '@/utils/markdownEnhancements';

const STREAMING_IMAGE_PLACEHOLDER = '<span class="streaming-image-loading"><span class="streaming-image-loading__skeleton"></span></span>';
const STREAMING_MERMAID_PLACEHOLDER = buildMermaidLoadingHtml();
const MERMAID_FENCE_START = '```mermaid';

export const replaceIncompleteImageWithPlaceholder = (content: string): string => {
  if (!content) return '';

  const lastImgStart = content.lastIndexOf('![');
  if (lastImgStart < 0) return content;

  const tail = content.slice(lastImgStart);
  const hasImageOpen = tail.startsWith('![');
  const hasBracketClose = tail.includes(']');
  const hasParenOpen = tail.includes('(');
  const hasParenClose = tail.includes(')');
  if (!hasImageOpen) return content;

  // Incomplete image syntax at stream tail, e.g. ![alt](local://...
  if (!hasBracketClose || (hasParenOpen && !hasParenClose)) {
    return content.slice(0, lastImgStart) + STREAMING_IMAGE_PLACEHOLDER;
  }

  return content;
};

/** Hide an unclosed trailing ```mermaid fence while streaming to avoid layout jitter. */
export const replaceIncompleteMermaidWithPlaceholder = (content: string): string => {
  if (!content) return '';

  const start = content.lastIndexOf(MERMAID_FENCE_START);
  if (start < 0) return content;

  const tail = content.slice(start + MERMAID_FENCE_START.length);
  if (tail.includes('```')) return content;

  return content.slice(0, start) + STREAMING_MERMAID_PLACEHOLDER;
};

const MERMAID_BLOCK_RE = /```mermaid[\s\S]*?```/;

/** Keep mermaid as a stable placeholder for the whole stream; render once at the end. */
export const maskMermaidBlocksForStreaming = (content: string): string => {
  if (!content) return '';
  const masked = content.replace(MERMAID_BLOCK_RE, STREAMING_MERMAID_PLACEHOLDER);
  return replaceIncompleteMermaidWithPlaceholder(masked);
};

export const hasCompleteMermaidBlock = (content: string): boolean => {
  return MERMAID_BLOCK_RE.test(content);
};

/** While streaming: loading placeholder for mermaid; swap to cached SVG after sanitize. */
export const prepareStreamingMermaidMarkdown = (
  content: string,
  _cachedSvgHtml?: string | null,
): string => {
  if (!content) return '';

  if (hasCompleteMermaidBlock(content)) {
    return maskMermaidBlocksForStreaming(content);
  }

  return replaceIncompleteMermaidWithPlaceholder(content);
};

export const extractFirstMermaidCode = (content: string): string | null => {
  const match = content.match(/```mermaid([\s\S]*?)```/);
  return match?.[1]?.trim() || null;
};

const STREAMING_MERMAID_LOADING_RE = /<div class="chat-mermaid-block[^"]*"[^>]*>[\s\S]*?<div class="streaming-mermaid-loading"[^>]*>[\s\S]*?<\/div>[\s\S]*?<\/div>/g;

/** Inject trusted Mermaid SVG after DOMPurify, replacing the streaming loading shell. */
export const injectCachedMermaidSvg = (
  html: string,
  cachedSvgHtml: string | null | undefined,
): string => {
  if (!html || !cachedSvgHtml) return html;
  return html.replace(
    STREAMING_MERMAID_LOADING_RE,
    buildMermaidBlockHtml(cachedSvgHtml, 'data-mermaid="cached"'),
  );
};

export const formatManualTitle = (question?: string): string => {
  if (!question) {
    return i18n.global.t('chat.sessionExcerpt');
  }
  const condensed = question.replace(/\s+/g, ' ').trim();
  if (!condensed) {
    return i18n.global.t('chat.sessionExcerpt');
  }
  return condensed.length > 40 ? `${condensed.slice(0, 40)}...` : condensed;
};

export const buildManualMarkdown = (_question: string, answer: string): string => {
  const safeAnswer = answer?.trim() || i18n.global.t('chat.noAnswerContent');
  return `${safeAnswer}`;
};

export const copyTextToClipboard = async (content: string): Promise<void> => {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    await navigator.clipboard.writeText(content);
    return;
  }

  const textArea = document.createElement('textarea');
  textArea.value = content;
  textArea.style.position = 'fixed';
  textArea.style.opacity = '0';
  document.body.appendChild(textArea);
  textArea.select();
  document.execCommand('copy');
  document.body.removeChild(textArea);
};
