<template>
  <!-- Per-node folder action menu for the knowledge folder tree. Modeled on
       WikiFolderActions but adds a "move folder" entry. Kept separate so the
       shared wiki component stays untouched. -->
  <t-popup v-model:visible="open" trigger="click" placement="bottom-start" destroy-on-close
    :overlay-class-name="popupOverlayClass" @visible-change="onVisibleChange">
    <span :class="['kf-action', 'kf-action--reveal', { 'is-open': open }]"
      :title="t('knowledgeBase.folderTreeTitle')" @click.stop @dragstart.prevent.stop>
      <t-icon name="more" />
    </span>
    <template #content>
      <div class="kf-folder-menu" @click.stop>
        <div v-if="mode === 'menu'" class="popup-menu">
          <div class="popup-menu-item" @click="enterMode('create')">
            <t-icon name="folder-add" class="menu-icon" />
            <span>{{ t('knowledgeBase.newSubfolder') }}</span>
          </div>
          <div class="popup-menu-item" @click="emitRename">
            <t-icon name="edit" class="menu-icon" />
            <span>{{ t('knowledgeEditor.wikiBrowser.renameFolder') }}</span>
          </div>
          <div class="popup-menu-item" @click="emitMove">
            <t-icon name="swap" class="menu-icon" />
            <span>{{ t('knowledgeBase.moveFolder') }}</span>
          </div>
          <div class="popup-menu-item delete" @click="enterMode('delete')">
            <t-icon name="delete" class="menu-icon" />
            <span>{{ t('knowledgeEditor.wikiBrowser.deleteFolder') }}</span>
          </div>
        </div>

        <div v-else-if="mode === 'create'" class="anchored-form-popup-inner">
          <div class="anchored-form-popup-title">{{ t('knowledgeBase.newSubfolder') }}</div>
          <t-input
            ref="inputRef"
            v-model="nameInput"
            :placeholder="t('knowledgeEditor.wikiBrowser.folderNamePlaceholder')"
            @enter="submitName"
          />
          <div class="anchored-form-popup-footer">
            <t-button variant="outline" @click="open = false">
              {{ t('common.cancel') }}
            </t-button>
            <t-button theme="primary" :disabled="!nameInput.trim()" @click="submitName">
              {{ t('common.confirm') }}
            </t-button>
          </div>
        </div>

        <div v-else class="anchored-form-popup-inner">
          <div class="anchored-form-popup-title">{{ t('knowledgeEditor.wikiBrowser.deleteFolder') }}</div>
          <div class="anchored-form-popup-body">
            {{
              deletable
                ? t('knowledgeEditor.wikiBrowser.deleteFolderConfirm', { name })
                : t('knowledgeEditor.wikiBrowser.deleteFolderNotEmpty')
            }}
          </div>
          <div class="anchored-form-popup-footer">
            <t-button variant="outline" @click="open = false">
              {{ deletable ? t('common.cancel') : t('common.confirm') }}
            </t-button>
            <t-button v-if="deletable" theme="danger" @click="submitDelete">
              {{ t('common.confirm') }}
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
  pageCount?: number
  hasChildren?: boolean
}>(), {
  name: '',
  pageCount: 0,
  hasChildren: false,
})

const emit = defineEmits<{
  (e: 'create', name: string): void
  (e: 'rename'): void
  (e: 'move'): void
  (e: 'delete'): void
}>()

const { t } = useI18n()

const open = ref(false)
const mode = ref<'menu' | 'create' | 'delete'>('menu')
const nameInput = ref('')
const inputRef = ref<{ focus: () => void } | null>(null)

const deletable = computed(() => props.pageCount === 0 && !props.hasChildren)

const popupOverlayClass = computed(() => (
  mode.value === 'menu'
    ? 'card-more-popup wiki-folder-action-overlay'
    : 'anchored-form-popup-overlay'
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

function emitMove() {
  emit('move')
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
.kf-action {
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

.kf-action--reveal {
  opacity: 1;
}
</style>

<style lang="less">
.wiki-folder-action-overlay {
  .kf-folder-menu {
    min-width: 188px;
  }
}
</style>
