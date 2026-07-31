<template>
  <section ref="browserRef" class="folder-mention-browser">
    <button type="button" class="mention-back-row" @click.stop="back">
      <t-icon name="chevron-left" />
      <span>{{ backLabel }}</span>
    </button>

    <header class="folder-mention-intro">
      <span class="folder-mention-intro__icon">
        <t-icon name="folder-open" />
      </span>
      <span>
        <strong>{{ t('knowledgeBase.folderMentionTitle') }}</strong>
        <small>{{ t('knowledgeBase.folderMentionDescription') }}</small>
      </span>
    </header>

    <template v-if="!selectedKnowledgeBase">
      <div class="folder-mention-section-label">
        <span>{{ t('knowledgeBase.folderMentionChooseKb') }}</span>
        <small>{{ t('knowledgeBase.folderMentionChooseKbHint') }}</small>
      </div>
      <div v-if="knowledgeBases.length > 0" class="mention-group folder-kb-list">
        <button
          v-for="(item, index) in knowledgeBases"
          :key="item.id"
          type="button"
          class="mention-item folder-browser-keyboard-item folder-kb-item"
          :class="{ active: activeIndex === index }"
          @click="openKnowledgeBase(item)"
          @mouseenter="activeIndex = index"
        >
          <span class="folder-kb-item__icon">
            <t-icon name="folder" />
          </span>
          <span class="folder-kb-item__main">
            <strong>{{ item.name }}</strong>
            <small>{{ t('knowledgeBase.documentCount', { count: item.count || 0 }) }}</small>
          </span>
          <t-icon name="chevron-right" class="folder-kb-item__arrow" />
        </button>
      </div>
      <div v-else class="folder-browser-empty">
        {{ emptyHint || t('common.noResult') }}
      </div>
    </template>

    <template v-else>
      <div class="folder-mention-location">
        <div class="folder-mention-kb-name">
          <t-icon name="folder" />
          <span>{{ selectedKnowledgeBase.name }}</span>
        </div>
        <nav class="folder-mention-breadcrumb" :aria-label="t('knowledgeBase.currentLocation')">
          <button
            type="button"
            :class="{ current: trail.length === 0 }"
            @click="openBreadcrumb(-1)"
          >
            {{ t('knowledgeBase.rootFolder') }}
          </button>
          <template v-for="(crumb, index) in trail" :key="crumb.id">
            <t-icon name="chevron-right" />
            <button
              type="button"
              :class="{ current: index === trail.length - 1 }"
              @click="openBreadcrumb(index)"
            >
              {{ crumb.name }}
            </button>
          </template>
        </nav>
      </div>

      <button
        v-if="trail.length > 0"
        type="button"
        class="folder-scope-choice folder-browser-keyboard-item"
        :class="{ active: activeIndex === 0 }"
        @click="selectCurrentFolder"
        @mouseenter="activeIndex = 0"
      >
        <span class="folder-scope-choice__icon">
          <t-icon name="check-circle" />
        </span>
        <span class="folder-scope-choice__main">
          <strong>{{ t('knowledgeBase.folderMentionUseCurrent') }}</strong>
          <small>{{ t('knowledgeBase.folderMentionUseCurrentHint') }}</small>
        </span>
        <span class="folder-scope-choice__name">{{ currentFolderName }}</span>
      </button>

      <div v-else class="folder-scope-root-hint">
        <t-icon name="info-circle" />
        <span>{{ t('knowledgeBase.folderMentionRootHint') }}</span>
      </div>

      <div v-if="loading && children.length === 0" class="folder-browser-loading">
        <t-loading size="small" />
      </div>

      <div v-else-if="error" class="folder-browser-error">
        <span>{{ t('knowledgeBase.loadFoldersFailed') }}</span>
        <button type="button" @click="loadChildren(currentParentId, true)">
          {{ t('knowledgeBase.retry') }}
        </button>
      </div>

      <div v-else-if="children.length > 0" class="mention-group folder-child-list">
        <button
          v-for="(item, index) in children"
          :key="item.id"
          type="button"
          class="mention-item folder-browser-keyboard-item folder-child-item"
          :class="{ active: activeIndex === childOffset + index }"
          @click="openChild(item)"
          @mouseenter="activeIndex = childOffset + index"
        >
          <span class="folder-child-item__icon">
            <t-icon :name="item.hasChildren ? 'folder' : 'folder-open'" />
          </span>
          <span class="folder-child-item__main">
            <strong>{{ item.name }}</strong>
            <small>
              {{ t('knowledgeBase.folderDirectDocuments', { count: item.documentCount || 0 }) }}
              <template v-if="item.hasChildren">
                · {{ t('knowledgeBase.folderContainsSubfolders') }}
              </template>
            </small>
          </span>
          <t-icon name="chevron-right" class="folder-child-item__arrow" />
        </button>
        <button
          v-if="hasMore"
          type="button"
          class="folder-browser-load-more"
          :disabled="loading"
          @click="loadMore"
        >
          <t-loading v-if="loading" size="small" />
          <span v-else>{{ t('knowledgeBase.folderLoadMore') }}</span>
        </button>
      </div>

      <div v-else class="folder-browser-empty folder-browser-empty--nested">
        <t-icon name="folder-open" />
        <span>{{ t('knowledgeBase.folderMentionEmpty') }}</span>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { listDocumentFolders } from '@/api/knowledge-base';
