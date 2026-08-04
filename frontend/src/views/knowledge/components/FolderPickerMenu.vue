<template>
  <div class="folder-picker">
    <div v-if="showBack" class="folder-picker__header" @click.stop="emit('back')">
      <t-icon name="chevron-left" size="16px" />
      <span>{{ t('knowledgeBase.moveToFolder.action') }}</span>
    </div>

    <div ref="listRef" class="folder-picker__list">
      <template v-for="row in renderRows" :key="row.key">
        <div
          v-if="row.kind === 'folder'"
          :data-folder-path="row.path || undefined"
          class="folder-picker__item"
          :class="{ current: effectiveCurrentPath === row.path }"
          :style="{ '--folder-picker-depth': row.depth }"
          :title="row.path || undefined"
          @click.stop="choose(row.path)"
        >
          <t-icon :name="row.isRoot ? 'folder-open' : 'folder'" class="folder-picker__icon" />
          <span class="folder-picker__name">{{ row.label }}</span>
          <button
            type="button"
            class="folder-picker__add"
            :title="row.isRoot
              ? t('knowledgeBase.moveToFolder.newFolderAddRoot')
              : t('knowledgeBase.moveToFolder.newFolderAddUnder', { folder: row.label })"
            :aria-label="row.isRoot
              ? t('knowledgeBase.moveToFolder.newFolderAddRoot')
              : t('knowledgeBase.moveToFolder.newFolderAddUnder', { folder: row.label })"
            @click.stop="startCreatingUnder(row.path)"
          >
            <t-icon name="folder-add" />
          </button>
          <t-icon v-if="effectiveCurrentPath === row.path" name="check" class="folder-picker__current" />
        </div>

        <div
          v-else
          class="folder-picker__item folder-picker__item--create"
          :style="{ '--folder-picker-depth': row.depth }"
          @click.stop
        >
          <t-icon name="folder" class="folder-picker__icon" />
          <input
            ref="newFolderInputRef"
            v-model.trim="newFolderName"
            class="folder-picker__input"
            :placeholder="t('knowledgeBase.moveToFolder.newFolderPlaceholder')"
            @keydown.enter.stop="commitNewFolder"
            @keydown.esc.stop="cancelCreating"
          />
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { folderOptionFromPath, joinFolderPath, normalizeFolderPath, sortFolderOptions } from '../folderTree'

export type FolderOption = { path: string; name: string; depth: number }

type FolderRow = {
  kind: 'folder'
  key: string
  path: string
  label: string
  depth: number
  isRoot?: boolean
}

type CreateRow = {
  kind: 'create'
  key: string
  parentPath: string
  depth: number
}

type RenderRow = FolderRow | CreateRow

const props = withDefaults(defineProps<{
  options: FolderOption[]
  currentPath?: string
  showBack?: boolean
  allowReselect?: boolean
}>(), {
  currentPath: '',
  showBack: false,
  allowReselect: false,
})

const emit = defineEmits<{
  back: []
  confirm: [folderPath: string]
  create: [folderPath: string]
}>()

const { t } = useI18n()

const creatingUnder = ref<string | null>(null)
const newFolderName = ref('')
const newFolderInputRef = ref<HTMLInputElement | null>(null)
const listRef = ref<HTMLElement | null>(null)
const localCreatedPaths = ref<string[]>([])
const selectedPath = ref<string | null>(null)

const effectiveCurrentPath = computed(() =>
  selectedPath.value !== null ? selectedPath.value : (props.currentPath ?? ''),
)

const displayOptions = computed(() => {
  const byPath = new Map<string, FolderOption>()
  props.options.forEach((option) => byPath.set(option.path, option))
  localCreatedPaths.value.forEach((path) => {
    if (!byPath.has(path)) byPath.set(path, folderOptionFromPath(path))
  })
  return sortFolderOptions([...byPath.values()])
})

const renderRows = computed<RenderRow[]>(() => {
  const rows: RenderRow[] = [{
    kind: 'folder',
    key: 'root',
    path: '',
    label: t('knowledgeBase.folderTree.rootRow'),
    depth: 0,
    isRoot: true,
  }]

  if (creatingUnder.value === '') {
    rows.push({
      kind: 'create',
      key: 'create-root',
      parentPath: '',
      depth: childCreateDepth(''),
    })
  }

  displayOptions.value.forEach((option) => {
    rows.push({
      kind: 'folder',
      key: option.path,
      path: option.path,
      label: option.name,
      depth: option.depth,
    })
    if (creatingUnder.value === option.path) {
      rows.push({
        kind: 'create',
        key: `create-${option.path}`,
        parentPath: option.path,
        depth: childCreateDepth(option.path),
      })
    }
  })

  return rows
})

