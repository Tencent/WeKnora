<template>
  <div class="embed-input">
    <textarea
      v-model="query"
      class="embed-input__textarea"
      :placeholder="t('input.placeholder')"
      rows="2"
      @keydown="onKeydown"
    />
    <div class="embed-input__actions">
      <button
        v-if="isReplying"
        type="button"
        class="embed-input__btn embed-input__btn--ghost"
        @click="emit('stop-generation')"
      >
        {{ t('input.stopGeneration') }}
      </button>
      <button
        v-else
        type="button"
        class="embed-input__btn embed-input__btn--primary"
        :disabled="!query.trim()"
        @click="submit"
      >
        {{ t('input.send') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

defineProps<{
  isReplying: boolean
}>()

const emit = defineEmits<{
  (e: 'send-msg', query: string): void
  (e: 'stop-generation'): void
}>()

const { t } = useI18n()
const query = ref('')

const submit = () => {
  const val = query.value.trim()
  if (!val) return
  emit('send-msg', val)
  query.value = ''
}

const onKeydown = (e: KeyboardEvent) => {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    if (!query.value.trim()) return
    submit()
  }
}
</script>

<style scoped>
.embed-input {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
  padding: 12px;
  box-sizing: border-box;
  border-top: 1px solid var(--embed-border, #e7e7e7);
  background: #fff;
}

.embed-input__textarea {
  width: 100%;
  min-height: 48px;
  max-height: 160px;
  padding: 10px 12px;
  border: 1px solid var(--embed-border, #e7e7e7);
  border-radius: 8px;
  font: inherit;
  resize: vertical;
  box-sizing: border-box;
  outline: none;
}

.embed-input__textarea:focus {
  border-color: var(--embed-primary, #0052d9);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--embed-primary, #0052d9) 20%, transparent);
}

.embed-input__actions {
  display: flex;
  justify-content: flex-end;
}

.embed-input__btn {
  border: none;
  border-radius: 6px;
  padding: 8px 16px;
  font: inherit;
  cursor: pointer;
}

.embed-input__btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.embed-input__btn--primary {
  background: var(--embed-primary, #0052d9);
  color: #fff;
}

.embed-input__btn--ghost {
  background: #f3f3f3;
  color: #333;
}
</style>
