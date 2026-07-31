<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(defineProps<{
  kind: 'root' | 'folder'
  name?: string
  canEdit?: boolean
  loading?: boolean
  revealed?: boolean
}>(), {
  name: '',
  canEdit: false,
  loading: false,
  revealed: false,
})

const emit = defineEmits<{
  (event: 'create', name: string): void
  (event: 'rename', name: string): void
  (event: 'delete'): void
  (event: 'start-chat'): void
}>()

const { t } = useI18n()

type ActionMode = 'menu' | 'create' | 'rename' | 'delete'

const open = ref(false)
const mode = ref<ActionMode>('menu')
const nameInput = ref('')
const inputRef = ref<{ focus: () => void } | null>(null)

const popupOverlayClass = computed(() => (
  mode.value === 'menu'
    ? 'card-more-popup knowledge-folder-action-overlay'
    : 'anchored-form-popup-overlay knowledge-folder-action-overlay'
))

const normalizedName = computed(() => nameInput.value.trim())
const nameSubmitDisabled = computed(() => (
  props.loading
  || normalizedName.value.length === 0
  || (mode.value === 'rename' && normalizedName.value === props.name)
))

function resetMode(): void {
  mode.value = 'menu'
  nameInput.value = ''
}

function onVisibleChange(visible: boolean): void {
  if (!visible) {
    resetMode()
    return
  }
  mode.value = 'menu'
}

function enterNameMode(nextMode: 'create' | 'rename'): void {
  mode.value = nextMode
  nameInput.value = nextMode === 'rename' ? props.name : ''
  void nextTick(() => inputRef.value?.focus())
}

function enterDeleteMode(): void {
  mode.value = 'delete'
}

function submitName(): void {
  if (nameSubmitDisabled.value) return
  if (mode.value === 'create') {
    emit('create', normalizedName.value)
  } else if (mode.value === 'rename') {
    emit('rename', normalizedName.value)
  }
  open.value = false
}

function submitDelete(): void {
  if (props.loading) return
  emit('delete')
  open.value = false
}

function startChat(): void {
  emit('start-chat')
  open.value = false
}
</script>

<template>
  <t-popup
    v-model:visible="open"
    trigger="click"
    placement="bottom-start"
    destroy-on-close
    :overlay-class-name="popupOverlayClass"
    @visible-change="onVisibleChange"
  >
    <button
      class="knowledge-folder-actions__trigger"
      :class="{ 'is-open': open, 'is-revealed': revealed }"
      type="button"
      :aria-label="t('knowledgeFolder.actions')"
      :title="t('knowledgeFolder.actions')"
      @click.stop
    >
      <t-icon name="more" aria-hidden="true" />
    </button>

    <template #content>
      <div class="knowledge-folder-actions__content" @click.stop>
        <div v-if="mode === 'menu'" class="popup-menu knowledge-folder-actions__menu">
          <button
            v-if="canEdit"
            class="popup-menu-item"
            type="button"
            :disabled="loading"
            @click="enterNameMode('create')"
          >
            <t-icon name="folder-add" class="menu-icon" aria-hidden="true" />
            <span>
              {{
                kind === 'root'
                  ? t('knowledgeFolder.createTopLevel')
                  : t('knowledgeFolder.createChild')
              }}
            </span>
          </button>
          <button
            v-if="kind === 'folder' && canEdit"
            class="popup-menu-item"
            type="button"
            :disabled="loading"
            @click="enterNameMode('rename')"
          >
            <t-icon name="edit" class="menu-icon" aria-hidden="true" />
            <span>{{ t('knowledgeFolder.rename') }}</span>
          </button>
          <button
            v-if="kind === 'folder' && canEdit"
            class="popup-menu-item delete"
            type="button"
            :disabled="loading"
            @click="enterDeleteMode"
          >
            <t-icon name="delete" class="menu-icon" aria-hidden="true" />
            <span>{{ t('knowledgeFolder.delete') }}</span>
          </button>
          <button
            class="popup-menu-item"
            type="button"
            :disabled="loading"
            @click="startChat"
          >
            <t-icon name="chat" class="menu-icon" aria-hidden="true" />
            <span>
              {{
                kind === 'root'
                  ? t('knowledgeFolder.startKnowledgeBaseChat')
                  : t('knowledgeFolder.startFolderChat')
              }}
            </span>
          </button>
        </div>

        <div
          v-else-if="mode === 'create' || mode === 'rename'"
          class="anchored-form-popup-inner knowledge-folder-actions__form"
        >
          <div class="anchored-form-popup-title">
            {{
              mode === 'rename'
                ? t('knowledgeFolder.rename')
                : kind === 'root'
                  ? t('knowledgeFolder.createTopLevel')
                  : t('knowledgeFolder.createChild')
            }}
          </div>
          <t-input
            ref="inputRef"
            v-model="nameInput"
            :disabled="loading"
            :placeholder="t('knowledgeFolder.folderNamePlaceholder')"
            @enter="submitName"
          />
          <div class="anchored-form-popup-footer">
            <t-button variant="outline" :disabled="loading" @click="open = false">
              {{ t('common.cancel') }}
            </t-button>
            <t-button
              theme="primary"
              :loading="loading"
              :disabled="nameSubmitDisabled"
              @click="submitName"
            >
              {{ t('common.confirm') }}
            </t-button>
          </div>
        </div>

        <div v-else class="anchored-form-popup-inner knowledge-folder-actions__form">
          <div class="anchored-form-popup-title">
            {{ t('knowledgeFolder.delete') }}
          </div>
          <div class="anchored-form-popup-body">
            {{ t('knowledgeFolder.deleteConfirm', { name }) }}
          </div>
          <div class="anchored-form-popup-footer">
            <t-button variant="outline" :disabled="loading" @click="open = false">
              {{ t('common.cancel') }}
            </t-button>
            <t-button theme="danger" :loading="loading" @click="submitDelete">
              {{ t('common.confirm') }}
            </t-button>
          </div>
        </div>
      </div>
    </template>
  </t-popup>
</template>

<style scoped>
.knowledge-folder-actions__trigger {
  display: inline-flex;
  flex: 0 0 28px;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  background: transparent;
  border: 0;
  border-radius: var(--td-radius-small);
  opacity: 0.18;
  transition: color 0.15s ease, background 0.15s ease, opacity 0.15s ease;
}

.knowledge-folder-actions__trigger:hover,
.knowledge-folder-actions__trigger:focus-visible,
.knowledge-folder-actions__trigger.is-open,
.knowledge-folder-actions__trigger.is-revealed {
  color: var(--td-brand-color);
  background: var(--td-bg-color-container-hover);
  outline: none;
  opacity: 1;
}

.knowledge-folder-actions__menu {
  min-width: 208px;
}

.knowledge-folder-actions__menu .popup-menu-item {
  width: 100%;
  border: 0;
}

.knowledge-folder-actions__menu .popup-menu-item:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.knowledge-folder-actions__form {
  width: 280px;
}
</style>
