<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import type { FolderBreadcrumbItem } from '@/types/knowledgeFolder';

// FolderBreadcrumb is a presentational navigation control. It receives the
// breadcrumb trail and search state via props and emits navigation events;
// it never calls folder APIs directly (the page/composable owns the data).
//
// The breadcrumb only handles navigation, collapses middle levels into an
// ellipsis entry when overflowing, and never truncates the current folder. The
// root API sentinel never appears here — the root is a local virtual item whose
// id is "".

const props = defineProps<{
  items: FolderBreadcrumbItem[];
  treeVisible: boolean;
  searchTerm: string;
}>();

const emit = defineEmits<{
  (e: 'navigate', folderId: string): void;
  (e: 'toggle-tree'): void;
}>();

const { t } = useI18n();

const trimmedSearch = computed(() => props.searchTerm.trim());
const hasSearch = computed(() => trimmedSearch.value.length > 0);

// Current folder is always the last item; it is rendered as plain text (not a
// link) with `aria-current="page"` (主文字色, 字重 600).
const current = computed<FolderBreadcrumbItem | null>(() =>
  props.items.length > 0 ? props.items[props.items.length - 1] : null,
);

// Collapse middle items into an ellipsis menu when there are more than 4 items.
// Always visible: first item (root) + last two items (parent + current). The
// middle slice is reachable via the ellipsis dropdown. This preserves the
// immediate parent context and never truncates the current folder.
const MAX_VISIBLE = 4;
const beforeEllipsis = computed<FolderBreadcrumbItem[]>(() =>
  props.items.length > 0 ? [props.items[0]] : [],
);
const collapsedMiddle = computed<FolderBreadcrumbItem[]>(() =>
  props.items.length > MAX_VISIBLE ? props.items.slice(1, -2) : [],
);
const afterEllipsis = computed<FolderBreadcrumbItem[]>(() => {
  if (props.items.length <= MAX_VISIBLE) {
    // No ellipsis: everything except the first item renders inline.
    return props.items.slice(1);
  }
  // With ellipsis: show the last two (parent + current).
  return props.items.slice(-2);
});
const showEllipsis = computed(() => collapsedMiddle.value.length > 0);

const revealTooltip = computed(() =>
  props.treeVisible
    ? t('knowledgeBase.hideFolderTree')
    : t('knowledgeBase.showFolderTree'),
);

const onNavigate = (item: FolderBreadcrumbItem) => {
  // The current folder is not navigable.
  if (current.value && item.id === current.value.id) return;
  emit('navigate', item.id);
};
</script>