watch(
  creatingUnder,
  async (value) => {
    if (value === null) return
    await nextTick()
    newFolderInputRef.value?.focus()
  },
)

watch(
  () => props.options,
  (options) => {
    const existing = new Set(options.map((option) => option.path))
    localCreatedPaths.value = localCreatedPaths.value.filter((path) => !existing.has(path))
  },
  { deep: true },
)

watch(
  () => props.currentPath,
  () => {
    selectedPath.value = null
  },
)

function childCreateDepth(parentPath: string): number {
  return parentPath.split('/').filter(Boolean).length
}

const choose = (path: string) => {
  if (path === effectiveCurrentPath.value && !props.allowReselect) return
  selectedPath.value = path
  emit('confirm', path)
}

const startCreatingUnder = (parentPath: string) => {
  if (creatingUnder.value === parentPath) {
    cancelCreating()
    return
  }
  creatingUnder.value = parentPath
  newFolderName.value = ''
}

const cancelCreating = () => {
  creatingUnder.value = null
  newFolderName.value = ''
}

const scrollToFolder = async (path: string) => {
  await nextTick()
  const row = listRef.value?.querySelector(`[data-folder-path="${CSS.escape(path)}"]`)
  row?.scrollIntoView({ block: 'nearest' })
}

const commitNewFolder = async () => {
  if (creatingUnder.value === null) return
  const name = normalizeFolderPath(newFolderName.value)
  if (!name) return
  const path = joinFolderPath(creatingUnder.value, name)
  if (displayOptions.value.some((option) => option.path === path)) return

  if (!localCreatedPaths.value.includes(path)) {
    localCreatedPaths.value = [...localCreatedPaths.value, path]
  }
  creatingUnder.value = null
  newFolderName.value = ''
  emit('create', path)
  await scrollToFolder(path)
}
</script>

<style scoped lang="less">
.folder-picker {
  --folder-picker-indent: 12px;
  min-width: 208px;
  max-width: 280px;
}

.folder-picker__header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  margin-bottom: 2px;
  border-bottom: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-secondary);
  font-size: 13px;
  cursor: pointer;

  &:hover {
    color: var(--td-brand-color);
  }
}

.folder-picker__list {
  max-height: 260px;
  overflow-y: auto;
  scrollbar-width: thin;

  &::-webkit-scrollbar {
    width: 4px;
  }

  &::-webkit-scrollbar-thumb {
    border-radius: 2px;
    background: var(--td-scrollbar-color);
  }
}

.folder-picker__item {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  box-sizing: border-box;
  padding: 0 8px 0 calc(var(--folder-picker-depth, 0) * var(--folder-picker-indent) + 10px);
  border-radius: 6px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);

    .folder-picker__add {
      opacity: 1;
      pointer-events: auto;
    }
  }

  &.current {
    color: var(--td-text-color-placeholder);
    cursor: default;

    &:hover {
      background: transparent;

      .folder-picker__add {
        opacity: 1;
        pointer-events: auto;
      }
    }
  }

  &--create {
    cursor: default;

    &:hover {
      background: transparent;
    }
  }
}

.folder-picker__icon {
  flex: 0 0 auto;
  font-size: 15px;
  color: var(--td-text-color-placeholder);
}

.folder-picker__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.folder-picker__add {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  margin-right: -2px;
  padding: 0;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  opacity: 0;
  pointer-events: none;
  cursor: pointer;
  transition: opacity 0.15s ease, color 0.15s ease, background 0.15s ease;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-bg-color-component);
  }
}

.folder-picker__current {
  flex: 0 0 auto;
  font-size: 14px;
  color: var(--td-text-color-placeholder);
}

.folder-picker__input {
  flex: 1;
  min-width: 0;
  height: 24px;
  padding: 0 6px;
  border: 1px solid var(--td-brand-color);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 13px;
  outline: none;
}
</style>
