<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { batchQueryKnowledge, listKnowledgeFiles, listKnowledgeFolders } from '@/api/knowledge-base';
import { useMentionFileBrowser, type FileBrowserContext } from '@/composables/useMentionFileBrowser';
import { folderBreadcrumbs } from '@/views/knowledge/folderTree';
import { fileDisabledReason, type BrowserFile, type BrowserKnowledgeBase } from '@/utils/mentionFileBrowser';
import { UnavailableFilesError, validateFileDraft, type DraftFile, type FileSelection } from '@/utils/mentionFileSelection';

const props = defineProps<{
  knowledgeBases: BrowserKnowledgeBase[];
  context: FileBrowserContext;
  supportedFileTypes: string[];
  selectedFiles: DraftFile[];
  wholeKbIds: string[];
  tagKbIds: string[];
}>();
const emit = defineEmits<{ select: [file: BrowserFile]; apply: [selection: FileSelection]; back: []; cancel: [] }>();
const { t } = useI18n();
const browser = useMentionFileBrowser(() => props.context, { folders: listKnowledgeFolders, files: listKnowledgeFiles });
const { state, folders } = browser;
const searchInput = ref<HTMLInputElement>();
const list = ref<HTMLElement>();
const keepWholeButton = ref<HTMLButtonElement>();
const selectionCountButton = ref<HTMLButtonElement>();
const query = ref('');
const composing = ref(false);
const active = ref(0);
const crumbs = computed(() => folderBreadcrumbs(state.path));
const kbRows = computed(() => props.knowledgeBases.filter(kb => kb.name.toLowerCase().includes(query.value.trim().toLowerCase())));

const draft = shallowRef<Map<string, DraftFile> | null>(null);
const isMulti = computed(() => draft.value !== null);
const reviewing = ref(false);
const confirmingConflict = ref(false);
const validating = ref(false);
const selectionError = ref<'unavailable' | 'checkFailed' | ''>('');
const unavailableIds = ref<string[]>([]);
let pendingValidation: AbortController | undefined;
let validationDecision: 'keep' | 'replace' | undefined;

const selectedIds = computed(() => props.selectedFiles.map(file => file.id));
const selectedIdSet = computed(() => new Set(selectedIds.value));
const draftChanged = computed(() => draft.value !== null && (
  draft.value.size !== selectedIdSet.value.size ||
  [...draft.value.keys()].some(id => !selectedIdSet.value.has(id))
));
const conflictingKbIds = computed(() => {
  const whole = new Set(props.wholeKbIds);
  const conflicts = new Set<string>();
  for (const file of draft.value?.values() || []) {
    if (file.kbId && whole.has(file.kbId)) conflicts.add(file.kbId);
  }
  return [...conflicts].sort();
});
const conflictNames = computed(() => conflictingKbIds.value.map(id =>
  props.knowledgeBases.find(kb => kb.id === id)?.name || id).join(', '));
const draftHasTags = computed(() => [...(draft.value?.values() || [])]
  .some(file => file.kbId && props.tagKbIds.includes(file.kbId)));

