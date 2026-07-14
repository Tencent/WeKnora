<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { listKnowledgeFolders, type KnowledgeFolder } from '@/api/knowledge-base';
import { flattenFolderPages, mergeFolderPage, type FolderPage, type FolderTreeRow } from '@/utils/knowledgeFolders';

const PAGE_SIZE = 50;
const props = withDefaults(defineProps<{
  kbId: string;
  modelValue: string;
  disabledIds?: string[];
}>(), { disabledIds: () => [] });
const emit = defineEmits<{ 'update:modelValue': [value: string] }>();
const { t } = useI18n();

type PageState = FolderPage<KnowledgeFolder>;
type PickerRow = FolderTreeRow<KnowledgeFolder>;

const pages = ref(new Map<string, PageState>());
const expanded = ref(new Set<string>());
const disabled = computed(() => new Set(props.disabledIds));

const responsePage = (response: any) => response?.data || {};

async function load(parentId = '', reset = false) {
  const current = pages.value.get(parentId);
  if (current?.loading) return;
  if (!reset && current && current.items.length >= current.total) return;
  const page = reset ? 1 : (current?.page || 0) + 1;
  pages.value.set(parentId, { items: reset ? [] : current?.items || [], page: page - 1, total: current?.total || 0, loading: true });
  pages.value = new Map(pages.value);
  try {
    const response: any = await listKnowledgeFolders(props.kbId, { parent_id: parentId, page, page_size: PAGE_SIZE });
    const result = responsePage(response);
    const nextItems = Array.isArray(result.data) ? result.data : [];
    pages.value.set(parentId, mergeFolderPage(current, nextItems, page, result.total, reset));
  } finally {
    const state = pages.value.get(parentId);
    if (state?.loading) pages.value.set(parentId, { ...state, loading: false });
    pages.value = new Map(pages.value);
  }
}

const rows = computed<PickerRow[]>(() => flattenFolderPages(pages.value, expanded.value));

async function toggle(folder: KnowledgeFolder) {
  if (!folder.has_children) return;
  if (expanded.value.has(folder.id)) expanded.value.delete(folder.id);
  else {
    expanded.value.add(folder.id);
    await load(folder.id);
  }
  expanded.value = new Set(expanded.value);
}

function select(folderId: string) {
  if (!disabled.value.has(folderId)) emit('update:modelValue', folderId);
}

watch(() => props.kbId, () => {
  pages.value = new Map();
  expanded.value = new Set();
  void load('', true);
});
onMounted(() => void load('', true));
</script>

<template>
  <div class="folder-picker" role="tree" :aria-label="t('knowledgeFolder.folders')">
    <button class="picker-row" :class="{ active: modelValue === '' }" role="treeitem" @click="select('')">
      <span class="picker-expand" />
      <t-icon name="home" />
      <span class="picker-name">{{ t('knowledgeFolder.root') }}</span>
    </button>
    <template v-for="(row, index) in rows" :key="row.kind === 'folder' ? row.folder.id : `more-${row.parentId}-${index}`">
      <button v-if="row.kind === 'folder'" class="picker-row"
        :class="{ active: modelValue === row.folder.id, disabled: disabled.has(row.folder.id) }"
        :style="{ paddingLeft: `${8 + row.depth * 18}px` }" role="treeitem"
        :aria-disabled="disabled.has(row.folder.id)" @click="select(row.folder.id)">
        <span class="picker-expand" @click.stop="toggle(row.folder)">
          <t-icon v-if="row.folder.has_children" :name="expanded.has(row.folder.id) ? 'chevron-down' : 'chevron-right'" />
        </span>
        <t-icon name="folder" />
        <span class="picker-name">{{ row.folder.name }}</span>
        <span class="picker-count">{{ row.folder.total_knowledge_count }}</span>
      </button>
      <t-button v-else class="picker-more" variant="text" size="small"
        :loading="pages.get(row.parentId)?.loading" :style="{ marginLeft: `${26 + row.depth * 18}px` }"
        @click="load(row.parentId)">
        {{ t('knowledgeFolder.loadMore') }}
      </t-button>
    </template>
    <div v-if="pages.get('')?.loading && !pages.get('')?.items.length" class="picker-loading"><t-loading size="small" /></div>
  </div>
</template>

<style scoped>
.folder-picker{height:min(52vh,420px);overflow:auto;border:1px solid var(--td-component-stroke);background:var(--td-bg-color-container)}
.picker-row{width:100%;height:36px;display:flex;align-items:center;gap:7px;border:0;background:transparent;color:var(--td-text-color-primary);font:inherit;text-align:left;padding:0 8px;cursor:pointer}
.picker-row:hover,.picker-row.active{background:var(--td-bg-color-container-hover)}.picker-row.active{color:var(--td-brand-color)}.picker-row.disabled{opacity:.45;cursor:not-allowed}
.picker-expand{width:18px;height:24px;display:flex;align-items:center;justify-content:center;flex:none}.picker-name{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1}.picker-count{font-size:12px;color:var(--td-text-color-placeholder)}
.picker-more{display:flex;margin-top:2px;margin-bottom:2px}.picker-loading{height:56px;display:flex;align-items:center;justify-content:center}
</style>
