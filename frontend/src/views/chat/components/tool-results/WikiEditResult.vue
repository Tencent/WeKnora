<template>
  <div class="wiki-edit-result">
    <div class="result-card wiki-card">
      <div class="result-header wiki-header">
        <div class="result-title">
          <span class="wiki-icon" :class="actionClass">{{ actionIcon }}</span>
          <span class="wiki-title-text">{{ headerTitle }}</span>
        </div>
        <div class="result-meta">
          <span class="action-badge" :class="actionClass">{{ actionLabel }}</span>
        </div>
      </div>
      <div class="result-content expanded">
        <div class="info-section">
          <!-- wiki_write_page -->
          <template v-if="data.display_type === 'wiki_write_page'">
            <div class="info-field">
              <span class="field-label">{{ $t('chat.wikiFieldSlug') }}</span>
              <span class="field-value"><code>{{ data.slug }}</code></span>
            </div>
            <div class="info-field">
              <span class="field-label">{{ $t('chat.wikiFieldTitle') }}</span>
              <span class="field-value">{{ data.title }}</span>
            </div>
            <div class="info-field">
              <span class="field-label">{{ $t('chat.wikiFieldPageType') }}</span>
              <span class="field-value"><code>{{ data.page_type }}</code></span>
            </div>
            <div class="info-field">
              <span class="field-label">{{ $t('chat.wikiFieldSummary') }}</span>
              <span class="field-value">{{ data.summary }}</span>
            </div>
          </template>

          <!-- wiki_replace_text -->
          <template v-else-if="data.display_type === 'wiki_replace_text'">
            <div class="info-field">
              <span class="field-label">{{ $t('chat.wikiFieldSlug') }}</span>
              <span class="field-value"><code>{{ data.slug }}</code></span>
            </div>
            <div class="info-field" v-if="data.title">
              <span class="field-label">{{ $t('chat.wikiFieldTitle') }}</span>
              <span class="field-value">{{ data.title }}</span>
            </div>
            <div class="diff-block">
              <div class="diff-line diff-old">
                <span class="diff-marker">-</span>
                <span class="diff-text">{{ data.old_text }}</span>
              </div>
              <div class="diff-line diff-new">
                <span class="diff-marker">+</span>
                <span class="diff-text">{{ data.new_text }}</span>
              </div>
            </div>
          </template>

          <!-- wiki_rename_page -->
          <template v-else-if="data.display_type === 'wiki_rename_page'">
            <div class="info-field" v-if="data.title">
              <span class="field-label">{{ $t('chat.wikiFieldTitle') }}</span>
              <span class="field-value">{{ data.title }}</span>
            </div>
            <div class="rename-visual">
              <code class="slug-old">{{ data.old_slug }}</code>
              <span class="rename-arrow">→</span>
              <code class="slug-new">{{ data.new_slug }}</code>
            </div>
            <div class="info-field" v-if="data.updated_count > 0">
              <span class="field-label">{{ $t('chat.wikiFieldAffectedPages') }}</span>
              <span class="field-value">{{ $t('chat.wikiAffectedCount', { count: data.updated_count }) }}</span>
            </div>
            <div class="affected-list" v-if="data.affected_pages?.length">
              <code v-for="slug in data.affected_pages" :key="slug" class="affected-slug">{{ slug }}</code>
            </div>
          </template>

          <!-- wiki_delete_page -->
          <template v-else-if="data.display_type === 'wiki_delete_page'">
            <div class="info-field">
              <span class="field-label">{{ $t('chat.wikiFieldSlug') }}</span>
              <span class="field-value"><code>{{ data.slug }}</code></span>
            </div>
            <div class="info-field" v-if="data.title">
              <span class="field-label">{{ $t('chat.wikiFieldTitle') }}</span>
              <span class="field-value">{{ data.title }}</span>
            </div>
            <div class="info-field" v-if="data.updated_count > 0">
              <span class="field-label">{{ $t('chat.wikiFieldAffectedPages') }}</span>
              <span class="field-value">{{ $t('chat.wikiAffectedCount', { count: data.updated_count }) }}</span>
            </div>
            <div class="affected-list" v-if="data.affected_pages?.length">
              <code v-for="slug in data.affected_pages" :key="slug" class="affected-slug">{{ slug }}</code>
            </div>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import type { WikiEditData } from '@/types/tool-results';

const props = defineProps<{
  data: WikiEditData;
}>();

const { t } = useI18n();

const actionIcon = computed(() => {
  switch (props.data.display_type) {
    case 'wiki_write_page': return (props.data as any).action === 'created' ? '✦' : '✎';
    case 'wiki_replace_text': return '⇄';
    case 'wiki_rename_page': return '↻';
    case 'wiki_delete_page': return '✕';
    default: return '•';
  }
});

