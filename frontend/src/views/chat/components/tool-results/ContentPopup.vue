<template>
  <div class="popup-content">
    <div class="popup-content-wrapper">
      <!-- Pre-rendered HTML (already sanitized upstream) -->
      <div v-if="isHtml" class="full-content html-content" v-html="processedContent"></div>

      <!-- Merged chunks from the same document -->
      <template v-else-if="blocks.length > 1">
        <div v-for="(block, idx) in blocks" :key="block.chunk_id || idx" class="chunk-block">
          <div class="chunk-block__label">{{ $t('chat.chunkOrdinal', { index: idx + 1 }) }}</div>
          <div class="full-content" v-html="block.html"></div>
        </div>
      </template>

      <!-- Single chunk -->
      <div v-else-if="blocks.length === 1" class="full-content" v-html="blocks[0].html"></div>
    </div>

    <div v-if="hasInfo" class="popup-footer">
      <span v-if="chunkId" class="popup-footer__item" :title="chunkId">
        <span class="popup-footer__key">{{ $t('chat.chunkIdLabel') }}</span>{{ chunkId }}
      </span>
      <span v-if="knowledgeId" class="popup-footer__item" :title="knowledgeId">
        <span class="popup-footer__key">{{ $t('chat.documentIdLabel') }}</span>{{ knowledgeId }}
      </span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { sanitizeHTML } from '@/utils/security';
import { highlightText, highlightRegex } from '@/components/GlobalCommandPalette/useHighlight';
import { cleanContent } from './contentClean';

interface ChunkContent {
  content: string;
  chunk_id?: string;
  knowledge_id?: string;
}

interface Props {
  content?: string;
  chunks?: ChunkContent[];
  chunkId?: string;
  knowledgeId?: string;
  highlight?: string;
  // When true, `highlight` is treated as a regex (e.g. grep's "a|b|c"),
  // otherwise as whitespace-separated literal terms.
  regex?: boolean;
  isHtml?: boolean;
}

const props = defineProps<Props>();

const hasInfo = computed(() => !!(props.chunkId || props.knowledgeId));

const processedContent = computed(() => {
  if (!props.content) return '';
  return props.isHtml ? sanitizeHTML(props.content) : props.content;
});

const renderBlock = (raw: string): string => {
  const cleaned = cleanContent(raw);
  const pattern = props.highlight ?? '';
  return props.regex ? highlightRegex(cleaned, pattern) : highlightText(cleaned, pattern);
};

const blocks = computed(() => {
  const source: ChunkContent[] = props.chunks?.length
    ? props.chunks
    : props.content
      ? [{ content: props.content, chunk_id: props.chunkId, knowledge_id: props.knowledgeId }]
      : [];
  return source
    .map((c) => ({ chunk_id: c.chunk_id, html: renderBlock(c.content) }))
    .filter((b) => b.html.trim() !== '');
});
</script>

<style lang="less" scoped>
.popup-content {
  display: flex;
  flex-direction: column;
  max-height: 420px;
  max-width: 500px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.12);
  word-wrap: break-word;
  word-break: break-word;
  overflow: hidden;

  .popup-content-wrapper {
    flex: 1;
    overflow-y: auto;
    overflow-x: hidden;
    padding: 14px;
    min-height: 0;
  }

  .full-content {
    font-size: 13px;
    color: var(--td-text-color-primary);
    line-height: 1.7;
    white-space: pre-wrap;
    word-break: break-word;

    &.html-content {
      white-space: normal;

      :deep(p) {
        margin: 8px 0;
        line-height: 1.7;
      }
    }
  }

  .chunk-block {
    & + & {
      margin-top: 12px;
      padding-top: 12px;
      border-top: 1px dashed var(--td-component-stroke);
    }

    .chunk-block__label {
      font-size: 11px;
      font-weight: 600;
      color: var(--td-text-color-placeholder);
      margin-bottom: 4px;
    }
  }

  :deep(.search-highlight) {
    background: rgba(255, 213, 0, 0.35);
    color: inherit;
    padding: 0 1px;
    border-radius: 2px;
    font-weight: 500;
  }

  .popup-footer {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 8px 14px;
    border-top: 1px solid var(--td-component-stroke);
  }

  .popup-footer__item {
    font-family: var(--app-font-family-mono);
    font-size: 10px;
    color: var(--td-text-color-placeholder);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .popup-footer__key {
    margin-right: 4px;
    font-family: inherit;
    color: var(--td-text-color-disabled);
  }
}
</style>
