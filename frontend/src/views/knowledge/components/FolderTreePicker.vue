<script setup lang="ts">
import { ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { listKnowledgeFolders, type KnowledgeFolderNode } from '@/api/knowledge-base';

// 懒加载文件夹选择树。自绘树而非 t-tree：节点量按需增长，
// 结构简单（缩进 + 展开箭头），不引入组件库树的受控状态负担。
interface TreeNode {
  folder: KnowledgeFolderNode;
  depth: number;
  expanded: boolean;
  loading: boolean;
  children: TreeNode[] | null; // null = 未加载
}

const props = defineProps<{
  visible: boolean;
  kbId: string;
  title?: string;
  // 选择时禁用的文件夹 ID（如"移动文件夹"时禁掉自己及其子树）
  disabledIds?: string[];
  // 是否提供"根目录"选项
  allowRoot?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:visible', v: boolean): void;
  (e: 'confirm', folderId: string, folderName: string): void;
}>();

const { t } = useI18n();

const rootNodes = ref<TreeNode[]>([]);
const rootLoading = ref(false);
const selectedId = ref<string | null>(null);
const selectedName = ref('');

const isDisabled = (id: string) => (props.disabledIds || []).includes(id);

async function loadChildren(parentId: string): Promise<TreeNode[]> {
  const res: any = await listKnowledgeFolders(props.kbId, { parent_id: parentId || undefined });
  const nodes = (res?.data || []) as KnowledgeFolderNode[];
  return nodes.map((folder) => ({
    folder,
    depth: folder.depth,
    expanded: false,
    loading: false,
    children: folder.has_children ? null : [],
  }));
}

async function reload() {
  rootLoading.value = true;
  selectedId.value = props.allowRoot ? '' : null;
  selectedName.value = props.allowRoot ? t('knowledgeBase.folder.root') : '';
  try {
    rootNodes.value = await loadChildren('');
  } catch (e) {
    console.error('Failed to load folders', e);
    rootNodes.value = [];
  } finally {
    rootLoading.value = false;
  }
}

watch(
  () => props.visible,
  (v) => {
    if (v && props.kbId) reload();
  },
);

async function toggleExpand(node: TreeNode) {
  if (node.children === null && !node.loading) {
    node.loading = true;
    try {
      node.children = await loadChildren(node.folder.id);
    } catch (e) {
      console.error('Failed to load folder children', e);
      node.children = [];
    } finally {
      node.loading = false;
    }
  }
  node.expanded = !node.expanded;
}

function select(id: string, name: string) {
  if (id !== '' && isDisabled(id)) return;
  selectedId.value = id;
  selectedName.value = name;
}

function onConfirm() {
  if (selectedId.value === null) return;
  emit('confirm', selectedId.value, selectedName.value);
  emit('update:visible', false);
}
</script>

<template>
  <t-dialog :visible="visible" :header="title || t('knowledgeBase.folder.pickTarget')" width="440px"
    :confirm-btn="{ content: t('common.confirm'), disabled: selectedId === null }"
    :cancel-btn="{ content: t('common.cancel') }" @confirm="onConfirm"
    @close="emit('update:visible', false)" @update:visible="(v: boolean) => emit('update:visible', v)">
    <div class="folder-picker">
      <div v-if="allowRoot" class="folder-picker-row" :class="{ selected: selectedId === '' }"
        @click="select('', t('knowledgeBase.folder.root'))">
        <span class="folder-picker-arrow" />
        <t-icon name="home" class="folder-picker-icon" />
        <span class="folder-picker-name">{{ t('knowledgeBase.folder.root') }}</span>
      </div>

      <div v-if="rootLoading" class="folder-picker-loading">
        <t-loading size="small" />
      </div>
      <div v-else-if="!rootNodes.length" class="folder-picker-empty">
        {{ t('knowledgeBase.folder.noFolders') }}
      </div>

      <template v-else>
        <!-- 递归展开：用显式栈渲染而非递归组件，保持单文件自包含 -->
        <template v-for="node in rootNodes" :key="node.folder.id">
          <FolderPickerNode :node="node" :selected-id="selectedId" :is-disabled="isDisabled"
            @toggle="toggleExpand" @select="select" />
        </template>
      </template>
    </div>
  </t-dialog>
</template>

<script lang="ts">
import { defineComponent, h, type PropType } from 'vue';
import { Icon as TIcon, Loading as TLoading } from 'tdesign-vue-next';

// 递归节点渲染器（渲染函数实现，避免额外的 SFC 文件）。
const FolderPickerNode: ReturnType<typeof defineComponent> = defineComponent({
  name: 'FolderPickerNode',
  props: {
    node: { type: Object as PropType<any>, required: true },
    selectedId: { type: [String, null] as PropType<string | null>, default: null },
    isDisabled: { type: Function as PropType<(id: string) => boolean>, required: true },
  },
  emits: ['toggle', 'select'],
  setup(props, { emit }) {
    const render = (node: any): any => {
      const disabled = props.isDisabled(node.folder.id);
      const row = h(
        'div',
        {
          class: [
            'folder-picker-row',
            { selected: props.selectedId === node.folder.id, disabled },
          ],
          style: { paddingLeft: `${(node.depth - 1) * 18 + 8}px` },
          onClick: () => !disabled && emit('select', node.folder.id, node.folder.name),
        },
        [
          h(
            'span',
            {
              class: ['folder-picker-arrow', { expandable: node.children === null || node.children.length > 0 }],
              onClick: (e: Event) => {
                e.stopPropagation();
                emit('toggle', node);
              },
            },
            node.loading
              ? [h(TLoading, { size: 'small' })]
              : node.children === null || node.children.length > 0
                ? [h(TIcon, { name: node.expanded ? 'chevron-down' : 'chevron-right', size: '14px' })]
                : [],
          ),
          h(TIcon, { name: node.expanded ? 'folder-open' : 'folder', class: 'folder-picker-icon' }),
          h('span', { class: 'folder-picker-name', title: node.folder.path }, node.folder.name),
          node.folder.knowledge_count
            ? h('span', { class: 'folder-picker-count' }, String(node.folder.knowledge_count))
            : null,
        ],
      );
      const children =
        node.expanded && node.children
          ? node.children.map((child: any) => render(child))
          : [];
      return h('div', { key: node.folder.id }, [row, ...children]);
    };
    return () => render(props.node);
  },
});

export default { components: { FolderPickerNode } };
</script>

<style lang="less">
.folder-picker {
  max-height: 320px;
  overflow-y: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  padding: 4px;

  .folder-picker-row {
    display: flex;
    align-items: center;
    gap: 6px;
    min-height: 32px;
    padding: 0 8px;
    border-radius: 5px;
    font-size: 13px;
    color: var(--td-text-color-primary);
    cursor: pointer;

    &:hover:not(.disabled) {
      background: var(--td-bg-color-secondarycontainer);
    }

    &.selected {
      background: var(--td-brand-color-light, #e6f3ef);
      color: var(--td-brand-color);
    }

    &.disabled {
      color: var(--td-text-color-disabled);
      cursor: not-allowed;
    }
  }

  .folder-picker-arrow {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    flex-shrink: 0;
    color: var(--td-text-color-secondary);

    &.expandable {
      cursor: pointer;
      border-radius: 3px;

      &:hover {
        background: var(--td-component-stroke);
      }
    }
  }

  .folder-picker-icon {
    flex-shrink: 0;
    color: #d97706;
  }

  .folder-picker-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .folder-picker-count {
    flex-shrink: 0;
    font-size: 11px;
    color: var(--td-text-color-placeholder);
  }

  .folder-picker-loading,
  .folder-picker-empty {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 64px;
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }
}
</style>
