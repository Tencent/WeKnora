<template>
  <!-- In-row popup for folder actions, mirroring WikiFolderActions so the two
       trees behave identically. Everything (menu, name input, delete confirm)
       happens inside this popup; click.stop keeps the surrounding row from
       treating the interaction as a select/expand. -->
  <t-popup v-model:visible="open" trigger="click" placement="bottom-start" destroy-on-close
    :overlay-class-name="popupOverlayClass" @visible-change="onVisibleChange">
    <span :class="['kb-folder-action', { 'is-open': open }]" :title="t('knowledgeBase.folderActions')" @click.stop
      @dragstart.prevent.stop>
      <t-icon name="more" />
    </span>
    <template #content>
      <div class="kb-folder-menu" @click.stop>
        <div v-if="mode === 'menu'" class="popup-menu">
          <div class="popup-menu-item" @click="enterMode('create')">
            <t-icon name="folder-add" class="menu-icon" />
            <span>{{ t('knowledgeBase.folderNewChild') }}</span>
          </div>
          <div class="popup-menu-item" @click="emitRename">
            <t-icon name="edit" class="menu-icon" />
            <span>{{ t('knowledgeBase.folderRename') }}</span>
          </div>
          <div class="popup-menu-item delete" @click="enterMode('delete')">
            <t-icon name="delete" class="menu-icon" />
            <span>{{ t('knowledgeBase.folderDelete') }}</span>
          </div>
        </div>

        <div v-else-if="mode === 'create'" class="anchored-form-popup-inner">
          <div class="anchored-form-popup-title">{{ t('knowledgeBase.folderNewChild') }}</div>
          <t-input ref="inputRef" v-model="nameInput" :placeholder="t('knowledgeBase.folderNamePlaceholder')"
            @enter="submitName" />
          <div class="anchored-form-popup-footer">
            <t-button variant="outline" @click="open = false">{{ t('common.cancel') }}</t-button>
            <t-button theme="primary" :disabled="!nameInput.trim()" @click="submitName">
              {{ t('common.confirm') }}
            </t-button>
          </div>
        </div>

        <div v-else class="anchored-form-popup-inner">
          <div class="anchored-form-popup-title">{{ t('knowledgeBase.folderDelete') }}</div>
          <!-- A non-empty folder is not a dead end: the documents can be lifted
               to the parent instead. Deleting documents is never on offer here,
               because a folder is a label, not an owner. -->
          <div class="anchored-form-popup-body">
            {{
              isEmpty
                ? t('knowledgeBase.folderDeleteConfirm', { name })
                : t('knowledgeBase.folderDeleteNotEmpty', { count: documentCount })
            }}
          </div>
          <div class="anchored-form-popup-footer">
            <t-button variant="outline" @click="open = false">{{ t('common.cancel') }}</t-button>
            <t-button theme="danger" @click="submitDelete">
              {{ isEmpty ? t('common.confirm') : t('knowledgeBase.folderDeleteReparent') }}
            </t-button>
          </div>
        </div>
      </div>
    </template>
  </t-popup>
</template>

<script setup lang="ts">
import { ref, computed, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(defineProps<{
  name?: string
  documentCount?: number
  hasChildren?: boolean
}>(), {
  name: '',
  documentCount: 0,
  hasChildren: false,
})

const emit = defineEmits<{
  (e: 'create', name: string): void
  (e: 'rename'): void
  (e: 'delete', strategy: 'fail' | 'reparent'): void
}>()

const { t } = useI18n()

const open = ref(false)
const mode = ref<'menu' | 'create' | 'delete'>('menu')
const nameInput = ref('')
const inputRef = ref<{ focus: () => void } | null>(null)

const isEmpty = computed(() => props.documentCount === 0 && !props.hasChildren)

const popupOverlayClass = computed(() => (
  mode.value === 'menu' ? 'card-more-popup kb-folder-action-overlay' : 'anchored-form-popup-overlay'
))

function onVisibleChange(visible: boolean) {
  mode.value = 'menu'
  if (!visible) nameInput.value = ''
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
  emit('delete', isEmpty.value ? 'fail' : 'reparent')
  open.value = false
}
</script>

<style lang="less" scoped>
.kb-folder-action {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  font-size: 15px;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  opacity: 0;
  transition: color 0.15s, opacity 0.15s;

  &:hover {
    color: var(--td-brand-color);
  }

  &.is-open {
    opacity: 1;
  }
}
</style>

<style lang="less">
// Menu chrome comes from card-more-popup + .popup-menu in dropdown-menu.less.
.kb-folder-action-overlay {
  .kb-folder-menu {
    min-width: 176px;
  }
}
</style>