function startSelection() {
  draft.value = new Map(props.selectedFiles.map(file => [file.id, file]));
  focusSearch();
}
function cancelSelection() {
  pendingValidation?.abort();
  emit('cancel');
}
function toggleFile(file: DraftFile) {
  if (!draft.value || validating.value) return;
  const next = new Map(draft.value);
  if (next.has(file.id)) {
    next.delete(file.id);
    if (reviewing.value) {
      const index = Math.min(active.value, next.size - 1);
      void nextTick(() => {
        const row = list.value?.querySelectorAll<HTMLElement>('.browser-row')[index];
        (row || selectionCountButton.value)?.focus();
      });
    }
  } else next.set(file.id, file);
  draft.value = next;
  confirmingConflict.value = false;
  selectionError.value = '';
}
function toggleReview() {
  reviewing.value = !reviewing.value;
  if (reviewing.value) void nextTick(() => list.value?.scrollTo(0, 0));
  else focusSearch();
}
async function applySelection(decision?: 'keep' | 'replace') {
  if (!draft.value || !draftChanged.value || validating.value) return;
  if (!decision && conflictingKbIds.value.length) {
    confirmingConflict.value = true;
    return;
  }

  const chosenConflicts = conflictingKbIds.value.join(',');
  const context = props.context;
  const controller = new AbortController();
  pendingValidation = controller;
  validationDecision = decision;
  validating.value = true;
  selectionError.value = '';
  unavailableIds.value = [];
  try {
    const files = await validateFileDraft([...draft.value.values()], props.knowledgeBases, props.supportedFileTypes,
      (ids, kbId, options) => {
        const params = new URLSearchParams();
        for (const id of ids) params.append('ids', id);
        return batchQueryKnowledge(params.toString(), kbId, options.agent_id, options.agent_source_tenant_id, options.signal);
      }, { ...context, signal: controller.signal });
    if (controller.signal.aborted || props.context.key !== context.key) return;
    // Authorization resolved legacy IDs without cached KB metadata. Any newly
    // discovered conflict requires a fresh decision before committing.
    draft.value = new Map(files.map(file => [file.id, file]));
    if (conflictingKbIds.value.length && (!decision || conflictingKbIds.value.join(',') !== chosenConflicts)) {
      confirmingConflict.value = true;
      return;
    }
    emit('apply', { contextKey: context.key, files, replaceWholeKbIds: decision === 'replace' ? conflictingKbIds.value : [] });
  } catch (error) {
    if (controller.signal.aborted) return;
    confirmingConflict.value = false;
    if (error instanceof UnavailableFilesError) {
      unavailableIds.value = error.ids;
      reviewing.value = true;
      selectionError.value = 'unavailable';
    } else {
      selectionError.value = 'checkFailed';
    }
  } finally {
    if (pendingValidation === controller) {
      pendingValidation = undefined;
      validationDecision = undefined;
      validating.value = false;
    }
  }
}

interface BrowserRow {
  key: string;
  name: string;
  subtext: string;
  meta: string;
  title: string;
  icon: 'folder' | 'file';
  disabled: string;
  action: () => void;
}

const rows = computed<BrowserRow[]>(() => reviewing.value && draft.value
  ? [...draft.value.values()].map(file => {
    const kbName = props.knowledgeBases.find(kb => kb.id === file.kbId)?.name;
    const path = file.folderPath === '' ? t('mentionBrowser.root') : file.folderPath;
    const location = [kbName, path].filter(Boolean).join(' / ');
    return {
      key: file.id,
      name: file.name,
      subtext: unavailableIds.value.includes(file.id) ? t('mentionBrowser.unavailableFile') : location,
      meta: '',
      title: [file.name, location].filter(Boolean).join(' — '),
      icon: 'file',
      disabled: '',
      action: () => toggleFile(file),
    };
  })
  : !state.kb
  ? kbRows.value.map(kb => ({
    key: kb.id,
    name: kb.name,
    subtext: '',
    meta: kb.orgName || '',
    title: kb.orgName ? `${kb.name} — ${kb.orgName}` : kb.name,
    icon: 'folder',
    disabled: '',
    action: () => openKb(kb),
  }))
  : state.loading || state.error ? [] : [
    ...folders.value.map(folder => {
      const countText = t('mentionBrowser.folderCount', { count: folder.total_count });
      return {
        key: `folder:${folder.path}`,
        name: folder.name,
        subtext: '',
        meta: String(folder.total_count),
        title: `${folder.name} — ${countText}`,
        icon: 'folder' as const,
        disabled: '',
        action: () => navigate(folder.path),
      };
    }),
    ...state.items.map(file => {
      const disabled = fileDisabledReason(file, props.supportedFileTypes, isMulti.value ? [] : selectedIds.value);
      const location = file.folderPath || t('mentionBrowser.root');
      const disabledText = disabled ? t(`mentionBrowser.${disabled}`) : '';
      const subtext = disabledText || (query.value.trim() ? location : '');
      return {
        key: file.id,
        name: file.name,
        subtext,
        meta: '',
        title: `${file.name} — ${disabledText || `${file.kbName} / ${location}`}`,
        icon: 'file' as const,
        disabled,
        action: () => selectFile(file),
      };
    }),
  ]);

