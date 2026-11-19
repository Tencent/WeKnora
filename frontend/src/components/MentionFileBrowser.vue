<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { listKnowledgeFiles, listKnowledgeFolders } from '@/api/knowledge-base';
import { useMentionFileBrowser, type FileBrowserContext } from '@/composables/useMentionFileBrowser';
import { folderBreadcrumbs } from '@/views/knowledge/folderTree';
import { fileDisabledReason, type BrowserFile, type BrowserKnowledgeBase } from '@/utils/mentionFileBrowser';

const props = defineProps<{
  knowledgeBases: BrowserKnowledgeBase[];
  context: FileBrowserContext;
  supportedFileTypes: string[];
  selectedFileIds: string[];
  wholeKbIds: string[];
  tagKbIds: string[];
}>();
const emit = defineEmits<{ select: [file: BrowserFile]; back: [] }>();
const { t } = useI18n();
const browser = useMentionFileBrowser(() => props.context, { folders: listKnowledgeFolders, files: listKnowledgeFiles });
const { state, folders } = browser;
const searchInput = ref<HTMLInputElement>();
const list = ref<HTMLElement>();
const query = ref('');
const composing = ref(false);
const active = ref(0);
const crumbs = computed(() => folderBreadcrumbs(state.path));
const kbRows = computed(() => props.knowledgeBases.filter(kb => kb.name.toLowerCase().includes(query.value.trim().toLowerCase())));
const rows = computed(() => !state.kb
  ? kbRows.value.map(kb => ({ key: kb.id, name: kb.name, detail: kb.orgName || '', icon: 'folder', disabled: '', action: () => openKb(kb) }))
  : state.loading || state.error ? [] : [
    ...folders.value.map(folder => ({ key: `folder:${folder.path}`, name: folder.name,
      detail: t('mentionBrowser.folderCount', { count: folder.total_count }), icon: 'folder', disabled: '', action: () => navigate(folder.path) })),
    ...state.items.map(file => {
      const disabled = fileDisabledReason(file, props.supportedFileTypes, props.selectedFileIds);
      return { key: file.id, name: file.name, detail: disabled ? t(`mentionBrowser.${disabled}`) : `${file.kbName} / ${file.folderPath || t('mentionBrowser.root')}`,
        icon: 'file', disabled, action: () => selectFile(file) };
    }),
  ]);
const focusSearch = () => nextTick(() => searchInput.value?.focus({ preventScroll: true }));
function openKb(kb: BrowserKnowledgeBase) { query.value = ''; void browser.selectKb(kb); focusSearch(); }
function navigate(path: string) { query.value = ''; void browser.navigate(path); focusSearch(); }
function selectFile(file: BrowserFile) {
  if (!state.loading && !state.error && props.knowledgeBases.some(kb => kb.id === file.kbId) &&
    state.items.some(item => item.id === file.id) && !fileDisabledReason(file, props.supportedFileTypes, props.selectedFileIds)) emit('select', file);
}
function search() { if (!composing.value && state.kb) void browser.search(query.value); }
function back() {
  if (query.value) { query.value = ''; if (state.kb) void browser.search(''); }
  else if (state.path) navigate(crumbs.value.at(-2)?.path || '');
  else if (state.kb) browser.reset();
  else { emit('back'); return; }
  focusSearch();
}
function moveActive(delta: number) {
  active.value = Math.max(0, Math.min(rows.value.length - 1, active.value + delta));
  nextTick(() => list.value?.querySelectorAll<HTMLElement>('.browser-row')[active.value]?.focus());
}
function confirmActive() { const row = rows.value[active.value]; if (row && !row.disabled) row.action(); }
function onKeydown(event: KeyboardEvent) {
  if (composing.value || event.isComposing || event.keyCode === 229) return;
  if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); back(); }
  else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); moveActive(event.key === 'ArrowDown' ? 1 : -1); }
  else if (event.key === 'Enter' && event.target === searchInput.value) { event.preventDefault(); confirmActive(); }
}
watch(rows, () => { active.value = 0; });
watch(() => props.context.key, () => { query.value = ''; emit('back'); }, { flush: 'sync' });
watch(() => props.knowledgeBases.map(kb => kb.id).join(','), () => {
  if (state.kb && !props.knowledgeBases.some(kb => kb.id === state.kb?.id)) browser.reset();
});
onMounted(focusSearch);
defineExpose({ back, moveActive, confirmActive });
</script>

