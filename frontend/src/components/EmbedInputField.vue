<template>
  <div class="embed-input-box" :class="{ 'is-replying': isReplying }">
    <div v-if="uploadedAttachments.length" class="embed-input-box__files">
      <div v-for="(att, index) in uploadedAttachments" :key="`${att.file.name}-${index}`" class="embed-file-chip">
        <t-icon name="file" size="14px" />
        <span class="embed-file-chip__name">{{ att.file.name }}</span>
        <button type="button" class="embed-file-chip__remove" @click="removeAttachment(index)">
          <t-icon name="close" size="12px" />
        </button>
      </div>
    </div>
    <div v-if="uploadedImages.length" class="embed-input-box__images">
      <div v-for="(img, index) in uploadedImages" :key="img.preview" class="embed-image-thumb">
        <img :src="img.preview" :alt="img.file.name" />
        <button type="button" class="embed-image-thumb__remove" @click="removeImage(index)">
          <t-icon name="close" size="12px" />
        </button>
      </div>
    </div>
    <t-textarea
      v-if="textareaReady"
      ref="textareaRef"
      v-model="query"
      class="embed-input-box__textarea"
      :class="{ 'has-images': uploadedImages.length > 0 }"
      :placeholder="t('input.placeholder')"
      :autosize="{ minRows: 2, maxRows: 6 }"
      @keydown="onKeydown"
      @compositionstart="isComposing = true"
      @compositionend="isComposing = false"
    />
    <input
      ref="imageInputRef"
      type="file"
      accept="image/jpeg,image/png,image/gif,image/webp"
      multiple
      class="embed-hidden-file-input"
      @change="handleImageSelect"
    />
    <input
      ref="fileInputRef"
      type="file"
      accept=".pdf,.doc,.docx,.txt,.md,.csv,.xlsx,.xls,.ppt,.pptx,application/pdf,text/plain"
      multiple
      class="embed-hidden-file-input"
      @change="handleFileSelect"
    />
    <div class="embed-input-box__bar">
      <div v-if="showWebSearchToggle || showFileUploadToggle" class="embed-input-box__controls">
        <t-tooltip v-if="showWebSearchToggle" placement="top">
          <template #content>
            {{ webSearchEnabled ? t('input.webSearch.toggleOff') : t('input.webSearch.toggleOn') }}
          </template>
          <button
            type="button"
            class="embed-control-btn embed-websearch-btn"
            :class="{ active: webSearchEnabled }"
            :aria-label="t('input.webSearch.label')"
            @click="toggleWebSearch"
          >
            <svg width="18" height="18" viewBox="0 0 18 18" fill="none" aria-hidden="true">
              <circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.2" fill="none" />
              <path d="M 9 2 A 3.5 7 0 0 0 9 16" stroke="currentColor" stroke-width="1.2" fill="none" />
              <path d="M 9 2 A 3.5 7 0 0 1 9 16" stroke="currentColor" stroke-width="1.2" fill="none" />
              <line x1="2.94" y1="5.5" x2="15.06" y2="5.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
              <line x1="2.94" y1="12.5" x2="15.06" y2="12.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
            </svg>
          </button>
        </t-tooltip>
        <t-tooltip v-if="showFileUploadToggle" placement="top" :content="t('input.imageUpload.tooltip')">
          <button
            type="button"
            class="embed-control-btn embed-image-btn"
            :class="{ active: uploadedImages.length > 0 }"
            :aria-label="t('input.imageUpload.label')"
            @click="triggerImageUpload"
          >
            <t-icon name="image" size="18px" />
          </button>
        </t-tooltip>
        <t-tooltip v-if="showFileUploadToggle" placement="top" :content="t('input.fileUpload.tooltip')">
          <button
            type="button"
            class="embed-control-btn embed-file-btn"
            :class="{ active: uploadedAttachments.length > 0 }"
            :aria-label="t('input.fileUpload.label')"
            @click="triggerFileUpload"
          >
            <t-icon name="attach" size="18px" />
          </button>
        </t-tooltip>
      </div>
      <div class="embed-input-box__actions">
        <t-tooltip v-if="isReplying" :content="t('input.stopGeneration')" placement="top">
          <button type="button" class="embed-stop-btn" @click="emit('stop-generation')">
            <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
              <rect x="5" y="5" width="6" height="6" rx="1" />
            </svg>
          </button>
        </t-tooltip>
        <button
          v-else
          type="button"
          class="embed-send-btn"
          :class="{ disabled: !canSend }"
          :aria-label="t('input.send')"
          @click="submit"
        >
          <img src="@/assets/img/sending-aircraft.svg" :alt="t('input.send')" />
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { embedToast } from '@/utils/embedToast'
import { isEmbedImageFile } from '@/utils/embedFile'