const focusSearch = () => nextTick(() => searchInput.value?.focus({ preventScroll: true }));
function openKb(kb: BrowserKnowledgeBase) { query.value = ''; void browser.selectKb(kb); focusSearch(); }
function navigate(path: string) { query.value = ''; void browser.navigate(path); focusSearch(); }
function selectFile(file: BrowserFile) {
  if (!state.loading && !state.error && props.knowledgeBases.some(kb => kb.id === file.kbId) &&
    state.items.some(item => item.id === file.id) &&
    !fileDisabledReason(file, props.supportedFileTypes, draft.value ? [] : selectedIds.value)) {
    if (draft.value) toggleFile(file);
    else emit('select', file);
  }
}
function search() { if (!composing.value && state.kb) void browser.search(query.value); }
function clearQuery() {
  query.value = '';
  if (state.kb) void browser.search('');
  focusSearch();
}
function back() {
  if (reviewing.value) reviewing.value = false;
  else if (query.value) { query.value = ''; if (state.kb) void browser.search(''); }
  else if (state.path) navigate(crumbs.value.at(-2)?.path || '');
  else if (state.kb) browser.reset();
  else { emit('back'); return; }
  focusSearch();
}
function scrollActiveIntoView() {
  nextTick(() => {
    const row = list.value?.querySelectorAll<HTMLElement>('.browser-row')[active.value];
    if (!row || !list.value) return;
    const listRect = list.value.getBoundingClientRect();
    const rowRect = row.getBoundingClientRect();
    if (rowRect.top < listRect.top) {
      list.value.scrollTop -= (listRect.top - rowRect.top);
    } else if (rowRect.bottom > listRect.bottom) {
      list.value.scrollTop += (rowRect.bottom - listRect.bottom);
    }
  });
}
function moveActive(delta: number) {
  if (!rows.value.length) return;
  active.value = Math.max(0, Math.min(rows.value.length - 1, active.value + delta));
  scrollActiveIntoView();
}
function confirmActive() { const row = rows.value[active.value]; if (row && !row.disabled) row.action(); }
function onKeydown(event: KeyboardEvent) {
  if (composing.value || event.isComposing || event.keyCode === 229) return;
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    if (draft.value) cancelSelection();
    else back();
  }
  else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    const fromRow = event.target instanceof HTMLElement && event.target.classList.contains('browser-row');
    if (event.target !== searchInput.value && !fromRow) return;
    event.preventDefault();
    moveActive(event.key === 'ArrowDown' ? 1 : -1);
    if (fromRow) list.value?.querySelectorAll<HTMLElement>('.browser-row')[active.value]?.focus();
  }
  else if (event.key === 'Enter' && event.target === searchInput.value) { event.preventDefault(); confirmActive(); }
  else if (event.key === 'Backspace' && event.target === searchInput.value && !query.value && state.kb) {
    event.preventDefault();
    back();
  }
}
watch(rows, () => { active.value = 0; });
watch(confirmingConflict, open => {
  if (open) void nextTick(() => keepWholeButton.value?.focus());
});
watch(() => props.context.key, () => {
  pendingValidation?.abort();
  query.value = '';
  emit('back');
}, { flush: 'sync' });
watch(() => props.selectedFiles, (files, previousFiles) => {
  if (!draft.value) return;
  if (files.length !== previousFiles.length || files.some((file, index) => file.id !== previousFiles[index].id)) {
    cancelSelection();
    return;
  }

  const next = new Map(draft.value);
  const refreshed = new Set<string>();
  for (const [index, file] of files.entries()) {
    const previous = previousFiles[index];
    // History can resolve a moved file while the picker is already open.
    // Refresh retained entries without adding back files the user removed.
    if (next.has(file.id) && (file.kbId !== previous.kbId || file.name !== previous.name ||
        file.folderPath !== previous.folderPath)) {
      next.set(file.id, file);
      refreshed.add(file.id);
    }
  }
  if (!refreshed.size) return;
  const previousConflicts = conflictingKbIds.value.join(',');
  draft.value = next;
  const conflictsChanged = conflictingKbIds.value.join(',') !== previousConflicts;
  if (conflictsChanged) confirmingConflict.value = false;
  unavailableIds.value = unavailableIds.value.filter(id => !refreshed.has(id));
  if (selectionError.value === 'unavailable' && !unavailableIds.value.length) selectionError.value = '';

  if (pendingValidation && !pendingValidation.signal.aborted) {
    const decision = conflictsChanged ? undefined : validationDecision;
    pendingValidation.abort();
    pendingValidation = undefined;
    validationDecision = undefined;
    validating.value = false;
    // An old request may have used the previous KB. Recheck the current draft,
    // and ask for a fresh decision if retaining the draft now affects other KBs.
    void applySelection(decision);
  }
});
watch(() => props.knowledgeBases.map(kb => kb.id).join(','), () => {
  if (state.kb && !props.knowledgeBases.some(kb => kb.id === state.kb?.id)) browser.reset();
});
onMounted(focusSearch);
onBeforeUnmount(() => pendingValidation?.abort());
defineExpose({ back, moveActive, confirmActive });
</script>

