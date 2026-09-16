import i18n from '@/i18n';
import { buildMermaidBlockHtml, buildMermaidLoadingHtml } from '@/utils/markdownEnhancements';
import {
  injectCachedMermaidSvg as injectCachedMermaidSvgHtml,
  maskMermaidBlocksForStreaming as maskMermaidBlocks,
  prepareStreamingMermaidMarkdown as prepareStreamingMermaid,
  replaceIncompleteMermaidWithPlaceholder as replaceIncompleteMermaid,
  type CachedMermaidSvgHtml,
} from './mermaidStreaming';

export type { CachedMermaidSvgHtml };
export {
  extractFirstMermaidCode,
  extractMermaidCodes,
  hasCompleteMermaidBlock,
  normalizeCachedMermaidSvgs,
} from './mermaidStreaming';

const STREAMING_IMAGE_PLACEHOLDER = '<span class="streaming-image-loading"><span class="streaming-image-loading__skeleton"></span></span>';
const STREAMING_MERMAID_PLACEHOLDER = buildMermaidLoadingHtml();

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
  return replaceIncompleteMermaid(content, STREAMING_MERMAID_PLACEHOLDER);
};

/** Keep every mermaid fence as a stable placeholder; render SVG via cache injection. */
export const maskMermaidBlocksForStreaming = (content: string): string => {
  return maskMermaidBlocks(content, STREAMING_MERMAID_PLACEHOLDER);
};

/** While streaming: loading placeholder for mermaid; swap to cached SVG after sanitize. */
export const prepareStreamingMermaidMarkdown = (
  content: string,
  _cachedSvgHtml?: CachedMermaidSvgHtml,
): string => {
  return prepareStreamingMermaid(content, STREAMING_MERMAID_PLACEHOLDER);
};

/** Inject trusted Mermaid SVG after DOMPurify, 1:1 with complete mermaid fences. */
export const injectCachedMermaidSvg = (
  html: string,
  cachedSvgHtml: CachedMermaidSvgHtml,
): string => {
  return injectCachedMermaidSvgHtml(html, cachedSvgHtml, buildMermaidBlockHtml);
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