import { useDocumentFolderPages } from '@/composables/useDocumentFolderPages';
import type { MentionItem } from '@/types/mention';

type FolderBrowserCrumb = { id: string; name: string };

const props = defineProps<{
  knowledgeBases: MentionItem[];
  emptyHint?: string;
  agentId?: string;
  agentSourceTenantId?: string;
}>();

const emit = defineEmits<{
  select: [item: MentionItem];
  back: [];
}>();

const { t } = useI18n();
const browserRef = ref<HTMLElement | null>(null);
const selectedKnowledgeBase = ref<MentionItem | null>(null);
const trail = ref<FolderBrowserCrumb[]>([]);
const activeIndex = ref(0);

const {
  items: children,
  loading,
  error,
  hasMore,
  load: loadFolderPage,
  reset: resetFolderPage,
} = useDocumentFolderPages<MentionItem>({
  knowledgeBaseId: () => selectedKnowledgeBase.value?.id || '',
  parentId: () => trail.value[trail.value.length - 1]?.id || '',
  mapFolder: (folder, { knowledgeBaseId }) => ({
    id: folder.id,
    name: folder.name,
    type: 'folder',
    kbId: knowledgeBaseId,
    kbName: selectedKnowledgeBase.value?.name,
    folderPath: folder.path || folder.name,
    parentId: folder.parent_id,
    documentCount: folder.document_count || 0,
    hasChildren: folder.has_children,
  }),
  fetchPage: (knowledgeBaseId, parentId, options) => listDocumentFolders(
    knowledgeBaseId,
    parentId,
    {
      ...options,
      ...(props.agentId ? { agent_id: props.agentId } : {}),
      ...(props.agentSourceTenantId
        ? { agent_source_tenant_id: props.agentSourceTenantId }
        : {}),
    },
  ),
  errorMessage: loadError => (
    (loadError as { message?: string })?.message || t('knowledgeBase.loadFoldersFailed')
  ),
  cacheTtlMs: 30_000,
  clearItemsOnError: true,
});

const childOffset = computed(() => trail.value.length > 0 ? 1 : 0);
const itemCount = computed(() => (
  selectedKnowledgeBase.value
    ? childOffset.value + children.value.length
    : props.knowledgeBases.length
));
const currentParentId = computed(() => trail.value[trail.value.length - 1]?.id || '');
const currentFolderName = computed(() => (
  trail.value[trail.value.length - 1]?.name || t('knowledgeBase.rootFolder')
));
const backLabel = computed(() => {
  if (trail.value.length > 0) {
    return trail.value.length > 1
      ? trail.value[trail.value.length - 2].name
      : selectedKnowledgeBase.value?.name || t('knowledgeBase.folderMentionBackToKb');
  }
  if (selectedKnowledgeBase.value) return t('knowledgeBase.folderMentionBackToKb');
  return t('knowledgeBase.folderPanelTitle');
});

function reset() {
  selectedKnowledgeBase.value = null;
  trail.value = [];
  activeIndex.value = 0;
  resetFolderPage();
}

async function openKnowledgeBase(item: MentionItem) {
  selectedKnowledgeBase.value = item;
  trail.value = [];
  activeIndex.value = 0;
  await loadChildren('');
}

async function openChild(item: MentionItem) {
  trail.value = [...trail.value, { id: item.id, name: item.name }];
  activeIndex.value = 0;
  await loadChildren(item.id);
}

async function openBreadcrumb(index: number) {
  trail.value = index < 0 ? [] : trail.value.slice(0, index + 1);
  activeIndex.value = 0;
  await loadChildren(currentParentId.value);
}

