<template>
  <transition name="follow-up-card">
    <div v-if="suggestionSet?.status === 'ready'" class="follow-ups" aria-live="polite">
      <div class="follow-ups__header">
        <span class="follow-ups__title">
          <t-icon name="lightbulb" />
          <span>{{ t('chat.followUpQuestions') }}</span>
        </span>
        <div class="follow-ups__actions">
          <button v-if="allowRegenerate" type="button" :disabled="loading" @click="emit('regenerate')">
            <t-icon :name="loading ? 'loading' : 'refresh'" :class="{ 'is-spinning': loading }" />
            <span>{{ t('chat.refreshSuggestedQuestions') }}</span>
          </button>
          <button type="button" :aria-label="t('common.close')" @click="dismiss">
            <t-icon name="close" />
          </button>
        </div>
      </div>
      <div class="follow-ups__list">
        <button v-for="item in suggestionSet?.questions || []" :key="item.id" type="button"
          class="follow-ups__item" @click="emit('select', item)">
          <span>{{ item.text }}</span>
          <t-icon name="arrow-up-right" />
        </button>
      </div>
    </div>
  </transition>
</template>

<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MessageSuggestionItem, MessageSuggestionSet } from '@/api/message-suggestion'

const props = defineProps<{
  suggestionSet?: MessageSuggestionSet | null
  loading?: boolean
  allowRegenerate?: boolean
}>()
const emit = defineEmits<{
  (event: 'select', item: MessageSuggestionItem): void
  (event: 'regenerate'): void
  (event: 'impression', set: MessageSuggestionSet): void
  (event: 'dismiss', set: MessageSuggestionSet): void
}>()
const { t } = useI18n()
const impressed = new Set<string>()

watch(
  () => props.suggestionSet,
  (set) => {
    if (set?.status === 'ready' && set.questions.length > 0 && !impressed.has(set.id)) {
      impressed.add(set.id)
      emit('impression', set)
    }
  },
  { immediate: true },
)

const dismiss = () => {
  if (props.suggestionSet) emit('dismiss', props.suggestionSet)
}
</script>

<style scoped lang="less">
.follow-ups {
  max-width: 760px;
  margin: -4px 0 28px 46px;
  padding: 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 12px;
  background: var(--td-bg-color-secondarycontainer);
}
.follow-ups__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  font-weight: 600;
}
.follow-ups__title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.follow-ups__title .t-icon {
  width: 14px;
  height: 14px;
  color: var(--td-text-color-placeholder);
  font-size: 14px;
}
.follow-ups__actions { display: flex; gap: 4px; }
.follow-ups__actions button {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background-color .2s, color .2s;
}
.follow-ups__actions button:hover:not(:disabled) {
  background: var(--td-bg-color-container-hover, rgba(0, 0, 0, .06));
  color: var(--td-brand-color);
}
.follow-ups__actions button:disabled {
  cursor: not-allowed;
  opacity: .6;
}
.follow-ups__list { display: flex; flex-direction: column; gap: 6px; }
.follow-ups__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
  padding: 9px 11px;
  border: 1px solid transparent;
  border-radius: 8px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  text-align: left;
  cursor: pointer;
}
.follow-ups__item { transition: border-color .2s, box-shadow .2s, transform .2s; }
.follow-ups__item:hover {
  border-color: var(--td-brand-color);
  box-shadow: 0 4px 14px rgba(0, 0, 0, .12);
  transform: translateY(-1px);
}
.follow-ups__item:hover .t-icon { color: var(--td-brand-color); }
.is-spinning { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

.follow-up-card-enter-active {
  transform-origin: 24px 0;
  transition:
    opacity .22s ease .04s,
    transform .28s cubic-bezier(.22, .61, .36, 1) .04s,
    clip-path .28s cubic-bezier(.22, .61, .36, 1) .04s;
  will-change: opacity, transform, clip-path;
}
.follow-up-card-enter-from {
  opacity: 0;
  transform: translateY(-7px) scale(.985);
  clip-path: inset(0 0 55% 0 round 12px);
}
.follow-up-card-enter-to {
  opacity: 1;
  transform: translateY(0) scale(1);
  clip-path: inset(0 0 0 0 round 12px);
}

@media (prefers-reduced-motion: reduce) {
  .follow-up-card-enter-active { transition: none; }
}
</style>
