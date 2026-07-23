<template>
  <div
    v-if="visible"
    class="folder-scope-selector"
    role="group"
    :aria-label="t('input.folderScope.selectFolderForKb', { name: knowledgeBaseName })"
    tabindex="-1"
    @keydown.down.prevent="moveActive(1)"
    @keydown.up.prevent="moveActive(-1)"
    @keydown.enter.prevent="confirmActive"
    @keydown.esc.prevent="close"
  >
      <button
        type="button"
        class="folder-scope-option folder-scope-option--entire"
        :class="{ selected: selectedFolderIDs.length === 0, active: activeIndex === 0 }"
        :aria-selected="selectedFolderIDs.length === 0"
        @click="clearScope"
        @mouseenter="activeIndex = 0"
      >
        <span class="folder-scope-option__icon">
          <t-icon name="folder" />
        </span>
        <span class="folder-scope-option__main">
          <span class="folder-scope-option__name">{{ t('input.folderScope.entireKnowledgeBase') }}</span>
          <span class="folder-scope-option__path">{{ t('input.folderScope.entireKnowledgeBaseHint') }}</span>
        </span>
        <t-icon v-if="selectedFolderIDs.length === 0" class="folder-scope-option__check" name="check" />
      </button>

      <div v-if="selectedState.status === 'invalid'" class="folder-scope-selector__invalid" role="alert">
        <span>{{ t('input.folderScope.partialInvalid') }}</span>
        <div v-for="folderID in selectedState.invalidFolderIDs" :key="folderID" class="folder-scope-selector__invalid-item">
          <code :title="folderID">{{ folderID }}</code>
          <button type="button" @click="toggleFolder(folderID)">{{ t('input.folderScope.removeCurrentFolder') }}</button>
        </div>
      </div>

      <div class="folder-scope-selector__search">
        <input
          ref="searchInputRef"
          v-model="searchQuery"
          type="text"
          :placeholder="t('input.folderScope.searchPlaceholder')"
          @keydown.down.prevent="moveActive(1)"
          @keydown.up.prevent="moveActive(-1)"
          @keydown.enter.prevent="confirmActive"
          @keydown.esc.prevent="close"
        />
      </div>

      <div class="folder-scope-selector__content" role="listbox" aria-multiselectable="true">
        <div v-if="loading" class="folder-scope-selector__status">
          <t-loading size="small" />
          <span>{{ t('input.folderScope.loading') }}</span>
        </div>
        <div v-else-if="error" class="folder-scope-selector__status folder-scope-selector__status--error">
          <span>{{ t('input.folderScope.loadFailed') }}</span>
          <button type="button" @click="$emit('retry')">{{ t('common.retry') }}</button>
        </div>
        <div v-else-if="folderRows.length === 0" class="folder-scope-selector__status">
          {{ searchQuery ? t('common.noResult') : t('input.folderScope.noFolders') }}
        </div>
        <template v-else>
          <div
            v-for="(row, index) in folderRows"
            :key="row.folder.id"
            class="folder-scope-option folder-scope-option--folder"
            :class="{
              selected: selectedFolderIDSet.has(row.folder.id),
              active: activeIndex === index + 1,
            }"
            :style="{ paddingLeft: `${8 + row.depth * 16}px` }"
            role="option"
            :aria-selected="selectedFolderIDSet.has(row.folder.id)"
            :title="getFolderPath(row.folder.id)"
            tabindex="0"
            @click="toggleFolder(row.folder.id)"
            @mouseenter="activeIndex = index + 1"
            @keydown.enter.prevent="toggleFolder(row.folder.id)"
            @keydown.space.prevent="toggleFolder(row.folder.id)"
          >
            <button
              type="button"
              class="folder-scope-option__toggle"
              :class="{ hidden: !row.hasChildren || !!searchQuery }"
              :aria-label="t('input.folderScope.toggleFolder')"
              :aria-expanded="row.hasChildren ? expandedIds.has(row.folder.id) : undefined"
              :disabled="!row.hasChildren || !!searchQuery"
              @click.stop="toggleExpanded(row.folder.id)"
            >
              <t-icon name="chevron-right" :class="{ expanded: expandedIds.has(row.folder.id) }" />
            </button>
            <span class="folder-scope-option__icon">
              <t-icon name="folder" />
            </span>
            <span class="folder-scope-option__main">
              <span class="folder-scope-option__name" :title="row.folder.name">{{ row.folder.name }}</span>
              <span class="folder-scope-option__path" :title="getFolderPath(row.folder.id)">{{ getFolderPath(row.folder.id) }}</span>
            </span>
            <t-icon v-if="selectedFolderIDSet.has(row.folder.id)" class="folder-scope-option__check" name="check" />
          </div>
        </template>
      </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Folder } from '@/types/folder'