<template>
  <div class="mention-browser" @keydown="onKeydown" @click.stop>
    <div class="browser-header">
      <button
        type="button"
        class="browser-back"
        :title="t('mentionDetail.back')"
        :aria-label="t('mentionDetail.back')"
        @mousedown.prevent
        @click="back"
      >
        <t-icon name="chevron-left" />
        <span class="browser-title">{{ state.kb ? state.kb.name : t('mentionBrowser.browse') }}</span>
      </button>
      <button v-if="!draft" type="button" class="browser-multi-toggle" @click="startSelection">
        {{ t('mentionBrowser.selectMultiple') }}
      </button>
    </div>

    <nav v-if="state.kb && !reviewing" class="browser-crumbs" :aria-label="t('mentionBrowser.path')">
      <button type="button" class="browser-crumb" @click="query = ''; browser.reset()">
        {{ t('common.knowledgeBase') }}
      </button>
      <t-icon class="browser-crumb-sep" name="chevron-right" />
      <button
        type="button"
        class="browser-crumb"
        :class="{ 'is-current': !crumbs.length }"
        @click="navigate('')"
      >
        {{ state.kb.name }}
      </button>
      <template v-for="(crumb, idx) in crumbs" :key="crumb.path">
        <t-icon class="browser-crumb-sep" name="chevron-right" />
        <button
          type="button"
          class="browser-crumb"
          :class="{ 'is-current': idx === crumbs.length - 1 }"
          @click="navigate(crumb.path)"
        >
          {{ crumb.name }}
        </button>
      </template>
    </nav>

    <div v-if="!reviewing" class="browser-search-wrap">
      <span class="browser-search-icon">
        <t-loading v-if="state.loading" size="12px" />
        <t-icon v-else name="search" />
      </span>
      <input
        ref="searchInput"
        v-model="query"
        class="browser-search"
        type="text"
        autocomplete="off"
        spellcheck="false"
        :aria-label="t(state.kb ? 'mentionBrowser.searchFolder' : 'mentionBrowser.searchKb')"
        :placeholder="t(state.kb ? 'mentionBrowser.searchFolder' : 'mentionBrowser.searchKb')"
        @input="search"
        @compositionstart="composing = true"
        @compositionend="composing = false; search()"
      />
      <button
        v-if="query"
        type="button"
        class="browser-search-clear"
        :aria-label="t('mentionDetail.back')"
        @mousedown.prevent
        @click="clearQuery"
      >
        <t-icon name="close-circle-filled" />
      </button>
    </div>

    <div
      v-if="!reviewing && state.kb && (query.trim() || (!draft && (wholeKbIds.includes(state.kb.id) || tagKbIds.includes(state.kb.id))))"
      class="browser-hints"
    >
      <p v-if="query.trim()" class="browser-hint">{{ t('mentionBrowser.subtree') }}</p>
      <p v-if="!draft && wholeKbIds.includes(state.kb.id) && !tagKbIds.includes(state.kb.id)" class="browser-hint">{{ t('mentionBrowser.wholeKb') }}</p>
      <p v-if="!draft && tagKbIds.includes(state.kb.id)" class="browser-hint">{{ t('mentionBrowser.tags') }}</p>
    </div>

    <div ref="list" class="browser-list" :aria-busy="(!reviewing && state.loading) || validating">
      <div v-if="!reviewing && state.loading" class="browser-status" role="status">
        <t-loading size="small" />
        <span>{{ t('common.loading') }}</span>
      </div>
      <div v-else-if="!reviewing && state.error" class="browser-status browser-status--error" role="alert">
        <p>{{ t(`mentionBrowser.${state.error}`) }}</p>
        <button type="button" class="browser-retry" @click="browser.retry">{{ t('mentionBrowser.retry') }}</button>
      </div>
      <template v-else>
        <button
          v-for="(row, index) in rows"
          :key="row.key"
          type="button"
          class="browser-row"
          :class="{ active: active === index, 'is-disabled': !!row.disabled }"
          :role="draft && row.icon === 'file' ? 'checkbox' : undefined"
          :aria-checked="draft && row.icon === 'file' ? draft.has(row.key) : undefined"
          :aria-disabled="!!row.disabled || (validating && row.icon === 'file')"
          :title="row.title"
          @mouseenter="active = index"
          @focus="active = index"
          @click="!row.disabled && row.action()"
        >
          <span v-if="draft && row.icon === 'file'" class="browser-check" :class="{ 'is-checked': draft.has(row.key) }" aria-hidden="true">
            <t-icon v-if="draft.has(row.key)" name="check" />
          </span>
          <span v-else class="browser-row__icon">
            <t-icon :name="row.icon" />
          </span>
          <span class="browser-label">
            <span class="browser-label__name">{{ row.name }}</span>
            <small v-if="row.subtext" class="browser-label__sub">{{ row.subtext }}</small>
          </span>
          <span v-if="row.meta" class="browser-row__meta">{{ row.meta }}</span>
          <t-icon v-if="row.icon === 'folder'" class="browser-row__arrow" name="chevron-right" />
        </button>
        <p v-if="!rows.length" class="browser-status">
          {{ t(reviewing ? 'mentionBrowser.noSelection' : state.kb ? 'mentionBrowser.empty' : 'common.noResult') }}
        </p>
      </template>
    </div>

    <div v-if="!reviewing && state.kb && !state.error && state.page" class="browser-pages">
      <button type="button" class="browser-page-btn" :disabled="state.loading || state.page <= 1" @click="browser.previous">
        <t-icon name="chevron-left" />
        <span>{{ t('mentionBrowser.previous') }}</span>
      </button>
      <span class="browser-page-info">{{ t('mentionBrowser.page', { page: state.page, total: state.total }) }}</span>
      <button type="button" class="browser-page-btn" :disabled="state.loading || !state.hasMore" @click="browser.next">
        <span>{{ t('mentionBrowser.next') }}</span>
        <t-icon name="chevron-right" />
      </button>
    </div>
    <div v-if="draft" class="browser-selection">
      <p v-if="selectionError" class="browser-hint browser-selection-error" role="alert">
        {{ t(`mentionBrowser.${selectionError}`) }}
      </p>
      <p v-if="draftHasTags" class="browser-hint">{{ t('mentionBrowser.tags') }}</p>
      <p v-if="confirmingConflict" class="browser-hint" role="status">
        {{ t('mentionBrowser.wholeConflict', { names: conflictNames }) }}
      </p>
      <p v-if="confirmingConflict && conflictingKbIds.some(id => tagKbIds.includes(id))"
        class="browser-hint browser-tagged-conflict" role="status">
        {{ t('mentionBrowser.taggedWholeConflict') }}
      </p>
      <div class="browser-selection-actions">
        <button ref="selectionCountButton" type="button" class="browser-selection-count" :aria-pressed="reviewing" @click="toggleReview">
          {{ t('mentionBrowser.selectionCount', { count: draft.size }) }}
          <t-icon :name="reviewing ? 'chevron-down' : 'chevron-up'" />
        </button>
        <button type="button" class="browser-cancel" @click="cancelSelection">{{ t('common.cancel') }}</button>
        <template v-if="confirmingConflict">
          <button ref="keepWholeButton" type="button" class="browser-keep-whole" :disabled="validating" @click="applySelection('keep')">
            {{ t('mentionBrowser.keepWhole') }}
          </button>
          <button type="button" class="browser-replace-whole browser-primary" :disabled="validating" @click="applySelection('replace')">
            {{ t('mentionBrowser.useFiles') }}
          </button>
        </template>
        <button v-else type="button" class="browser-apply browser-primary" :disabled="!draftChanged || validating" @click="applySelection()">
          {{ t(validating ? 'common.loading' : 'mentionBrowser.applySelection') }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.mention-browser {
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
}

.browser-header,
.browser-crumbs,
.browser-search-wrap,
.browser-hints,
.browser-pages,
.browser-selection {
  flex-shrink: 0;
}

button {
  font: inherit;
  color: inherit;
  cursor: pointer;
  border: 0;
  background: transparent;
  border-radius: var(--app-radius-sm);
}

button:focus-visible {
  outline: 1.5px solid var(--td-brand-color);
  outline-offset: -1.5px;
}

.browser-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  padding: 2px 2px 4px;
}

