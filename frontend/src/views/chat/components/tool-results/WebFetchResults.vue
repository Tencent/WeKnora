<template>
  <div class="web-fetch-results">
    <div v-if="items.length > 0" class="results-list">
      <div
        v-for="(item, index) in items"
        :key="indexKey(index, item)"
        class="result-card"
      >
        <div class="result-header" @click="toggleCard(index)">
          <div class="result-title">
            <span class="result-index">#{{ index + 1 }}</span>
            <a
              v-if="item.url"
              :href="item.url"
              class="result-link"
              target="_blank"
              rel="noopener noreferrer"
              @click.stop
            >
              <span class="result-domain">{{ safeHostname(item.url) }}</span>
            </a>
            <span v-else class="result-domain">{{ $t('chat.unknownLink') }}</span>
          </div>
          <div class="result-meta">
            <span
              v-if="itemStatus(item)"
              class="meta-pill"
              :class="statusClass(item)"
            >{{ statusLabel(item) }}</span>
            <span v-if="item.method" class="meta-pill method">{{ formatMethod(item.method) }}</span>
            <span v-if="item.content_length" class="meta-text">{{ $t('chat.contentLengthLabel', { value: formatLength(item.content_length) }) }}</span>
            <t-icon
              :name="isExpanded(index) ? 'chevron-up' : 'chevron-down'"
              class="expand-icon"
            />
          </div>
        </div>

        <div class="result-content" :class="{ expanded: isExpanded(index) }">
          <div class="info-section">
            <div class="info-field">
              <span class="field-label">URL</span>
              <span class="field-value">
                <a
                  v-if="item.url"
                  :href="item.url"
                  target="_blank"
                  rel="noopener noreferrer"
                >{{ item.url }}</a>
                <span v-else>{{ $t('chat.notProvided') }}</span>
              </span>
            </div>
            <div v-if="item.prompt" class="info-field">
              <span class="field-label">{{ $t('chat.promptLabel') }}</span>
              <span class="field-value">{{ item.prompt }}</span>
            </div>
          </div>

          <div v-if="itemError(item)" class="info-section">
            <div class="info-section-title error">{{ $t('chat.errorMessageLabel') }}</div>
            <div v-if="item.error_code" class="info-field">
              <span class="field-label">{{ $t('chat.webFetchErrorCode') }}</span>
              <span class="field-value">{{ item.error_code }}</span>
            </div>
            <div v-if="item.retryable !== undefined" class="info-field">
              <span class="field-label">{{ $t('chat.webFetchRetryable') }}</span>
              <span class="field-value">{{ item.retryable ? $t('common.yes') : $t('common.no') }}</span>
            </div>
            <div class="full-content error-text">{{ itemError(item) }}</div>
          </div>

          <div v-else-if="isSuccessItem(item)">
            <div v-if="item.summary" class="info-section">
              <div class="info-section-title">{{ $t('chat.summaryLabel') }}</div>
              <div class="full-content">{{ item.summary }}</div>
            </div>

            <div v-else-if="item.summary_status === 'failed'" class="info-section">
              <div class="info-section-title error">{{ $t('chat.webFetchSummaryFailed') }}</div>
              <div v-if="item.summary_error_code" class="info-field">
                <span class="field-label">{{ $t('chat.webFetchErrorCode') }}</span>
                <span class="field-value">{{ item.summary_error_code }}</span>
              </div>
              <div v-if="item.summary_error_message" class="full-content error-text">
                {{ item.summary_error_message }}
              </div>
            </div>

            <div v-if="item.raw_content" class="info-section">
              <div class="info-section-title">
                {{ $t('chat.rawTextLabel') }}
                <span class="raw-length" v-if="item.content_length">
                  （{{ formatLength(item.content_length) }}）
                </span>
              </div>
              <div v-if="isRawExpanded(index)" class="full-content">
                {{ item.raw_content }}
              </div>
              <div v-else class="content-preview">
                {{ truncate(item.raw_content) }}
              </div>
              <button class="action-button" @click.stop="toggleRaw(index)">
                {{ isRawExpanded(index) ? $t('chat.collapseRaw') : $t('chat.expandRaw') }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div v-else class="empty-state">{{ $t('chat.noWebContent') }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { WebFetchResultsData, WebFetchResultItem } from '@/types/tool-results';
import { useI18n } from 'vue-i18n';

interface Props {
  data: WebFetchResultsData;
}

const props = defineProps<Props>();
const { t } = useI18n();

const items = computed<WebFetchResultItem[]>(() => props.data.results || []);
const expandedCards = ref<Set<number>>(new Set());
const expandedRaw = ref<Record<number, boolean>>({});

watch(
  items,
  (list) => {
    const set = new Set<number>();
    list.forEach((_item, idx) => {
      set.add(idx);
    });
    expandedCards.value = set;
    expandedRaw.value = {};
  },
  { immediate: true }
);

const itemStatus = (item: WebFetchResultItem): string | undefined => item.status;

const isSuccessItem = (item: WebFetchResultItem): boolean => {
  return !item.status || item.status === 'success';
};

const itemError = (item: WebFetchResultItem): string => {
  return item.error_message || item.error || '';
};

const statusLabel = (item: WebFetchResultItem): string => {
  switch (item.status) {
    case 'failed':
      return t('chat.webFetchStatusFailed');
    case 'skipped':
      return t('chat.webFetchStatusSkipped');
    case 'success':
      return t('chat.webFetchStatusSuccess');
    default:
      return t('chat.webFetchStatusSuccess');
  }
};

const statusClass = (item: WebFetchResultItem): string => {
  switch (item.status) {
    case 'failed':
      return 'status-failed';
    case 'skipped':
      return 'status-skipped';
    default:
      return 'status-success';
  }
};

const toggleCard = (index: number) => {
  if (expandedCards.value.has(index)) {
    expandedCards.value.delete(index);
  } else {
    expandedCards.value.add(index);
  }
};

const isExpanded = (index: number): boolean => expandedCards.value.has(index);

const toggleRaw = (index: number) => {
  expandedRaw.value[index] = !expandedRaw.value[index];
};

const isRawExpanded = (index: number): boolean => !!expandedRaw.value[index];

const truncate = (content: string, maxLength = 480): string => {
  if (!content) return '';
  if (content.length <= maxLength) return content;
  return `${content.substring(0, maxLength)}…`;
};

const safeHostname = (url: string): string => {
  try {
    const urlObj = new URL(url);
    return urlObj.hostname;
  } catch {
    return url;
  }
};

const formatLength = (length: number): string => {
  if (!length || Number.isNaN(length)) return t('chat.lengthChars', { value: 0 });
  if (length >= 10000) {
    return t('chat.lengthTenThousands', { value: (length / 10000).toFixed(1) });
  }
  if (length >= 1000) {
    return t('chat.lengthThousands', { value: (length / 1000).toFixed(1) });
  }
  return t('chat.lengthChars', { value: length });
};

const formatMethod = (method: string): string => {
  if (!method) return '';
  if (method.toLowerCase() === 'chromedp') {
    return 'Chromedp';
  }
  if (method.toLowerCase() === 'http') {
    return 'HTTP';
  }
  return method;
};

const indexKey = (index: number, item: WebFetchResultItem): string => {
  return `${index}-${item.url || 'unknown'}`;
};
</script>

<style lang="less" scoped>
@import './tool-results.less';

.web-fetch-results {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 6px 6px 0 6px;
}

.results-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.result-index {
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
}

.result-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--td-text-color-primary);
  font-size: 12px;
  font-weight: 500;
  text-decoration: none;
  transition: color 0.15s ease;

  &:hover {
    color: var(--td-brand-color);
    text-decoration: underline;
  }
}

.result-domain {
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.meta-pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 6px;
  border-radius: 999px;
  font-size: 10px;
  font-weight: 600;
  line-height: 1.4;

  &.status-success {
    background: rgba(7, 192, 95, 0.08);
    color: var(--td-success-color);
  }

  &.status-failed {
    background: var(--td-error-color-light);
    color: var(--td-error-color);
  }

  &.status-skipped {
    background: rgba(0, 0, 0, 0.06);
    color: var(--td-text-color-secondary);
  }

  &.method {
    background: rgba(7, 192, 95, 0.08);
    color: var(--td-success-color);
  }
}

.meta-text {
  font-size: 11px;
  color: var(--td-text-color-secondary);
}

.result-content.expanded {
  padding-top: 10px;
}

.info-field .field-value a {
  color: var(--td-brand-color);
  text-decoration: none;

  &:hover {
    text-decoration: underline;
  }
}

.raw-length {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  margin-left: 4px;
  font-weight: normal;
}

.action-button {
  margin-top: 6px;
}

.info-section-title.error {
  color: var(--td-error-color);
}

.full-content.error-text {
  background: var(--td-error-color-light);
  border-color: var(--td-error-color-focus);
  color: var(--td-error-color);
}
</style>
