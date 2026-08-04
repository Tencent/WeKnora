<template>
  <div class="folder-picker">
    <div v-if="showBack" class="folder-picker__header" @click.stop="emit('back')">
      <t-icon name="chevron-left" size="16px" />
      <span>{{ t('knowledgeBase.moveToFolder.action') }}</span>
    </div>

    <div class="folder-picker__list">
      <div
        class="folder-picker__item"
        :class="{ current: currentPath === '' }"
        @click.stop="choose('')"
      >
        <t-icon name="folder-open" class="folder-picker__icon" />
        <span class="folder-picker__name">{{ t('knowledgeBase.folderTree.rootRow') }}</span>
        <t-icon v-if="currentPath === ''" name="check" class="folder-picker__current" />
      </div>

      <div
        v-for="option in options"
        :key="option.path"
        class="folder-picker__item"
        :class="{ current: currentPath === option.path }"
        :style="{ '--folder-picker-depth': option.depth }"
        :title="option.path"
        @click.stop="choose(option.path)"
      >
        <t-icon name="folder" class="folder-picker__icon" />
        <span class="folder-picker__name">{{ option.name }}</span>
        <t-icon v-if="currentPath === option.path" name="check" class="folder-picker__current" />
      </div>
    </div>

    <div class="folder-picker__create" @click.stop>
      <div v-if="!creating" class="folder-picker__item" @click.stop="startCreating">
        <t-icon name="folder-add" class="folder-picker__icon is-action" />
        <span class="folder-picker__name is-action">{{ t('knowledgeBase.moveToFolder.newFolder') }}</span>
      </div>
      <template v-else>
        <input
          ref="newFolderInputRef"
          v-model.trim="newFolderName"
          class="folder-picker__input"
          :placeholder="t('knowledgeBase.moveToFolder.newFolderPlaceholder')"
          @keydown.enter.stop="commitNewFolder"
          @keydown.esc.stop="creating = false"
        />
        <p class="folder-picker__hint">{{ t('knowledgeBase.moveToFolder.newFolderHint') }}</p>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { normalizeFolderPath } from '../folderTree'

export type FolderOption = { path: string; name: string; depth: number }

const props = withDefaults(defineProps<{
  options: FolderOption[]
  /** Folder the documents are in today, marked so the current place is obvious. */
  currentPath?: string
  /** Show a back row when embedded in a menu that has a parent level. */
  showBack?: boolean
}>(), {
  currentPath: '',
  showBack: false,
})

const emit = defineEmits<{
  back: []
  confirm: [folderPath: string]
}>()

const { t } = useI18n()

const creating = ref(false)
const newFolderName = ref('')
const newFolderInputRef = ref<HTMLInputElement | null>(null)

// Picking a folder is the whole action — there is nothing else to configure, so
// there is no confirm step to sit through.
const choose = (path: string) => {
  if (path === props.currentPath) return
  emit('confirm', path)
}

const startCreating = async () => {
  creating.value = true
  newFolderName.value = ''
  await nextTick()
  newFolderInputRef.value?.focus()
}

const commitNewFolder = () => {
  const path = normalizeFolderPath(newFolderName.value)
  if (!path) return
  creating.value = false
  newFolderName.value = ''
  emit('confirm', path)
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
  max-height: 220px;
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
  gap: 8px;
  height: 30px;
  box-sizing: border-box;
  padding: 0 10px 0 calc(var(--folder-picker-depth, 0) * var(--folder-picker-indent) + 10px);
  border-radius: 6px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.current {
    color: var(--td-text-color-placeholder);
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

  &.is-action {
    color: var(--td-brand-color);
  }
}

.folder-picker__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;

  &.is-action {
    color: var(--td-brand-color);
  }
}

.folder-picker__current {
  flex: 0 0 auto;
  font-size: 14px;
  color: var(--td-text-color-placeholder);
}

.folder-picker__create {
  margin-top: 2px;
  padding-top: 2px;
  border-top: 1px solid var(--td-component-stroke);
}

.folder-picker__input {
  box-sizing: border-box;
  width: calc(100% - 20px);
  height: 28px;
  margin: 4px 10px 0;
  padding: 0 8px;
  border: 1px solid var(--td-brand-color);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 13px;
  outline: none;
}

.folder-picker__hint {
  margin: 4px 10px 6px;
  font-size: 11px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}
</style>
