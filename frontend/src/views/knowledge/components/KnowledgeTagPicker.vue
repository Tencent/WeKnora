<template>
  <div class="knowledge-tag-picker">
    <t-input v-model="query" :disabled="busy" :placeholder="$t('knowledgeBase.tagPickerSearch')" :maxlength="40" clearable>
      <template #prefix-icon><t-icon name="search" size="16px" /></template>
    </t-input>
    <div class="tag-picker-list" :aria-label="$t('knowledgeBase.columnTag')">
      <t-loading v-if="loading && !tags.length" size="small" />
      <template v-for="(tag, index) in orderedTags" :key="tag.id">
        <div v-if="index === 0 || index === selectedTags.length" class="tag-picker-group-title" role="heading" aria-level="3">
          <span>{{ $t(selectedIds.includes(tag.id) ? 'knowledgeBase.tagPickerSelected' : 'knowledgeBase.tagPickerUnselected') }}</span>
          <span class="tag-picker-group-count">{{ selectedIds.includes(tag.id) ? selectedTags.length : unselectedTags.length }}</span>
        </div>
        <div class="tag-picker-row">
          <template v-if="editingId === tag.id">
            <t-input v-model="editingName" autofocus :maxlength="40" size="small" :disabled="busy"
              :aria-label="$t('knowledgeBase.tagEditAction')" @enter="saveName(tag)"
              @keydown="(_v: string, ctx: { e: KeyboardEvent }) => { if (ctx.e.key === 'Escape') cancelEdit() }" />
            <t-button variant="text" shape="square" size="small" :loading="busy" :aria-label="$t('common.save')" @click="saveName(tag)"><t-icon name="check" size="14px" /></t-button>
            <t-button theme="default" variant="text" shape="square" size="small" :disabled="busy" :aria-label="$t('common.cancel')" @click="cancelEdit"><t-icon name="close" size="14px" /></t-button>
          </template>
          <template v-else>
            <t-checkbox :checked="selectedIds.includes(tag.id)" :disabled="busy" @change="toggle(tag.id)"><span :title="tag.name">{{ tag.name }}</span></t-checkbox>
            <t-popup attach="body" trigger="click" placement="bottom-right" :visible="menuTagId === tag.id"
              @visible-change="(visible: boolean) => { if (visible) menuTagId = tag.id; else if (menuTagId === tag.id) menuTagId = '' }">
              <button type="button" class="tag-picker-more" :class="{ 'is-open': menuTagId === tag.id }" :disabled="busy"
                :aria-label="`${tag.name} · ${$t('knowledgeBase.columnActions')}`" :aria-expanded="menuTagId === tag.id"><t-icon name="ellipsis" size="16px" /></button>
              <template #content>
                <div class="tag-picker-menu">
                  <button type="button" @click="startEdit(tag)">{{ $t('knowledgeBase.tagEditAction') }}</button>
                  <t-tooltip v-if="isUsed(tag)" :content="$t('knowledgeBase.tagPickerInUse')">
                    <span><button type="button" disabled>{{ $t('knowledgeBase.tagDeleteAction') }}</button></span>
                  </t-tooltip>
                  <t-popconfirm v-else attach="body" :content="$t('knowledgeBase.tagPickerDeleteConfirm', { name: tag.name })"
                    :confirm-btn="{ content: $t('common.delete'), theme: 'danger', loading: busy }"
                    :cancel-btn="{ content: $t('common.cancel') }" @confirm="removeTag(tag)">
                    <button type="button" class="tag-picker-delete" :disabled="busy">{{ $t('knowledgeBase.tagDeleteAction') }}</button>
                  </t-popconfirm>
                </div>
              </template>
            </t-popup>
          </template>
        </div>
      </template>
      <button v-if="canCreate && !loading" type="button" class="tag-picker-create" :disabled="busy" @click="createTag">
        <t-icon :name="busy ? 'loading' : 'add'" size="16px" />
        <span :title="query.trim()">{{ $t('knowledgeBase.tagCreateAction') }} “{{ query.trim() }}”</span>
      </button>
      <p v-if="!loading && !tags.length && !canCreate" class="tag-picker-empty">{{ $t(query.trim() ? 'knowledgeBase.tagEmptyResult' : 'knowledgeBase.noTags') }}</p>
      <t-button v-if="hasMore" variant="text" theme="default" size="small" :loading="loading" @click="load(false)">{{ $t('tenant.loadMore') }}</t-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { listKnowledgeTags, createKnowledgeBaseTag, updateKnowledgeBaseTag, deleteKnowledgeBaseTag } from '@/api/knowledge-base';