const props = defineProps<{
  isReplying: boolean
  showWebSearchToggle?: boolean
  webSearchEnabled?: boolean
  showFileUploadToggle?: boolean
}>()

const emit = defineEmits<{
  (e: 'send-msg', query: string, imageFiles: File[], attachmentFiles: File[]): void
  (e: 'stop-generation'): void
  (e: 'update:webSearchEnabled', value: boolean): void
}>()

const { t } = useI18n()
const query = ref('')
const isComposing = ref(false)
const textareaReady = ref(false)
const textareaRef = ref<{ $el?: HTMLElement } | HTMLTextAreaElement | null>(null)

const getTextareaEl = (): HTMLTextAreaElement | null => {
  const el = textareaRef.value
  if (!el) return null
  if (el instanceof HTMLTextAreaElement) return el
  const inner = el.$el?.querySelector?.('textarea')
  return inner instanceof HTMLTextAreaElement ? inner : null
}

onMounted(() => {
  nextTick(() => {
    textareaReady.value = true
  })
})
const imageInputRef = ref<HTMLInputElement | null>(null)
const fileInputRef = ref<HTMLInputElement | null>(null)
const uploadedImages = ref<Array<{ file: File; preview: string }>>([])
const uploadedAttachments = ref<Array<{ file: File }>>([])

const canSend = computed(() =>
  query.value.trim().length > 0 || uploadedImages.value.length > 0 || uploadedAttachments.value.length > 0)

const toggleWebSearch = () => {
  const next = !props.webSearchEnabled
  emit('update:webSearchEnabled', next)
  embedToast(next ? t('input.messages.webSearchEnabled') : t('input.messages.webSearchDisabled'))
}

const triggerImageUpload = () => {
  imageInputRef.value?.click()
}

const triggerFileUpload = () => {
  fileInputRef.value?.click()
}

const addImageFiles = (files: File[]) => {
  const allowed = ['image/jpeg', 'image/png', 'image/gif', 'image/webp']
  const maxSize = 10 * 1024 * 1024
  for (const file of files) {
    if (!isEmbedImageFile(file)) continue
    if (uploadedImages.value.length >= 5) {
      embedToast(t('chat.imageTooMany'))
      break
    }
    if (!allowed.includes(file.type)) {
      embedToast(t('chat.imageTypeSizeError'))
      continue
    }
    if (file.size > maxSize) {
      embedToast(t('chat.imageTypeSizeError'))
      continue
    }
    uploadedImages.value.push({ file, preview: URL.createObjectURL(file) })
  }
}

const handleImageSelect = (event: Event) => {
  const input = event.target as HTMLInputElement
  if (!input.files) return
  addImageFiles(Array.from(input.files))
  input.value = ''
}

const addAttachmentFiles = (files: File[]) => {
  const maxSize = 20 * 1024 * 1024
  for (const file of files) {
    if (isEmbedImageFile(file)) continue
    if (uploadedAttachments.value.length >= 5) {
      embedToast(t('input.fileUpload.tooMany'))
      break
    }
    if (file.size > maxSize) {
      embedToast(t('input.fileUpload.tooLarge'))
      continue
    }
    uploadedAttachments.value.push({ file })
  }
}

const handleFileSelect = (event: Event) => {
  const input = event.target as HTMLInputElement
  if (!input.files) return
  addAttachmentFiles(Array.from(input.files))
  input.value = ''
}

const removeAttachment = (index: number) => {
  uploadedAttachments.value.splice(index, 1)
}

