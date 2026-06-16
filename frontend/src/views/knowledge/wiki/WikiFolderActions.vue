<template>
  <!-- Anchored in-place popup for folder actions. The trigger is icon-only to
       keep the directory row compact; everything (menu, name input, delete
       confirm) happens inside this one popup so no full-page dialog is spawned.
       click.stop on the trigger and content prevents the surrounding directory
       row from treating the interaction as an expand/collapse toggle. -->
  <t-popup
    v-model:visible="open"
    trigger="click"
    placement="bottom-start"
    destroy-on-close
    overlay-class-name="card-more-popup wiki-folder-action-overlay"
    @visible-change="onVisibleChange"
  >
    <span
      :class="[
        'wiki-directory-action',
        { 'wiki-directory-action--reveal': !createOnly, 'is-open': open, 'wiki-directory-action--labeled': createOnly && label },
      ]"
      :title="createOnly ? t('knowledgeEditor.wikiBrowser.newFolder') : t('knowledgeEditor.wikiBrowser.folderActions')"
      @click.stop
      @dragstart.prevent.stop
    >
      <t-icon :name="createOnly ? 'folder-add' : 'more'" />
      <span v-if="createOnly && label" class="wiki-directory-action-label">{{ label }}</span>
    </span>
    <template #content>
      <div class="wiki-folder-menu" @click.stop>
        <div v-if="mode === 'menu'" class="popup-menu">
          <div class="popup-menu-item" @click="enterMode('create')">
            <t-icon name="folder-add" class="menu-icon" />
            <span>{{ t('knowledgeEditor.wikiBrowser.newSubfolder') }}</span>
          </div>
          <div class="popup-menu-item" @click="emitRename">
            <t-icon name="edit" class="menu-icon" />
            <span>{{ t('knowledgeEditor.wikiBrowser.renameFolder') }}</span>
          </div>
          <div class="popup-menu-item delete" @click="enterMode('delete')">
            <t-icon name="delete" class="menu-icon" />
            <span>{{ t('knowledgeEditor.wikiBrowser.deleteFolder') }}</span>
          </div>
        </div>

        <template v-else-if="mode === 'create'">
          <t-input
            ref="inputRef"
            v-model="nameInput"
            :placeholder="t('knowledgeEditor.wikiBrowser.folderNamePlaceholder')"
            @enter="submitName"
          />
          <div class="wiki-folder-menu-footer">
            <t-button size="small" variant="outline" @click="open = false">
              {{ t('common.cancel') }}
            </t-button>
            <t-button size="small" theme="primary" :disabled="!nameInput.trim()" @click="submitName">
              {{ t('common.confirm') }}
            </t-button>
          </div>
        </template>

        <template v-else>
          <div v-if="!deletable" class="wiki-folder-confirm">
            {{ t('knowledgeEditor.wikiBrowser.deleteFolderNotEmpty') }}
          </div>
          <div v-else class="wiki-folder-confirm">
            {{ t('knowledgeEditor.wikiBrowser.deleteFolderConfirm', { name }) }}
          </div>
          <div class="wiki-folder-menu-footer">
            <t-button size="small" variant="outline" @click="open = false">
              {{ deletable ? t('common.cancel') : t('common.confirm') }}
            </t-button>
            <t-button v-if="deletable" size="small" theme="danger" @click="submitDelete">
              {{ t('common.confirm') }}
            </t-button>
          </div>
        </template>
      </div>
    </template>
  </t-popup>
</template>

<script setup lang="ts">
import { ref, computed, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(defineProps<{
  name?: string
  pageCount?: number
  hasChildren?: boolean
  // createOnly turns the trigger into a single "new folder" affordance that
  // opens straight to the name input (used by the root-folder toolbar entry).
  createOnly?: boolean
  // label, when set on a createOnly trigger, renders text next to the icon so
  // the toolbar entry reads as a button instead of a bare icon.
  label?: string
}>(), {
  name: '',
  pageCount: 0,
  hasChildren: false,
  createOnly: false,
  label: '',
})

const emit = defineEmits<{
  (e: 'create', name: string): void
  (e: 'rename'): void
  (e: 'delete'): void
}>()

const { t } = useI18n()

const open = ref(false)
const mode = ref<'menu' | 'create' | 'delete'>('menu')
const nameInput = ref('')
const inputRef = ref<{ focus: () => void } | null>(null)

const deletable = computed(() => props.pageCount === 0 && !props.hasChildren)

function onVisibleChange(visible: boolean) {
  if (!visible) {
    mode.value = 'menu'
    nameInput.value = ''
    return
  }
  // createOnly skips the menu and lands directly on the name input.
  if (props.createOnly) {
    enterMode('create')
  } else {
    mode.value = 'menu'
  }
}

function enterMode(next: 'create' | 'delete') {
  mode.value = next
  if (next === 'create') {
    nameInput.value = ''
    nextTick(() => inputRef.value?.focus())
  }
}

function emitRename() {
  emit('rename')
  open.value = false
}

function submitName() {
  const value = nameInput.value.trim()
  if (!value) return
  emit('create', value)
  open.value = false
}

function submitDelete() {
  emit('delete')
  open.value = false
}
</script>

<style lang="less" scoped>
.wiki-directory-action {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  font-size: 15px;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: color 0.15s, opacity 0.15s;

  &:hover {
    color: var(--td-brand-color);
  }
}

// Hover-reveal for the per-folder "more" trigger: hidden by default, shown when
// the row is hovered (parent supplies that rule via :deep) or the popup is open.
.wiki-directory-action--reveal {
  opacity: 0;

  &.is-open {
    opacity: 1;
  }
}

// Labeled variant: the toolbar "new folder" button reads as icon + text.
.wiki-directory-action--labeled {
  gap: 4px;
  padding: 2px 6px;
  border-radius: 6px;
  font-size: 12px;
  color: var(--td-text-color-secondary);

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-bg-color-container-hover);
  }
}

.wiki-directory-action-label {
  line-height: 1;
}
</style>

<style lang="less">
// Popup content teleports to <body>, so these must be global. The menu chrome
// (background, border, shadow, item styling) comes from the shared
// `card-more-popup` + `.popup-menu` rules in assets/dropdown-menu.less so this
// menu matches every other dropdown in the app; only the create-input and
// delete-confirm panes need a little extra layout here.
.wiki-folder-action-overlay {
  .wiki-folder-menu {
    min-width: 188px;
  }

  .wiki-folder-confirm {
    font-size: 13px;
    line-height: 1.5;
    color: var(--td-text-color-primary);
    padding: 4px 6px 8px;
    max-width: 280px;
  }

  .wiki-folder-menu-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 8px;
    padding: 0 2px;
  }
}
</style>