<template>
  <div class="mention-browser" @keydown="onKeydown" @click.stop>
    <div class="browser-header">
      <button type="button" class="browser-back" :aria-label="t('mentionDetail.back')" @click="back"><t-icon name="chevron-left" /></button>
      <strong>{{ t('mentionBrowser.browse') }}</strong>
    </div>
    <nav v-if="state.kb" class="browser-crumbs" :aria-label="t('mentionBrowser.path')">
      <button type="button" @click="query = ''; browser.reset()">{{ t('common.knowledgeBase') }}</button>
      <span>/</span><button type="button" @click="navigate('')">{{ state.kb.name }}</button>
      <template v-for="crumb in crumbs" :key="crumb.path"><span>/</span><button type="button" @click="navigate(crumb.path)">{{ crumb.name }}</button></template>
    </nav>
    <input ref="searchInput" v-model="query" class="browser-search" :aria-label="t(state.kb ? 'mentionBrowser.searchFolder' : 'mentionBrowser.searchKb')"
      :placeholder="t(state.kb ? 'mentionBrowser.searchFolder' : 'mentionBrowser.searchKb')"
      @input="search" @compositionstart="composing = true" @compositionend="composing = false; search()" />
    <p v-if="state.kb && query.trim()" class="browser-hint">{{ t('mentionBrowser.subtree') }}</p>
    <p v-if="state.kb && wholeKbIds.includes(state.kb.id) && !tagKbIds.includes(state.kb.id)" class="browser-hint">{{ t('mentionBrowser.wholeKb') }}</p>
    <p v-if="state.kb && tagKbIds.includes(state.kb.id)" class="browser-hint">{{ t('mentionBrowser.tags') }}</p>
    <div ref="list" class="browser-list" :aria-busy="state.loading">
      <div v-if="state.loading" class="browser-status" role="status"><t-loading size="small" /> {{ t('common.loading') }}</div>
      <div v-else-if="state.error" class="browser-status" role="alert">
        <p>{{ t(`mentionBrowser.${state.error}`) }}</p>
        <button type="button" @click="browser.retry">{{ t('mentionBrowser.retry') }}</button>
      </div>
      <template v-else>
        <button v-for="(row, index) in rows" :key="row.key" type="button" class="browser-row"
          :class="{ active: active === index }" :aria-disabled="!!row.disabled" :title="`${row.name} — ${row.detail}`"
          @focus="active = index" @click="!row.disabled && row.action()">
          <t-icon :name="row.icon" /><span class="browser-label"><span>{{ row.name }}</span><small>{{ row.detail }}</small></span>
          <t-icon v-if="row.icon === 'folder'" name="chevron-right" />
        </button>
        <p v-if="!rows.length" class="browser-status">{{ t(state.kb ? 'mentionBrowser.empty' : 'common.noResult') }}</p>
      </template>
    </div>
    <div v-if="state.kb && !state.error && state.page" class="browser-pages">
      <button type="button" :disabled="state.loading || state.page <= 1" @click="browser.previous">{{ t('mentionBrowser.previous') }}</button>
      <span>{{ t('mentionBrowser.page', { page: state.page, total: state.total }) }}</span>
      <button type="button" :disabled="state.loading || !state.hasMore" @click="browser.next">{{ t('mentionBrowser.next') }}</button>
    </div>
  </div>
</template>

<style scoped>
.mention-browser { display: flex; flex-direction: column; min-height: 0; overflow: hidden; color: var(--td-text-color-primary); font-size: var(--app-text-sm); }
.browser-header, .browser-crumbs, .browser-search, .browser-hint, .browser-pages { flex-shrink: 0; }
.browser-header { display: flex; align-items: center; gap: 6px; padding: 4px; }
button { font: inherit; color: inherit; cursor: pointer; border: 0; background: transparent; border-radius: var(--app-radius-sm); }
button:focus-visible, input:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: -2px; }
.browser-back { padding: 6px; }
.browser-crumbs { display: flex; flex-wrap: wrap; gap: 4px; padding: 4px 8px; max-height: 66px; overflow: auto; overflow-wrap: anywhere; }
.browser-crumbs button { text-align: left; overflow-wrap: anywhere; }
.browser-search { margin: 4px; padding: 8px; min-width: 0; border: 1px solid var(--td-component-border); border-radius: var(--app-radius-sm); background: var(--td-bg-color-container); color: inherit; }
.browser-hint { margin: 4px 8px; font-size: var(--app-text-xs); color: var(--td-text-color-secondary); }
.browser-list { flex: 1; overflow-y: auto; min-height: 50px; }
.browser-row { display: flex; align-items: center; gap: 8px; width: 100%; text-align: left; padding: 8px; }
.browser-row:hover, .browser-row.active { background: var(--td-bg-color-container-hover); }
.browser-row[aria-disabled="true"] { opacity: .55; cursor: default; }
.browser-label { min-width: 0; flex: 1; display: flex; flex-direction: column; gap: 3px; }
.browser-label span, .browser-label small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.browser-label small { color: var(--td-text-color-secondary); }
.browser-status { padding: 12px; text-align: center; }
.browser-pages { display: flex; justify-content: space-between; align-items: center; gap: 4px; padding: 6px 2px; border-top: 1px solid var(--td-component-border); }
.browser-pages button { padding: 5px; }
.browser-pages button:disabled { opacity: .4; cursor: default; }
</style>