type Tag = { id: string; seq_id: number; name: string; knowledge_count?: number; chunk_count?: number };
const props = defineProps<{ kbId: string; selectedIds: string[] }>();
const emit = defineEmits<{
  'update:selectedIds': [ids: string[]];
  'busy-change': [busy: boolean];
  changed: [payload?: { deletedTagId?: string }];
}>();
const { t } = useI18n();
const query = ref('');
const tags = ref<Tag[]>([]);
const loading = ref(false);
const busy = ref(false);
const page = ref(1);
const hasMore = ref(false);
const editingId = ref('');
const menuTagId = ref('');
const editingName = ref('');
let requestVersion = 0;
let debounce: ReturnType<typeof setTimeout> | undefined;
const canCreate = computed(() => query.value.trim() && !tags.value.some(tag => tag.name === query.value.trim()));
const selectedTagIds = computed(() => new Set(props.selectedIds));
const selectedTags = computed(() => tags.value.filter(tag => selectedTagIds.value.has(tag.id)));
const unselectedTags = computed(() => tags.value.filter(tag => !selectedTagIds.value.has(tag.id)));
const orderedTags = computed(() => [...selectedTags.value, ...unselectedTags.value]);
const isUsed = (tag: Tag) => !!(tag.knowledge_count || tag.chunk_count);
const reportError = (error: any) => MessagePlugin.error(error?.message || t('common.operationFailed'));
watch(busy, value => emit('busy-change', value), { flush: 'sync' });

async function load(reset = true) {
  if (!props.kbId || (!reset && loading.value)) return;
  const version = ++requestVersion;
  if (reset) { page.value = 1; tags.value = []; hasMore.value = false; }
  loading.value = true;
  try {
    const res: any = await listKnowledgeTags(props.kbId, { page: page.value, page_size: 50, keyword: query.value.trim() || undefined });
    if (version !== requestVersion) return;
    const rows = (res.data?.data || []).map((tag: Tag) => ({ ...tag, id: String(tag.id) }));
    tags.value = reset ? rows : [...tags.value, ...rows];
    hasMore.value = tags.value.length < (res.data?.total || 0);
    page.value++;
  } catch (error) { if (version === requestVersion) reportError(error); }
  finally { if (version === requestVersion) loading.value = false; }
}
watch(query, () => {
  clearTimeout(debounce);
  requestVersion++;
  loading.value = true;
  cancelEdit();
  menuTagId.value = '';
  debounce = setTimeout(() => { void load(); }, 250);
});
watch(() => props.kbId, () => { void load(); }, { immediate: true });
onBeforeUnmount(() => { clearTimeout(debounce); requestVersion++; emit('busy-change', false); });

function toggle(id: string) {
  emit('update:selectedIds', props.selectedIds.includes(id) ? props.selectedIds.filter(value => value !== id) : [...props.selectedIds, id]);
}
function startEdit(tag: Tag) { menuTagId.value = ''; editingId.value = tag.id; editingName.value = tag.name; }
function cancelEdit() { if (!busy.value) { editingId.value = ''; editingName.value = ''; } }
async function createTag() {
  const name = query.value.trim();
  if (!name || busy.value || loading.value) return;
  busy.value = true;
  try {
    const res: any = await createKnowledgeBaseTag(props.kbId, { name });
    const tag = res.data || res;
    emit('update:selectedIds', [...new Set([...props.selectedIds, String(tag.id)])]);
    emit('changed');
    query.value = '';
    MessagePlugin.success(t('knowledgeBase.tagCreateSuccess'));
  } catch (error) { reportError(error); }
  finally { busy.value = false; }
}
async function saveName(tag: Tag) {
  const name = editingName.value.trim();
  if (busy.value) return;
  if (!name) { MessagePlugin.warning(t('knowledgeBase.tagNameRequired')); return; }
  if (name === tag.name) { cancelEdit(); return; }
  busy.value = true;
  try {
    await updateKnowledgeBaseTag(props.kbId, tag.id, { name });
    tag.name = name;
    editingId.value = '';
    emit('changed');
    MessagePlugin.success(t('knowledgeBase.tagEditSuccess'));
  } catch (error) { reportError(error); }
  finally { busy.value = false; }
}
async function removeTag(tag: Tag) {
  if (busy.value || isUsed(tag)) return;
  busy.value = true;
  try {
    // Do not force deletion: force also deletes the associated knowledge content.
    await deleteKnowledgeBaseTag(props.kbId, tag.seq_id);
    emit('update:selectedIds', props.selectedIds.filter(id => id !== tag.id));
    menuTagId.value = '';
    emit('changed', { deletedTagId: tag.id });
    await load();
    MessagePlugin.success(t('knowledgeBase.tagDeleteSuccess'));
  } catch (error) { reportError(error); }
  finally { busy.value = false; }
}
</script>