.browser-multi-toggle {
  flex-shrink: 0;
  padding: 4px 6px;
  font-size: var(--app-text-xs);
  color: var(--td-brand-color);
}

.browser-multi-toggle:hover,
.browser-selection-actions button:hover:not(:disabled):not(.browser-primary) {
  background: var(--td-bg-color-secondarycontainer-hover);
}

.browser-check {
  display: inline-flex;
  flex: 0 0 14px;
  height: 14px;
  align-items: center;
  justify-content: center;
  box-sizing: border-box;
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-sm);
}

.browser-check.is-checked,
.browser-primary {
  border-color: var(--td-brand-color);
  background: var(--td-brand-color);
  color: var(--td-text-color-anti);
}

.browser-primary:hover:not(:disabled) {
  background: var(--td-brand-color-hover);
}

.browser-selection {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 6px 4px 2px;
  border-top: .5px solid var(--td-component-stroke);
}

.browser-selection-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
}

.browser-selection .browser-hint:not(.browser-tagged-conflict) {
  max-height: 52px;
  overflow-y: auto;
}

.browser-selection-actions button {
  min-height: 26px;
  padding: 3px 6px;
  font-size: var(--app-text-xs);
}

.browser-selection-actions button:disabled {
  opacity: .5;
  cursor: default;
}