async function loadChildren(parentId: string, force = false, append = false) {
  await loadFolderPage({ parentId, force, append });
}

function loadMore() {
  if (!hasMore.value || loading.value) return;
  void loadChildren(currentParentId.value, false, true);
}

function selectCurrentFolder() {
  const knowledgeBase = selectedKnowledgeBase.value;
  const current = trail.value[trail.value.length - 1];
  if (!knowledgeBase || !current) return;
  emit('select', {
    id: current.id,
    name: current.name,
    type: 'folder',
    kbId: knowledgeBase.id,
    kbName: knowledgeBase.name,
    folderPath: trail.value.map(crumb => crumb.name).join(' / '),
  });
}

function back() {
  if (trail.value.length > 0) {
    void openBreadcrumb(trail.value.length - 2);
    return;
  }
  if (selectedKnowledgeBase.value) {
    reset();
    return;
  }
  emit('back');
}

function moveActive(delta: number) {
  const maxIndex = Math.max(0, itemCount.value - 1);
  activeIndex.value = Math.min(maxIndex, Math.max(0, activeIndex.value + delta));
  nextTick(() => {
    const item = browserRef.value?.querySelectorAll<HTMLElement>(
      '.folder-browser-keyboard-item',
    )[activeIndex.value];
    item?.scrollIntoView({ block: 'nearest' });
  });
}

function confirmActive() {
  if (!selectedKnowledgeBase.value) {
    const knowledgeBase = props.knowledgeBases[activeIndex.value];
    if (knowledgeBase) void openKnowledgeBase(knowledgeBase);
    return;
  }
  if (trail.value.length > 0 && activeIndex.value === 0) {
    selectCurrentFolder();
    return;
  }
  const child = children.value[activeIndex.value - childOffset.value];
  if (child) void openChild(child);
}

defineExpose({
  back,
  confirmActive,
  moveActive,
  reset,
});
</script>

<style scoped lang="less">
.folder-mention-browser {
  min-height: 250px;
  padding: 0 6px 7px;
}