const removeImage = (index: number) => {
  const removed = uploadedImages.value.splice(index, 1)
  if (removed.length > 0) URL.revokeObjectURL(removed[0].preview)
}

const submit = () => {
  if (props.isReplying || !canSend.value) return
  const val = query.value.trim()
  const imageFiles = uploadedImages.value.map((img) => img.file)
  const attachmentFiles = uploadedAttachments.value.map((att) => att.file)
  const textarea = getTextareaEl()
  if (textarea) textarea.blur()
  emit('send-msg', val, imageFiles, attachmentFiles)
  if (getTextareaEl()) query.value = ''
  uploadedImages.value.forEach((img) => URL.revokeObjectURL(img.preview))
  uploadedImages.value = []
  uploadedAttachments.value = []
}

const onKeydown = (_val: string, ctx: { e: KeyboardEvent }) => {
  const e = ctx?.e
  if (!e || e.keyCode !== 13) return
  if (isComposing.value) return
  if (e.shiftKey || e.ctrlKey) return
  e.preventDefault()
  submit()
}

onUnmounted(() => {
  uploadedImages.value.forEach((img) => URL.revokeObjectURL(img.preview))
})
</script>

<style scoped lang="less">
.embed-input-box {
  position: relative;
  width: 100%;
  max-width: 800px;
  margin: 0 auto;
  background: var(--td-bg-color-container, #fff);
  border-radius: 12px;
  border: 0.5px solid var(--td-component-border, #e7e7e7);
  box-shadow: 0 6px 6px rgba(0, 0, 0, 0.04), 0 12px 12px -1px rgba(0, 0, 0, 0.08);
  transition: border-color 0.15s ease;

  &:focus-within {
    border-color: var(--embed-primary, var(--td-brand-color, #07c05f));
  }

  &__files {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    padding: 12px 16px 0;
  }

  &__images {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    padding: 12px 16px 0;
  }

  &__textarea {
    width: 100%;

    :deep(.t-textarea__inner) {
      border: none;
      box-shadow: none;
      background: transparent;
      padding: 14px 16px 52px;
      font-size: 14px;
      line-height: 1.5;
      resize: none;
    }

    &.has-images :deep(.t-textarea__inner) {
      padding-top: 8px;
    }
  }

  &__bar {
    position: absolute;
    left: 12px;
    right: 12px;
    bottom: 12px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    pointer-events: none;

    > * {
      pointer-events: auto;
    }
  }

  &__controls {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  &__actions {
    margin-left: auto;
    display: flex;
    align-items: center;
  }
}

.embed-hidden-file-input {
  display: none;
}

.embed-file-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 220px;
  padding: 6px 10px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer, #f3f3f3);
  font-size: 12px;

  &__name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__remove {
    border: none;
    background: transparent;
    cursor: pointer;
    padding: 0;
    color: var(--td-text-color-placeholder);
  }
}

.embed-image-thumb {
  position: relative;
  width: 56px;
  height: 56px;
  border-radius: 8px;
  overflow: hidden;
  border: 1px solid var(--td-component-border, #e7e7e7);

  img {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  &__remove {
    position: absolute;
    top: 2px;
    right: 2px;
    width: 18px;
    height: 18px;
    border: none;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    color: #fff;
    background: rgba(0, 0, 0, 0.55);
  }
}

.embed-control-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: none;
  border-radius: 6px;
  cursor: pointer;
  color: var(--td-text-color-secondary);
  background: transparent;
  transition: color 0.15s ease, background 0.15s ease;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }

  &.active {
    color: var(--embed-primary, var(--td-brand-color, #07c05f));
    background: color-mix(in srgb, var(--embed-primary, #07c05f) 12%, transparent);
  }
}

.embed-send-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: none;
  border-radius: 6px;
  cursor: pointer;
  background: var(--embed-primary, var(--td-brand-color, #07c05f));
  transition: background 0.15s ease, opacity 0.15s ease;

  &:hover:not(.disabled) {
    filter: brightness(0.94);
  }

  &.disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }

  img {
    width: 16px;
    height: 16px;
  }
}

.embed-stop-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: none;
  border-radius: 6px;
  cursor: pointer;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);

  &:hover {
    background: var(--td-bg-color-component-hover);
  }
}
</style>