.browser-selection-count {
  margin-inline-end: auto;
  color: var(--td-brand-color);
}

.browser-selection-error {
  color: var(--td-error-color);
}

.browser-back {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  min-width: 0;
  height: 26px;
  padding: 0 6px 0 4px;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-weight: 500;
  transition: background var(--app-motion-instant), color var(--app-motion-instant);
}

.browser-back:hover {
  background: var(--td-bg-color-secondarycontainer);
}

.browser-back :deep(.t-icon) {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.browser-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.browser-crumbs {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px;
  margin: 0 2px 4px;
  padding: 4px 6px;
  max-height: 54px;
  overflow-y: auto;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  font-size: var(--app-text-xs);
  line-height: 16px;
}

.browser-crumb {
  padding: 1px 4px;
  color: var(--td-text-color-secondary);
  text-align: left;
  overflow-wrap: anywhere;
  transition: color var(--app-motion-instant), background var(--app-motion-instant);
}

.browser-crumb:hover {
  color: var(--td-brand-color);
  background: var(--td-bg-color-container);
}

.browser-crumb.is-current {
  color: var(--td-text-color-primary);
  font-weight: 500;
}

.browser-crumb-sep {
  flex-shrink: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

/* 与 MentionSelector 的 .mention-search 保持一致的无边框内联搜索条 */
.browser-search-wrap {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  margin-bottom: 2px;
  border-top: .5px solid var(--td-component-stroke);
  border-bottom: .5px solid var(--td-component-stroke);
}

.browser-search-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

.browser-search {
  flex: 1;
  min-width: 0;
  height: 20px;
  padding: 0;
  border: 0;
  outline: none;
  background: transparent;
  color: var(--td-text-color-primary);
  font: inherit;
  font-size: var(--app-text-md);
  line-height: 20px;
}

.browser-search::placeholder {
  color: var(--td-text-color-placeholder);
}

.browser-search-clear {
  width: 16px;
  height: 16px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  color: var(--td-text-color-placeholder);
  transition: color var(--app-motion-instant);
}

.browser-search-clear:hover {
  color: var(--td-text-color-secondary);
}

.browser-hints {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 2px 2px 4px;
}

.browser-hint {
  margin: 0;
  padding: 4px 8px;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  font-size: var(--app-text-xs);
  line-height: 16px;
  color: var(--td-text-color-secondary);
}

.browser-list {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.browser-row {
  width: 100%;
  min-height: 30px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 8px;
  margin: 0;
  box-sizing: border-box;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
  line-height: 20px;
  text-align: left;
  transition: background var(--app-motion-instant), color var(--app-motion-instant);
}

.browser-row:hover {
  background: var(--td-bg-color-secondarycontainer-hover);
}

.browser-row.active {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}

.browser-row.is-disabled {
  opacity: 0.55;
  cursor: default;
}

.browser-row.is-disabled:hover {
  background: transparent;
}

.browser-row__icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
  transition: color var(--app-motion-instant);
}

.browser-row.active .browser-row__icon {
  color: var(--td-brand-color);
}

.browser-label {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
}

.browser-label__name,
.browser-label__sub {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.browser-label__sub {
  font-size: var(--app-text-xs);
  line-height: 16px;
  color: var(--td-text-color-placeholder);
}

.browser-row__meta {
  flex-shrink: 0;
  max-width: 88px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  font-variant-numeric: tabular-nums;
}

.browser-row__arrow {
  flex-shrink: 0;
  margin-right: -4px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

.browser-status {
  margin: 0;
  min-height: 52px;
  padding: 14px 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);
  line-height: 20px;
}

.browser-status--error {
  flex-direction: column;
  gap: 8px;
  color: var(--td-text-color-secondary);
}

.browser-status--error p {
  margin: 0;
}

.browser-retry {
  padding: 4px 12px;
  border: .5px solid var(--td-component-border);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-sm);
  transition: border-color var(--app-motion-instant), color var(--app-motion-instant);
}

.browser-retry:hover {
  border-color: var(--td-brand-color);
  color: var(--td-brand-color);
}

.browser-pages {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 4px;
  margin-top: 2px;
  padding: 6px 4px 2px;
  border-top: .5px solid var(--td-component-stroke);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
}

.browser-page-btn {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px 6px;
  color: var(--td-text-color-secondary);
  transition: background var(--app-motion-instant), color var(--app-motion-instant);
}

.browser-page-btn:hover:not(:disabled) {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}

.browser-page-btn:disabled {
  opacity: 0.4;
  cursor: default;
}

.browser-page-info {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
