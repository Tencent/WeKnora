<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Folder } from '@/types/folder'
import {
  buildFolderBreadcrumb,
  buildFolderTree,
  flattenVisibleFolderTree,
  isFolderMoveTargetDisabled,
} from '../utils/folderTree'

const props = withDefaults(defineProps<{
  visible: boolean
  folders: Folder[]
  currentFolderId?: string | null
  disabledFolderIds?: ReadonlySet<string>
  rootDisabled?: boolean
  title: string
  submitting?: boolean
}>(), {
  disabledFolderIds: () => new Set<string>(),
  rootDisabled: false,
  submitting: false,
})

const emit = defineEmits<{
  (event: 'update:visible', visible: boolean): void
  (event: 'confirm', folderId: string | null): void
}>()

const { t } = useI18n()
const tree = computed(() => buildFolderTree(props.folders))
const selectedFolderId = ref<string | null>(null)
const expandedIds = ref<Set<string>>(new Set())
const rows = computed(() => flattenVisibleFolderTree(tree.value.roots, expandedIds.value))

const selectedIsDisabled = computed(() =>
  isFolderMoveTargetDisabled(
    selectedFolderId.value,
    props.disabledFolderIds,
    props.rootDisabled,
  ),
)
const confirmDisabled = computed(() =>
  props.submitting ||
  selectedIsDisabled.value ||
  (
    props.currentFolderId !== undefined &&
    selectedFolderId.value === props.currentFolderId
  ),
)

const expandCurrentPath = () => {
  const next = new Set([...expandedIds.value].filter(id => tree.value.byId.has(id)))
  const path = buildFolderBreadcrumb(props.currentFolderId ?? null, tree.value.byId)
  for (const folder of path) next.add(folder.id)
  expandedIds.value = next
}

watch(
  () => props.visible,
  visible => {
    if (!visible) return
    selectedFolderId.value = props.currentFolderId ?? null
    expandedIds.value = new Set()
    expandCurrentPath()
  },
)

watch(
  () => props.folders,
  () => {
    if (props.visible) expandCurrentPath()
  },
)

const requestClose = () => {
  if (!props.submitting) emit('update:visible', false)
}

const updateVisible = (visible: boolean) => {
  if (!visible) requestClose()
}

const selectTarget = (folderId: string | null) => {
  const disabled = isFolderMoveTargetDisabled(
    folderId,
    props.disabledFolderIds,
    props.rootDisabled,
  )
  if (!disabled) selectedFolderId.value = folderId
}

const toggleExpanded = (folderId: string) => {
  const next = new Set(expandedIds.value)
  if (next.has(folderId)) next.delete(folderId)
  else next.add(folderId)
  expandedIds.value = next
}

const confirmMove = () => {
  if (!confirmDisabled.value) emit('confirm', selectedFolderId.value)
}
</script>

<template>
  <t-dialog
    :visible="visible"
    :header="title"
    :close-btn="!submitting"
    :close-on-overlay-click="!submitting"
    :confirm-btn="{
      content: t('common.confirm'),
      theme: 'primary',
      loading: submitting,
      disabled: confirmDisabled,
    }"
    :cancel-btn="{ content: t('common.cancel'), disabled: submitting }"
    width="480px"
    dialog-class-name="folder-move-dialog"
    @update:visible="updateVisible"
    @cancel="requestClose"
    @close="requestClose"
    @confirm="confirmMove"
  >
    <p class="folder-move-dialog__hint">{{ t('knowledgeFolderMove.selectTarget') }}</p>
    <div class="folder-target-tree" role="tree">
      <button
        type="button"
        role="treeitem"
        class="folder-target-row folder-target-row--root"
        :class="{
          active: selectedFolderId === null,
          disabled: rootDisabled,
        }"
        :disabled="rootDisabled"
        :aria-selected="selectedFolderId === null"
        @click="selectTarget(null)"
      >
        <span class="folder-target-row__toggle" />
        <t-icon name="home" />
        <span class="folder-target-row__name">{{ t('knowledgeFolder.root') }}</span>
        <span v-if="currentFolderId === null" class="folder-target-row__current">
          {{ t('knowledgeFolderMove.current') }}
        </span>
      </button>

      <div
        v-for="row in rows"
        :key="row.folder.id"
        class="folder-target-row-wrap"
        :style="{ '--folder-depth': row.depth }"
      >
        <button
          type="button"
          role="treeitem"
          class="folder-target-row"
          :class="{
            active: selectedFolderId === row.folder.id,
            disabled: disabledFolderIds.has(row.folder.id),
          }"
          :disabled="disabledFolderIds.has(row.folder.id)"
          :aria-selected="selectedFolderId === row.folder.id"
          :aria-expanded="row.hasChildren ? expandedIds.has(row.folder.id) : undefined"
          @click="selectTarget(row.folder.id)"
        >
          <span
            class="folder-target-row__toggle"
            :class="{ visible: row.hasChildren, expanded: expandedIds.has(row.folder.id) }"
            @click.stop="row.hasChildren && toggleExpanded(row.folder.id)"
          >
            <t-icon v-if="row.hasChildren" name="chevron-right" />
          </span>
          <t-icon name="folder" />
          <span class="folder-target-row__name" :title="row.folder.name">{{ row.folder.name }}</span>
          <span v-if="currentFolderId === row.folder.id" class="folder-target-row__current">
            {{ t('knowledgeFolderMove.current') }}
          </span>
        </button>
      </div>
    </div>
  </t-dialog>
</template>

<style scoped lang="less">
.folder-move-dialog__hint {
  margin: 0 0 10px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.folder-target-tree {
  max-height: 420px;
  overflow: auto;
  padding: 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
}

.folder-target-row-wrap {
  padding-left: calc(var(--folder-depth) * 16px);
}

.folder-target-row {
  width: 100%;
  min-height: 36px;
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 6px 10px 6px 4px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  text-align: left;

  &:hover:not(.disabled) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &.active {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }

  &.disabled {
    color: var(--td-text-color-disabled);
    cursor: not-allowed;
  }
}

.folder-target-row__toggle {
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;

  :deep(.t-icon) {
    transition: transform 0.15s ease;
  }

  &.expanded :deep(.t-icon) {
    transform: rotate(90deg);
  }
}

.folder-target-row__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.folder-target-row__current {
  flex: 0 0 auto;
  margin-left: auto;
  padding: 1px 6px;
  border-radius: 9px;
  background: var(--td-bg-color-component);
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}
</style>
