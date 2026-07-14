<template>
  <div v-if="loading || suggestionSet?.status === 'ready'" class="follow-ups" aria-live="polite">
    <div class="follow-ups__header">
      <span>{{ t('chat.followUpQuestions') }}</span>
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
    <div v-if="loading && !suggestionSet?.questions?.length" class="follow-ups__skeletons">
      <span v-for="n in 3" :key="n" :style="{ width: skeletonWidths[n - 1] }" />
    </div>
    <div v-else class="follow-ups__list">
      <button v-for="item in suggestionSet?.questions || []" :key="item.id" type="button"
        class="follow-ups__item" @click="emit('select', item)">
        <span>{{ item.text }}</span>
        <t-icon name="arrow-up-right" />
      </button>
    </div>
  </div>
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
const skeletonWidths = ['92%', '78%', '85%']

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
.follow-ups__skeletons { display: flex; flex-direction: column; gap: 6px; }
.follow-ups__skeletons span {
  height: 38px;
  border-radius: 8px;
  background: linear-gradient(
    100deg,
    var(--td-bg-color-component) 30%,
    var(--td-bg-color-container-hover, rgba(255, 255, 255, .35)) 50%,
    var(--td-bg-color-component) 70%
  );
  background-size: 200% 100%;
  animation: shimmer 3s ease-in-out infinite;
}
.is-spinning { animation: spin 1s linear infinite; }
@keyframes shimmer {
  0% { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