import {
  buildFolderBreadcrumb,
  buildFolderTree,
  flattenVisibleFolderTree,
} from '@/views/knowledge/utils/folderTree'
import { normalizeFolderIDs, resolveFolderScopeState } from '@/utils/folderScope'

const props = defineProps<{
  visible: boolean
  knowledgeBaseName: string
  modelValue?: string[]
  folders?: Folder[] | null
  loading?: boolean
  error?: string | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'clear'): void
  (e: 'toggle', folderID: string): void
  (e: 'retry'): void
}>()

const { t } = useI18n()
const searchInputRef = ref<HTMLInputElement | null>(null)
const searchQuery = ref('')
const activeIndex = ref(0)
const expandedIds = ref<Set<string>>(new Set())

const folders = computed(() => props.folders || [])
const selectedFolderIDs = computed(() => normalizeFolderIDs(props.modelValue))
const selectedFolderIDSet = computed(() => new Set(selectedFolderIDs.value))
const folderTree = computed(() => buildFolderTree(folders.value))
const allFolderIds = computed(() => new Set(folders.value.map(folder => folder.id)))
const effectiveExpandedIds = computed(() => {
  if (searchQuery.value.trim()) return allFolderIds.value
  return expandedIds.value
})
const allRows = computed(() => flattenVisibleFolderTree(folderTree.value.roots, effectiveExpandedIds.value))
const folderRows = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  if (!query) return allRows.value
  return allRows.value.filter(row => getFolderPath(row.folder.id).toLowerCase().includes(query))
})
const selectedState = computed(() =>
  resolveFolderScopeState(selectedFolderIDs.value, props.folders, !!props.loading, props.error),
)

function getFolderPath(folderID: string): string {
  const breadcrumb = buildFolderBreadcrumb(folderID, folderTree.value.byId)
  return breadcrumb.map(folder => folder.name).filter(Boolean).join(' / ')
}

function expandSelectedAncestors() {
  if (selectedFolderIDs.value.length === 0 || folders.value.length === 0) return
  const next = new Set(expandedIds.value)
  selectedFolderIDs.value.forEach(folderID => {
    buildFolderBreadcrumb(folderID, folderTree.value.byId).forEach(folder => next.add(folder.id))
  })
  expandedIds.value = next
}

function toggleExpanded(folderID: string) {
  const next = new Set(expandedIds.value)
  if (next.has(folderID)) {
    next.delete(folderID)
  } else {
    next.add(folderID)
  }
  expandedIds.value = next
}

function close() {
  emit('close')
}

function clearScope() {
  emit('clear')
}

function toggleFolder(folderID: string) {
  emit('toggle', folderID)
}

function moveActive(delta: number) {
  const total = folderRows.value.length + 1
  if (total <= 0) return
  activeIndex.value = Math.min(total - 1, Math.max(0, activeIndex.value + delta))
}

function confirmActive() {
  if (activeIndex.value === 0) {
    clearScope()
    return
  }
  const row = folderRows.value[activeIndex.value - 1]
  if (row) toggleFolder(row.folder.id)
}

