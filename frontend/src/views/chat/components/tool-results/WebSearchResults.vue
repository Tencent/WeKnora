<template>
  <div class="web-search-results">
    <!-- Grouped Results List -->
    <div v-if="groupedResults && groupedResults.length > 0" class="results-groups">
      <div 
        v-for="group in groupedResults" 
        :key="group.key"
        class="results-group"
      >
        <!-- <div class="group-header">
          <span class="group-intro">{{ $t('chat.webGroupIntro', { count: group.items.length }) }}</span>
          <span class="group-source">{{ group.label }}</span>
        </div> -->
        <div class="results-list">
          <div 
            v-for="result in group.items" 
            :key="result.result_index"
            class="result-item"
          >
            <div class="result-header">
              <div class="result-index">#{{ result.result_index }}</div>
              <a 
                v-if="result.url"
                :href="result.url" 
                :title="result.url"
                target="_blank" 
                rel="noopener noreferrer"
                class="result-title-link one-line"
              >
                <span class="result-title">{{ result.title }}</span>
              </a>
              <div v-else class="result-title-text one-line">
                <span class="result-title">{{ result.title }}</span>
              </div>
            </div>
            
            <div v-if="result.published_at" class="result-meta">
              <span class="meta-item">
                <t-icon name="time" class="meta-icon" />
                {{ formatDate(result.published_at) }}
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
    
    <!-- Empty State -->
    <div v-else class="empty-state">
      {{ $t('chat.webSearchNoResults') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { WebSearchResultsData, WebSearchResultItem } from '@/types/tool-results';
import { useI18n } from 'vue-i18n';

interface Props {
  data: WebSearchResultsData;
}

const props = defineProps<Props>();
const { t, locale } = useI18n();

const results = computed(() => props.data.results || []);

// Group results by source first, then by domain if source is missing
type Group = { key: string; label: string; items: WebSearchResultItem[] };
const groupedResults = computed<Group[]>(() => {
  const list = results.value || [];
  const groupsMap: Record<string, Group> = {};
  for (const item of list) {
    const source = (item as any).source as string | undefined;
    let key = '';
    let label = '';
    if (source && source.trim()) {
      key = `src:${source.trim()}`;
      label = source.trim();
    } else {
      // fallback to domain
      const url = (item as any).url as string | undefined;
      const hostname = url ? safeHostname(url) : t('chat.otherSource');
      key = `dom:${hostname}`;
      label = hostname;
    }
    if (!groupsMap[key]) {
      groupsMap[key] = { key, label, items: [] };
    }
    groupsMap[key].items.push(item);
  }
  // Keep original order by first occurrence
  const ordered: Group[] = [];
  const seen = new Set<string>();
  for (const item of list) {
    const source = (item as any).source as string | undefined;
    const url = (item as any).url as string | undefined;
    const hostname = url ? safeHostname(url) : t('chat.otherSource');
    const key = source && source.trim() ? `src:${source.trim()}` : `dom:${hostname}`;
    if (!seen.has(key)) {
      seen.add(key);
      if (groupsMap[key]) ordered.push(groupsMap[key]);
    }
  }
  return ordered;
});

const formatUrl = (url: string): string => {
  try {
    const urlObj = new URL(url);
    return urlObj.hostname + urlObj.pathname;
  } catch {
    return url;
  }
};

const safeHostname = (url: string): string => {
  try {
    const urlObj = new URL(url);
    return urlObj.hostname;
  } catch {
    return t('chat.otherSource');
  }
};

const truncateContent = (content: string, maxLength: number = 300): string => {
  if (!content) return '';
  if (content.length <= maxLength) return content;
  return content.substring(0, maxLength) + '...';
};

const formatDate = (dateStr: string): string => {
  try {
    const date = new Date(dateStr);
    return date.toLocaleDateString(locale.value || 'zh-CN', {
      year: 'numeric',
      month: 'long',
      day: 'numeric'
    });
  } catch {
    return dateStr;
  }
};
</script>

<style lang="less" scoped>
@import './tool-results.less';

.web-search-results {
  display: flex;
  flex-direction: column;
  padding: 0 0 0 12px;
  gap: 4px;
}

.results-groups {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.results-group {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.group-header {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  /* Align with title start (after index column) */
  padding-left: 34px;
}

.group-intro {
  color: var(--td-text-color-secondary);
}

.group-source {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-primary);
  font-weight: 600;
}

.group-count {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.results-list {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.result-item {
  background: var(--td-bg-color-container);
  border: none;
  border-radius: 0;
  transition: none;
}

.result-header {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 0;
  padding: 2px 0;
  
  :deep(a) {
    pointer-events: auto;
  }
}

.result-index {
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
  min-width: 24px;
  text-align: right;
}

.result-title-link {
  display: flex;
  align-items: baseline;
  gap: 6px;
  flex: 1;
  text-decoration: none;
  color: var(--td-text-color-primary);
  transition: color 0.15s ease;

  &:hover {
    color: var(--td-brand-color);
    
    .result-title {
      text-decoration: underline;
    }
  }
}

.result-title {
  font-size: 12px;
  font-weight: 500;
  line-height: 1.4;
  color: inherit;
  flex: 1;
  word-break: break-word;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.one-line {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.result-title-text {
  display: flex;
  align-items: baseline;
  flex: 1;
  
  .result-title {
    font-size: 12px;
    font-weight: 500;
    line-height: 1.4;
    color: var(--td-text-color-primary);
  }
}

.result-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 4px;
  padding-top: 4px;
  border-top: 1px solid var(--td-bg-color-secondarycontainer);
  font-size: 10px;
  color: var(--td-text-color-placeholder);
  
  .meta-item {
    display: flex;
    align-items: center;
    gap: 3px;
  }
  
  .meta-icon {
    font-size: 10px;
  }
}

.empty-state {
  padding: 16px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-style: italic;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  border: 1px dashed var(--td-component-stroke);
}
</style>