const actionClass = computed(() => {
  switch (props.data.display_type) {
    case 'wiki_write_page':
      return (props.data as any).action === 'created' ? 'created' : 'updated';
    case 'wiki_replace_text': return 'updated';
    case 'wiki_rename_page': return 'renamed';
    case 'wiki_delete_page': return 'deleted';
    default: return '';
  }
});

const actionLabel = computed(() => {
  switch (props.data.display_type) {
    case 'wiki_write_page':
      return (props.data as any).action === 'created'
        ? t('chat.wikiActionCreated')
        : t('chat.wikiActionUpdated');
    case 'wiki_replace_text': return t('chat.wikiActionUpdated');
    case 'wiki_rename_page': return t('chat.wikiActionRenamed');
    case 'wiki_delete_page': return t('chat.wikiActionDeleted');
    default: return '';
  }
});

const headerTitle = computed(() => {
  switch (props.data.display_type) {
    case 'wiki_write_page': return t('chat.wikiWritePageTitle');
    case 'wiki_replace_text': return t('chat.wikiReplaceTextTitle');
    case 'wiki_rename_page': return t('chat.wikiRenamePageTitle');
    case 'wiki_delete_page': return t('chat.wikiDeletePageTitle');
    default: return 'Wiki';
  }
});
</script>

<style lang="less" scoped>
@import './tool-results.less';

.wiki-edit-result {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.wiki-card {
  margin: 0 8px 8px 8px;
}

.wiki-header {
  .wiki-icon {
    font-size: 14px;
    width: 20px;
    height: 20px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 4px;
    flex-shrink: 0;

    &.created {
      color: var(--td-success-color);
      background: rgba(0, 168, 112, 0.1);
    }
    &.updated {
      color: var(--td-brand-color);
      background: rgba(7, 192, 95, 0.1);
    }
    &.renamed {
      color: var(--td-warning-color);
      background: rgba(255, 152, 0, 0.1);
    }
    &.deleted {
      color: var(--td-error-color);
      background: rgba(227, 77, 89, 0.1);
    }
  }

  .wiki-title-text {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }
}

.action-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 10px;
  line-height: 1.5;

  &.created {
    color: var(--td-success-color);
    background: rgba(0, 168, 112, 0.1);
    border: 1px solid rgba(0, 168, 112, 0.2);
  }
  &.updated {
    color: var(--td-brand-color);
    background: rgba(7, 192, 95, 0.1);
    border: 1px solid rgba(7, 192, 95, 0.2);
  }
  &.renamed {
    color: var(--td-warning-color);
    background: rgba(255, 152, 0, 0.1);
    border: 1px solid rgba(255, 152, 0, 0.2);
  }
  &.deleted {
    color: var(--td-error-color);
    background: rgba(227, 77, 89, 0.1);
    border: 1px solid rgba(227, 77, 89, 0.2);
  }
}

.diff-block {
  margin-top: 8px;
  border: 1px solid @card-border;
  border-radius: 4px;
  overflow: hidden;
  font-family: var(--app-font-family-mono);
  font-size: 12px;
}

.diff-line {
  display: flex;
  gap: 8px;
  padding: 6px 10px;
  line-height: 1.5;

  &.diff-old {
    background: rgba(227, 77, 89, 0.06);
    color: var(--td-error-color);
    border-bottom: 1px solid @card-border;
  }
  &.diff-new {
    background: rgba(0, 168, 112, 0.06);
    color: var(--td-success-color);
  }
}

.diff-marker {
  font-weight: 700;
  flex-shrink: 0;
  width: 14px;
  text-align: center;
}

.diff-text {
  word-break: break-word;
  color: var(--td-text-color-primary);
}

.rename-visual {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 0;
  flex-wrap: wrap;

  .slug-old {
    text-decoration: line-through;
    opacity: 0.6;
  }
  .slug-new {
    color: var(--td-success-color);
    font-weight: 600;
  }
  .rename-arrow {
    font-size: 16px;
    color: var(--td-text-color-placeholder);
  }
}

.affected-list {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 6px;
  padding-left: 90px;

  .affected-slug {
    font-size: 11px;
    padding: 2px 6px;
    border-radius: 3px;
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid @card-border;
  }
}

code {
  font-family: var(--app-font-family-mono);
  font-size: 11px;
  background: var(--td-bg-color-secondarycontainer);
  padding: 2px 5px;
  border-radius: 3px;
  color: var(--td-text-color-primary);
}
</style>