watch(() => props.visible, (visible) => {
  if (!visible) return
  searchQuery.value = ''
  activeIndex.value = selectedFolderIDs.value.length > 0 ? 1 : 0
  expandSelectedAncestors()
  nextTick(() => {
    searchInputRef.value?.focus()
  })
})

watch([() => props.modelValue, folders], expandSelectedAncestors)
</script>

<style scoped lang="less">
.folder-scope-selector {
  display: flex;
  width: 100%;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  background: transparent;
}

.folder-scope-option--entire {
  margin-top: 4px;
}

.folder-scope-selector__search {
  padding: 8px 10px;
  border-bottom: 1px solid var(--td-component-stroke, #e7e7e7);

  input {
    width: 100%;
    height: 30px;
    padding: 0 10px;
    border: 1px solid var(--td-component-border, #e7e9eb);
    border-radius: 6px;
    color: var(--td-text-color-primary, #1f2937);
    background: var(--td-bg-color-container, #fff);
    box-sizing: border-box;
    outline: none;

    &:focus {
      border-color: var(--td-brand-color, #07c05f);
    }
  }
}

.folder-scope-selector__content {
  flex: 1;
  min-height: 0;
  max-height: 236px;
  overflow-y: auto;
  padding: 6px 8px 8px;
}

.folder-scope-selector__invalid {
  display: flex;
  align-items: stretch;
  flex-direction: column;
  gap: 8px;
  margin: 8px 10px 0;
  padding: 8px 10px;
  border: 1px solid var(--td-error-color-3, #f8c9c9);
  border-radius: 6px;
  color: var(--td-error-color, #d54941);
  background: var(--td-error-color-1, #fff0ed);
  font-size: 12px;

  button {
    border: 0;
    background: transparent;
    color: inherit;
    font-weight: 600;
    cursor: pointer;
  }
}

.folder-scope-selector__invalid-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;

  code {
    min-width: 0;
    overflow: hidden;
    color: inherit;
    font-size: 10px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.folder-scope-selector__status {
  display: flex;
  min-height: 88px;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--td-text-color-secondary, #6b7280);
  font-size: 12px;
  text-align: center;

  button {
    border: 0;
    background: transparent;
    color: var(--td-brand-color, #07c05f);
    font-weight: 600;
    cursor: pointer;
  }
}

.folder-scope-selector__status--error {
  color: var(--td-error-color, #d54941);
}

.folder-scope-option {
  display: flex;
  width: 100%;
  min-height: 42px;
  align-items: center;
  gap: 8px;
  padding: 7px 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-primary, #1f2937);
  text-align: left;
  cursor: pointer;

  &:hover,
  &.active {
    background: var(--td-bg-color-secondarycontainer-hover, #f2f3f5);
  }

  &.selected {
    background: var(--td-brand-color-light, #eefdf5);
  }
}

.folder-scope-option--entire {
  margin: 8px 8px 0;
  width: calc(100% - 16px);
}

.folder-scope-option__toggle {
  display: inline-flex;
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-secondary, #6b7280);
  cursor: pointer;

  &.hidden {
    visibility: hidden;
  }

  &:disabled {
    cursor: default;
  }

  .expanded {
    transform: rotate(90deg);
  }
}

.folder-scope-option__icon {
  display: inline-flex;
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  align-items: center;
  justify-content: center;
  color: var(--td-brand-color, #07c05f);
}

.folder-scope-option__main {
  display: flex;
  flex: 1;
  min-width: 0;
  flex-direction: column;
  gap: 1px;
}

.folder-scope-option__name,
.folder-scope-option__path {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.folder-scope-option__name {
  font-size: 13px;
  font-weight: 600;
  line-height: 18px;
}

.folder-scope-option__path {
  font-size: 11px;
  line-height: 15px;
  color: var(--td-text-color-secondary, #6b7280);
}

.folder-scope-option__check {
  flex: 0 0 auto;
  color: var(--td-brand-color, #07c05f);
}

</style>
