<template>
  <t-dialog
    :visible="visible"
    :header="t('knowledgeBase.moveToFolder.title', { count })"
    :confirm-btn="{ content: t('knowledgeBase.moveToFolder.confirm'), theme: 'primary', loading: submitting, disabled: !canSubmit }"
    :cancel-btn="{ content: t('common.cancel') }"
    width="480px"
    @confirm="handleConfirm"
    @close="close"
    @cancel="close"
  >
    <div class="move-folder-body">
      <p class="move-folder-hint">{{ t('knowledgeBase.moveToFolder.hint') }}</p>

      <div class="move-folder-list">
        <button
          type="button"
          class="move-folder-option"
          :class="{ active: selected === '' && !creating }"
          @click="pick('')"
        >
          <t-icon name="folder-open" class="move-folder-option__icon" />
          <span class="move-folder-option__label">{{ t('knowledgeBase.folderTree.rootRow') }}</span>
          <t-icon v-if="selected === '' && !creating" name="check" class="move-folder-option__check" />
        </button>

        <button
          v-for="option in options"
          :key="option.path"
          type="button"
          class="move-folder-option"
          :class="{ active: selected === option.path && !creating }"
          :style="{ '--move-folder-depth': option.depth }"
          :title="option.path"
          @click="pick(option.path)"
        >
          <t-icon name="folder" class="move-folder-option__icon" />
          <span class="move-folder-option__label">{{ option.name }}</span>
          <t-icon v-if="selected === option.path && !creating" name="check" class="move-folder-option__check" />
        </button>
      </div>

      <div class="move-folder-create">
        <button v-if="!creating" type="button" class="move-folder-create__trigger" @click="startCreating">
          <t-icon name="folder-add" />
          <span>{{ t('knowledgeBase.moveToFolder.newFolder') }}</span>
        </button>
        <template v-else>
          <t-input
            v-model.trim="newFolderName"
            :placeholder="t('knowledgeBase.moveToFolder.newFolderPlaceholder')"
            autofocus
            clearable
            @enter="handleConfirm"
          />
          <p class="move-folder-create__preview">
            {{ newFolderPath
              ? t('knowledgeBase.moveToFolder.newFolderPreview', { path: newFolderPath })
              : t('knowledgeBase.moveToFolder.newFolderUnderRoot') }}
          </p>
        </template>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KnowledgeFolderNode, KnowledgeFolderTree } from '@/api/knowledge-base/index'
import { joinFolderPath } from '../folderTree'

const props = withDefaults(defineProps<{
  visible: boolean
  tree: KnowledgeFolderTree | null
  count: number
  submitting?: boolean
  /** Folder the documents are currently in, pre-selected as the starting point. */
  currentPath?: string
}>(), {
  submitting: false,
  currentPath: '',
})

const emit = defineEmits<{
  'update:visible': [value: boolean]
  confirm: [folderPath: string]
}>()

const { t } = useI18n()

const selected = ref('')
const creating = ref(false)
const newFolderName = ref('')

type FolderOption = { path: string; name: string; depth: number }

const options = computed<FolderOption[]>(() => {
  const result: FolderOption[] = []
  const walk = (nodes: KnowledgeFolderNode[], depth: number) => {
    nodes.forEach((node) => {
      result.push({ path: node.path, name: node.name, depth })
      walk(node.children || [], depth + 1)
    })
  }
  walk(props.tree?.folders ?? [], 0)
  return result
})

// A new folder is created under whichever folder is currently highlighted, so
// the picker doubles as "where to put it".
const newFolderPath = computed(() => joinFolderPath(selected.value, newFolderName.value))

const canSubmit = computed(() => (creating.value ? !!newFolderPath.value : true))

const pick = (path: string) => {
  selected.value = path
  creating.value = false
  newFolderName.value = ''
}

const startCreating = () => {
  creating.value = true
  newFolderName.value = ''
}

const close = () => {
  emit('update:visible', false)
}

const handleConfirm = () => {
  if (!canSubmit.value) return
  emit('confirm', creating.value ? newFolderPath.value : selected.value)
}

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    selected.value = props.currentPath || ''
    creating.value = false
    newFolderName.value = ''
  },
  { immediate: true },
)
</script>

<style scoped lang="less">
.move-folder-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.move-folder-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--td-text-color-placeholder);
}

.move-folder-list {
  --move-folder-indent: 16px;
  max-height: 260px;
  overflow-y: auto;
  padding: 4px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  scrollbar-width: thin;

  &::-webkit-scrollbar {
    width: 4px;
  }

  &::-webkit-scrollbar-thumb {
    border-radius: 2px;
    background: var(--td-scrollbar-color);
  }
}

.move-folder-option {
  display: flex;
  align-items: center;
  gap: 6px;
  box-sizing: border-box;
  width: 100%;
  height: 30px;
  padding: 0 8px 0 calc(var(--move-folder-depth, 0) * var(--move-folder-indent) + 8px);
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.active {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    color: var(--td-brand-color);

    .move-folder-option__icon {
      color: var(--td-brand-color);
    }
  }
}

.move-folder-option__icon {
  flex: 0 0 auto;
  font-size: 15px;
  color: var(--td-text-color-placeholder);
}

.move-folder-option__label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.move-folder-option__check {
  flex: 0 0 auto;
  font-size: 14px;
}

.move-folder-create__trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--td-brand-color);
  font-family: var(--app-font-family);
  font-size: 13px;
  cursor: pointer;

  .t-icon {
    font-size: 15px;
  }
}

.move-folder-create__preview {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--td-text-color-placeholder);
  word-break: break-all;
}
</style>