<template>
  <nav class="folder-breadcrumb" :aria-label="t('knowledgeBase.folderBreadcrumbLabel')">
    <!-- 26px labelled tree-reveal button. The label stays constant; the chevron
         indicates the toggle direction and the tooltip carries the verb.
         aria-expanded reflects the panel visibility. -->
    <t-tooltip :content="revealTooltip" placement="bottom">
      <button
        type="button"
        class="folder-tree-reveal-btn"
        :aria-expanded="treeVisible"
        :aria-label="revealTooltip"
        @click="emit('toggle-tree')"
      >
        <t-icon
          :name="treeVisible ? 'chevron-left' : 'chevron-right'"
          size="14px"
          aria-hidden="true"
        />
        <span class="folder-tree-reveal-label">{{ t('knowledgeBase.folderTreeTitle') }}</span>
      </button>
    </t-tooltip>

    <!-- Search status overrides the path when a search term is active. The
         breadcrumb path is hidden because search is KB-scoped, not
         folder-scoped (breadcrumb does not mix with QA range). -->
    <div v-if="hasSearch" class="folder-breadcrumb-search" role="status">
      <t-icon name="search" size="14px" aria-hidden="true" />
      <span class="folder-breadcrumb-search-text">
        {{ t('knowledgeBase.searchWholeKb', { q: trimmedSearch }) }}
      </span>
    </div>

    <!-- Breadcrumb path. Separator is a decorative chevron (aria-hidden). -->
    <ol v-else-if="items.length > 0" class="folder-breadcrumb-path">
      <li
        v-for="item in beforeEllipsis"
        :key="`bc-${item.id || 'root'}`"
        class="folder-breadcrumb-item"
      >
        <button
          v-if="current && item.id !== current.id"
          type="button"
          class="folder-breadcrumb-link"
          @click="onNavigate(item)"
        >
          <t-icon
            v-if="item.isRoot"
            name="root-list"
            size="14px"
            aria-hidden="true"
          />
          <span>{{ item.name }}</span>
        </button>
        <span v-else class="folder-breadcrumb-current" aria-current="page">
          <t-icon
            v-if="item.isRoot"
            name="root-list"
            size="14px"
            aria-hidden="true"
          />
          <span>{{ item.name }}</span>
        </span>
      </li>

      <!-- Ellipsis menu for collapsed middle items (折叠中间层级). -->
      <li v-if="showEllipsis" class="folder-breadcrumb-item">
        <t-icon
          name="chevron-right"
          size="14px"
          class="folder-breadcrumb-sep"
          aria-hidden="true"
        />
        <t-dropdown trigger="click" placement="bottom-start" :min-column-width="148">
          <button
            type="button"
            class="folder-breadcrumb-ellipsis-trigger"
            :aria-label="t('knowledgeBase.breadcrumbCollapsedItems', { count: collapsedMiddle.length })"
          >
            <t-icon name="ellipsis" size="14px" aria-hidden="true" />
          </button>
          <template #dropdown>
            <t-dropdown-menu>
              <t-dropdown-item
                v-for="item in collapsedMiddle"
                :key="`bc-more-${item.id}`"
                @click="onNavigate(item)"
              >
                {{ item.name }}
              </t-dropdown-item>
            </t-dropdown-menu>
          </template>
        </t-dropdown>
      </li>

      <li
        v-for="item in afterEllipsis"
        :key="`bc-${item.id || 'root'}`"
        class="folder-breadcrumb-item"
      >
        <t-icon
          name="chevron-right"
          size="14px"
          class="folder-breadcrumb-sep"
          aria-hidden="true"
        />
        <button
          v-if="current && item.id !== current.id"
          type="button"
          class="folder-breadcrumb-link"
          @click="onNavigate(item)"
        >
          <span>{{ item.name }}</span>
        </button>
        <span v-else class="folder-breadcrumb-current" aria-current="page">
          <span>{{ item.name }}</span>
        </span>
      </li>
    </ol>
  </nav>
</template>

<style scoped lang="less">
.folder-breadcrumb {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  flex: 0 0 auto;
}

// 26px labelled tree-reveal button. Compact outline button with a directional
// chevron + constant label. The outline style follows the secondary-button
// treatment (次按钮); the 26px height is a deliberate compact size for the
// breadcrumb (below the 28-30px icon-button default).
.folder-tree-reveal-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 26px;
  padding: 0 8px;
  flex-shrink: 0;
  border: 1px solid var(--td-component-border);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  transition: color 0.15s ease, border-color 0.15s ease, background-color 0.15s ease;

  &:hover {
    color: var(--td-brand-color);
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 1px;
  }

  .folder-tree-reveal-label {
    white-space: nowrap;
  }
}

// Search status: brand-colored to signal the active search overrides folder
// context (brand/link text for emphasis).
.folder-breadcrumb-search {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: var(--td-brand-color);
  font-size: 14px;
  font-weight: 500;

  .folder-breadcrumb-search-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.folder-breadcrumb-path {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  margin: 0;
  padding: 0;
  list-style: none;
}

.folder-breadcrumb-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

// 分隔图标 14px, 占位文字色.
.folder-breadcrumb-sep {
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

// 非当前层级 次文字色; hover 转品牌色 + 轻背景; 圆角 6px;
// 层级间距 6px. 链接热区向外扩展 (padding 2px 6px).
.folder-breadcrumb-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 200px;
  padding: 2px 6px;
  border: none;
  background: transparent;
  border-radius: 6px;
  color: var(--td-text-color-secondary);
  font-size: 14px;
  cursor: pointer;
  transition: color 0.15s ease, background-color 0.15s ease;

  > span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 1px;
  }
}

// 当前层级 主文字色, 字重 600.
.folder-breadcrumb-current {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 240px;
  padding: 2px 6px;
  color: var(--td-text-color-primary);
  font-size: 14px;
  font-weight: 600;

  > span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.folder-breadcrumb-ellipsis-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 26px;
  padding: 0;
  border: none;
  background: transparent;
  border-radius: 6px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: color 0.15s ease, background-color 0.15s ease;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 1px;
  }
}
</style>