.mention-back-row {
  display: flex;
  width: calc(100% - 12px);
  min-height: 30px;
  box-sizing: border-box;
  align-items: center;
  gap: 8px;
  margin: 1px 6px 4px;
  padding: 0 8px;
  border: 0;
  border-bottom: 1px solid var(--td-component-stroke, #f0f0f0);
  background: transparent;
  color: var(--td-text-color-secondary, #666);
  font-family: var(--app-font-family);
  font-size: var(--td-font-size-body-medium, 14px);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-secondarycontainer, #f3f3f3);
  }

  span {
    min-width: 0;
    overflow: hidden;
    flex: 1;
    text-align: left;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.mention-group {
  padding: 2px 0 5px;
}

.mention-item {
  display: flex;
  min-height: 32px;
  box-sizing: border-box;
  align-items: center;
  gap: 8px;
  margin: 1px 6px;
  padding: 4px 8px;
  border-radius: var(--td-radius-medium, 6px);
  color: var(--td-text-color-primary, #333);
  font-family: var(--app-font-family);
  font-size: var(--td-font-size-body-medium, 14px);
  cursor: pointer;
  transition: background 0.15s ease;

  &:hover,
  &.active {
    background: var(--td-bg-color-secondarycontainer, #f3f3f3);
  }
}

.folder-mention-intro {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 4px 2px 8px;
  padding: 10px;
  border-radius: 9px;
  background: color-mix(in srgb, var(--td-brand-color) 6%, var(--td-bg-color-container));

  > span:last-child {
    display: flex;
    min-width: 0;
    flex-direction: column;
  }

  strong {
    color: var(--td-text-color-primary);
    font-size: 13px;
    font-weight: 600;
    line-height: 19px;
  }

  small {
    color: var(--td-text-color-placeholder);
    font-size: 10px;
    line-height: 15px;
  }
}

.folder-mention-intro__icon {
  display: inline-flex;
  width: 32px;
  height: 32px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 9px;
  background: var(--td-bg-color-container);
  color: var(--td-brand-color);
  font-size: 17px;
}

.folder-mention-section-label {
  display: flex;
  align-items: flex-start;
  flex-direction: column;
  padding: 5px 10px 6px;

  span {
    color: var(--td-text-color-secondary);
    font-size: 12px;
    font-weight: 600;
    line-height: 18px;
  }

  small {
    color: var(--td-text-color-placeholder);
    font-size: 10px;
    line-height: 15px;
  }
}

.folder-kb-list,
.folder-child-list {
  padding-bottom: 0;
  border-bottom: 0;
}

.folder-kb-item,
.folder-child-item {
  width: calc(100% - 4px);
  min-height: 48px;
  margin: 2px;
  border: 0;
  background: transparent;
  text-align: left;
}

.folder-kb-item__icon,
.folder-child-item__icon {
  display: inline-flex;
  width: 30px;
  height: 30px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-brand-color);
  font-size: 16px;
}

.folder-kb-item__main,
.folder-child-item__main {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;

  strong {
    overflow: hidden;
    color: var(--td-text-color-primary);
    font-size: 13px;
    font-weight: 500;
    line-height: 18px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  small {
    overflow: hidden;
    color: var(--td-text-color-placeholder);
    font-size: 10px;
    line-height: 15px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.folder-kb-item__arrow,
.folder-child-item__arrow {
  flex: 0 0 auto;
  color: var(--td-text-color-placeholder);
  font-size: 15px;
}

.folder-browser-load-more {
  display: inline-flex;
  width: calc(100% - 8px);
  min-height: 32px;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin: 4px;
  padding: 0 10px;
  border: 0;
  border-radius: 8px;
  background: var(--td-bg-color-component);
  color: var(--td-brand-color);
  font-family: var(--app-font-family);
  font-size: 11px;
  cursor: pointer;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-component-hover);
  }

  &:disabled {
    cursor: default;
    opacity: 0.65;
  }
}

.folder-mention-location {
  padding: 2px 9px 8px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.folder-mention-kb-name {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-secondary);
  font-size: 11px;
  font-weight: 600;

  span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.folder-mention-breadcrumb {
  display: flex;
  align-items: center;
  gap: 2px;
  margin-top: 5px;
  overflow-x: auto;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }

  > button {
    max-width: 116px;
    flex: 0 0 auto;
    overflow: hidden;
    padding: 2px 5px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
    font-size: 10px;
    cursor: pointer;
    text-overflow: ellipsis;
    white-space: nowrap;

    &:hover,
    &.current {
      background: var(--td-bg-color-secondarycontainer);
      color: var(--td-text-color-primary);
    }
  }

  > .t-icon {
    flex: 0 0 auto;
    color: var(--td-text-color-disabled);
    font-size: 11px;
  }
}

.folder-scope-choice {
  display: flex;
  width: calc(100% - 4px);
  min-height: 52px;
  box-sizing: border-box;
  align-items: center;
  gap: 9px;
  margin: 8px 2px 6px;
  padding: 7px 9px;
  border: 1px solid color-mix(in srgb, var(--td-brand-color) 22%, var(--td-component-stroke));
  border-radius: 9px;
  background: color-mix(in srgb, var(--td-brand-color) 5%, var(--td-bg-color-container));
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  cursor: pointer;
  text-align: left;

  &:hover,
  &.active {
    border-color: color-mix(in srgb, var(--td-brand-color) 46%, var(--td-component-stroke));
    background: color-mix(in srgb, var(--td-brand-color) 9%, var(--td-bg-color-container));
  }
}

.folder-scope-choice__icon {
  display: inline-flex;
  width: 30px;
  height: 30px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: var(--td-bg-color-container);
  color: var(--td-brand-color);
  font-size: 16px;
}

.folder-scope-choice__main {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;

  strong {
    font-size: 12px;
    font-weight: 600;
    line-height: 18px;
  }

  small {
    color: var(--td-text-color-placeholder);
    font-size: 10px;
    line-height: 15px;
  }
}

.folder-scope-choice__name {
  max-width: 86px;
  overflow: hidden;
  flex: 0 0 auto;
  color: var(--td-brand-color);
  font-size: 11px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.folder-scope-root-hint {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 7px 8px 4px;
  color: var(--td-text-color-placeholder);
  font-size: 10px;
  line-height: 16px;
}

.folder-browser-loading,
.folder-browser-error,
.folder-browser-empty {
  display: flex;
  min-height: 78px;
  align-items: center;
  justify-content: center;
  gap: 7px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  text-align: center;
}

.folder-browser-error > button {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--td-brand-color);
  font-family: var(--app-font-family);
  font-size: 11px;
  cursor: pointer;
}

.folder-browser-empty--nested {
  flex-direction: column;

  > .t-icon {
    font-size: 22px;
  }
}
</style>
