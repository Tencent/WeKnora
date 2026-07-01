<template>
  <div class="folder-tree-node">
    <div
      class="folder-tree-item"
      :class="{
        selected: folder.id === selectedId,
        disabled: disabledIds.includes(folder.id),
      }"
      :style="{ paddingLeft: `${depth * 20 + 12}px` }"
      @click="handleSelect"
    >
      <t-icon
        v-if="hasChildren"
        :name="expanded ? 'chevron-down' : 'chevron-right'"
        class="expand-icon"
        @click.stop="toggleExpand"
      />
      <folder-icon class="folder-icon" />
      <span class="folder-name" :title="folder.name">{{ folder.name }}</span>
      <span v-if="folder.knowledge_count !== undefined" class="folder-count">
        {{ folder.knowledge_count }}
      </span>
    </div>

    <!-- 子文件夹 -->
    <div v-if="hasChildren && expanded" class="folder-children">
      <FolderTreeNode
        v-for="child in folder.children"
        :key="child.id"
        :folder="child"
        :selected-id="selectedId"
        :disabled-ids="disabledIds"
        :depth="depth + 1"
        @select="$emit('select', $event)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import { FolderIcon } from 'tdesign-icons-vue-next';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

interface Props {
  folder: KnowledgeFolder;
  selectedId?: string | null;
  disabledIds?: string[];
  depth?: number;
}

const props = withDefaults(defineProps<Props>(), {
  selectedId: null,
  disabledIds: () => [],
  depth: 0,
});

const emit = defineEmits<{
  (e: 'select', folderId: string): void;
}>();

const expanded = ref(false);

const hasChildren = computed(() => {
  return props.folder.children && props.folder.children.length > 0;
});

const toggleExpand = () => {
  if (hasChildren.value) {
    expanded.value = !expanded.value;
  }
};

const handleSelect = () => {
  if (!props.disabledIds.includes(props.folder.id)) {
    emit('select', props.folder.id);
  }
};
</script>

<style scoped lang="less">
.folder-tree-node {
  .folder-tree-item {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 12px;
    border-radius: 4px;
    cursor: pointer;
    transition: background-color 0.2s;
    font-size: 14px;
    user-select: none;

    &:hover:not(.disabled) {
      background: var(--td-bg-color-container-hover);
    }

    &.selected {
      background: var(--td-brand-color-light);
      color: var(--td-brand-color);
      font-weight: 500;
    }

    &.disabled {
      cursor: not-allowed;
      opacity: 0.5;
      color: var(--td-text-color-disabled);

      &:hover {
        background: transparent;
      }
    }

    .expand-icon {
      flex-shrink: 0;
      font-size: 16px;
      cursor: pointer;
      transition: transform 0.2s;

      &:hover {
        color: var(--td-brand-color);
      }
    }

    .folder-icon {
      flex-shrink: 0;
      font-size: 18px;
      color: var(--td-warning-color);
    }

    .folder-name {
      flex: 1;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .folder-count {
      flex-shrink: 0;
      font-size: 12px;
      color: var(--td-text-color-placeholder);
      background: var(--td-bg-color-component);
      padding: 2px 6px;
      border-radius: 10px;
    }
  }

  .folder-children {
    margin-left: 0;
  }
}
</style>