<style scoped lang="less">
.knowledge-tag-picker { display: flex; flex-direction: column; gap: 8px; margin-top: 16px; }
.tag-picker-list { max-height: min(280px, 40vh); overflow-y: auto; overflow-x: hidden; scrollbar-width: thin; scrollbar-gutter: stable; padding: 2px 4px 2px 0; }
.tag-picker-group-title {
  display: flex; align-items: center; gap: 6px; padding: 5px 4px;
  color: var(--td-text-color-secondary); font-size: var(--app-text-xs); line-height: 18px;
  &:not(:first-child) { margin-top: 6px; padding-top: 10px; border-top: 1px solid var(--td-component-stroke); }
}
.tag-picker-group-count { color: var(--td-text-color-placeholder); font-variant-numeric: tabular-nums; }
.tag-picker-row {
  display: flex; align-items: center; gap: 4px; min-width: 0; box-sizing: border-box; min-height: 34px; padding: 2px 4px; border-radius: var(--app-radius-xs);
  &:hover { background: var(--td-bg-color-container-hover); }
  > .t-checkbox { flex: 1; min-width: 0; margin: 0; }
  :deep(.t-checkbox__label) { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 400; }
  > .t-button, > .t-popup { flex-shrink: 0; }
  > .t-input__wrap { flex: 1; min-width: 0; }
  :deep(.t-button--theme-default) { color: var(--td-text-color-secondary); }
  :deep(.t-button--theme-default.t-is-disabled) { color: var(--td-text-color-disabled); }
}
.tag-picker-empty { padding: 12px 4px; margin: 0; font-size: var(--app-text-sm); color: var(--td-text-color-placeholder); }
.tag-picker-more {
  display: flex; align-items: center; justify-content: center; width: 26px; height: 26px;
  padding: 0; border: 0; border-radius: var(--app-radius-xs); background: transparent;
  color: var(--td-text-color-placeholder); cursor: pointer; opacity: 0;
  &:hover, &.is-open { background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-primary); }
}
.tag-picker-row:hover .tag-picker-more, .tag-picker-row:focus-within .tag-picker-more, .tag-picker-more.is-open { opacity: 1; }
.tag-picker-more:focus-visible, .tag-picker-create:focus-visible { outline: 2px solid var(--app-focus-border); outline-offset: 2px; }
@media (hover: none) { .tag-picker-more { opacity: 1; } }
.tag-picker-menu {
  min-width: 112px; display: flex; flex-direction: column; gap: 2px;
  button { width: 100%; display: block; border: 0; border-radius: var(--app-radius-xs); background: transparent; padding: 6px 10px;
    font: inherit; font-size: var(--app-text-base); font-weight: 400; line-height: 20px; text-align: left; color: var(--td-text-color-primary); cursor: pointer; }
  button:hover:enabled { background: var(--td-bg-color-container-hover); color: var(--td-brand-color); }
  button:disabled { color: var(--td-text-color-disabled); cursor: not-allowed; }
  .tag-picker-delete:hover:enabled { color: var(--td-error-color); }
}
.tag-picker-create {
  display: flex; align-items: center; gap: 8px; width: 100%; min-height: 36px;
  padding: 4px; border: 0; border-radius: var(--app-radius-xs); background: transparent;
  font: inherit; font-size: var(--app-text-md); text-align: left; color: var(--td-text-color-secondary); cursor: pointer;
  .t-icon { color: var(--td-brand-color); flex-shrink: 0; }
  span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  &:hover:enabled { background: var(--td-bg-color-container-hover); color: var(--td-text-color-primary); }
  &:disabled { cursor: wait; }
}
</style>
