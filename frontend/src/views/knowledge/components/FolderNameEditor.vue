<template>
  <form class="folder-name-editor" @submit.prevent="submit">
    <t-icon :name="icon" class="folder-name-editor__icon" aria-hidden="true" />
    <input
      ref="inputRef"
      v-model="value"
      class="folder-name-editor__input"
      :placeholder="placeholder"
      :disabled="loading"
      :aria-invalid="nameTooLong || undefined"
      @keydown.esc.prevent="emit('cancel')"
    />
    <button
      type="submit"
      class="folder-name-editor__action folder-name-editor__action--confirm"
      :aria-label="t('common.confirm')"
      :disabled="loading || !trimmedValue || nameTooLong"
    >
      <t-loading v-if="loading" size="small" />
      <t-icon v-else name="check" />
    </button>
    <button
      type="button"
      class="folder-name-editor__action folder-name-editor__action--cancel"
      :aria-label="t('common.cancel')"
      :disabled="loading"
      @click="emit('cancel')"
    >
      <t-icon name="close" />
    </button>
    <span v-if="nameTooLong" class="folder-name-editor__error" role="alert">
      {{ t('knowledgeBase.folderNameTooLong', { count: nameByteLength }) }}
    </span>
  </form>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { getUtf8ByteLength } from '../utils/folderUploadPaths';

const MAX_FOLDER_NAME_BYTES = 255;

const props = withDefaults(defineProps<{
  initialValue?: string;
  placeholder?: string;
  icon?: string;
  loading?: boolean;
}>(), {
  initialValue: '',
  placeholder: '',
  icon: 'folder-add',
  loading: false,
});

const emit = defineEmits<{
  (e: 'submit', value: string): void;
  (e: 'cancel'): void;
}>();

const { t } = useI18n();
const inputRef = ref<HTMLInputElement | null>(null);
const value = ref(props.initialValue);
const trimmedValue = computed(() => value.value.trim());
const nameByteLength = computed(() => getUtf8ByteLength(trimmedValue.value));
const nameTooLong = computed(() => nameByteLength.value > MAX_FOLDER_NAME_BYTES);

function submit() {
  const name = trimmedValue.value;
  if (!name || nameTooLong.value || props.loading) return;
  emit('submit', name);
}

onMounted(() => {
  nextTick(() => {
    inputRef.value?.focus();
    if (props.initialValue) inputRef.value?.select();
  });
});
</script>

<style scoped lang="less">
.folder-name-editor {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto auto;
  align-items: center;
  column-gap: 5px;
  row-gap: 2px;
  min-width: 0;
  min-height: 34px;
  box-sizing: border-box;
}

.folder-name-editor__icon {
  flex: 0 0 auto;
  color: var(--td-brand-color);
  font-size: 16px;
}

.folder-name-editor__input {
  flex: 1;
  min-width: 0;
  height: 28px;
  box-sizing: border-box;
  padding: 0 8px;
  border: 1px solid var(--td-brand-color);
  border-radius: 6px;
  outline: none;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 13px;
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-brand-color) 10%, transparent);

  &::placeholder {
    color: var(--td-text-color-placeholder);
  }

  &:disabled {
    cursor: wait;
    opacity: 0.7;
  }
}

.folder-name-editor__action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  flex: 0 0 auto;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;

  &:disabled {
    cursor: not-allowed;
    opacity: 0.4;
  }

  &--confirm:not(:disabled):hover {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
  }

  &--cancel:not(:disabled):hover {
    background: var(--td-error-color-1);
    color: var(--td-error-color-6);
  }
}

.folder-name-editor__error {
  grid-column: 2 / -1;
  color: var(--td-error-color-6);
  font-size: 11px;
  line-height: 16px;
}
</style>
